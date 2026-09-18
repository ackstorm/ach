// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

// runBroker is `ach-mock broker`: an mcp-oauth stand-in for the e2e chain.
// /register hands out a client id; /authorize verifies login_hint against
// the issuer's JWKS (found through its RFC 8414 document, like mcp-oauth),
// writes the grant projection oauth:<scope>:state:<sub> = {"granted":true}
// and bounces to redirect_uri with a code. No provider, no consent screen.
func runBroker() {
	issuer := strings.TrimRight(envOr("BROKER_ISSUER", "http://ach.e2e.local:8080"), "/")
	rdb := redis.NewClient(&redis.Options{Addr: envOr("BROKER_REDIS_ADDR", "valkey-primary.ach-system.svc.cluster.local:6379")})
	keys := &jwksCache{issuer: issuer}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) == 0 {
			http.Error(w, `{"error":"invalid_redirect_uri"}`, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "mock-broker-client", "redirect_uris": body.RedirectURIs})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		store := q.Get("scope")
		sub, err := keys.verifyHint(r.Context(), q.Get("login_hint"), store)
		if err != nil {
			log.Printf("broker: login_hint rejected: %v", err)
			http.Error(w, "an account the provider did not name: "+err.Error(), 400)
			return
		}
		if err := rdb.Set(r.Context(), "oauth:"+store+":state:"+sub, `{"granted":true,"via":"ach-mock-broker"}`, 0).Err(); err != nil {
			http.Error(w, "projection write failed", 503)
			return
		}
		p := url.Values{"code": {"mock-broker-code"}, "state": {q.Get("state")}}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+p.Encode(), 302)
	})
	addr := envOr("MOCK_BIND_ADDRESS", ":9090")
	log.Printf("ach-mock broker listening on %s (issuer %s)", addr, issuer)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type jwksCache struct {
	issuer string
	mu     sync.Mutex
	keys   map[string]ed25519.PublicKey
	at     time.Time
}

func (c *jwksCache) verifyHint(ctx context.Context, raw, aud string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("no login_hint")
	}
	tok, err := jwtv5.Parse(raw, func(t *jwtv5.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		k, err := c.key(ctx, kid)
		if err != nil {
			return nil, err
		}
		return k, nil
	}, jwtv5.WithValidMethods([]string{"EdDSA"}), jwtv5.WithIssuer(c.issuer), jwtv5.WithAudience(aud), jwtv5.WithExpirationRequired())
	if err != nil {
		return "", err
	}
	sub, _ := tok.Claims.GetSubject()
	if sub == "" || sub != strings.ToLower(sub) {
		return "", fmt.Errorf("sub %q missing or not lowercase", sub)
	}
	return sub, nil
}

func (c *jwksCache) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok && time.Since(c.at) < time.Minute {
		return k, nil
	}
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := getJSON(ctx, c.issuer+"/.well-known/oauth-authorization-server", &meta); err != nil || meta.JWKSURI == "" {
		return nil, fmt.Errorf("as metadata: %v", err)
	}
	var jwks struct {
		Keys []struct {
			Kid, Kty, Crv, X string
		} `json:"keys"`
	}
	if err := getJSON(ctx, meta.JWKSURI, &jwks); err != nil {
		return nil, err
	}
	c.keys = map[string]ed25519.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Kty == "OKP" && k.Crv == "Ed25519" {
			if x, err := base64.RawURLEncoding.DecodeString(k.X); err == nil && len(x) == ed25519.PublicKeySize {
				c.keys[k.Kid] = ed25519.PublicKey(x)
			}
		}
	}
	c.at = time.Now()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

func getJSON(ctx context.Context, u string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

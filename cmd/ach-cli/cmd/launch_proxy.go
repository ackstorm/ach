// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ackstorm/ach/internal/cli/oauthlogin"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// launchCred resolves the launched tool's ACH credential through the same
// path as every network command (--key → ACH_KEY → the profile), holding a
// JWT until its refresh window and refreshing under the token lock. The
// first resolve pins the profile and its URL: switching the active profile
// elsewhere must not send another deployment's credential to this upstream.
type launchCred struct {
	f     credFlags
	mu    sync.Mutex
	c     cred
	url   string
	until time.Time
}

func (l *launchCred) get(ctx context.Context) (cred, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c.Bearer != "" && time.Now().Before(l.until) {
		return l.c, nil
	}
	_, release, err := acquireTokenLock(ctx)
	if err != nil {
		return cred{}, err
	}
	defer release()
	c, err := resolveCred(ctx, l.f)
	if err != nil {
		l.c = cred{}
		return cred{}, err
	}
	if l.url == "" {
		l.url = c.BaseURL
		if !synthetic.IsActive() {
			l.f.Profile = c.ProfileName
		}
	} else if c.BaseURL != l.url {
		return cred{}, fmt.Errorf("profile %q now points at %s, not %s; restart launch", c.ProfileName, c.BaseURL, l.url)
	}
	l.c, l.until = c, time.Now().Add(time.Minute)
	if exp, ok := jwtExp(c.Bearer); ok {
		l.until = exp.Add(-oauthlogin.RefreshSkew)
	}
	return c, nil
}

// forget drops the cached credential: the server refused it, so the next
// request re-reads the profile (a fresh `ach-cli login` takes effect).
func (l *launchCred) forget() {
	l.mu.Lock()
	l.until = time.Time{}
	l.mu.Unlock()
}

// startLaunchProxy serves the ACH URL's /v1 surface on a loopback port for
// the launched tool and returns the base URL to give it. Every request
// carries x-ach-key with the current credential; any other ACH credential
// the tool sends (a static ek- in a custom header) is dropped, and the rest
// passes untouched — the tool's own provider login in Authorization and
// anthropic-beta included. Responses stream unbuffered.
//
// Only the tool may use it: the base URL holds a random path prefix, the
// Host must be the listener's (no DNS rebinding from a browser), and only
// /v1/ is forwarded, so the user's credential never reaches /platform/*.
func startLaunchProxy(src *launchCred, upstream string) (string, func(), error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return "", nil, err
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", nil, err
	}
	prefix := "/" + hex.EncodeToString(secret)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	host := ln.Addr().String()
	quiet := log.New(io.Discard, "", 0)
	rp := &httputil.ReverseProxy{
		Rewrite:       func(r *httputil.ProxyRequest) { r.SetURL(target) },
		FlushInterval: -1,
		ErrorLog:      quiet,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode == http.StatusUnauthorized {
				src.forget()
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				writeAnthropicError(w, http.StatusBadGateway, "api_error", "ACH unreachable: "+err.Error())
			}
		},
	}
	srv := &http.Server{ReadHeaderTimeout: 30 * time.Second, ErrorLog: quiet,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, ok := strings.CutPrefix(r.URL.Path, prefix)
			if r.Host != host || !ok || !strings.HasPrefix(path, "/v1/") {
				http.NotFound(w, r)
				return
			}
			c, err := src.get(r.Context())
			if err != nil {
				writeAnthropicError(w, http.StatusUnauthorized, "authentication_error",
					"ACH: "+err.Error()+" (in another terminal)")
				return
			}
			for k, vs := range r.Header {
				if isACHCredential(vs, c.Bearer) {
					r.Header.Del(k)
				}
			}
			r.Header.Set("x-ach-key", c.Bearer)
			r.URL.Path, r.URL.RawPath = path, ""
			rp.ServeHTTP(w, r)
		})}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + host + prefix, func() { _ = srv.Close() }, nil
}

// isACHCredential reports an ACH key (ek-/pk-) or the launch credential
// itself, bare or as a Bearer value.
func isACHCredential(vs []string, bearer string) bool {
	for _, v := range vs {
		v = strings.TrimSpace(v)
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			v = strings.TrimSpace(v[7:])
		}
		if strings.HasPrefix(v, "ek-") || strings.HasPrefix(v, "pk-") || v == bearer {
			return true
		}
	}
	return false
}

// writeAnthropicError answers in the Anthropic error shape, which the tool
// shows to the user: the proxy cannot write to a terminal the tool is
// drawing on.
func writeAnthropicError(w http.ResponseWriter, status int, typ, msg string) {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

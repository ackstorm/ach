// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
)

const (
	oauthChainTTL        = 10 * time.Minute
	oauthBrokerClientTTL = 30 * 24 * time.Hour
	hintTTL              = 600 * time.Second
)

type oauthChain struct {
	oauthPending
	PendingID string `json:"pending_id"`
	Sub       string `json:"sub"`
	UserID    string `json:"user_id"`
	Broker    string `json:"broker"`
	Audience  string `json:"audience"`
}

type brokerEndpoints struct {
	Authorization string `json:"authorization_endpoint"`
	Registration  string `json:"registration_endpoint"`
}

func rfc8414URL(issuer string) (string, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid broker issuer")
	}
	u.Path = "/.well-known/oauth-authorization-server" + strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func (d OAuthDeps) brokerMetadata(ctx context.Context, issuer string) (brokerEndpoints, error) {
	endpoint, err := rfc8414URL(issuer)
	if err != nil {
		return brokerEndpoints{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return brokerEndpoints{}, err
	}
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return brokerEndpoints{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return brokerEndpoints{}, fmt.Errorf("broker metadata: %s", resp.Status)
	}
	var out brokerEndpoints
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Authorization == "" || out.Registration == "" {
		return brokerEndpoints{}, errors.New("broker metadata: missing endpoint")
	}
	return out, nil
}

func (d OAuthDeps) brokerClientID(ctx context.Context, issuer, registration string) (string, error) {
	var cached struct {
		ClientID string `json:"client_id"`
	}
	if ok, err := d.Store.Get(ctx, "brokerclient", issuer, &cached); err == nil && ok && cached.ClientID != "" {
		return cached.ClientID, nil
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "ACH",
		"redirect_uris":              []string{d.brokerCallbackURL()},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registration, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("broker register: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&cached); err != nil || cached.ClientID == "" {
		return "", errors.New("broker register: no client_id")
	}
	_ = d.Store.Put(ctx, "brokerclient", issuer, cached, oauthBrokerClientTTL)
	return cached.ClientID, nil
}

func (d OAuthDeps) brokerCallbackURL() string {
	return strings.TrimRight(d.Issuer, "/") + "/platform/oauth/broker-callback"
}

func (d OAuthDeps) httpClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (d OAuthDeps) chainStart(w http.ResponseWriter, r *http.Request, c oauthChain) {
	meta, err := d.brokerMetadata(r.Context(), c.Broker)
	if err == nil {
		var clientID string
		clientID, err = d.brokerClientID(r.Context(), c.Broker, meta.Registration)
		if err == nil {
			chainID, sidErr := NewSessionID()
			if sidErr != nil {
				err = sidErr
			} else if err = d.Store.Put(r.Context(), "chain", chainID, c, oauthChainTTL); err == nil {
				verifier := oauth2.GenerateVerifier()
				hint, signErr := d.Signer.Sign(r.Context(), jwt.Claims{Iss: d.Issuer, Sub: c.Sub, Aud: c.Audience, TTL: hintTTL})
				if signErr != nil {
					err = signErr
				} else {
					q := url.Values{
						"response_type":         {"code"},
						"client_id":             {clientID},
						"redirect_uri":          {d.brokerCallbackURL()},
						"state":                 {chainID},
						"code_challenge":        {oauth2.S256ChallengeFromVerifier(verifier)},
						"code_challenge_method": {pkceS256},
						"login_hint":            {hint},
					}
					http.Redirect(w, r, meta.Authorization+"?"+q.Encode(), http.StatusFound)
					return
				}
			}
		}
	}
	d.Auth.Logger.Warn("oauth: consent broker unavailable; issuing without chain", "broker", c.Broker, "err", err)
	d.finish(w, r, c.oauthPending, c.PendingID, c.Sub, c.UserID)
}

func (d OAuthDeps) brokerCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var c oauthChain
	ok, err := d.Store.Take(r.Context(), "chain", q.Get("state"), &c)
	if err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	if !ok {
		htmlError(w, 400, "no authorization is in progress — start again from your client")
		return
	}
	ck, cerr := r.Cookie(bindingCookieName(c.PendingID, d.Auth.InsecureCookie))
	if cerr != nil || subtle.ConstantTimeCompare([]byte(bindingHash(ck.Value)), []byte(c.Binding)) != 1 {
		http.SetCookie(w, bindingCookie(c.PendingID, "", d.Auth.InsecureCookie, -1))
		d.Auth.Logger.Warn("oauth: broker-callback from a browser that did not start the authorization", "client_id", c.ClientID)
		htmlError(w, 400, "this browser did not start the authorization request — start again from your client")
		return
	}
	if e := q.Get("error"); e != "" {
		d.Auth.Logger.Info("oauth: broker declined", "mcp_key", c.MCPKey, "error", e)
	}
	if d.probe(r.Context(), c.Sub, c.UserID, c.MCPKey) != probeOK {
		http.SetCookie(w, bindingCookie(c.PendingID, "", d.Auth.InsecureCookie, -1))
		htmlError(w, http.StatusBadGateway, "the consent broker did not grant access to this backend")
		return
	}
	d.finish(w, r, c.oauthPending, c.PendingID, c.Sub, c.UserID)
}

func (d OAuthDeps) finish(w http.ResponseWriter, r *http.Request, p oauthPending, pendingID, email, userID string) {
	http.SetCookie(w, bindingCookie(pendingID, "", d.Auth.InsecureCookie, -1))
	code, err := NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	if err := d.Store.Put(r.Context(), "code", code, oauthCode{oauthPending: p, Sub: email, UserID: userID}, oauthCodeTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	pv := url.Values{"code": {code}}
	if p.State != "" {
		pv.Set("state", p.State)
	}
	d.Auth.Logger.Info("oauth: authorization code issued", "client_id", p.ClientID, "mcp_key", p.MCPKey)
	clientRedirect(w, r, p.RedirectURI, pv)
}

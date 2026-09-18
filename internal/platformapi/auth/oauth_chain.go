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
	"github.com/ackstorm/ach/internal/platformapi/auth/cli"
)

const (
	oauthChainTTL        = 10 * time.Minute
	oauthBrokerClientTTL = 30 * 24 * time.Hour
	// hintTTL bounds the login_hint handed to a broker: one chain step, not a
	// session (alitellm-auth HINT_TTL = 600).
	hintTTL = 600 * time.Second
)

// oauthChain is a pending authorization parked while the browser is at a
// service broker. Todo[0] is the service being consented to right now.
type oauthChain struct {
	oauthPending
	PendingID string   `json:"pending_id"` // the binding cookie's name suffix
	Sub       string   `json:"sub"`
	UserID    string   `json:"user_id"`
	Todo      []string `json:"todo"`
	// Verifier is unused today: the broker's code is never redeemed (the
	// projection is the truth). Kept so a revision that redeems it can.
	Verifier string `json:"verifier"`
}

// brokerClientID registers ACH once as a public client of broker (RFC 7591)
// and remembers the id. HTTPClient is a seam for tests (nil → 10s default).
func (d OAuthDeps) brokerClientID(ctx context.Context, broker string) (string, error) {
	var cached struct {
		ClientID string `json:"client_id"`
	}
	if ok, err := d.Store.Get(ctx, "brokerclient", broker, &cached); err == nil && ok && cached.ClientID != "" {
		return cached.ClientID, nil
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "ACH",
		"redirect_uris":              []string{d.brokerCallbackURL()},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, broker+"/register", bytes.NewReader(body))
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
	_ = d.Store.Put(ctx, "brokerclient", broker, cached, oauthBrokerClientTTL) // best effort: a miss re-registers
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

// chainNext sends the browser to the broker of c.Todo[0]. A broker we
// cannot register with is skipped (logged) — the token simply lacks that
// scope — so one dead broker never blocks the whole login.
func (d OAuthDeps) chainNext(w http.ResponseWriter, r *http.Request, c oauthChain) {
	for len(c.Todo) > 0 {
		key := c.Todo[0]
		svc := d.Services[key]
		clientID, err := d.brokerClientID(r.Context(), svc.Broker)
		if err != nil {
			d.Auth.Logger.Warn("oauth: broker unreachable, skipping scope", "scope", key, "broker", svc.Broker, "err", err)
			c.Todo = c.Todo[1:]
			continue
		}
		chainID, err := cli.NewSessionID()
		if err != nil {
			htmlError(w, 500, "")
			return
		}
		c.Verifier = oauth2.GenerateVerifier()
		if err := d.Store.Put(r.Context(), "chain", chainID, c, oauthChainTTL); err != nil {
			htmlError(w, 500, "store unavailable")
			return
		}
		// Who this ceremony is for: the browser reaches the broker with no
		// header of ours, and the account it then picks at the provider need
		// not carry our email (Zoho names none), so the broker keys the grant
		// by this instead — signed by us, aud = the broker's own store name so
		// it verifies nowhere else, short-lived.
		hint, err := d.Signer.Sign(r.Context(), jwt.Claims{Iss: d.Issuer, Sub: c.Sub, Aud: svc.Store, TTL: hintTTL})
		if err != nil {
			htmlError(w, 500, "signer not loaded")
			return
		}
		q := url.Values{
			"response_type":         {"code"},
			"client_id":             {clientID},
			"redirect_uri":          {d.brokerCallbackURL()},
			"scope":                 {svc.Store},
			"state":                 {chainID},
			"code_challenge":        {oauth2.S256ChallengeFromVerifier(c.Verifier)},
			"code_challenge_method": {pkceS256},
			"login_hint":            {hint},
		}
		http.Redirect(w, r, svc.Broker+"/authorize?"+q.Encode(), http.StatusFound)
		return
	}
	d.finish(w, r, c.oauthPending, c.PendingID, c.Sub, c.UserID)
}

// brokerCallback: back from a service broker. A `code` means the broker ran
// the provider consent and stored the grant before minting it; the
// projection is what we trust, so the code is not redeemed. An `error`
// means the user declined: the token is issued without that scope.
func (d OAuthDeps) brokerCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var c oauthChain
	ok, err := d.Store.Take(r.Context(), "chain", q.Get("state"), &c) // burned whatever follows
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
		d.Auth.Logger.Info("oauth: broker declined", "scope", c.Todo[0], "error", e)
	}
	c.Todo = c.Todo[1:]
	d.chainNext(w, r, c)
}

// finish mints the client's authorization code and clears the binding
// cookie — the end of both the plain and the chained ceremony.
func (d OAuthDeps) finish(w http.ResponseWriter, r *http.Request, p oauthPending, pendingID, email, userID string) {
	http.SetCookie(w, bindingCookie(pendingID, "", d.Auth.InsecureCookie, -1))
	code, err := cli.NewSessionID()
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
	d.Auth.Logger.Info("oauth: authorization code issued", "client_id", p.ClientID, "scopes", strings.Join(p.Scopes, " "))
	clientRedirect(w, r, p.RedirectURI, pv)
}

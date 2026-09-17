// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/platformapi/auth/cli"
)

const (
	oauthPendingTTL = 10 * time.Minute
	oauthCodeTTL    = 2 * time.Minute
	pkceS256        = "S256"
)

// oauthPending is an /authorize request parked while the browser is at Dex.
// Its id travels as Dex's `state`, so no cookie is involved and two clients
// may authorize concurrently in one browser.
type oauthPending struct {
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	State         string `json:"state"`
	CodeChallenge string `json:"code_challenge"`
	DexVerifier   string `json:"dex_verifier"`
}

// oauthCode is a single-use authorization code, bound to everything the
// token endpoint must re-check.
type oauthCode struct {
	oauthPending
	Sub    string `json:"sub"`     // lower-cased email
	UserID string `json:"user_id"` // LiteLLM user id from provisionUser
}

func clientRedirect(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	http.Redirect(w, r, redirectURI+sep+params.Encode(), http.StatusFound)
}

func htmlError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<h1>Authorization failed</h1><p>%s</p>", html.EscapeString(msg))
}

func (d OAuthDeps) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var client oauthClient
	ok, err := d.Store.Get(r.Context(), "client", q.Get("client_id"), &client)
	if err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	if !ok {
		htmlError(w, 400, "unknown client_id")
		return
	}
	redirectURI := q.Get("redirect_uri")
	registered := false
	for _, u := range client.RedirectURIs {
		if redirectMatches(u, redirectURI) {
			registered = true
			break
		}
	}
	if !registered {
		// Never bounce to an address the client did not register.
		htmlError(w, 400, "redirect_uri is not registered for this client")
		return
	}
	state := q.Get("state")
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != pkceS256 || q.Get("code_challenge") == "" {
		p := url.Values{"error": {"invalid_request"}, "error_description": {"response_type=code with PKCE S256 is required"}}
		if state != "" {
			p.Set("state", state)
		}
		clientRedirect(w, r, redirectURI, p)
		return
	}
	// `scope` and `resource` (RFC 8707) are accepted and ignored: one
	// audience, authorization lives in precheck.
	pendingID, err := cli.NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	dexVerifier := oauth2.GenerateVerifier()
	p := oauthPending{ClientID: client.ClientID, RedirectURI: redirectURI, State: state,
		CodeChallenge: q.Get("code_challenge"), DexVerifier: dexVerifier}
	if err := d.Store.Put(r.Context(), "pending", pendingID, p, oauthPendingTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	http.Redirect(w, r, d.dexLogin(pendingID, dexVerifier), http.StatusFound)
}

func (d OAuthDeps) asCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var p oauthPending
	ok, err := d.Store.Take(r.Context(), "pending", q.Get("state"), &p)
	if err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	if !ok {
		htmlError(w, 400, "no authorization request is pending — start again from your client")
		return
	}
	if e := q.Get("error"); e != "" {
		pv := url.Values{"error": {"access_denied"}}
		if p.State != "" {
			pv.Set("state", p.State)
		}
		clientRedirect(w, r, p.RedirectURI, pv)
		return
	}
	email, err := d.dexExchange(r.Context(), q.Get("code"), p.DexVerifier)
	if err != nil || email == "" {
		d.Auth.Logger.Warn("oauth: dex callback failed", "err", err)
		htmlError(w, 400, "the identity provider did not complete the login")
		return
	}
	email = strings.ToLower(strings.TrimSpace(email))
	userID, err := d.provision(r.Context(), email)
	if err != nil {
		htmlError(w, 503, "user provisioning failed")
		return
	}
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
	d.Auth.Logger.Info("oauth: authorization code issued", "client_id", p.ClientID)
	clientRedirect(w, r, p.RedirectURI, pv)
}

// --- Dex leg: the real thing, behind the seams ------------------------------

func (d OAuthDeps) dexConfig() *oauth2.Config {
	cfg := *d.Auth.OAuth2Cfg // copy: only the redirect differs from the console's
	cfg.RedirectURL = strings.TrimRight(d.Issuer, "/") + "/platform/oauth/as-callback"
	return &cfg
}

func (d OAuthDeps) dexLogin(state, verifier string) string {
	if d.DexLogin != nil {
		return d.DexLogin(state, verifier)
	}
	return d.dexConfig().AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

func (d OAuthDeps) dexExchange(ctx context.Context, code, verifier string) (string, error) {
	if d.DexExchange != nil {
		return d.DexExchange(ctx, code, verifier)
	}
	tok, err := d.dexConfig().Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", err
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return "", errors.New("no id_token in the Dex response")
	}
	idt, err := d.Auth.IDTokenVerifier.Verify(ctx, rawID)
	if err != nil {
		return "", err
	}
	var claims idTokenClaims
	if err := idt.Claims(&claims); err != nil {
		return "", err
	}
	return claims.Email, nil
}

func (d OAuthDeps) provision(ctx context.Context, email string) (string, error) {
	if d.Provision != nil {
		return d.Provision(ctx, email)
	}
	return provisionUser(ctx, d.Auth, email)
}

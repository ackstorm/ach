// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	oauthPendingTTL = 10 * time.Minute
	oauthCodeTTL    = 2 * time.Minute
	pkceS256        = "S256"
)

// oauthPending is an /authorize request parked while the browser is at Dex.
// Its id travels as Dex's `state`; the browser that started it is bound by
// a per-pending cookie (see bindingCookieName) whose SHA-256 is Binding.
// Per-pending names let two clients authorize concurrently in one browser.
//
// Without the binding, whoever completes the Dex login for a given `state`
// is the identity the code is minted for — an attacker could start
// /authorize for THEIR client, hand the Dex URL to a victim, and redeem the
// resulting code with their own PKCE verifier (login CSRF / code injection).
type oauthPending struct {
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	State         string `json:"state"`
	CodeChallenge string `json:"code_challenge"`
	DexVerifier   string `json:"dex_verifier"`
	Binding       string `json:"binding"` // hex(sha256(browser cookie value))
	MCPKey        string `json:"mcp_key,omitempty"`
	// DeviceCode is set on a pending parked by the device verification page
	// (RFC 8628); it has no RedirectURI and no client PKCE — the outcome is
	// written to the device record for /token to pick up.
	DeviceCode string `json:"device_code,omitempty"`
}

// bindingCookieName is the per-pending browser-binding cookie. __Host- on an
// https base (Path=/, Secure, no Domain, browser-enforced); the plain name on
// a plain-http base, like the SSO cookie (Deps.InsecureCookie). pendingID is
// base64url, so the name needs no escaping.
func bindingCookieName(pendingID string, insecure bool) string {
	if insecure {
		return "ach_oauth_" + pendingID
	}
	return "__Host-ach_oauth_" + pendingID
}

// bindingCookie builds the Set-Cookie for one pending authorization. Lax,
// not Strict: /as-callback is reached by a top-level GET redirected from
// Dex, which may live on another site; Lax still withholds the cookie from
// cross-site sub-requests and POSTs. maxAge<0 deletes it.
func bindingCookie(pendingID, value string, insecure bool, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     bindingCookieName(pendingID, insecure),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   !insecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

func bindingHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
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
	mcpKey := mcpKeyFromResource(q["resource"], d.Issuer)
	pendingID, err := NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	dexVerifier := oauth2.GenerateVerifier()
	binding, err := NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	p := oauthPending{ClientID: client.ClientID, RedirectURI: redirectURI, State: state,
		CodeChallenge: q.Get("code_challenge"), DexVerifier: dexVerifier, Binding: bindingHash(binding), MCPKey: mcpKey}
	if err := d.Store.Put(r.Context(), "pending", pendingID, p, oauthPendingTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	http.SetCookie(w, bindingCookie(pendingID, binding, d.Auth.InsecureCookie, int(oauthPendingTTL.Seconds())))
	http.Redirect(w, r, d.dexLogin(pendingID, dexVerifier), http.StatusFound)
}

func (d OAuthDeps) asCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pendingID := q.Get("state")
	var p oauthPending
	// Take first: the pending is burned whether or not the binding matches,
	// so a stolen Dex URL is spent by the first arrival either way.
	ok, err := d.Store.Take(r.Context(), "pending", pendingID, &p)
	if err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	if !ok {
		htmlError(w, 400, "no authorization request is pending — start again from your client")
		return
	}
	c, cerr := r.Cookie(bindingCookieName(pendingID, d.Auth.InsecureCookie))
	if cerr != nil || subtle.ConstantTimeCompare([]byte(bindingHash(c.Value)), []byte(p.Binding)) != 1 {
		http.SetCookie(w, bindingCookie(pendingID, "", d.Auth.InsecureCookie, -1))
		d.Auth.Logger.Warn("oauth: as-callback from a browser that did not start the authorization", "client_id", p.ClientID)
		htmlError(w, 400, "this browser did not start the authorization request — start again from your client")
		return
	}
	if e := q.Get("error"); e != "" {
		if p.DeviceCode != "" {
			d.deviceFinish(w, r, p, pendingID, deviceStatusDenied, "", "")
			return
		}
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
	if p.DeviceCode != "" {
		d.deviceFinish(w, r, p, pendingID, deviceStatusApproved, email, userID)
		return
	}
	if p.MCPKey != "" {
		bip, berr := d.consentBIP(r.Context(), p.MCPKey)
		if berr != nil {
			d.Auth.Logger.Warn("oauth: consent policy lookup failed; issuing without chain", "err", berr)
		} else if bip != nil && d.probe(r.Context(), email, userID, p.MCPKey) == probeAuthRequired {
			d.chainStart(w, r, oauthChain{oauthPending: p, PendingID: pendingID, Sub: email, UserID: userID, Broker: bip.ConsentBroker, Audience: bip.ConsentAudience})
			return
		}
	}
	d.finish(w, r, p, pendingID, email, userID)
}

var mcpKeyRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func mcpKeyFromResource(resources []string, base string) string {
	prefix := strings.TrimRight(base, "/") + "/mcp/"
	for _, resource := range resources {
		if !strings.HasPrefix(resource, prefix) {
			continue
		}
		key, _, _ := strings.Cut(strings.TrimPrefix(resource, prefix), "/")
		if mcpKeyRe.MatchString(key) {
			return key
		}
	}
	return ""
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

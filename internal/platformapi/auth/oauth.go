// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/oauthsvc"
)

// OAuthDeps is what the OAuth 2.1 authorization server needs beyond
// auth.Deps (the Dex leg, LiteLLM, the pk_ mint). Wired in server.go from
// platform-api's config; tests build it by hand and fill the seams.
type OAuthDeps struct {
	Auth       Deps
	Store      *OAuthStore
	Signer     *jwt.Ed25519Signer
	Issuer     string // ACH_BASE_URL — what clients dial and what `iss` says
	Audience   string // ACH_OAUTH_AUDIENCE, default "ach"
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Now        func() time.Time

	// Services is the MCP-service map (ACH_OAUTH_SERVICES); nil → no scopes
	// beyond the audience, no broker chain. Grants reads the brokers' grant
	// projection; required when Services is non-empty.
	Services map[string]oauthsvc.Service
	Grants   GrantReader
	// HTTPClient is a seam for tests calling a broker's /register; nil → a
	// 10s stdlib client.
	HTTPClient *http.Client

	// Seams. nil → the real thing: the oauth2+oidc Dex leg, provisionUser,
	// Auth.MintPK, db.ActiveOAuthPK, db.RevokePersonalKey + LiteLLM revoke.
	DexLogin      func(state, pkceVerifier string) string
	DexExchange   func(ctx context.Context, code, pkceVerifier string) (email string, err error)
	Provision     func(ctx context.Context, email string) (userID string, err error)
	Mint          func(ctx context.Context, email, userID, purpose string) (string, db.PkInsertRow, error)
	OAuthPKLookup func(ctx context.Context, email string) (*db.PkKeyInfo, error)
	OAuthPKRevoke func(ctx context.Context, keyID string) error
}

// MountOAuth registers the AS endpoints under /platform/oauth. Mounted
// OUTSIDE the Authn group: every one of them is reached by a client that
// does not yet hold a credential.
func MountOAuth(d OAuthDeps) func(chi.Router) {
	if d.Now == nil {
		d.Now = time.Now
	}
	return func(r chi.Router) {
		r.Post("/register", d.register)
		r.Get("/authorize", d.authorize)
		r.Get("/as-callback", d.asCallback)
		r.Get("/broker-callback", d.brokerCallback)
		r.Post("/token", d.token)
	}
}

func isLoopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "https" && u.Hostname() != "") || isLoopback(raw)
}

// redirectMatches: exact, except a loopback redirect may change port
// (RFC 8252 §7.3) — every client in the conformance doc picks a random port.
func redirectMatches(registered, presented string) bool {
	if registered == presented {
		return true
	}
	if !isLoopback(registered) || !isLoopback(presented) {
		return false
	}
	a, _ := url.Parse(registered)
	b, _ := url.Parse(presented)
	return a.Hostname() == b.Hostname() && a.Path == b.Path && a.RawQuery == b.RawQuery
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

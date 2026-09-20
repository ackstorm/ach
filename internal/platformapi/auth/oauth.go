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
)

// OAuthDeps is what the OAuth 2.1 authorization server needs beyond
// auth.Deps (the Dex leg, LiteLLM, the pk_ mint). Wired in server.go from
// platform-api's config; tests build it by hand and fill the seams.
type OAuthDeps struct {
	Auth       Deps
	Store      *OAuthStore
	Signer     *jwt.Ed25519Signer
	Issuer     string // ACH_BASE_URL — what clients dial and what `iss` says
	Audience   string // fixed ACH OAuth audience (`ach`)
	Namespace  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Now        func() time.Time

	// HTTPClient is a seam for tests calling broker metadata/registration;
	// nil → a 10s stdlib client.
	HTTPClient *http.Client
	ConsentBIP func(ctx context.Context, key string) (*db.BIPRow, error)
	Probe      func(ctx context.Context, email, userID, key string) probeOutcome

	// Seams. nil → the real thing: the oauth2+oidc Dex leg, provisionUser,
	// Auth.MintPK, db.ActiveOAuthPK, db.RevokePersonalKey + LiteLLM revoke.
	DexLogin      func(state, pkceVerifier string) string
	DexExchange   func(ctx context.Context, code, pkceVerifier string) (email string, err error)
	Provision     func(ctx context.Context, email string) (userID string, err error)
	Mint          func(ctx context.Context, email, userID, purpose string) (string, db.PkInsertRow, error)
	OAuthPKLookup func(ctx context.Context, email string) (*db.PkKeyInfo, error)
	OAuthPKRevoke func(ctx context.Context, keyID string) error
}

func (d OAuthDeps) consentBIP(ctx context.Context, key string) (*db.BIPRow, error) {
	if d.ConsentBIP != nil {
		return d.ConsentBIP(ctx, key)
	}
	return db.ConsentBIP(ctx, d.Auth.Pool, d.Namespace, key)
}

func (d OAuthDeps) probe(ctx context.Context, email, userID, key string) probeOutcome {
	if d.Probe != nil {
		return d.Probe(ctx, email, userID, key)
	}
	return d.probeGrant(ctx, email, userID, key)
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
		r.Post("/device_authorization", d.deviceAuthorization)
		r.Get("/device", d.devicePage)
		r.Post("/device", d.devicePage)
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

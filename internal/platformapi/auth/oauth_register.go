// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// oauthClient is a DCR registration. Public clients only (RFC 7591).
type oauthClient struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name,omitempty"`
	RedirectURIs []string `json:"redirect_uris"`
	IssuedAt     int64    `json:"client_id_issued_at"`
}

const oauthClientTTL = 90 * 24 * time.Hour

// /register is anonymous and every accepted request stores a record for
// oauthClientTTL in the Valkey that holds the refresh tokens, so what it may
// store is bounded here. A client registers once and keeps the client_id:
// ach-cli per profile, the OpenCode plugin per issuer, an MCP client per
// server, so a login or a token refresh never comes through this route.
const (
	// A real registration is a few hundred bytes: ach-cli sends 113, the
	// OpenCode plugin 187.
	registerMaxBody       = 4 << 10
	registerMaxClientName = 256

	// One address, best-effort: client identity is proxy-supplied. The burst
	// fits a user wiring up every MCP server of an Environment at once, and
	// make e2e-full, which registers 71 clients from one address, without
	// counting on the refill. The refill carries an office behind one egress
	// address through a day of first-time setups.
	registerClientRate  = 0.1
	registerClientBurst = 80
	// All addresses, per replica: the hard bound on what is stored. 17 280
	// records a day × 4 KiB × 90 days retains about 5.9 GiB per replica.
	// Onboarding 1000 people at a dozen clients each fits one day of it.
	registerGlobalRate  = 0.2
	registerGlobalBurst = 300

	// registerRetryAfter is seconds until a token is back in a client's
	// bucket: 1/registerClientRate.
	registerRetryAfter = 10
)

func registerRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(registerRetryAfter))
	oauthError(w, http.StatusTooManyRequests, "rate_limited", "too many client registrations")
}

func (d OAuthDeps) register(w http.ResponseWriter, r *http.Request) {
	if !d.registerLimit.AllowClient(middleware.ClientIP(r)) {
		registerRateLimited(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, registerMaxBody)
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			oauthError(w, http.StatusRequestEntityTooLarge, "invalid_client_metadata", "body too large")
			return
		}
		oauthError(w, 400, "invalid_client_metadata", "body must be JSON")
		return
	}
	if len(body.ClientName) > registerMaxClientName {
		oauthError(w, 400, "invalid_client_metadata", "client_name too long")
		return
	}
	if len(body.RedirectURIs) == 0 {
		oauthError(w, 400, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, u := range body.RedirectURIs {
		if !d.redirectAllowed(u) {
			oauthError(w, 400, "invalid_redirect_uri", "redirect_uris must be loopback http or https on an allowed host")
			return
		}
	}
	if body.AuthMethod != "" && body.AuthMethod != "none" {
		oauthError(w, 400, "invalid_client_metadata", "only public clients are registered here")
		return
	}
	// The retained bound counts the stored record, so it is held to the cap too.
	c := oauthClient{ClientName: body.ClientName, RedirectURIs: body.RedirectURIs, IssuedAt: d.Now().Unix()}
	if rec, err := json.Marshal(c); err != nil || len(rec) > registerMaxBody {
		oauthError(w, http.StatusRequestEntityTooLarge, "invalid_client_metadata", "body too large")
		return
	}
	// Last: a refused body stores nothing and must not spend what real
	// registrations depend on.
	if !d.registerLimit.AllowGlobal() {
		registerRateLimited(w)
		return
	}
	id, err := NewSessionID()
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	c.ClientID = id
	if err := d.Store.Put(r.Context(), "client", id, c, oauthClientTTL); err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id": c.ClientID, "client_name": c.ClientName, "redirect_uris": c.RedirectURIs,
		"client_id_issued_at": c.IssuedAt, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token", deviceGrantType}, "response_types": []string{"code"},
	})
}

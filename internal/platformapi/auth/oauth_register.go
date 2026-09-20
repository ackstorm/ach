// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"net/http"
	"time"
)

// oauthClient is a DCR registration. Public clients only (RFC 7591).
type oauthClient struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name,omitempty"`
	RedirectURIs []string `json:"redirect_uris"`
	IssuedAt     int64    `json:"client_id_issued_at"`
}

const oauthClientTTL = 90 * 24 * time.Hour

func (d OAuthDeps) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		oauthError(w, 400, "invalid_client_metadata", "body must be JSON")
		return
	}
	if len(body.RedirectURIs) == 0 {
		oauthError(w, 400, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, u := range body.RedirectURIs {
		if !redirectAllowed(u) {
			oauthError(w, 400, "invalid_redirect_uri", "redirect_uris must be https or loopback http")
			return
		}
	}
	if body.AuthMethod != "" && body.AuthMethod != "none" {
		oauthError(w, 400, "invalid_client_metadata", "only public clients are registered here")
		return
	}
	id, err := NewSessionID() // 192-bit opaque id, same generator as the device-code sessions
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	c := oauthClient{ClientID: id, ClientName: body.ClientName, RedirectURIs: body.RedirectURIs, IssuedAt: d.Now().Unix()}
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

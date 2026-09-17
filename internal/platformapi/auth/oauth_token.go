// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/platformapi/auth/cli"
)

// oauthPKMinRemaining: an OAuth row this close to expiry is replaced at the
// next grant, so a 1h access token never outlives the row behind it.
const oauthPKMinRemaining = 2 * time.Hour

type oauthRefresh struct {
	Sub      string `json:"sub"`
	UserID   string `json:"user_id"`
	ClientID string `json:"client_id"`
}

func pkceOK(challenge, verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func (d OAuthDeps) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "form body required")
		return
	}
	clientID := r.PostForm.Get("client_id")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		var c oauthCode
		ok, err := d.Store.Take(r.Context(), "code", r.PostForm.Get("code"), &c) // burned whatever follows
		if err != nil {
			oauthError(w, 500, "server_error", "")
			return
		}
		if !ok || c.ClientID != clientID || !redirectMatches(c.RedirectURI, r.PostForm.Get("redirect_uri")) ||
			!pkceOK(c.CodeChallenge, r.PostForm.Get("code_verifier")) {
			oauthError(w, 400, "invalid_grant", "")
			return
		}
		d.issue(w, r, c.Sub, c.UserID, clientID)
	case "refresh_token":
		var rf oauthRefresh
		ok, err := d.Store.Take(r.Context(), "refresh", r.PostForm.Get("refresh_token"), &rf) // rotation: the old one is gone
		if err != nil {
			oauthError(w, 500, "server_error", "")
			return
		}
		if !ok || rf.ClientID != clientID {
			oauthError(w, 400, "invalid_grant", "")
			return
		}
		d.issue(w, r, rf.Sub, rf.UserID, clientID)
	default:
		oauthError(w, 400, "unsupported_grant_type", "")
	}
}

func (d OAuthDeps) issue(w http.ResponseWriter, r *http.Request, sub, userID, clientID string) {
	if err := d.ensureOAuthPK(r.Context(), sub, userID); err != nil {
		if errors.Is(err, ErrMintLiteLLM) {
			oauthError(w, 503, "temporarily_unavailable", "litellm unreachable")
			return
		}
		d.Auth.Logger.Error("oauth: ensure pk_ failed", "err", err)
		oauthError(w, 500, "server_error", "")
		return
	}
	access, err := d.Signer.Sign(r.Context(), jwt.Claims{Iss: d.Issuer, Sub: sub, Aud: d.Audience, Email: sub, TTL: d.AccessTTL})
	if err != nil {
		oauthError(w, 500, "server_error", "signer not loaded")
		return
	}
	refresh, err := cli.NewSessionID()
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	if err := d.Store.Put(r.Context(), "refresh", refresh, oauthRefresh{Sub: sub, UserID: userID, ClientID: clientID}, d.RefreshTTL); err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(d.AccessTTL / time.Second),
		"refresh_token": refresh,
	})
}

// ensureOAuthPK guarantees one live purpose='oauth' row for sub: reuse it,
// or revoke the old and mint a fresh one — in THAT order, because the
// partial unique index (migration 000020) allows one active oauth row per
// owner. If the mint then fails the user has no row until the next grant,
// which is the same state as a first login. The pk_ plaintext is discarded —
// the forwarder resolves the row by owner_email, never by presenting it.
func (d OAuthDeps) ensureOAuthPK(ctx context.Context, sub, userID string) error {
	cur, err := d.lookupOAuthPK(ctx, sub)
	if err != nil {
		return err
	}
	if cur != nil && cur.ExpiresAt.After(d.Now().Add(oauthPKMinRemaining)) {
		return nil
	}
	if cur != nil {
		if err := d.revokeOAuthPK(ctx, cur.KeyID); err != nil {
			return err
		}
	}
	_, _, err = d.mint(ctx, sub, userID, "oauth")
	return err
}

func (d OAuthDeps) lookupOAuthPK(ctx context.Context, email string) (*db.PkKeyInfo, error) {
	if d.OAuthPKLookup != nil {
		return d.OAuthPKLookup(ctx, email)
	}
	return db.ActiveOAuthPK(ctx, d.Auth.Pool, email)
}

func (d OAuthDeps) revokeOAuthPK(ctx context.Context, keyID string) error {
	if d.OAuthPKRevoke != nil {
		return d.OAuthPKRevoke(ctx, keyID)
	}
	info, err := db.RevokePersonalKey(ctx, d.Auth.Pool, keyID)
	if err != nil || info == nil || info.LiteLLMToken == nil {
		return err
	}
	if err := d.Auth.LiteLLM.RevokeKey(ctx, *info.LiteLLMToken); err != nil {
		// The row is revoked (fail-closed for ACH); the LiteLLM key lingers
		// until its own expiry. Log, don't block the grant.
		d.Auth.Logger.Warn("oauth: litellm revoke of the previous oauth pk_ failed", "key_id", keyID, "err", err)
	}
	return nil
}

func (d OAuthDeps) mint(ctx context.Context, email, userID, purpose string) (string, db.PkInsertRow, error) {
	if d.Mint != nil {
		return d.Mint(ctx, email, userID, purpose)
	}
	return d.Auth.MintPK(ctx, email, userID, purpose)
}

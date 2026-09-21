// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
)

// oauthPKMinRemaining: an OAuth row this close to expiry is replaced at the
// next grant, so a 1h access token never outlives the row behind it.
const oauthPKMinRemaining = 2 * time.Hour

type oauthRefresh struct {
	oauthUser
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
		d.issue(w, r, c.oauthUser, clientID)
	case "refresh_token":
		d.refreshToken(w, r, clientID)
	case deviceGrantType:
		d.deviceToken(w, r, clientID)
	default:
		oauthError(w, 400, "unsupported_grant_type", "")
	}
}

// refreshToken is the refresh_token grant. The identity provider is asked
// first: the user's Dex refresh token (one per user, shared by all their
// sessions) is replayed at Dex, and only a user the IdP still honours gets
// a new pair. A refusal ends every session of that user — the Dex token
// and the oauth pk_ go, so each JWT dies at its own expiry and each other
// session fails its next refresh without asking Dex — and the client is
// told invalid_grant, which sends it back to login (where the IdP says
// no). Dex unreachable is a 503 and the presented token stays valid.
func (d OAuthDeps) refreshToken(w http.ResponseWriter, r *http.Request, clientID string) {
	presented := r.PostForm.Get("refresh_token")
	var rf oauthRefresh
	ok, err := d.Store.Get(r.Context(), "refresh", presented, &rf)
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	if !ok || rf.ClientID != clientID {
		oauthError(w, 400, "invalid_grant", "")
		return
	}
	if err := d.revalidateAtIdP(r.Context(), rf.Sub); err != nil {
		if errors.Is(err, errIdPRefused) {
			_ = d.Store.Del(r.Context(), "refresh", presented)
			oauthError(w, 400, "invalid_grant", "the identity provider no longer honours this session")
			return
		}
		if errors.Is(err, ErrIdPUnreachable) {
			oauthError(w, 503, "temporarily_unavailable", "identity provider unreachable")
			return
		}
		oauthError(w, 500, "server_error", "")
		return
	}
	// rotation: the old one is gone; a concurrent refresh already took it
	if ok, err := d.Store.Take(r.Context(), "refresh", presented, &rf); err != nil || !ok {
		oauthError(w, 400, "invalid_grant", "")
		return
	}
	d.issue(w, r, rf.oauthUser, clientID)
}

// errIdPRefused: the identity provider no longer honours the user — every
// session of that user is over (the Dex token and the oauth pk_ are gone).
var errIdPRefused = errors.New("oauth: identity provider refused the user")

// ErrIdPUnreachable: Dex did not answer; the presented credential stays valid.
var ErrIdPUnreachable = errors.New("identity provider unreachable")

// revalidateAtIdP replays the user's shared Dex refresh token (D-22) — the
// one check both the /token refresh grant and the console session run.
// nil: the IdP still honours the user and the rotated token is stored.
// errIdPRefused: sessions ended (Dex token deleted, oauth pk_ revoked).
// ErrIdPUnreachable: Dex did not answer — nothing changed, retry later.
func (d OAuthDeps) revalidateAtIdP(ctx context.Context, sub string) error {
	var dexRefresh string
	if ok, err := d.Store.Get(ctx, dexRefreshKind, sub, &dexRefresh); err != nil {
		return err
	} else if !ok {
		return errIdPRefused
	}
	rotated, err := d.dexRefresh(ctx, dexRefresh)
	if err != nil {
		if !dexDenied(err) {
			d.Auth.Logger.Warn("oauth: dex refresh unreachable", "err", err)
			return fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
		}
		d.Auth.Logger.Info("oauth: identity provider refused the refresh; sessions ended", "sub", sub, "err", err)
		_ = d.Store.Del(ctx, dexRefreshKind, sub)
		if cur, lerr := d.lookupOAuthPK(ctx, sub); lerr == nil && cur != nil {
			if rerr := d.revokeOAuthPK(ctx, cur.KeyID); rerr != nil {
				d.Auth.Logger.Error("oauth: revoke of the oauth pk_ after IdP refusal failed", "key_id", cur.KeyID, "err", rerr)
			}
		}
		return errIdPRefused
	}
	return d.Store.Put(ctx, dexRefreshKind, sub, rotated, d.RefreshTTL)
}

func (d OAuthDeps) issue(w http.ResponseWriter, r *http.Request, u oauthUser, clientID string) {
	sub, userID := u.Sub, u.UserID
	var err error
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
	refresh, err := NewSessionID()
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	if err := d.Store.Put(r.Context(), "refresh", refresh, oauthRefresh{oauthUser: u, ClientID: clientID}, d.RefreshTTL); err != nil {
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

// ensureOAuthPK guarantees one live purpose='oauth' row for sub: reuse it
// when LiteLLM still has its key, or revoke the old and mint a fresh one —
// in THAT order, because the partial unique index (migration 000020)
// allows one active oauth row per owner. If the mint then fails the user
// has no row until the next grant, which is the same state as a first
// login. The pk_ plaintext is discarded — the forwarder resolves the row by
// owner_email, never by presenting it.
//
// The LiteLLM check is one GET /key/list per token issue (login and each
// refresh): a row whose key vanished from LiteLLM (deleted in its UI, a
// second release on another LiteLLM sharing this DB, a reset) would
// otherwise be replayed forever as "Invalid proxy server token".
func (d OAuthDeps) ensureOAuthPK(ctx context.Context, sub, userID string) error {
	cur, err := d.lookupOAuthPK(ctx, sub)
	if err != nil {
		return err
	}
	if cur != nil && cur.ExpiresAt.After(d.Now().Add(oauthPKMinRemaining)) {
		live, err := d.liteLLMHasKey(ctx, cur)
		if err != nil {
			return err
		}
		if live {
			return nil
		}
		d.Auth.Logger.Warn("oauth: litellm no longer has the oauth pk_ key; re-minting", "key_id", cur.KeyID)
	}
	if cur != nil {
		if err := d.revokeOAuthPK(ctx, cur.KeyID); err != nil {
			return err
		}
	}
	_, _, err = d.mint(ctx, sub, userID, "oauth")
	return err
}

// liteLLMHasKey reports whether LiteLLM still lists the row's key under
// its user. A LiteLLM failure is ErrMintLiteLLM: the grant answers 503
// like a failed mint.
func (d OAuthDeps) liteLLMHasKey(ctx context.Context, row *db.PkKeyInfo) (bool, error) {
	if row.LiteLLMUserID == nil || row.LiteLLMToken == nil {
		return false, nil
	}
	keys, err := d.Auth.LiteLLM.ListUserKeys(ctx, *row.LiteLLMUserID)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrMintLiteLLM, err)
	}
	for _, k := range keys {
		if k.Token == *row.LiteLLMToken {
			return true, nil
		}
	}
	return false, nil
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

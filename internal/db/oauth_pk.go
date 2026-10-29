// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ActiveOAuthPK returns the user's single active purpose='oauth' row, with
// the resolve-path columns populated (litellm_key_material_enc included) so
// the caller can build a KeyInfo without a plaintext. nil, nil when there is
// none — the token endpoint then mints one. Does NOT extend expires_at: the
// OAuth row's lifetime is managed at /platform/oauth/token, not per request.
// An expired row is not returned: this is the resolve path.
func ActiveOAuthPK(ctx context.Context, pool *pgxpool.Pool, ownerEmail string) (*PkKeyInfo, error) {
	return oauthPK(ctx, pool, ownerEmail, `AND expires_at > now()`)
}

// ActiveOAuthPKAnyExpiry is ActiveOAuthPK without the expiry filter: the
// row the partial unique index personal_keys_one_active_oauth_per_owner
// (migration 000020) counts. The token endpoint must see an expired-but-
// active row to revoke it before minting, or the mint violates the index.
func ActiveOAuthPKAnyExpiry(ctx context.Context, pool *pgxpool.Pool, ownerEmail string) (*PkKeyInfo, error) {
	return oauthPK(ctx, pool, ownerEmail, "")
}

func oauthPK(ctx context.Context, pool *pgxpool.Pool, ownerEmail, expiryFilter string) (*PkKeyInfo, error) {
	sql := `
		SELECT key_id, owner_email, expires_at, litellm_user_id, litellm_token,
		       litellm_key_material_enc, status, created_at, last_used_at
		  FROM personal_keys
		 WHERE owner_email = $1 AND purpose = 'oauth' AND status = 'active'
		` + expiryFilter
	r := &PkKeyInfo{}
	err := pool.QueryRow(ctx, sql, ownerEmail).Scan(
		&r.KeyID, &r.OwnerEmail, &r.ExpiresAt, &r.LiteLLMUserID, &r.LiteLLMToken,
		&r.LiteLLMKeyMaterial, &r.Status, &r.CreatedAt, &r.LastUsedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

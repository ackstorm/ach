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
func ActiveOAuthPK(ctx context.Context, pool *pgxpool.Pool, ownerEmail string) (*PkKeyInfo, error) {
	const sql = `
		SELECT key_id, owner_email, expires_at, litellm_user_id, litellm_token,
		       litellm_key_material_enc, status, created_at, last_used_at
		  FROM personal_keys
		 WHERE owner_email = $1 AND purpose = 'oauth' AND status = 'active'
		   AND expires_at > now()
	`
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

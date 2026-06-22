// SPDX-License-Identifier: Apache-2.0

// Owner-scoped personal key revocation helper.
//
// RevokePersonalKeyByOwner is the caller-facing counterpart to the admin-only
// RevokePersonalKey: it adds an AND owner_email = $2 predicate so a user can
// revoke ONLY their own active pk_ key. Zero rows updated (wrong owner, absent
// key, or already revoked) returns ErrKeyNotFoundOrNotOwner — a single sentinel
// that deliberately conflates the three cases so the handler can return a
// 404/403 without leaking whether the key exists.

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrKeyNotFoundOrNotOwner is returned by RevokePersonalKeyByOwner when the
// UPDATE matched zero rows: the key does not exist, is not owned by the given
// owner, or is already revoked/expired. The caller MUST NOT distinguish between
// these cases — mapping to 404/403 without leaking existence is the contract.
var ErrKeyNotFoundOrNotOwner = errors.New("db: key not found, not owned by caller, or not active")

// RevokePersonalKeyByOwner atomically flips status='active' → 'revoked' and
// stamps revoked_at = now() for the row matching BOTH key_id AND owner_email.
// It returns the revoked row's litellm_token (nil when the column is NULL) so
// the caller can issue a best-effort LiteLLM RevokeKey without an extra SELECT.
//
// Callers MUST treat ErrKeyNotFoundOrNotOwner as a unified 404/403 — the three
// indistinguishable zero-row cases (wrong owner, missing, already revoked) are
// intentionally collapsed to prevent existence-oracle attacks.
func RevokePersonalKeyByOwner(ctx context.Context, pool *pgxpool.Pool, keyID, owner string) (litellmToken *string, err error) {
	const sql = `
		UPDATE personal_keys
		   SET status     = 'revoked',
		       revoked_at = now()
		 WHERE key_id      = $1
		   AND owner_email = $2
		   AND status      = 'active'
		RETURNING litellm_token
	`
	var tok *string
	if err := pool.QueryRow(ctx, sql, keyID, owner).Scan(&tok); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyNotFoundOrNotOwner
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: RevokePersonalKeyByOwner(%s): %w", keyID, err)
	}
	return tok, nil
}

// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the environment_keys table (Hub §8 / §15).
//
// The Plan 03-08 §8.2 create-flow handler and Plan 03-09 admin handlers
// consume these helpers:
//
//   - InsertEnvironmentKey: §8.2 step 7 row-write inside the create
//     transaction; on failure the handler runs the LiteLLM compensation
//     (RevokeKey on the token returned by step 6).
//   - GetEnvironmentKey: read-by-key_id for admin force-revoke path.
//   - RevokeEnvironmentKey: §8.5 LiteLLM-first revocation — this helper
//     runs AFTER litellm.RevokeKey ack (the handler ordering enforces
//     KEY-08; this helper is the DB step only).
//   - ListEnvironmentKeysByOwner: paginated lister for the user-visible
//     §15.6 endpoint.
//   - ListEnvironmentKeysByOwnerWithFilter: admin variant where nil
//     filter returns all rows for the deployment.

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InsertEnvironmentKey writes a single environment_keys row. status defaults
// to 'active' and created_at defaults to now().
//
// Per D-12 step 7, the handler is responsible for running the LiteLLM
// compensation (RevokeKey) when this returns a non-transient error.
func InsertEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, row EkInsertRow) error {
	const sql = `
		INSERT INTO environment_keys
		    (key_id, credential_hash, environment, owner_email, name,
		     status, litellm_user_id, litellm_token)
		VALUES ($1, $2, $3, $4, $5, 'active', $6, $7)
	`
	if _, err := pool.Exec(ctx, sql,
		row.KeyID, row.CredentialHash, row.Environment, row.OwnerEmail, row.Name,
		row.LiteLLMUserID, row.LiteLLMToken,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: InsertEnvironmentKey(%s): %w", row.KeyID, err)
	}
	return nil
}

// GetEnvironmentKey reads a row by key_id. Returns (nil, nil) on absent.
//
// The returned EkKeyInfo carries CredentialHash (Plan 03-08 needs it to
// derive the keystore cache key "ach:key:" + credential_hash for the §8.5
// revoke flow). The other SELECT-returning helper EkResolve deliberately
// does NOT include credential_hash because the resolver path already has
// the plaintext and can recompute the hash via credhash.Hash.
func GetEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*EkKeyInfo, error) {
	const sql = `
		SELECT key_id, credential_hash, environment, owner_email, name,
		       litellm_user_id, litellm_token,
		       status, created_at, last_used_at, revoked_at
		  FROM environment_keys
		 WHERE key_id = $1
	`
	r := &EkKeyInfo{}
	err := pool.QueryRow(ctx, sql, keyID).Scan(
		&r.KeyID, &r.CredentialHash, &r.Environment, &r.OwnerEmail, &r.Name,
		&r.LiteLLMUserID, &r.LiteLLMToken,
		&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetEnvironmentKey(%s): %w", keyID, err)
	}
	return r, nil
}

// RevokeEnvironmentKey atomically flips status='active' → 'revoked' and
// stamps revoked_at = now(). Returns (nil, nil) when the row is already
// revoked or absent.
//
// Per Hub §8.5 (KEY-08), this is the DB step in the LiteLLM-first
// revocation order — the handler in Plan 03-09 MUST run litellm.RevokeKey
// FIRST and only call this helper after the LiteLLM ack. The LiteLLM-side
// flip is the load-bearing barrier; the DB flip + Redis TTL bound the
// Content Service window.
func RevokeEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*EkKeyInfo, error) {
	const sql = `
		UPDATE environment_keys SET
		    status     = 'revoked',
		    revoked_at = now()
		 WHERE key_id = $1
		   AND status = 'active'
		RETURNING key_id, credential_hash, environment, owner_email, name,
		          litellm_user_id, litellm_token,
		          status, created_at, last_used_at, revoked_at
	`
	r := &EkKeyInfo{}
	err := pool.QueryRow(ctx, sql, keyID).Scan(
		&r.KeyID, &r.CredentialHash, &r.Environment, &r.OwnerEmail, &r.Name,
		&r.LiteLLMUserID, &r.LiteLLMToken,
		&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: RevokeEnvironmentKey(%s): %w", keyID, err)
	}
	return r, nil
}

// ListEnvironmentKeysByOwner returns a paginated slice of EkKeyInfo for the
// given owner_email, ordered by (created_at DESC, key_id DESC).
//
// Same limit clamping + opaque base64 cursor as ListPersonalKeysByOwner.
func ListEnvironmentKeysByOwner(ctx context.Context, pool *pgxpool.Pool, ownerEmail string, limit int, cursor string) ([]EkKeyInfo, string, error) {
	return listEnvironmentKeys(ctx, pool, &ownerEmail, limit, cursor)
}

// ListEnvironmentKeysByOwnerWithFilter is the admin variant. When
// ownerEmailFilter is nil the helper returns ALL environment_keys rows for
// the deployment (paginated); when non-nil it behaves identically to
// ListEnvironmentKeysByOwner.
func ListEnvironmentKeysByOwnerWithFilter(ctx context.Context, pool *pgxpool.Pool, ownerEmailFilter *string, limit int, cursor string) ([]EkKeyInfo, string, error) {
	return listEnvironmentKeys(ctx, pool, ownerEmailFilter, limit, cursor)
}

// listEnvironmentKeys is the shared paginated-list implementation; the
// owner_email predicate is added only when ownerEmailFilter is non-nil.
func listEnvironmentKeys(ctx context.Context, pool *pgxpool.Pool, ownerEmailFilter *string, limit int, cursor string) ([]EkKeyInfo, string, error) {
	limit = clampLimit(limit)
	cursorTs, cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}

	// Build query + args. Four shapes: with/without owner filter, with/without cursor.
	const baseCols = `
		SELECT key_id, environment, owner_email, name,
		       litellm_user_id, litellm_token,
		       status, created_at, last_used_at, revoked_at
		  FROM environment_keys
	`
	const orderLimit = ` ORDER BY created_at DESC, key_id DESC LIMIT `

	var rows pgx.Rows
	switch {
	case ownerEmailFilter != nil && cursor != "":
		rows, err = pool.Query(ctx, baseCols+
			` WHERE owner_email = $1 AND (created_at, key_id) < ($2, $3)`+
			orderLimit+`$4`,
			*ownerEmailFilter, cursorTs, cursorID, limit+1)
	case ownerEmailFilter != nil && cursor == "":
		rows, err = pool.Query(ctx, baseCols+
			` WHERE owner_email = $1`+orderLimit+`$2`,
			*ownerEmailFilter, limit+1)
	case ownerEmailFilter == nil && cursor != "":
		rows, err = pool.Query(ctx, baseCols+
			` WHERE (created_at, key_id) < ($1, $2)`+
			orderLimit+`$3`,
			cursorTs, cursorID, limit+1)
	default: // no filter, no cursor
		rows, err = pool.Query(ctx, baseCols+orderLimit+`$1`, limit+1)
	}
	if err != nil {
		if isTransientPgErr(err) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("db: ListEnvironmentKeys: %w", err)
	}
	defer rows.Close()

	out := make([]EkKeyInfo, 0, limit)
	for rows.Next() {
		var r EkKeyInfo
		if err := rows.Scan(
			&r.KeyID, &r.Environment, &r.OwnerEmail, &r.Name,
			&r.LiteLLMUserID, &r.LiteLLMToken,
			&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt,
		); err != nil {
			return nil, "", fmt.Errorf("db: ListEnvironmentKeys scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("db: ListEnvironmentKeys iterate: %w", err)
	}

	var nextCursor string
	if len(out) > limit {
		last := out[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.KeyID)
		out = out[:limit]
	}
	return out, nextCursor, nil
}

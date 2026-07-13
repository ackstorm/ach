// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the personal_keys table (Hub §7 / §15).
//
// The Plan 03-07 SSO callback handler and the Plan 03-09 admin handlers
// consume these helpers:
//
//   - InsertPersonalKey: §7.1 row-write on every successful Dex SSO callback
//     (the LiteLLM `KeyGenerate` response's plaintext is hashed before the
//     INSERT — never persisted).
//   - GetPersonalKey: read-by-key_id for the Plan 03-09 admin force-revoke
//     path; returns (nil, nil) on absent.
//   - RevokePersonalKey: Hub §7.1 DB-first revocation barrier — UPDATE flips
//     status before the LiteLLM-side compensation runs.
//   - ListPersonalKeysByOwner: paginated lister for §15.5; opaque
//     base64-encoded cursor over (created_at, key_id) so ties on identical
//     created_at timestamps resolve deterministically.
//
// SQL discipline (carry-forward from external_refs.go):
//   - Every value binds via $N — zero string concatenation.
//   - pgconn class 08/57 propagates raw via isTransientPgErr (errors.go).
//   - Wrapped errors carry only non-secret identifiers (key_id is opaque;
//     credential_hash NEVER appears).

package db

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pagination caps for §15.5 list endpoints. Defaults to 100, max 500.
const (
	defaultListLimit = 100
	maxListLimit     = 500
)

// clampLimit normalizes a caller-supplied limit to [1, maxListLimit].
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

// encodeCursor serializes (created_at, key_id) into an opaque base64 token.
// The cursor MUST be treated as opaque by callers — its encoding may change
// across releases.
func encodeCursor(createdAt time.Time, keyID string) string {
	// RFC3339Nano preserves the timestamp's sub-millisecond resolution so
	// rows inserted within the same second still sort deterministically.
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "\x00" + keyID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reverses encodeCursor. Returns (zero, "", nil) when cursor is
// empty (no pagination requested). A malformed cursor returns an error so
// the handler can render 400.
func decodeCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor: %w", err)
	}
	tsRaw, id, ok := strings.Cut(string(raw), "\x00")
	if !ok {
		return time.Time{}, "", errors.New("invalid cursor: malformed payload")
	}
	ts, err := time.Parse(time.RFC3339Nano, tsRaw)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor: %w", err)
	}
	return ts, id, nil
}

// InsertPersonalKey writes a single personal_keys row inside an implicit
// transaction (pool.Exec). status defaults to 'active' and created_at
// defaults to now() — neither appears on the PkInsertRow struct.
//
// Returns the raw error on pgconn class 08/57 (transient). Constraint
// violations (e.g. 23505 unique_violation on credential_hash) are wrapped
// with the key_id so the handler can detect-and-retry with a fresh ulid.
func InsertPersonalKey(ctx context.Context, pool *pgxpool.Pool, row PkInsertRow) error {
	const sql = `
		INSERT INTO personal_keys
		    (key_id, credential_hash, owner_email, expires_at,
		     status, litellm_user_id, litellm_token, litellm_key_material_enc)
		VALUES ($1, $2, $3, $4, 'active', $5, $6, $7)
	`
	if _, err := pool.Exec(ctx, sql,
		row.KeyID, row.CredentialHash, row.OwnerEmail, row.ExpiresAt,
		row.LiteLLMUserID, row.LiteLLMToken, row.LiteLLMKeyMaterial,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: InsertPersonalKey(%s): %w", row.KeyID, err)
	}
	return nil
}

// GetPersonalKey reads a row by key_id. Returns (nil, nil) on pgx.ErrNoRows
// (absent row is not an error — Plan 03-09 admin handler renders 404).
func GetPersonalKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*PkKeyInfo, error) {
	const sql = `
		SELECT key_id, owner_email, expires_at, litellm_user_id, litellm_token,
		       status, created_at, last_used_at, revoked_at
		  FROM personal_keys
		 WHERE key_id = $1
	`
	r := &PkKeyInfo{}
	err := pool.QueryRow(ctx, sql, keyID).Scan(
		&r.KeyID, &r.OwnerEmail, &r.ExpiresAt, &r.LiteLLMUserID, &r.LiteLLMToken,
		&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetPersonalKey(%s): %w", keyID, err)
	}
	return r, nil
}

// RevokePersonalKey atomically flips status='active' → 'revoked' and stamps
// revoked_at = now(). Returns the revoked row's PkKeyInfo on success; returns
// (nil, nil) when the row is already revoked, expired, or absent (the WHERE
// status='active' predicate matched zero rows). The Plan 03-09 admin handler
// uses the (nil, nil) signal to render 404.
//
// Per Hub §7.1, the personal_keys UPDATE IS the visible revocation barrier;
// the LiteLLM-side compensation and the Redis DEL are downstream best-effort
// (the handler in Plan 03-09 owns that orchestration).
func RevokePersonalKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*PkKeyInfo, error) {
	const sql = `
		UPDATE personal_keys SET
		    status     = 'revoked',
		    revoked_at = now()
		 WHERE key_id = $1
		   AND status = 'active'
		RETURNING key_id, owner_email, expires_at, litellm_user_id, litellm_token,
		          status, created_at, last_used_at, revoked_at
	`
	r := &PkKeyInfo{}
	err := pool.QueryRow(ctx, sql, keyID).Scan(
		&r.KeyID, &r.OwnerEmail, &r.ExpiresAt, &r.LiteLLMUserID, &r.LiteLLMToken,
		&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: RevokePersonalKey(%s): %w", keyID, err)
	}
	return r, nil
}

// ListPersonalKeysByOwner returns a paginated slice of PkKeyInfo for the
// given owner_email, ordered by (created_at DESC, key_id DESC) so a stable
// cursor walks the most-recent rows first.
//
// limit is clamped to [1, 500]; a zero or negative limit becomes 100. cursor
// is the opaque base64 token returned by a previous call's nextCursor; pass
// "" for the first page. nextCursor is "" on the last page.
func ListPersonalKeysByOwner(ctx context.Context, pool *pgxpool.Pool, ownerEmail string, limit int, cursor string) ([]PkKeyInfo, string, error) {
	limit = clampLimit(limit)
	cursorTs, cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	// Fetch limit+1 rows so we can detect "more available" without a second
	// round-trip — if we got limit+1, there's a next page and we drop the
	// trailing row before returning to the caller.
	const sqlNoCursor = `
		SELECT key_id, owner_email, expires_at, litellm_user_id, litellm_token,
		       status, created_at, last_used_at, revoked_at
		  FROM personal_keys
		 WHERE owner_email = $1
		 ORDER BY created_at DESC, key_id DESC
		 LIMIT $2
	`
	const sqlWithCursor = `
		SELECT key_id, owner_email, expires_at, litellm_user_id, litellm_token,
		       status, created_at, last_used_at, revoked_at
		  FROM personal_keys
		 WHERE owner_email = $1
		   AND (created_at, key_id) < ($2, $3)
		 ORDER BY created_at DESC, key_id DESC
		 LIMIT $4
	`
	var rows pgx.Rows
	if cursor == "" {
		rows, err = pool.Query(ctx, sqlNoCursor, ownerEmail, limit+1)
	} else {
		rows, err = pool.Query(ctx, sqlWithCursor, ownerEmail, cursorTs, cursorID, limit+1)
	}
	if err != nil {
		if isTransientPgErr(err) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("db: ListPersonalKeysByOwner(%s): %w", ownerEmail, err)
	}
	return paginate(rows, limit,
		func(r pgx.Rows) (PkKeyInfo, error) {
			var k PkKeyInfo
			err := r.Scan(
				&k.KeyID, &k.OwnerEmail, &k.ExpiresAt, &k.LiteLLMUserID, &k.LiteLLMToken,
				&k.Status, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt,
			)
			return k, err
		},
		func(k PkKeyInfo) (time.Time, string) { return k.CreatedAt, k.KeyID },
	)
}

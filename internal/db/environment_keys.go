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
//     §15.6 endpoint; also used by the admin bulk-revoke path.

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DrainEnvironmentKeysSQL and CountUndrainedEnvironmentKeysSQL are the two
// statements behind the Environment finalizer's §6.5 drain loop
// (internal/controller/ach/environment_controller.go's drainEkRows): every
// non-revoked row — active, suspended, expired, access-invalid alike (D-23,
// AC-12) — is revoked, not just 'active' ones. Exported so the drain test in
// internal/db pins the exact statement the controller runs.
const (
	DrainEnvironmentKeysSQL = `UPDATE environment_keys SET status='revoked', revoked_at=now() ` +
		`WHERE environment=$1 AND status<>'revoked'`
	CountUndrainedEnvironmentKeysSQL = `SELECT count(*) FROM environment_keys ` +
		`WHERE environment=$1 AND status<>'revoked'`
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
		     status, litellm_user_id, litellm_token, litellm_key_material_enc, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, $8, $9)
	`
	if _, err := pool.Exec(ctx, sql,
		row.KeyID, row.CredentialHash, row.Environment, row.OwnerEmail, row.Name,
		row.LiteLLMUserID, row.LiteLLMToken, row.LiteLLMKeyMaterial, row.ExpiresAt,
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
		       status, created_at, last_used_at, revoked_at, expires_at
		  FROM environment_keys
		 WHERE key_id = $1
	`
	r := &EkKeyInfo{}
	err := pool.QueryRow(ctx, sql, keyID).Scan(
		&r.KeyID, &r.CredentialHash, &r.Environment, &r.OwnerEmail, &r.Name,
		&r.LiteLLMUserID, &r.LiteLLMToken,
		&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt, &r.ExpiresAt,
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

// RevokeEnvironmentKey flips any non-revoked status → 'revoked' (§8.4) and
// stamps revoked_at = now(). Returns (nil, nil) when already revoked or
// absent.
//
// Per Hub §8.5 (KEY-08), this is the DB step in the LiteLLM-first
// revocation order — the handler in Plan 03-09 MUST run litellm.RevokeKey
// FIRST and only call this helper after the LiteLLM ack. The LiteLLM-side
// flip is the load-bearing barrier; the DB flip + Redis TTL bound the
// Content Service window.
func RevokeEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*EkKeyInfo, error) {
	return flipEnvironmentKey(ctx, pool, "RevokeEnvironmentKey", keyID,
		`UPDATE environment_keys SET status = 'revoked', revoked_at = now()
		  WHERE key_id = $1 AND status <> 'revoked'`)
}

// SuspendEnvironmentKey flips active → suspended (§8.2). (nil, nil) when
// the row is not active (already suspended, revoked, absent) — the handler
// re-reads to tell those apart. A suspended row never authenticates:
// EkResolve keeps status='active'.
func SuspendEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*EkKeyInfo, error) {
	return flipEnvironmentKey(ctx, pool, "SuspendEnvironmentKey", keyID,
		`UPDATE environment_keys SET status = 'suspended'
		  WHERE key_id = $1 AND status = 'active'`)
}

// ResumeEnvironmentKey flips suspended → active, unless the key has
// expired (Expired outranks Suspended, §7.1). The single-statement
// predicate is what makes a resume unable to overwrite a concurrent
// revocation (§7.2).
func ResumeEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, keyID string) (*EkKeyInfo, error) {
	return flipEnvironmentKey(ctx, pool, "ResumeEnvironmentKey", keyID,
		`UPDATE environment_keys SET status = 'active'
		  WHERE key_id = $1 AND status = 'suspended'
		    AND (expires_at IS NULL OR expires_at > now())`)
}

const ekReturning = ` RETURNING key_id, credential_hash, environment, owner_email, name,
		          litellm_user_id, litellm_token,
		          status, created_at, last_used_at, revoked_at, expires_at`

// flipEnvironmentKey runs a single-statement UPDATE...RETURNING status
// transition and normalizes the "no matching row" outcome to (nil, nil) —
// the shared implementation behind RevokeEnvironmentKey,
// SuspendEnvironmentKey, and ResumeEnvironmentKey.
func flipEnvironmentKey(ctx context.Context, pool *pgxpool.Pool, op, keyID, update string) (*EkKeyInfo, error) {
	r := &EkKeyInfo{}
	err := pool.QueryRow(ctx, update+ekReturning, keyID).Scan(
		&r.KeyID, &r.CredentialHash, &r.Environment, &r.OwnerEmail, &r.Name,
		&r.LiteLLMUserID, &r.LiteLLMToken,
		&r.Status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt, &r.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: %s(%s): %w", op, keyID, err)
	}
	return r, nil
}

// EkRevokeRow is one row from ListEnvironmentKeysForRevoke — just the
// two fields the Environment finalizer's key-revoke leg needs before it may
// delete the environment's access group and shell team
// (references/litellm-permission-model.md §8: revoking every ek_ in LiteLLM
// FIRST is the load-bearing ordering). litellm_token is guaranteed non-NULL
// by the query's WHERE clause, so it is a plain string rather than
// EkKeyInfo's nullable *string form.
type EkRevokeRow struct {
	KeyID        string
	LiteLLMToken string
}

// ListEnvironmentKeysForRevoke returns every non-revoked, LiteLLM-linked
// row — active, suspended, expired and access-invalid alike (§8.4, AC-12) —
// for environment: the query the Environment finalizer's
// revokeEnvironmentKeys leg runs before deleting the access group and shell
// team (internal/controller/ach/environment_shellteam.go).
func ListEnvironmentKeysForRevoke(ctx context.Context, pool *pgxpool.Pool, environment string) ([]EkRevokeRow, error) {
	const sql = `
		SELECT key_id, litellm_token FROM environment_keys
		 WHERE environment=$1 AND status<>'revoked' AND litellm_token IS NOT NULL
	`
	rows, err := pool.Query(ctx, sql, environment)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListEnvironmentKeysForRevoke(%s): %w", environment, err)
	}
	defer rows.Close()

	var out []EkRevokeRow
	for rows.Next() {
		var r EkRevokeRow
		if scanErr := rows.Scan(&r.KeyID, &r.LiteLLMToken); scanErr != nil {
			if isTransientPgErr(scanErr) {
				return nil, scanErr
			}
			return nil, fmt.Errorf("db: ListEnvironmentKeysForRevoke(%s) scan: %w", environment, scanErr)
		}
		out = append(out, r)
	}
	if rerr := rows.Err(); rerr != nil {
		if isTransientPgErr(rerr) {
			return nil, rerr
		}
		return nil, fmt.Errorf("db: ListEnvironmentKeysForRevoke(%s): %w", environment, rerr)
	}
	return out, nil
}

// ListEnvironmentKeysByOwner returns a paginated slice of EkKeyInfo for the
// given owner_email, ordered by (created_at DESC, key_id DESC).
//
// Same limit clamping + opaque base64 cursor as ListPersonalKeysByOwner.
func ListEnvironmentKeysByOwner(ctx context.Context, pool *pgxpool.Pool, ownerEmail string, limit int, cursor string) ([]EkKeyInfo, string, error) {
	return listEnvironmentKeys(ctx, pool, &ownerEmail, limit, cursor)
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
		       status, created_at, last_used_at, revoked_at, expires_at
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
	return paginate(rows, limit,
		func(r pgx.Rows) (EkKeyInfo, error) {
			var k EkKeyInfo
			err := r.Scan(
				&k.KeyID, &k.Environment, &k.OwnerEmail, &k.Name,
				&k.LiteLLMUserID, &k.LiteLLMToken,
				&k.Status, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &k.ExpiresAt,
			)
			return k, err
		},
		func(k EkKeyInfo) (time.Time, string) { return k.CreatedAt, k.KeyID },
	)
}

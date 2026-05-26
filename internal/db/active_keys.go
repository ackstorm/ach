// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the OP-15 orphan-cleanup loop (Hub §18.4).
//
// Two helpers live here:
//
//   - ListActiveACHKeyIDs (Phase 2) — DISTINCT union of every active
//     personal_keys.key_id and environment_keys.key_id. Phase 2 approximation
//     used before ACH had a stable join-key with the LiteLLM `/key/list`
//     response.
//   - ListActiveACHKeyTokens (Phase 3, this plan) — DISTINCT union of every
//     active personal_keys.litellm_token and environment_keys.litellm_token
//     where the column is non-null. Closes the Phase 02.2 D-02 prerequisite:
//     once Phase 3's INSERT path populates litellm_token on every new key, the
//     orphan loop's set-difference key swaps from key_id (which never matches
//     a LiteLLM sk-... token) to litellm_token (which does). The Phase 2
//     ListActiveACHKeyIDs helper is preserved unchanged — Phase 4+ may retire
//     it once every Phase 3+ row carries a litellm_token.
//
// SQL discipline: parameterless SELECT (no $N binds), pgconn class
// 08/57 propagation per the package convention established in
// litellm_users.go / external_refs.go.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListActiveACHKeyIDs returns the DISTINCT union of every active
// personal_keys.key_id and environment_keys.key_id (both prefixed
// 'pkid_' / 'ekid_' per Hub §16 DB-02).
//
// Used by the orphan-cleanup Runnable (Plan 02-08) to compute the
// "ACH-active key_id set" — the membership test that determines
// whether a LiteLLM-side key is orphan per Hub §18.4. Note that
// Phase 2 ACH does NOT yet record the LiteLLM-side key_id for each
// pk_/ek_; the orphan check approximation in Phase 2 flags every
// LiteLLM key as orphan (because LiteLLM key_ids are 'sk-...'
// values that never match the 'pkid_'/'ekid_' prefix). Phase 3 will
// add a litellm_key_id column to personal_keys + environment_keys
// and ListActiveACHKeyIDs will be replaced by a more precise helper
// (the Runnable contract itself does not change — the swap is
// purely at the db-helper layer).
//
// Filters:
//   - status = 'active' on both tables (Hub §18.4 — revoked / expired
//     rows MUST NOT contribute; their key_ids are no longer "owned"
//     by ACH and should not block orphan detection for re-issued keys
//     in a Phase 3+ world where litellm_key_id is tracked).
//   - No NULL or empty-string filter is needed: key_id is the PRIMARY
//     KEY on both tables and CHECK-constrained to LIKE 'pkid_%' /
//     LIKE 'ekid_%' (migration 000001), so NULL and ” are not
//     storable values.
//
// Returns ([]string{}, nil) on zero matches — never (nil, error).
// Pgconn 08/57 errors propagate raw so the caller's retry backoff
// works correctly; other errors wrap via fmt.Errorf.
func ListActiveACHKeyIDs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	const sql = `
		SELECT DISTINCT key_id FROM (
		    SELECT key_id FROM personal_keys    WHERE status = 'active'
		    UNION
		    SELECT key_id FROM environment_keys WHERE status = 'active'
		) AS u
	`
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListActiveACHKeyIDs: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db: ListActiveACHKeyIDs scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListActiveACHKeyIDs iterate: %w", err)
	}
	return out, nil
}

// ListActiveACHKeyTokens is the Phase 3 successor to ListActiveACHKeyIDs.
// It returns the DISTINCT union of every active personal_keys.litellm_token
// and environment_keys.litellm_token where the column is non-null.
//
// Hub §18.4 D-02 (Phase 02.2 closure) — the orphan-cleanup loop's set-
// difference key changes from key_id (Phase 2) to litellm_token (Phase 3+).
// LiteLLM's GET /key/list response carries `token` (an opaque hex), NEVER
// `key_id`; matching on litellm_token is the FIRST PRECISE join key
// available to the orphan loop, so the existing approximation (every
// LiteLLM key flagged as orphan because no sk-... value can match a
// pkid_/ekid_ value) is replaced with a true membership test once Phase 3
// rows start landing.
//
// Filters:
//   - status = 'active' on both tables (revoked rows MUST NOT contribute —
//     their LiteLLM-side tokens are intentionally orphan, awaiting the
//     orphan-cleanup loop's RevokeKey + DeleteKey).
//   - Non-null litellm_token on both tables (Phase 2 / 02.2 rows leave the
//     column NULL until Phase 3 SSO write; until then this helper returns
//     an empty slice — that is the expected steady-state during the
//     migration period).
//
// Returns ([]string{}, nil) on zero matches — never (nil, error).
// Pgconn 08/57 errors propagate raw; other errors wrap via fmt.Errorf.
func ListActiveACHKeyTokens(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	const sql = `
		SELECT DISTINCT litellm_token FROM (
		    SELECT litellm_token FROM personal_keys
		        WHERE status = 'active' AND litellm_token IS NOT NULL
		    UNION
		    SELECT litellm_token FROM environment_keys
		        WHERE status = 'active' AND litellm_token IS NOT NULL
		) AS u
	`
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListActiveACHKeyTokens: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var tok string
		if err := rows.Scan(&tok); err != nil {
			return nil, fmt.Errorf("db: ListActiveACHKeyTokens scan: %w", err)
		}
		out = append(out, tok)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListActiveACHKeyTokens iterate: %w", err)
	}
	return out, nil
}

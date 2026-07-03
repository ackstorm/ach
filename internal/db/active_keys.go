// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the OP-15 orphan-cleanup loop (Hub §18.4).
//
// ListActiveACHKeyIDs — DISTINCT union of every active
// personal_keys.key_id and environment_keys.key_id. This is the LIVE
// join helper the orphan loop uses: ACH-minted LiteLLM keys carry
// metadata.ach_key_id in this same key_id namespace (pkid_*/ekid_*),
// so the loop's ownership/membership test is metadata.ach_key_id ↔
// this set.
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
// Used by the orphan-cleanup Runnable to compute the "ACH-active
// key_id set" — the membership half of the ownership-gated orphan test
// (Hub §18.4). The join key is metadata.ach_key_id, surfaced from
// LiteLLM GET /key/list (return_full_object=true): ACH-minted keys carry
// ach_key_id in this same key_id namespace (set at mint in sso.go /
// envkeys/handler.go), so a LiteLLM key is a true orphan iff it carries
// an ach_key_id that is NOT in this set. Keys WITHOUT ach_key_id are
// foreign and are never revoked. This makes ListActiveACHKeyIDs the live
// join helper — key_id is the PRIMARY KEY (never NULL), which is why the
// loop joins on it rather than litellm_token (whose NULL-during-migration
// rows would fail open).
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

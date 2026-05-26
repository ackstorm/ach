// SPDX-License-Identifier: Apache-2.0

// Package db helper for the OP-15 orphan-cleanup loop (Hub §18.4).
//
// ListACHManagedLitellmUsers returns the DISTINCT union of the
// litellm_user_id values across active personal_keys + environment_keys
// rows. The Plan 02-08 orphan-cleanup Runnable iterates this set and calls
// litellm.Client.ListUserKeys per entry to enumerate LiteLLM-side keys for
// each ACH-managed user.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListACHManagedLitellmUsers returns the DISTINCT union of the
// litellm_user_id values across active personal_keys and
// environment_keys rows.
//
// Hub §18.4 D-16 — the orphan-cleanup loop (Plan 02-08 Runnable)
// iterates this set and calls litellm.Client.ListUserKeys per
// entry to enumerate LiteLLM-side keys for each ACH-managed user.
// A LiteLLM key is "orphan" iff its key_id is absent from the
// active ACH rows AND it is ≥10min old AND its owning user is
// in the set returned by this function.
//
// Phase 02.2 invariant: BOTH the litellm_user_id column (added in
// migration 000002) AND the litellm_token column (added in migration
// 000003) are nullable and never written by Phase 2 / 02.2 code
// (Phase 3 lands the SSO write path on /key/generate). Phase 02.2's
// orphan loop therefore returns an empty slice on every tick until
// Phase 3 ships — that is the expected steady-state.
//
// Filters:
//   - status = 'active' on both tables (Hub §18.4 — revoked/expired
//     rows MUST NOT contribute, otherwise the orphan loop would
//     attempt to revoke keys for users we no longer manage).
//   - litellm_user_id IS NOT NULL guards against the Phase 2 schema
//     where the column is freshly-added nullable.
//   - litellm_user_id <> ” defends against an empty string until
//     Phase 3 SSO landing enforces non-empty values; without this
//     filter the orphan loop would issue a malformed LiteLLM
//     ListUserKeys call.
//
// Returns ([]string{}, nil) on zero matches — never (nil, error).
// Pgconn 08/57 errors propagate raw so the caller's retry backoff
// works correctly; other errors wrap via fmt.Errorf.
func ListACHManagedLitellmUsers(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	const sql = `
		SELECT DISTINCT litellm_user_id FROM (
		    SELECT litellm_user_id FROM personal_keys
		        WHERE status = 'active' AND litellm_user_id IS NOT NULL
		    UNION
		    SELECT litellm_user_id FROM environment_keys
		        WHERE status = 'active' AND litellm_user_id IS NOT NULL
		) AS u
		WHERE litellm_user_id <> ''
	`
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListACHManagedLitellmUsers: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db: ListACHManagedLitellmUsers scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListACHManagedLitellmUsers iterate: %w", err)
	}
	return out, nil
}

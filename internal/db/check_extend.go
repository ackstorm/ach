// SPDX-License-Identifier: Apache-2.0

// Hub §7.1 atomic sliding-window check-and-extend SQL helper.
//
// PkCheckAndExtend is the SOLE pk_ resolution helper for every Hub component
// (Phase 3 Platform API, Phase 4 Forwarder, Phase 5 Content Service). The
// "atomic single CTE" wording in REQUIREMENTS.md KEY-04 line 41 and BLK-04 is
// honored verbatim: the body is a literal `WITH candidate AS (SELECT … FOR
// UPDATE) UPDATE personal_keys … FROM candidate … RETURNING …` shape — the
// CTE holds the row lock for the duration of the UPDATE and the WHERE-snapshot
// (status='active', expires_at > now()) is evaluated atomically inside the
// same statement.
//
// Zero rows returned ⇒ revoked / expired / unknown ⇒ helper returns (nil, nil)
// so the caller renders 401 expired_or_revoked. KEY-04 makes the three causes
// indistinguishable by design — no timing channel, no information leak.
//
// The last_used_at + expires_at UPDATE is a SIDE EFFECT inside the same
// statement, embedded as two parallel CASE expressions. The auth decision
// (the candidate CTE) NEVER reads last_used_at; the sliding-window debounce
// is purely a write-side concern (Hub §7.1 v9).

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PkCheckAndExtend is the Hub §7.1 atomic sliding-window check-and-extend.
//
// Zero rows ⇒ revoked / expired / unknown ⇒ returns (nil, nil) for the caller
// to render 401 expired_or_revoked. One row ⇒ valid; the returned PkKeyInfo
// carries the post-extend expires_at wall-clock (which equals the pre-extend
// value when the 5-minute debounce window has not elapsed).
//
// SQL discipline:
//   - The CTE candidate is the AUTHORITATIVE auth-decision predicate. It uses
//     FOR UPDATE so concurrent writers on the same row serialize correctly
//     under READ COMMITTED (pgx default).
//   - The UPDATE references personal_keys.last_used_at in the CASE WHEN —
//     this is the pre-statement value (Postgres evaluates each row's update
//     against its pre-update snapshot), so the debounce window is checked
//     against the LAST commit, not against itself.
//   - The credentialHashHex parameter MUST NOT flow into any audit trail or
//     structured event (Hub §16.1 / T-AUDIT-01). Errors wrap the function
//     name only — no parameter contents appear in any wrapped error.
func PkCheckAndExtend(ctx context.Context, pool *pgxpool.Pool, credentialHashHex string) (*PkKeyInfo, error) {
	const sql = `
		WITH candidate AS (
		    SELECT key_id, owner_email
		      FROM personal_keys
		     WHERE credential_hash = $1
		       AND status = 'active'
		       AND expires_at > now()
		     FOR UPDATE
		)
		UPDATE personal_keys SET
		    last_used_at = CASE
		        WHEN personal_keys.last_used_at IS NULL
		          OR personal_keys.last_used_at < now() - interval '5 minutes'
		        THEN now()
		        ELSE personal_keys.last_used_at
		    END,
		    expires_at = CASE
		        WHEN personal_keys.last_used_at IS NULL
		          OR personal_keys.last_used_at < now() - interval '5 minutes'
		        THEN now() + interval '7 days'
		        ELSE personal_keys.expires_at
		    END
		  FROM candidate
		 WHERE personal_keys.key_id = candidate.key_id
		RETURNING personal_keys.key_id,
		          personal_keys.owner_email,
		          personal_keys.expires_at,
		          personal_keys.litellm_user_id,
		          personal_keys.litellm_token
	`
	r := &PkKeyInfo{}
	err := pool.QueryRow(ctx, sql, credentialHashHex).Scan(
		&r.KeyID, &r.OwnerEmail, &r.ExpiresAt, &r.LiteLLMUserID, &r.LiteLLMToken,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// KEY-04: revoked / expired / unknown are indistinguishable by design.
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: PkCheckAndExtend: %w", err)
	}
	return r, nil
}

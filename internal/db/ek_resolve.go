// SPDX-License-Identifier: Apache-2.0

// Hub §8.1 ek_ resolve helper with debounced last_used_at update.
//
// EkResolve is the SOLE ek_ resolution helper for every Hub component (Phase 3
// Platform API, Phase 4 Forwarder, Phase 5 Content Service). Per KEY-06 the
// last_used_at UPDATE does NOT participate in the auth decision: `status =
// 'active'` is the authoritative predicate. environment_keys has no
// expiration column (revocation-only per migration 000001 lines 47-49) so the
// sliding window logic from check_extend.go does not apply.
//
// Zero rows returned ⇒ revoked / unknown ⇒ helper returns (nil, nil) so the
// caller renders 401 expired_or_revoked. The two causes are indistinguishable
// by design — no timing channel, no information leak.

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EkResolve is the Hub §8.1 ek_ resolve + debounced last_used_at UPDATE.
//
// Zero rows ⇒ revoked / unknown ⇒ returns (nil, nil). One row ⇒ valid; the
// caller receives the full EkKeyInfo (Environment, OwnerEmail, Name,
// LiteLLMUserID, LiteLLMToken).
//
// SQL discipline:
//   - `status = 'active'` is the authoritative predicate (KEY-06).
//   - last_used_at debounce embedded in the same UPDATE statement; the CASE
//     expression checks last_used_at against the LAST commit (pre-update
//     snapshot), not against itself.
//   - The credentialHashHex parameter MUST NOT flow into any audit trail or
//     structured event (Hub §16.1 / T-AUDIT-01). Errors wrap the function
//     name only; no parameter contents appear in any wrapped error.
func EkResolve(ctx context.Context, pool *pgxpool.Pool, credentialHashHex string) (*EkKeyInfo, error) {
	const sql = `
		UPDATE environment_keys SET
		    last_used_at = CASE
		        WHEN last_used_at IS NULL
		          OR last_used_at < now() - interval '5 minutes'
		        THEN now()
		        ELSE last_used_at
		    END
		 WHERE credential_hash = $1
		   AND status = 'active'
		RETURNING key_id, environment, owner_email, name,
		          litellm_user_id, litellm_token
	`
	r := &EkKeyInfo{}
	err := pool.QueryRow(ctx, sql, credentialHashHex).Scan(
		&r.KeyID, &r.Environment, &r.OwnerEmail, &r.Name,
		&r.LiteLLMUserID, &r.LiteLLMToken,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// KEY-06: revoked / unknown indistinguishable by design.
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: EkResolve: %w", err)
	}
	return r, nil
}

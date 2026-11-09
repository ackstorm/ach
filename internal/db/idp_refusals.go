// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SetIdPRefused records (refused) or lifts (!refused) the upstream IdP's
// refusal of email. While recorded, every ek_ the owner holds stops
// resolving (EkResolve, ExchangeKeyResolve, GetEnvironmentKey.OwnerRefused).
func SetIdPRefused(ctx context.Context, pool *pgxpool.Pool, email string, refused bool) error {
	q := `DELETE FROM idp_refusals WHERE email = lower($1)`
	if refused {
		q = `INSERT INTO idp_refusals (email) VALUES (lower($1))
		     ON CONFLICT (email) DO UPDATE SET refused_at = now()`
	}
	if _, err := pool.Exec(ctx, q, email); err != nil {
		return fmt.Errorf("db: SetIdPRefused: %w", err)
	}
	return nil
}

// IdPRefused reports whether email is recorded in idp_refusals.
func IdPRefused(ctx context.Context, pool *pgxpool.Pool, email string) (bool, error) {
	var refused bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM idp_refusals WHERE email = lower($1))`, email).Scan(&refused); err != nil {
		return false, fmt.Errorf("db: IdPRefused: %w", err)
	}
	return refused, nil
}

// EkOwnersInUse lists, lower-cased, the owners of an active, unexpired ek_
// used within window, leaving out owners already refused.
func EkOwnersInUse(ctx context.Context, pool *pgxpool.Pool, window time.Duration) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT lower(k.owner_email)
		  FROM environment_keys k
		 WHERE k.status = 'active'
		   AND (k.expires_at IS NULL OR k.expires_at > now())
		   AND k.last_used_at > now() - make_interval(secs => $1)
		   AND NOT EXISTS (SELECT 1 FROM idp_refusals r WHERE r.email = lower(k.owner_email))`,
		window.Seconds())
	if err != nil {
		return nil, fmt.Errorf("db: EkOwnersInUse: %w", err)
	}
	owners, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("db: EkOwnersInUse: %w", err)
	}
	return owners, nil
}

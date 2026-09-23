// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/litellm"
)

// UserKeyAllowance reports how many non-revoked ek_ keys `email` holds and
// how many they may hold. Both halves come back from ONE round trip: the
// count and the ceiling are read together because every caller needs both
// (the create path compares them, the console renders them).
//
// defaultMax is the chart-wide fallback (ACH_USER_MAX_KEYS) used when the
// person has no user_limits row. There is deliberately no seed-at-login
// equivalent to the spend budget's: the fallback is evaluated on every read,
// so changing the chart default immediately retunes everyone who has no
// explicit override, while an override always wins.
//
// Revoked keys are excluded — counting them would turn the ceiling into a
// lifetime quota. Suspended and expired keys DO count: both are one call
// away from carrying traffic again.
func UserKeyAllowance(ctx context.Context, pool *pgxpool.Pool, email string, defaultMax int) (used int, max int, err error) {
	const sql = `
		SELECT
		    (SELECT count(*) FROM environment_keys
		      WHERE lower(owner_email) = $1 AND status <> 'revoked'),
		    COALESCE((SELECT max_keys FROM user_limits WHERE email = $1), $2)
	`
	row := pool.QueryRow(ctx, sql, litellm.NormalizeEmail(email), defaultMax)
	if err := row.Scan(&used, &max); err != nil {
		return 0, 0, err
	}
	return used, max, nil
}

// SetUserMaxKeys upserts one person's ceiling. Called only by the admin
// route; a caller never raises their own.
func SetUserMaxKeys(ctx context.Context, pool *pgxpool.Pool, email string, maxKeys int) error {
	const sql = `
		INSERT INTO user_limits (email, max_keys)
		VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE
		    SET max_keys = EXCLUDED.max_keys, updated_at = now()
	`
	_, err := pool.Exec(ctx, sql, litellm.NormalizeEmail(email), maxKeys)
	return err
}

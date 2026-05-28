// SPDX-License-Identifier: Apache-2.0

// Package db Postgres NOTIFY emit helper (issue #34).
//
// Emit wraps pg_notify(text, text) so a callsite can fire a notification on
// a constant channel name with a string payload. Channel name MUST be a
// safe identifier ([a-z0-9_]+, ≤63 bytes) — interpolation through pg_notify
// avoids any SQL injection concern but the validation guards against typos
// at runtime.
//
// Coupling rule (revision 1 of the plan): a bare Emit is NOT atomic with a
// preceding pool.Exec — the NOTIFY fires on the Exec, not on the prior
// statement's commit, so consumers can SELECT a snapshot that doesn't yet
// see the projection write. Use db.WithTxNotify (with_tx_notify.go) for any
// path that pairs a projection write with a notification. The bare Emit
// helper is retained for one-off operational paths and tests.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Emit fires NOTIFY <channel>, '<payload>' via pg_notify(text, text).
// Payload MUST be a printable string ≤ 8000 bytes (Postgres limit). Returns
// a wrapped error on invalid channel name or pg_notify failure.
func Emit(ctx context.Context, pool *pgxpool.Pool, channel, payload string) error {
	if !validChannel(channel) {
		return fmt.Errorf("db.Emit: invalid channel name %q", channel)
	}
	if _, err := pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db.Emit(%s): %w", channel, err)
	}
	return nil
}

// validChannel restricts channel names to [a-z0-9_]+ with len ∈ [1,63].
// Postgres' NOTIFY identifier limit is the same as other SQL identifiers
// (NAMEDATALEN-1 = 63 bytes); validation reduces noise from runtime
// surprises when a caller passes a typo or uppercased name.
func validChannel(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// SPDX-License-Identifier: Apache-2.0

// Package db transaction-coupled NOTIFY helper (issue #34, revision 1).
//
// WithTxNotify closes the visibility race the bare db.Emit has when paired
// with a separate projection write. Postgres queues pg_notify calls inside a
// transaction and only fires them on COMMIT, AFTER the transaction's writes
// are visible to other backends — so a consumer that wakes on the NOTIFY can
// safely SELECT and will see the projection mutation.
//
// Every controller projection-mutation path (UpsertEnvironment, UpsertPlugin,
// UpsertPrompt, UpsertArtifact, UpsertMarketplacePlugin, UpsertBIP,
// UpsertLiteLLMConnection, plus the symmetric SoftDeleteX paths) runs
// through this helper from its reconciler. The bare db.Emit is retained for
// one-off operational paths and tests.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTxNotify runs fn inside a single Postgres transaction and, if fn
// returns nil, issues pg_notify(channel, payload) inside the same transaction
// before COMMIT. If fn returns an error, the transaction rolls back and no
// NOTIFY is delivered — the consumer's 5-minute periodic refresh (in
// bipcache / envstore) is the safety net that catches any silently-dropped
// write.
//
// Channel name is validated against validChannel (see notify.go); payload
// is passed as a $-parameter to pg_notify(text, text), so any string content
// is safe (no escaping needed at the call site).
func WithTxNotify(ctx context.Context, pool *pgxpool.Pool, channel, payload string, fn func(pgx.Tx) error) error {
	if !validChannel(channel) {
		return fmt.Errorf("db.WithTxNotify: invalid channel name %q", channel)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db.WithTxNotify: Begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db.WithTxNotify(%s): pg_notify: %w", channel, err)
	}
	if err := tx.Commit(ctx); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db.WithTxNotify(%s): Commit: %w", channel, err)
	}
	return nil
}

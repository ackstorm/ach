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

// UpsertEnvironmentTx exposes the tx-form Environment upsert for callers
// that already hold a pgx.Tx (typically inside db.WithTxNotify). Mirrors the
// pool-form UpsertEnvironment semantics 1:1: ErrOriginConflict on UI-row
// collision, transient pgconn 08/57 propagated raw, other errors wrapped
// with non-secret (namespace, name).
func UpsertEnvironmentTx(ctx context.Context, tx pgx.Tx, row EnvironmentRow) error {
	return upsertEnvironmentTx(ctx, tx, row)
}

// SoftDeleteEnvironmentTx exposes the tx-form Environment soft-delete for
// callers inside db.WithTxNotify. Idempotent on already-drained rows.
func SoftDeleteEnvironmentTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	return softDeleteEnvironmentTx(ctx, tx, ns, name)
}

// UpsertPluginTx — see UpsertEnvironmentTx.
func UpsertPluginTx(ctx context.Context, tx pgx.Tx, row PluginRow) error {
	return upsertPluginTx(ctx, tx, row)
}

// SoftDeletePluginTx — see SoftDeleteEnvironmentTx.
func SoftDeletePluginTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	return softDeletePluginTx(ctx, tx, ns, name)
}

// UpsertPromptTx — see UpsertEnvironmentTx.
func UpsertPromptTx(ctx context.Context, tx pgx.Tx, row PromptRow) error {
	return upsertPromptTx(ctx, tx, row)
}

// SoftDeletePromptTx — see SoftDeleteEnvironmentTx.
func SoftDeletePromptTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	return softDeletePromptTx(ctx, tx, ns, name)
}

// UpsertArtifactTx — see UpsertEnvironmentTx.
func UpsertArtifactTx(ctx context.Context, tx pgx.Tx, row ArtifactRow) error {
	return upsertArtifactTx(ctx, tx, row)
}

// SoftDeleteArtifactTx — see SoftDeleteEnvironmentTx.
func SoftDeleteArtifactTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	return softDeleteArtifactTx(ctx, tx, ns, name)
}

// UpsertExternalRefTx — see UpsertEnvironmentTx.
func UpsertExternalRefTx(ctx context.Context, tx pgx.Tx, r ExternalRef) error {
	return upsertExternalRefTx(ctx, tx, r)
}

// UpsertMarketplacePluginTx — see UpsertEnvironmentTx.
func UpsertMarketplacePluginTx(ctx context.Context, tx pgx.Tx, p MarketplacePlugin) error {
	return upsertMarketplacePluginTx(ctx, tx, p)
}

// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runInTx runs fn inside a transaction: Begin → fn → Commit, with a deferred
// Rollback (no-op after Commit). Transient pgconn 08/57 errors from Begin/
// Commit propagate raw so the caller's workqueue can back off; other errors
// wrap with a non-secret prefix. Mirrors WithTxNotify's envelope minus the
// pg_notify step (use WithTxNotify when a NOTIFY must fire in the same tx).
func runInTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// upsertReturning runs an origin-gated UPSERT that RETURNING-projects one
// ident column. ErrNoRows (the ON CONFLICT WHERE origin='cr' filtered the row
// out → UI-owned) maps to ErrOriginConflict; transient pgconn 08/57 propagate
// raw; other errors wrap with the non-secret label. args are the SQL params.
//
// Used by the UI-side write helpers (internal/db/ui_objects.go), whose UPSERTs
// gate on origin='ui' so a CR-owned row blocks the write → ErrOriginConflict.
//
// The operator-side writers (UpsertEnvironment, UpsertExternalRef, …) are
// UN-gated under the GitOps-wins model (G2): their ON CONFLICT DO UPDATE has no
// origin WHERE clause and sets origin='cr', so a CR applied over a UI-owned row
// TAKES IT OVER (origin flips 'ui'→'cr', locked=TRUE, primary key preserved).
// The DO UPDATE therefore always fires and RETURNING always yields a row, so
// the operator path never returns ErrOriginConflict.
func upsertReturning(ctx context.Context, tx pgx.Tx, sql, label string, args ...any) error {
	var col string
	if err := tx.QueryRow(ctx, sql, args...).Scan(&col); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOriginConflict
		}
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: %s: %w", label, err)
	}
	return nil
}

// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the external_refs table (Hub §16 / §10.3).
//
// The Plan 02-05 Plugin / Prompt / Artifact reconcilers call:
//
//   - UpsertExternalRef after a successful rename(2) (Hub §10.3 step 5) — also
//     clears force_refresh_requested_at in the same UPDATE per D-07 so the
//     pending force-refresh marker disappears once a refresh has actually
//     completed.
//   - GetExternalRef to read the prior UpstreamRev for conditional-GET on the
//     next reconcile; absent row returns (nil, nil) — "first reconcile".
//   - ResetExternalRefRefreshOnEmptyCache on Operator startup when the cache
//     root is empty (OP-11 / Hub §6.6 + §10.3 PVC-loss recovery).
//   - DeleteExternalRef on §10.3 finalizer cleanup to keep external_refs row
//     counts aligned with live CRs.
//
// SQL discipline (Phase 1 carry-forward; see internal/controller/ach/
// environment_controller.go lines 196-241 for the established pattern):
//
//   - Every value binds via $N — zero string concatenation (T-02-03-01).
//   - pgconn.PgError class "08" (connection exception) or "57" (operator
//     intervention) → return the raw error so controller-runtime treats it
//     as transient and applies exponential backoff.
//   - Other errors wrap via fmt.Errorf with non-secret CR identifiers
//     (Kind / Name) — never with pgErr.Message contents that could echo
//     bound parameter values (T-02-03-03).

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExternalRef mirrors the external_refs row schema after migration 000002
// (Hub §16 + Plan 02-03 Task 1 additions).
//
// The Kind ∈ {"plugin", "prompt", "artifact"} and Name uniquely identify a
// row; together they form the PRIMARY KEY (kind, name).
type ExternalRef struct {
	Kind                    string    // "plugin" / "prompt" / "artifact"
	Name                    string    // CR metadata.name
	StorageLocation         string    // absolute path the rename(2) published to
	UpstreamRev             string    // FetchResult.UpstreamRev (commit SHA / ETag / generation)
	LastSuccessfulRefresh   time.Time // last successful refresh wall-clock
	NextRefreshAt           time.Time // when the next reconcile should run
	MaxStalenessSeconds     int64     // spec.refresh.maxStaleness in seconds
	ForceRefreshRequestedAt time.Time // zero when no pending force-refresh marker (D-07)
}

// UpsertExternalRef inserts-or-updates a row keyed by (kind, name).
//
// The ON CONFLICT DO UPDATE replaces every non-PK column. force_refresh_requested_at
// is force-set to NULL in the same UPDATE so a pending Platform-API force-refresh
// marker (D-07) clears when an actual refresh completes — the next reconcile
// reading the column will see NULL and skip the force-refresh fast path.
//
// Returns the raw error on pgconn class 08/57 (transient) so callers'
// controller-runtime workqueue applies exponential backoff. Other errors wrap
// via fmt.Errorf("db: UpsertExternalRef(%s/%s): %w", Kind, Name, err) — Kind
// and Name are non-secret CR identifiers, safe in log output.
const upsertExternalRefSQL = `
	INSERT INTO external_refs
	    (kind, name, storage_location, upstream_rev,
	     last_successful_refresh, next_refresh_at,
	     max_staleness_seconds, force_refresh_requested_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, 'cr', TRUE)
	ON CONFLICT (kind, name) DO UPDATE SET
	    storage_location           = EXCLUDED.storage_location,
	    upstream_rev               = EXCLUDED.upstream_rev,
	    last_successful_refresh    = EXCLUDED.last_successful_refresh,
	    next_refresh_at            = EXCLUDED.next_refresh_at,
	    max_staleness_seconds      = EXCLUDED.max_staleness_seconds,
	    force_refresh_requested_at = NULL,
	    locked                     = TRUE
	WHERE external_refs.origin = 'cr'
	RETURNING kind
`

func UpsertExternalRef(ctx context.Context, pool *pgxpool.Pool, r ExternalRef) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertExternalRefTx(ctx, tx, r)
	})
}

// UpsertExternalRefTx — see UpsertEnvironmentTx.
func UpsertExternalRefTx(ctx context.Context, tx pgx.Tx, r ExternalRef) error {
	return upsertReturning(ctx, tx, upsertExternalRefSQL, "UpsertExternalRef("+r.Kind+"/"+r.Name+")",
		r.Kind, r.Name, r.StorageLocation, r.UpstreamRev,
		r.LastSuccessfulRefresh, r.NextRefreshAt, r.MaxStalenessSeconds,
	)
}

// GetExternalRef reads the row keyed by (kind, name). On pgx.ErrNoRows it
// returns (nil, nil) — absence is not an error; the Plan 02-05 reconciler
// treats a nil result as "no prior refresh" / first-reconcile semantics.
//
// Pgconn 08/57 errors propagate raw; other errors wrap with the (kind, name)
// identifiers per the package convention.
func GetExternalRef(ctx context.Context, pool *pgxpool.Pool, kind, name string) (*ExternalRef, error) {
	const sql = `
		SELECT kind, name, storage_location,
		       COALESCE(upstream_rev, ''),
		       COALESCE(last_successful_refresh, 'epoch'::timestamptz),
		       COALESCE(next_refresh_at, 'epoch'::timestamptz),
		       max_staleness_seconds,
		       COALESCE(force_refresh_requested_at, '0001-01-01 00:00:00+00'::timestamptz)
		  FROM external_refs
		 WHERE kind = $1 AND name = $2
	`
	r := &ExternalRef{}
	if err := pool.QueryRow(ctx, sql, kind, name).Scan(
		&r.Kind, &r.Name, &r.StorageLocation, &r.UpstreamRev,
		&r.LastSuccessfulRefresh, &r.NextRefreshAt, &r.MaxStalenessSeconds,
		&r.ForceRefreshRequestedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetExternalRef(%s/%s): %w", kind, name, err)
	}
	return r, nil
}

// ResetExternalRefRefreshOnEmptyCache NULLs out last_successful_refresh on
// every row. The Plan 02-09 cmd/operator/main.go startup branch calls this
// when the cache root is empty (OP-11) so every reconciler reissues the
// upstream fetch on first reconcile.
//
// Loud-warn semantic: the calling site MUST log
//
//	setupLog.Info("PVC was empty on startup — external_refs.last_successful_refresh reset")
//
// so an operator reading logs understands the data-plane churn.
func ResetExternalRefRefreshOnEmptyCache(ctx context.Context, pool *pgxpool.Pool) error {
	const sql = `UPDATE external_refs SET last_successful_refresh = NULL`
	if _, err := pool.Exec(ctx, sql); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: ResetExternalRefRefreshOnEmptyCache: %w", err)
	}
	return nil
}

// DeleteExternalRef drops the row keyed by (kind, name). The Plan 02-05
// finalizer path uses os.Remove on the cached file before RemoveFinalizer;
// Phase 2 also drops the DB row so external_refs row counts match live CRs.
//
// Absence is not an error — DELETE of a non-existent row is a no-op.
func DeleteExternalRef(ctx context.Context, pool *pgxpool.Pool, kind, name string) error {
	const sql = `DELETE FROM external_refs WHERE kind = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, kind, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteExternalRef(%s/%s): %w", kind, name, err)
	}
	return nil
}

// isTransientPgErr lives in internal/db/errors.go (lifted from this file
// in Phase 03-03 so check_extend.go, ek_resolve.go, personal_keys.go,
// environment_keys.go, and active_keys.go can all share the classifier
// without duplication). Both files live in the same `db` package so the
// move was source-only — call sites resolve through package-level scope.

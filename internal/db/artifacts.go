// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the artifacts projection table (Phase 5 D-13,
// spec v4 §5.2 reversal). Mirrors environments.go and plugins.go shape;
// the kind-specific column is scope text NOT NULL CHECK (scope IN
// ('object','directory')) — Content Service uses scope to dispatch on-disk
// path resolution (CS-07: object → artifact/<name>; directory →
// artifact/<name>.tar.gz with Content-Type application/gzip).
//
// Written by the Operator's Artifact reconciler (Plan 05-04); read by the
// Content Service authz pipeline (Plan 05-05) on every GET
// /content/artifact/{name}.
//
// SQL discipline mirrors environments.go: every value binds via $N; pgconn
// class 08/57 errors propagate raw; other errors wrap with non-secret
// (namespace, name) identifiers — pgErr.Message NEVER echoed.

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ArtifactRow mirrors the artifacts projection row schema after migration
// 000004 (Phase 5 D-13). (Namespace, Name) form the PRIMARY KEY.
//
// Scope ∈ {"object","directory"} per the SQL CHECK constraint — Content
// Service uses it to choose Content-Type (application/octet-stream vs
// application/gzip per CS-06) and the on-disk path (artifact/<name> vs
// artifact/<name>.tar.gz per CS-07).
//
// LastSuccessfulRefresh nullable: nil on first reconcile / OP-11 reset →
// CS-10 emits 503 stale_cache_expired.
type ArtifactRow struct {
	Namespace             string     // PK part 1
	Name                  string     // PK part 2
	StorageLocation       string     // absolute path the rename(2) published to
	Scope                 string     // "object" | "directory" — enforced by SQL CHECK
	LastSuccessfulRefresh *time.Time // nil on first reconcile / OP-11 reset
	MaxStalenessSeconds   int64      // spec.refresh.maxStaleness in seconds
	DeletionTimestamp     *time.Time // non-nil → drain-mode (CS-09)
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
}

// UpsertArtifact inserts-or-updates a row keyed by (namespace, name). The
// ON CONFLICT DO UPDATE replaces every non-PK column EXCEPT
// deletion_timestamp (preserved per CS-09); updated_at is force-set to
// now() in the UPDATE branch.
//
// pgconn class 08/57 errors propagate raw (transient backoff). Other
// errors wrap with non-secret (namespace, name) identifiers — pgErr.Message
// NEVER included. A row with Scope ∉ {"object","directory"} fails the SQL
// CHECK and is returned wrapped as a terminal error (caller's bug).
const upsertArtifactSQL = `
	INSERT INTO artifacts
	    (namespace, name, storage_location, scope,
	     last_successful_refresh, max_staleness_seconds,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, $7, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    storage_location        = EXCLUDED.storage_location,
	    scope                   = EXCLUDED.scope,
	    last_successful_refresh = EXCLUDED.last_successful_refresh,
	    max_staleness_seconds   = EXCLUDED.max_staleness_seconds,
	    resource_version        = EXCLUDED.resource_version,
	    updated_at              = now(),
	    locked                  = TRUE
	WHERE artifacts.origin = 'cr'
	RETURNING namespace
`

func UpsertArtifact(ctx context.Context, pool *pgxpool.Pool, row ArtifactRow) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertArtifactTx(ctx, tx, row)
	})
}

// UpsertArtifactTx — see UpsertEnvironmentTx.
func UpsertArtifactTx(ctx context.Context, tx pgx.Tx, row ArtifactRow) error {
	return upsertReturning(ctx, tx, upsertArtifactSQL, "UpsertArtifact("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name, row.StorageLocation, row.Scope,
		row.LastSuccessfulRefresh, row.MaxStalenessSeconds,
		row.ResourceVersion,
	)
}

// GetArtifactByName reads the row keyed by (namespace, name). pgx.ErrNoRows
// → (nil, nil). Per CS-09 the row is returned with DeletionTimestamp
// populated when set — caller MUST NOT filter on it here; Content Service
// keeps serving until hard-delete by finalizer drain.
func GetArtifactByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*ArtifactRow, error) {
	const sql = `
		SELECT namespace, name, storage_location, scope,
		       last_successful_refresh, max_staleness_seconds,
		       deletion_timestamp, resource_version, updated_at
		  FROM artifacts
		 WHERE namespace = $1 AND name = $2
	`
	r := &ArtifactRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation, &r.Scope,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetArtifactByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// SoftDeleteArtifact sets deletion_timestamp = now() without removing the
// row (CS-09). Idempotent: rows already in drain-mode are left untouched
// so duplicate finalizer ticks do not refresh the drain clock.
func SoftDeleteArtifact(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeleteArtifactSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteArtifact(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SoftDeleteArtifactTx — see SoftDeleteEnvironmentTx.
func SoftDeleteArtifactTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeleteArtifactSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteArtifact(%s/%s): %w", ns, name, err)
	}
	return nil
}

const softDeleteArtifactSQL = `
	UPDATE artifacts
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// DeleteArtifact removes the row keyed by (namespace, name) outright. Called
// only after finalizer drain completes. Absence is not an error.
func DeleteArtifact(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM artifacts WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteArtifact(%s/%s): %w", ns, name, err)
	}
	return nil
}

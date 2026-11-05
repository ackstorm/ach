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
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
}

// UpsertArtifact inserts-or-updates a row keyed by (namespace, name). The
// ON CONFLICT DO UPDATE replaces every non-PK column; updated_at is force-set
// to now() in the UPDATE branch.
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
// → (nil, nil).
func GetArtifactByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*ArtifactRow, error) {
	const sql = `
		SELECT namespace, name, storage_location, scope,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM artifacts
		 WHERE namespace = $1 AND name = $2
	`
	r := &ArtifactRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation, &r.Scope,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.ResourceVersion, &r.UpdatedAt,
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

// ListArtifacts returns every artifacts row in ns ordered by name ASC.
// Used by the platform-api admin inventory endpoint (read-only).
func ListArtifacts(ctx context.Context, pool *pgxpool.Pool, ns string) ([]ArtifactRow, error) {
	const sql = `
		SELECT namespace, name, storage_location, scope,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM artifacts
		 WHERE namespace = $1
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListArtifacts(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []ArtifactRow{}
	for rows.Next() {
		var r ArtifactRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.StorageLocation, &r.Scope,
			&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
			&r.ResourceVersion, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("db: ListArtifacts(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListArtifacts(%s) iterate: %w", ns, err)
	}
	return out, nil
}

const deleteArtifactSQL = `DELETE FROM artifacts WHERE namespace = $1 AND name = $2`

// DeleteArtifact removes the row keyed by (namespace, name). Absence is not an
// error.
func DeleteArtifact(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, deleteArtifactSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteArtifact(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeleteArtifactTx is DeleteArtifact inside an outer transaction (db.WithTxNotify).
func DeleteArtifactTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, deleteArtifactSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteArtifact(%s/%s): %w", ns, name, err)
	}
	return nil
}

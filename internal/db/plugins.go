// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the plugins projection table (Phase 5 D-13,
// spec v4 §5.2 reversal) and ResolvePluginByName (marketplace-aware
// plugin resolution).
//
// ResolvePluginByName two-arm semantics:
//
//   - bare (marketplace == ""): ONLY the plugins (CRD) row for (ns, name)
//     where deletion_timestamp IS NULL. No marketplace fallback — the Plugin
//     CRD namespace is the sole resolution target for bare names.
//   - scoped (marketplace != ""): the marketplace_plugins row with the exact
//     (marketplace_name, name) PRIMARY KEY. No alphabetical tiebreak; the
//     caller supplies the marketplace explicitly (parsed from the
//     pluginref.Parse result).
//
// In both arms (nil, nil) means no row found → caller emits 404.
//
// Caching policy (D-08): NO caching for plugin resolution or staleness
// reads — direct Postgres query on every request. SC#3 and CS-10 say so
// verbatim. pgx prepared statements + connection pool (db.Open) absorb the
// hot-path cost.
//
// SQL discipline mirrors environments.go and marketplace_plugins.go: every
// value binds via $N (T-02-03-01); pgconn class 08/57 errors propagate raw
// for controller-runtime backoff; other errors wrap with non-secret
// (namespace, name) identifiers — pgErr.Message NEVER included
// (T-02-03-03).

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PluginRow mirrors the plugins projection row schema after migration
// 000004 (Phase 5 D-13). (Namespace, Name) form the PRIMARY KEY.
//
// LastSuccessfulRefresh is nullable: nil on first reconcile / OP-11 reset
// → CS-10 emits 503 stale_cache_expired.
type PluginRow struct {
	Namespace             string     // PK part 1
	Name                  string     // PK part 2
	StorageLocation       string     // absolute path the rename(2) published to
	LastSuccessfulRefresh *time.Time // nil on first reconcile / OP-11 reset
	MaxStalenessSeconds   int64      // spec.refresh.maxStaleness in seconds
	DeletionTimestamp     *time.Time // non-nil → drain-mode (CS-09)
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
}

// PluginResolution is the §12.3 CTE result shape — the single row returned
// by ResolvePluginByName when a plugin can be served.
//
// Source ∈ {"plugin", "marketplace"} distinguishes the resolution arm:
//   - "plugin": Namespace is the K8s namespace of the matching Plugin CRD.
//   - "marketplace": Namespace is the PARENT marketplace_plugins.marketplace_name
//     (NOT a K8s namespace — the marketplace's metadata.name surfaced
//     here to let callers distinguish "internal plugin in namespace X"
//     from "marketplace plugin under marketplace=anthropic-marketplace").
//
// Caller in Plan 05-05 uses Source to choose the on-disk path-resolution
// arm and the audit-event target prefix.
type PluginResolution struct {
	Source                string     // "plugin" | "marketplace"
	Namespace             string     // K8s namespace OR marketplace_name (per Source)
	Name                  string     // plugin name (always the request param)
	StorageLocation       string     // absolute path on the Content Service PVC
	LastSuccessfulRefresh *time.Time // nullable — CS-10 staleness gate input
	MaxStalenessSeconds   int64      // CS-10 staleness gate input
}

// UpsertPlugin inserts-or-updates a plugins row keyed by (namespace, name).
// ON CONFLICT DO UPDATE replaces every non-PK column EXCEPT
// deletion_timestamp (preserved per CS-09); updated_at is force-set to now().
//
// pgconn class 08/57 errors propagate raw (transient backoff). Other errors
// wrap with non-secret (namespace, name) identifiers — pgErr.Message NEVER
// included.
// upsertPluginSQL: origin='cr' guarded UPSERT (issue #34). See
// upsertEnvironmentSQL for the row-blocking pattern.
const upsertPluginSQL = `
	INSERT INTO plugins
	    (namespace, name, storage_location,
	     last_successful_refresh, max_staleness_seconds,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    storage_location        = EXCLUDED.storage_location,
	    last_successful_refresh = EXCLUDED.last_successful_refresh,
	    max_staleness_seconds   = EXCLUDED.max_staleness_seconds,
	    resource_version        = EXCLUDED.resource_version,
	    updated_at              = now(),
	    locked                  = TRUE
	WHERE plugins.origin = 'cr'
	RETURNING namespace
`

func UpsertPlugin(ctx context.Context, pool *pgxpool.Pool, row PluginRow) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertPluginTx(ctx, tx, row)
	})
}

// UpsertPluginTx — see UpsertEnvironmentTx.
func UpsertPluginTx(ctx context.Context, tx pgx.Tx, row PluginRow) error {
	return upsertReturning(ctx, tx, upsertPluginSQL, "UpsertPlugin("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name, row.StorageLocation,
		row.LastSuccessfulRefresh, row.MaxStalenessSeconds,
		row.ResourceVersion,
	)
}

// GetPluginByName reads the plugins row keyed by (namespace, name).
// pgx.ErrNoRows → (nil, nil). Per CS-09 the row is returned WITH
// DeletionTimestamp populated when set — callers MUST NOT filter on it
// here; Content Service keeps serving until hard-delete by finalizer drain.
func GetPluginByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*PluginRow, error) {
	const sql = `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       deletion_timestamp, resource_version, updated_at
		  FROM plugins
		 WHERE namespace = $1 AND name = $2
	`
	r := &PluginRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetPluginByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// SoftDeletePlugin sets deletion_timestamp = now() without removing the row
// (CS-09). Idempotent on already-drained rows.
func SoftDeletePlugin(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeletePluginSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeletePlugin(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SoftDeletePluginTx — see SoftDeleteEnvironmentTx.
func SoftDeletePluginTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeletePluginSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeletePlugin(%s/%s): %w", ns, name, err)
	}
	return nil
}

const softDeletePluginSQL = `
	UPDATE plugins
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// DeletePlugin removes the plugins row keyed by (namespace, name) outright.
// Called only after finalizer drain. Absence is not an error.
func DeletePlugin(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM plugins WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeletePlugin(%s/%s): %w", ns, name, err)
	}
	return nil
}

// ResolvePluginByName implements scoped plugin-precedence:
//
//   - marketplace == ""  → bare reference: ONLY the plugins (CRD) row with
//     matching (ns, name) and deletion_timestamp IS NULL. No marketplace
//     fallback — Plugin CRD is the sole bare namespace.
//   - marketplace != ""  → scoped reference: the marketplace_plugins row with
//     the exact (marketplace_name, name) PK. No alphabetical tiebreak.
//
// Returns (nil, nil) when no row matches → caller emits 404.
//
// pgconn class 08/57 errors propagate raw (transient backoff). Other
// errors wrap with non-secret identifiers — pgErr.Message NEVER included.
func ResolvePluginByName(ctx context.Context, pool *pgxpool.Pool, ns, name, marketplace string) (*PluginResolution, error) {
	if marketplace == "" {
		const sql = `
			SELECT 'plugin'::text AS source,
			       namespace, name, storage_location,
			       last_successful_refresh, max_staleness_seconds
			  FROM plugins
			 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL`
		return scanResolution(ctx, pool, sql, ns, name)
	}
	const sql = `
		SELECT 'marketplace'::text AS source,
		       marketplace_name AS namespace,
		       name, storage_location,
		       last_successful_refresh, max_staleness_seconds
		  FROM marketplace_plugins
		 WHERE marketplace_name = $1 AND name = $2`
	return scanResolution(ctx, pool, sql, marketplace, name)
}

// scanResolution runs a single-row resolution query, mapping pgx.ErrNoRows
// to (nil, nil) and preserving transient-error propagation.
func scanResolution(ctx context.Context, pool *pgxpool.Pool, sql, arg1, arg2 string) (*PluginResolution, error) {
	r := &PluginResolution{}
	if err := pool.QueryRow(ctx, sql, arg1, arg2).Scan(
		&r.Source, &r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ResolvePluginByName(%s/%s): %w", arg1, arg2, err)
	}
	return r, nil
}

// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the plugins projection table (Phase 5 D-13,
// spec v4 §5.2 reversal) AND the §12.3 plugin-resolution CTE
// (ResolvePluginByName).
//
// Plugin precedence (§12.3) on every Content Service request:
//
//  1. The plugins row (CRD-derived projection) with the matching name in
//     the requested namespace wins, ONLY when its deletion_timestamp IS
//     NULL — a soft-deleted CRD must not shadow live marketplace rows
//     (T-05-02-04 — locked in by TestResolvePluginByName_SoftDeletedCRDFallsThrough).
//  2. Otherwise the marketplace_plugins row keyed on (any_marketplace, name)
//     whose marketplace_name sorts alphabetically lowest (Unicode
//     code-point, default Postgres text collation) wins.
//  3. Otherwise (nil, nil) → caller emits 404 content_not_found.
//
// Drift-flag #5 resolution: The marketplace_plugins table uses
// (marketplace_name, name) PK per migration 000002 (see
// marketplace_plugins.go line 36). The column is literally `name` — NOT
// `plugin_name` as the CONTEXT Specifics block sketches. The CTE below
// references `marketplace_plugins.name` to match the live schema.
//
// Caching policy (D-08): NO caching for §12.3 plugin resolution or staleness
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertPlugin(%s/%s): begin: %w", row.Namespace, row.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertPluginTx(ctx, tx, row); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertPlugin(%s/%s): commit: %w", row.Namespace, row.Name, err)
	}
	return nil
}

func upsertPluginTx(ctx context.Context, tx pgx.Tx, row PluginRow) error {
	var ns string
	err := tx.QueryRow(ctx, upsertPluginSQL,
		row.Namespace, row.Name, row.StorageLocation,
		row.LastSuccessfulRefresh, row.MaxStalenessSeconds,
		row.ResourceVersion,
	).Scan(&ns)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOriginConflict
		}
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertPlugin(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
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

func softDeletePluginTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
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

// ResolvePluginByName implements the §12.3 plugin-precedence CTE:
//
//  1. The plugins row (CRD-derived projection) with the matching (ns, name)
//     wins when its deletion_timestamp IS NULL.
//  2. Otherwise the marketplace_plugins row with the matching name whose
//     marketplace_name sorts alphabetically lowest wins.
//  3. Otherwise (nil, nil) — caller emits 404 content_not_found.
//
// The CTE uses a single SELECT … UNION ALL … LIMIT 1 so Postgres returns
// at most one row, and the marketplace_match arm runs only when
// plugin_match is empty (`WHERE NOT EXISTS (SELECT 1 FROM plugin_match)`).
// LIMIT 1 on both arms caps the result-set size; combined with the
// (marketplace_name, name) PRIMARY KEY index on marketplace_plugins, the
// query is one or two index probes worst-case (T-05-02-03 acceptance).
//
// Drift-flag #5: the marketplace_plugins column is `name`, NOT
// `plugin_name` — confirmed against migration 000001 line 64 and the
// MarketplacePlugin struct (marketplace_plugins.go line 39). The CTE
// references `marketplace_plugins.name` directly.
//
// pgconn class 08/57 errors propagate raw (transient backoff). Other
// errors wrap with non-secret (namespace, name) identifiers — pgErr.Message
// NEVER included.
func ResolvePluginByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*PluginResolution, error) {
	const sql = `
		WITH plugin_match AS (
		    SELECT 'plugin'::text AS source,
		           namespace, name, storage_location,
		           last_successful_refresh, max_staleness_seconds
		      FROM plugins
		     WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
		),
		marketplace_match AS (
		    SELECT 'marketplace'::text AS source,
		           marketplace_name AS namespace,
		           name, storage_location,
		           last_successful_refresh, max_staleness_seconds
		      FROM marketplace_plugins
		     WHERE name = $2
		     ORDER BY marketplace_name ASC
		     LIMIT 1
		)
		SELECT * FROM plugin_match
		UNION ALL
		SELECT * FROM marketplace_match WHERE NOT EXISTS (SELECT 1 FROM plugin_match)
		LIMIT 1
	`
	r := &PluginResolution{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Source, &r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ResolvePluginByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

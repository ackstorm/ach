// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the marketplace_plugins table (Hub §16 / §12.4).
//
// The Plan 02-06 PluginMarketplace reconciler runs a three-stage refresh
// (Hub §12.4): Stage-1 marketplace-file fetch+parse (failure aborts before
// any UPSERT), Stage-2 per-plugin best-effort UPSERT, Stage-3 final DELETE
// sweep of vanished names.
//
//   - UpsertMarketplacePlugin is called once per plugin in Stage-2; also
//     clears force_refresh_requested_at in the same UPDATE (D-07).
//   - ListMarketplacePlugins returns the current row-set under one
//     marketplace_name; Stage-3 diffs against the upstream set to find
//     vanished rows.
//   - DeleteMarketplacePlugin removes one row in Stage-3 sweep.
//   - ResetMarketplacePluginsRefreshOnEmptyCache mirrors the external_refs
//     reset on PVC-loss recovery (OP-11).
//
// SQL discipline mirrors external_refs.go: every value binds via $N;
// pgconn class 08/57 errors return raw; other errors wrap with non-secret
// (MarketplaceName, Name) identifiers (T-02-03-01, T-02-03-03).

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MarketplacePlugin mirrors the marketplace_plugins row schema after
// migration 000002 (Hub §16 + Plan 02-03 Task 1 additions). MarketplaceName
// is the parent PluginMarketplace.metadata.name; Name is the plugin's name
// inside that marketplace's plugins[]. Together they form PK (marketplace_name, name).
type MarketplacePlugin struct {
	MarketplaceName       string
	Name                  string
	StorageLocation       string
	UpstreamRev           string
	LastSuccessfulRefresh time.Time
	NextRefreshAt         time.Time
	MaxStalenessSeconds   int64
}

// UpsertMarketplacePlugin inserts-or-updates a row keyed by
// (marketplace_name, name). force_refresh_requested_at is force-set to NULL
// in the same UPDATE per D-07 (Phase 3 Platform-API force-refresh marker
// clears once an actual refresh completes).
const upsertMarketplacePluginSQL = `
	INSERT INTO marketplace_plugins
	    (marketplace_name, name, storage_location, upstream_rev,
	     last_successful_refresh, next_refresh_at,
	     max_staleness_seconds, force_refresh_requested_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, 'cr', TRUE)
	ON CONFLICT (marketplace_name, name) DO UPDATE SET
	    storage_location           = EXCLUDED.storage_location,
	    upstream_rev               = EXCLUDED.upstream_rev,
	    last_successful_refresh    = EXCLUDED.last_successful_refresh,
	    next_refresh_at            = EXCLUDED.next_refresh_at,
	    max_staleness_seconds      = EXCLUDED.max_staleness_seconds,
	    force_refresh_requested_at = NULL,
	    locked                     = TRUE
	WHERE marketplace_plugins.origin = 'cr'
	RETURNING marketplace_name
`

func UpsertMarketplacePlugin(ctx context.Context, pool *pgxpool.Pool, p MarketplacePlugin) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertMarketplacePluginTx(ctx, tx, p)
	})
}

// UpsertMarketplacePluginTx — see UpsertEnvironmentTx.
func UpsertMarketplacePluginTx(ctx context.Context, tx pgx.Tx, p MarketplacePlugin) error {
	return upsertReturning(ctx, tx, upsertMarketplacePluginSQL, "UpsertMarketplacePlugin("+p.MarketplaceName+"/"+p.Name+")",
		p.MarketplaceName, p.Name, p.StorageLocation, p.UpstreamRev,
		p.LastSuccessfulRefresh, p.NextRefreshAt, p.MaxStalenessSeconds,
	)
}

// ListMarketplacePlugins returns every row under marketplaceName. Returns
// an empty slice (not nil) on zero matches — an empty marketplace is
// legitimate steady-state.
//
// Stage-3 of the Plan 02-06 refresh diffs the returned slice against the
// upstream plugins[] set and calls DeleteMarketplacePlugin on the difference.
func ListMarketplacePlugins(ctx context.Context, pool *pgxpool.Pool, marketplaceName string) ([]MarketplacePlugin, error) {
	const sql = `
		SELECT marketplace_name, name, storage_location,
		       COALESCE(upstream_rev, ''),
		       COALESCE(last_successful_refresh, 'epoch'::timestamptz),
		       COALESCE(next_refresh_at, 'epoch'::timestamptz),
		       max_staleness_seconds
		  FROM marketplace_plugins
		 WHERE marketplace_name = $1
	`
	rows, err := pool.Query(ctx, sql, marketplaceName)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListMarketplacePlugins(%s): %w", marketplaceName, err)
	}
	defer rows.Close()

	out := []MarketplacePlugin{}
	for rows.Next() {
		var p MarketplacePlugin
		if err := rows.Scan(
			&p.MarketplaceName, &p.Name, &p.StorageLocation, &p.UpstreamRev,
			&p.LastSuccessfulRefresh, &p.NextRefreshAt, &p.MaxStalenessSeconds,
		); err != nil {
			return nil, fmt.Errorf("db: ListMarketplacePlugins(%s) scan: %w", marketplaceName, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListMarketplacePlugins(%s) iterate: %w", marketplaceName, err)
	}
	return out, nil
}

// ListAllMarketplacePlugins returns every row across all marketplaces,
// ordered by (marketplace_name, name) ASC. marketplace_plugins has no
// deletion_timestamp column, so all rows are live. Used by the platform-api
// admin inventory endpoint (read-only). Returns an empty (non-nil) slice on
// zero rows.
func ListAllMarketplacePlugins(ctx context.Context, pool *pgxpool.Pool) ([]MarketplacePlugin, error) {
	const sql = `
		SELECT marketplace_name, name, storage_location,
		       COALESCE(upstream_rev, ''),
		       COALESCE(last_successful_refresh, 'epoch'::timestamptz),
		       COALESCE(next_refresh_at, 'epoch'::timestamptz),
		       max_staleness_seconds
		  FROM marketplace_plugins
		 ORDER BY marketplace_name ASC, name ASC
	`
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListAllMarketplacePlugins: %w", err)
	}
	defer rows.Close()

	out := []MarketplacePlugin{}
	for rows.Next() {
		var p MarketplacePlugin
		if err := rows.Scan(
			&p.MarketplaceName, &p.Name, &p.StorageLocation, &p.UpstreamRev,
			&p.LastSuccessfulRefresh, &p.NextRefreshAt, &p.MaxStalenessSeconds,
		); err != nil {
			return nil, fmt.Errorf("db: ListAllMarketplacePlugins scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListAllMarketplacePlugins iterate: %w", err)
	}
	return out, nil
}

// DeleteMarketplacePlugin removes the row keyed by (marketplaceName, name).
// Absence is not an error.
func DeleteMarketplacePlugin(ctx context.Context, pool *pgxpool.Pool, marketplaceName, name string) error {
	const sql = `DELETE FROM marketplace_plugins WHERE marketplace_name = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, marketplaceName, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteMarketplacePlugin(%s/%s): %w", marketplaceName, name, err)
	}
	return nil
}

// MaxMarketplaceForceRefresh returns the most recent force_refresh_requested_at
// across every row under marketplaceName. Returns the Go zero time when no
// row has a pending marker (NULL force_refresh_requested_at on every row).
// Used by the PluginMarketplace reconciler's within-interval gate to honor
// Admin-API force-refresh signals (issue #34 B2 — SetForceRefresh stamps
// the marker on every CR-origin row under the marketplace).
func MaxMarketplaceForceRefresh(ctx context.Context, pool *pgxpool.Pool, marketplaceName string) (time.Time, error) {
	const sql = `
		SELECT COALESCE(MAX(force_refresh_requested_at), '0001-01-01 00:00:00+00'::timestamptz)
		  FROM marketplace_plugins
		 WHERE marketplace_name = $1
	`
	var ts time.Time
	if err := pool.QueryRow(ctx, sql, marketplaceName).Scan(&ts); err != nil {
		if isTransientPgErr(err) {
			return time.Time{}, err
		}
		return time.Time{}, fmt.Errorf("db: MaxMarketplaceForceRefresh(%s): %w", marketplaceName, err)
	}
	return ts, nil
}

// ResetMarketplacePluginsRefreshOnEmptyCache NULLs out last_successful_refresh
// on every row. Mirrors ResetExternalRefRefreshOnEmptyCache; called from
// Plan 02-09 startup on empty-PVC detection (OP-11). The Plan 02-09 call
// site MUST log the reset for operator visibility.
func ResetMarketplacePluginsRefreshOnEmptyCache(ctx context.Context, pool *pgxpool.Pool) error {
	const sql = `UPDATE marketplace_plugins SET last_successful_refresh = NULL`
	if _, err := pool.Exec(ctx, sql); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: ResetMarketplacePluginsRefreshOnEmptyCache: %w", err)
	}
	return nil
}

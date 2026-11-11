// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the marketplaces projection table (migration 000008,
// admin-inventory redesign).
//
// The operator's PluginMarketplace reconciler projects each CR's terminal
// Synced status + pluginsCount here so platform-api's admin inventory can show
// marketplace OBJECTS without reading CRDs (platform-api reads Postgres only,
// issue #34). The plugins discovered inside each marketplace stay in
// marketplace_plugins; this table is the marketplace object itself.
//
// SQL discipline mirrors litellm_connections.go: every value binds via $N;
// transient pgconn 08/57 errors propagate raw for controller-runtime backoff;
// other errors wrap with the non-secret (namespace, name) identifiers. Unlike
// litellm_connections there is NO origin gate — marketplaces are CR-only.

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MarketplaceRow mirrors the marketplaces row schema (migration 000008).
// (Namespace, Name) form the PRIMARY KEY.
type MarketplaceRow struct {
	Namespace       string
	Name            string
	SyncedStatus    string // metav1.Condition.Status: "True" | "False" | "Unknown" | ""
	SyncedReason    string // condition Reason when not Synced
	PluginsCount    int
	ResourceVersion string
	UpdatedAt       time.Time
	// UpstreamRev / LatestUpstreamRev mirror status: the catalog's served
	// commit and the head of the watched ref.
	UpstreamRev       string
	LatestUpstreamRev string
}

const upsertMarketplaceSQL = `
	INSERT INTO marketplaces
	    (namespace, name, synced_status, synced_reason, plugins_count,
	     resource_version, updated_at, upstream_rev, latest_upstream_rev)
	VALUES ($1, $2, $3, $4, $5, $6, now(), $7, $8)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    synced_status      = EXCLUDED.synced_status,
	    synced_reason      = EXCLUDED.synced_reason,
	    plugins_count      = EXCLUDED.plugins_count,
	    resource_version   = EXCLUDED.resource_version,
	    upstream_rev       = EXCLUDED.upstream_rev,
	    latest_upstream_rev = EXCLUDED.latest_upstream_rev,
	    updated_at         = now()
`

// UpsertMarketplace inserts-or-updates the row keyed by (Namespace, Name).
// Transient pgconn 08/57 errors propagate raw for controller-runtime backoff.
func UpsertMarketplace(ctx context.Context, pool *pgxpool.Pool, row MarketplaceRow) error {
	if _, err := pool.Exec(ctx, upsertMarketplaceSQL,
		row.Namespace, row.Name, row.SyncedStatus, row.SyncedReason,
		row.PluginsCount, row.ResourceVersion, row.UpstreamRev, row.LatestUpstreamRev,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertMarketplace(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// UpsertMarketplaceTx exposes the tx-form upsert for callers inside
// db.WithTxNotify.
func UpsertMarketplaceTx(ctx context.Context, tx pgx.Tx, row MarketplaceRow) error {
	if _, err := tx.Exec(ctx, upsertMarketplaceSQL,
		row.Namespace, row.Name, row.SyncedStatus, row.SyncedReason,
		row.PluginsCount, row.ResourceVersion, row.UpstreamRev, row.LatestUpstreamRev,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertMarketplace(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// ListMarketplaces returns every marketplaces row in ns ordered by name
// ASC. Used by the
// platform-api admin inventory endpoint (read-only).
func ListMarketplaces(ctx context.Context, pool *pgxpool.Pool, ns string) ([]MarketplaceRow, error) {
	const sql = `
		SELECT namespace, name, synced_status, synced_reason, plugins_count,
		       resource_version, updated_at,
		       COALESCE(upstream_rev, ''), COALESCE(latest_upstream_rev, '')
		  FROM marketplaces
		 WHERE namespace = $1
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListMarketplaces(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []MarketplaceRow{}
	for rows.Next() {
		var r MarketplaceRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.SyncedStatus, &r.SyncedReason, &r.PluginsCount,
			&r.ResourceVersion, &r.UpdatedAt, &r.UpstreamRev, &r.LatestUpstreamRev,
		); err != nil {
			return nil, fmt.Errorf("db: ListMarketplaces(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListMarketplaces(%s) iterate: %w", ns, err)
	}
	return out, nil
}

// DeleteMarketplace removes the row outright. Called from the finalizer
// deletion path. Absence is not an error.
func DeleteMarketplace(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM marketplaces WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteMarketplace(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeleteMarketplaceTx is the tx-form of DeleteMarketplace, used inside
// WithTxNotify so removals emit ach_marketplaces_changed.
func DeleteMarketplaceTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	const sql = `DELETE FROM marketplaces WHERE namespace = $1 AND name = $2`
	if _, err := tx.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteMarketplace(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeCatalogChannel is the NOTIFY channel fired once per successful
// catalog sync (payload = connector_name). Platform-api reads the catalog
// per request today; the notification is the seam for a future cache.
const RuntimeCatalogChannel = "ach_runtime_catalog_changed"

// RuntimeCatalogRow mirrors one runtime_catalog_entries row.
type RuntimeCatalogRow struct {
	Namespace          string
	ConnectorName      string
	Kind               string // "model" | "mcp_server" | "a2a_agent" | "team"
	Name               string
	Status             string // "active" | "missing"
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	LastSuccessfulSync time.Time
	DeletedAt          *time.Time
}

const upsertRuntimeCatalogSQL = `
INSERT INTO runtime_catalog_entries
    (namespace, connector_name, kind, name, status,
     first_seen_at, last_seen_at, last_successful_sync, deleted_at)
VALUES ($1, $2, $3, $4, 'active', $5, $5, $5, NULL)
ON CONFLICT (namespace, connector_name, kind, name) DO UPDATE SET
    status               = 'active',
    last_seen_at         = EXCLUDED.last_seen_at,
    last_successful_sync = EXCLUDED.last_successful_sync,
    deleted_at           = NULL
`

const tombstoneRuntimeCatalogSQL = `
UPDATE runtime_catalog_entries
   SET status     = 'missing',
       deleted_at = $3
 WHERE namespace            = $1
   AND connector_name       = $2
   AND last_successful_sync < $3
   AND status               = 'active'
`

// ReplaceRuntimeCatalog upserts every currently-registered runtime name as
// 'active' (last_successful_sync = syncedAt) then tombstones any previously-
// active row this connector did NOT see this sync, all inside one
// WithTxNotify transaction that fires RuntimeCatalogChannel on commit.
func ReplaceRuntimeCatalog(
	ctx context.Context,
	pool *pgxpool.Pool,
	ns, connector string,
	models, mcpServers, a2aAgents, teams map[string]struct{},
	syncedAt time.Time,
) error {
	return WithTxNotify(ctx, pool, RuntimeCatalogChannel, connector, func(tx pgx.Tx) error {
		for kind, names := range map[string]map[string]struct{}{
			"model":      models,
			"mcp_server": mcpServers,
			"a2a_agent":  a2aAgents,
			"team":       teams,
		} {
			for name := range names {
				if _, err := tx.Exec(ctx, upsertRuntimeCatalogSQL, ns, connector, kind, name, syncedAt); err != nil {
					if isTransientPgErr(err) {
						return err
					}
					return fmt.Errorf("db: ReplaceRuntimeCatalog upsert(%s/%s/%s): %w", ns, connector, kind, err)
				}
			}
		}
		if _, err := tx.Exec(ctx, tombstoneRuntimeCatalogSQL, ns, connector, syncedAt); err != nil {
			if isTransientPgErr(err) {
				return err
			}
			return fmt.Errorf("db: ReplaceRuntimeCatalog tombstone(%s/%s): %w", ns, connector, err)
		}
		return nil
	})
}

const listRuntimeCatalogSQL = `
SELECT namespace, connector_name, kind, name, status,
       first_seen_at, last_seen_at, last_successful_sync, deleted_at
  FROM runtime_catalog_entries
 WHERE namespace      = $1
   AND connector_name = $2
   AND ($3 = '' OR kind = $3)
 ORDER BY kind, name
`

// ListRuntimeCatalog returns catalog rows for one connector. kind=="" returns
// all kinds; otherwise filters to that kind.
func ListRuntimeCatalog(ctx context.Context, pool *pgxpool.Pool, ns, connector, kind string) ([]RuntimeCatalogRow, error) {
	rows, err := pool.Query(ctx, listRuntimeCatalogSQL, ns, connector, kind)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListRuntimeCatalog(%s/%s): %w", ns, connector, err)
	}
	defer rows.Close()

	var out []RuntimeCatalogRow
	for rows.Next() {
		var r RuntimeCatalogRow
		if err := rows.Scan(&r.Namespace, &r.ConnectorName, &r.Kind, &r.Name, &r.Status,
			&r.FirstSeenAt, &r.LastSeenAt, &r.LastSuccessfulSync, &r.DeletedAt); err != nil {
			return nil, fmt.Errorf("db: ListRuntimeCatalog scan(%s/%s): %w", ns, connector, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListRuntimeCatalog rows(%s/%s): %w", ns, connector, err)
	}
	return out, nil
}

// MaxRuntimeCatalogSync returns the newest last_successful_sync for a
// connector. ok=false when the connector has no catalog rows yet.
func MaxRuntimeCatalogSync(ctx context.Context, pool *pgxpool.Pool, ns, connector string) (time.Time, bool, error) {
	const q = `SELECT max(last_successful_sync) FROM runtime_catalog_entries WHERE namespace = $1 AND connector_name = $2`
	var ts *time.Time
	if err := pool.QueryRow(ctx, q, ns, connector).Scan(&ts); err != nil {
		if isTransientPgErr(err) {
			return time.Time{}, false, err
		}
		return time.Time{}, false, fmt.Errorf("db: MaxRuntimeCatalogSync(%s/%s): %w", ns, connector, err)
	}
	if ts == nil {
		return time.Time{}, false, nil
	}
	return *ts, true, nil
}

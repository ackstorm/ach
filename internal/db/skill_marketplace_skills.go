// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the skill_marketplace_skills table (migration 000010).
//
// The SkillMarketplace reconciler runs a three-stage refresh mirroring
// PluginMarketplace: Stage-1 fetch+discover (failure aborts before any UPSERT),
// Stage-2 per-skill best-effort UPSERT, Stage-3 final DELETE sweep of vanished
// names.
//
//   - UpsertSkillMarketplaceSkill is called once per discovered skill in
//     Stage-2; also clears force_refresh_requested_at in the same UPDATE (D-07).
//   - ListSkillMarketplaceSkills returns the current row-set under one
//     marketplace_name; Stage-3 diffs against the discovered set to find
//     vanished rows.
//   - DeleteSkillMarketplaceSkill removes one row in Stage-3 sweep.
//   - ResetSkillMarketplaceSkillsRefreshOnEmptyCache mirrors the external_refs
//     reset on PVC-loss recovery (OP-11).
//
// SQL discipline mirrors marketplace_plugins.go: every value binds via $N;
// pgconn class 08/57 errors return raw; other errors wrap with non-secret
// (MarketplaceName, Name) identifiers.

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SkillMarketplaceSkill mirrors the skill_marketplace_skills row schema
// (migration 000010). MarketplaceName is the parent SkillMarketplace.metadata.name;
// Name is the skill's name inside that collection. Together they form
// PK (marketplace_name, name).
type SkillMarketplaceSkill struct {
	MarketplaceName       string
	Name                  string
	StorageLocation       string
	UpstreamRev           string
	LastSuccessfulRefresh time.Time
	NextRefreshAt         time.Time
	MaxStalenessSeconds   int64
}

// UpsertSkillMarketplaceSkill inserts-or-updates a row keyed by
// (marketplace_name, name). force_refresh_requested_at is force-set to NULL in
// the same UPDATE per D-07. The origin='cr' guard mirrors marketplace_plugins.
const upsertSkillMarketplaceSkillSQL = `
	INSERT INTO skill_marketplace_skills
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
	WHERE skill_marketplace_skills.origin = 'cr'
	RETURNING marketplace_name
`

func UpsertSkillMarketplaceSkill(ctx context.Context, pool *pgxpool.Pool, s SkillMarketplaceSkill) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertSkillMarketplaceSkillTx(ctx, tx, s)
	})
}

// UpsertSkillMarketplaceSkillTx — see UpsertMarketplacePluginTx.
func UpsertSkillMarketplaceSkillTx(ctx context.Context, tx pgx.Tx, s SkillMarketplaceSkill) error {
	return upsertReturning(ctx, tx, upsertSkillMarketplaceSkillSQL, "UpsertSkillMarketplaceSkill("+s.MarketplaceName+"/"+s.Name+")",
		s.MarketplaceName, s.Name, s.StorageLocation, s.UpstreamRev,
		s.LastSuccessfulRefresh, s.NextRefreshAt, s.MaxStalenessSeconds,
	)
}

// ListSkillMarketplaceSkills returns every row under marketplaceName. Returns
// an empty slice (not nil) on zero matches — an empty marketplace is
// legitimate steady-state. Stage-3 diffs the returned slice against the
// discovered set and calls DeleteSkillMarketplaceSkill on the difference.
func ListSkillMarketplaceSkills(ctx context.Context, pool *pgxpool.Pool, marketplaceName string) ([]SkillMarketplaceSkill, error) {
	const sql = `
		SELECT marketplace_name, name, storage_location,
		       COALESCE(upstream_rev, ''),
		       COALESCE(last_successful_refresh, 'epoch'::timestamptz),
		       COALESCE(next_refresh_at, 'epoch'::timestamptz),
		       max_staleness_seconds
		  FROM skill_marketplace_skills
		 WHERE marketplace_name = $1
	`
	rows, err := pool.Query(ctx, sql, marketplaceName)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListSkillMarketplaceSkills(%s): %w", marketplaceName, err)
	}
	defer rows.Close()

	out := []SkillMarketplaceSkill{}
	for rows.Next() {
		var s SkillMarketplaceSkill
		if err := rows.Scan(
			&s.MarketplaceName, &s.Name, &s.StorageLocation, &s.UpstreamRev,
			&s.LastSuccessfulRefresh, &s.NextRefreshAt, &s.MaxStalenessSeconds,
		); err != nil {
			return nil, fmt.Errorf("db: ListSkillMarketplaceSkills(%s) scan: %w", marketplaceName, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListSkillMarketplaceSkills(%s) iterate: %w", marketplaceName, err)
	}
	return out, nil
}

// ListAllSkillMarketplaceSkills returns every row across all marketplaces,
// ordered by (marketplace_name, name) ASC. skill_marketplace_skills has no
// deletion_timestamp column, so all rows are live. Used by the platform-api
// admin inventory endpoint (read-only). Returns an empty (non-nil) slice on
// zero rows.
func ListAllSkillMarketplaceSkills(ctx context.Context, pool *pgxpool.Pool) ([]SkillMarketplaceSkill, error) {
	const sql = `
		SELECT marketplace_name, name, storage_location,
		       COALESCE(upstream_rev, ''),
		       COALESCE(last_successful_refresh, 'epoch'::timestamptz),
		       COALESCE(next_refresh_at, 'epoch'::timestamptz),
		       max_staleness_seconds
		  FROM skill_marketplace_skills
		 ORDER BY marketplace_name ASC, name ASC
	`
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListAllSkillMarketplaceSkills: %w", err)
	}
	defer rows.Close()

	out := []SkillMarketplaceSkill{}
	for rows.Next() {
		var s SkillMarketplaceSkill
		if err := rows.Scan(
			&s.MarketplaceName, &s.Name, &s.StorageLocation, &s.UpstreamRev,
			&s.LastSuccessfulRefresh, &s.NextRefreshAt, &s.MaxStalenessSeconds,
		); err != nil {
			return nil, fmt.Errorf("db: ListAllSkillMarketplaceSkills scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListAllSkillMarketplaceSkills iterate: %w", err)
	}
	return out, nil
}

// DeleteSkillMarketplaceSkill removes the row keyed by (marketplaceName, name).
// Absence is not an error.
func DeleteSkillMarketplaceSkill(ctx context.Context, pool *pgxpool.Pool, marketplaceName, name string) error {
	const sql = `DELETE FROM skill_marketplace_skills WHERE marketplace_name = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, marketplaceName, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteSkillMarketplaceSkill(%s/%s): %w", marketplaceName, name, err)
	}
	return nil
}

// DeleteSkillMarketplaceSkillTx is the tx-form of DeleteSkillMarketplaceSkill,
// used inside WithTxNotify so removals emit ach_skill_marketplace_skills_changed.
func DeleteSkillMarketplaceSkillTx(ctx context.Context, tx pgx.Tx, marketplaceName, name string) error {
	const sql = `DELETE FROM skill_marketplace_skills WHERE marketplace_name = $1 AND name = $2`
	if _, err := tx.Exec(ctx, sql, marketplaceName, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteSkillMarketplaceSkill(%s/%s): %w", marketplaceName, name, err)
	}
	return nil
}

// MaxSkillMarketplaceForceRefresh returns the most recent
// force_refresh_requested_at across every row under marketplaceName. Returns
// the Go zero time when no row has a pending marker. Mirrors
// MaxMarketplaceForceRefresh.
func MaxSkillMarketplaceForceRefresh(ctx context.Context, pool *pgxpool.Pool, marketplaceName string) (time.Time, error) {
	const sql = `
		SELECT COALESCE(MAX(force_refresh_requested_at), '0001-01-01 00:00:00+00'::timestamptz)
		  FROM skill_marketplace_skills
		 WHERE marketplace_name = $1
	`
	var ts time.Time
	if err := pool.QueryRow(ctx, sql, marketplaceName).Scan(&ts); err != nil {
		if isTransientPgErr(err) {
			return time.Time{}, err
		}
		return time.Time{}, fmt.Errorf("db: MaxSkillMarketplaceForceRefresh(%s): %w", marketplaceName, err)
	}
	return ts, nil
}

// ResetSkillMarketplaceSkillsRefreshOnEmptyCache NULLs out
// last_successful_refresh on every row. Mirrors
// ResetMarketplacePluginsRefreshOnEmptyCache; called on empty-PVC detection
// (OP-11). The call site MUST log the reset for operator visibility.
func ResetSkillMarketplaceSkillsRefreshOnEmptyCache(ctx context.Context, pool *pgxpool.Pool) error {
	const sql = `UPDATE skill_marketplace_skills SET last_successful_refresh = NULL`
	if _, err := pool.Exec(ctx, sql); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: ResetSkillMarketplaceSkillsRefreshOnEmptyCache: %w", err)
	}
	return nil
}

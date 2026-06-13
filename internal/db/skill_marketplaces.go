// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the skill_marketplaces projection table (migration
// 000010).
//
// The operator's SkillMarketplace reconciler projects each CR's terminal Synced
// status + skillsCount here so platform-api's admin inventory can show
// marketplace OBJECTS without reading CRDs (platform-api reads Postgres only,
// issue #34). The skills discovered inside each marketplace stay in
// skill_marketplace_skills; this table is the marketplace object itself.
//
// SQL discipline mirrors marketplaces.go: every value binds via $N; transient
// pgconn 08/57 errors propagate raw for controller-runtime backoff; other
// errors wrap with the non-secret (namespace, name) identifiers. There is NO
// origin gate — skill marketplaces are CR-only.

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SkillMarketplaceRow mirrors the skill_marketplaces row schema (migration
// 000010). (Namespace, Name) form the PRIMARY KEY.
type SkillMarketplaceRow struct {
	Namespace         string
	Name              string
	SyncedStatus      string // metav1.Condition.Status: "True" | "False" | "Unknown" | ""
	SyncedReason      string // condition Reason when not Synced
	SkillsCount       int
	DeletionTimestamp *time.Time
	ResourceVersion   string
	UpdatedAt         time.Time
}

const upsertSkillMarketplaceSQL = `
	INSERT INTO skill_marketplaces
	    (namespace, name, synced_status, synced_reason, skills_count,
	     resource_version, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, now())
	ON CONFLICT (namespace, name) DO UPDATE SET
	    synced_status      = EXCLUDED.synced_status,
	    synced_reason      = EXCLUDED.synced_reason,
	    skills_count       = EXCLUDED.skills_count,
	    resource_version   = EXCLUDED.resource_version,
	    deletion_timestamp = NULL,
	    updated_at         = now()
`

// UpsertSkillMarketplace inserts-or-updates the row keyed by (Namespace, Name).
// Transient pgconn 08/57 errors propagate raw for controller-runtime backoff.
func UpsertSkillMarketplace(ctx context.Context, pool *pgxpool.Pool, row SkillMarketplaceRow) error {
	if _, err := pool.Exec(ctx, upsertSkillMarketplaceSQL,
		row.Namespace, row.Name, row.SyncedStatus, row.SyncedReason,
		row.SkillsCount, row.ResourceVersion,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertSkillMarketplace(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// UpsertSkillMarketplaceTx exposes the tx-form upsert for callers inside
// db.WithTxNotify.
func UpsertSkillMarketplaceTx(ctx context.Context, tx pgx.Tx, row SkillMarketplaceRow) error {
	if _, err := tx.Exec(ctx, upsertSkillMarketplaceSQL,
		row.Namespace, row.Name, row.SyncedStatus, row.SyncedReason,
		row.SkillsCount, row.ResourceVersion,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertSkillMarketplace(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// ListSkillMarketplaces returns every live skill_marketplaces row in ns ordered
// by name ASC. Soft-deleted rows (deletion_timestamp set) are excluded. Used by
// the platform-api admin inventory endpoint (read-only).
func ListSkillMarketplaces(ctx context.Context, pool *pgxpool.Pool, ns string) ([]SkillMarketplaceRow, error) {
	const sql = `
		SELECT namespace, name, synced_status, synced_reason, skills_count,
		       deletion_timestamp, resource_version, updated_at
		  FROM skill_marketplaces
		 WHERE namespace = $1 AND deletion_timestamp IS NULL
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListSkillMarketplaces(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []SkillMarketplaceRow{}
	for rows.Next() {
		var r SkillMarketplaceRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.SyncedStatus, &r.SyncedReason, &r.SkillsCount,
			&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("db: ListSkillMarketplaces(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListSkillMarketplaces(%s) iterate: %w", ns, err)
	}
	return out, nil
}

// DeleteSkillMarketplace removes the row outright. Called from the finalizer
// deletion path. Absence is not an error.
func DeleteSkillMarketplace(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM skill_marketplaces WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteSkillMarketplace(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeleteSkillMarketplaceTx is the tx-form of DeleteSkillMarketplace, used
// inside WithTxNotify so removals emit ach_skill_marketplaces_changed.
func DeleteSkillMarketplaceTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	const sql = `DELETE FROM skill_marketplaces WHERE namespace = $1 AND name = $2`
	if _, err := tx.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteSkillMarketplace(%s/%s): %w", ns, name, err)
	}
	return nil
}

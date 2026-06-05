// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the skills projection table (agent-skill content
// kind, migration 000009) and ResolveSkillByName.
//
// A skill is a directory tree (SKILL.md + optional scripts/references/assets)
// stored as skill/<name>.tar.gz on the artifact PVC. Skills mirror plugins
// (000004 + 000005) minus the marketplace resolution arm and the content_type
// / scope columns.
//
// ResolveSkillByName has two arms: the bare arm returns the skills (CRD) row
// for (ns, name) where deletion_timestamp IS NULL; the marketplace arm returns
// the skill_marketplace_skills row for (marketplace_name, name).
//
// SQL discipline mirrors plugins.go: every value binds via $N; pgconn class
// 08/57 errors propagate raw for controller-runtime backoff; other errors
// wrap with non-secret (namespace, name) identifiers — pgErr.Message NEVER
// included.

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SkillRow mirrors the skills projection row schema after migration 000009.
// (Namespace, Name) form the PRIMARY KEY.
//
// LastSuccessfulRefresh is nullable: nil on first reconcile / reset → the
// content-service staleness gate emits 503 stale_cache_expired.
type SkillRow struct {
	Namespace             string     // PK part 1
	Name                  string     // PK part 2
	StorageLocation       string     // absolute path the rename(2) published to
	LastSuccessfulRefresh *time.Time // nil on first reconcile / reset
	MaxStalenessSeconds   int64      // spec.refresh.maxStaleness in seconds
	DeletionTimestamp     *time.Time // non-nil → drain-mode
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
}

// SkillResolution is the single row returned by ResolveSkillByName when a
// skill can be served. Source is "skill" (bare arm) or "marketplace" (scoped
// arm); for the marketplace arm Namespace carries the parent marketplace_name.
type SkillResolution struct {
	Source                string     // "skill" | "marketplace"
	Namespace             string     // K8s namespace (skill) | marketplace_name (marketplace)
	Name                  string     // skill name (always the request param)
	StorageLocation       string     // absolute path on the Content Service PVC
	LastSuccessfulRefresh *time.Time // nullable — staleness gate input
	MaxStalenessSeconds   int64      // staleness gate input
}

// upsertSkillSQL: origin='cr' guarded UPSERT (issue #34). A live reconcile
// clears deletion_timestamp to NULL so a recreated CR reusing a soft-deleted
// name drops the stale drain marker (mirrors plugins.go).
const upsertSkillSQL = `
	INSERT INTO skills
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
	    deletion_timestamp      = NULL,
	    locked                  = TRUE
	WHERE skills.origin = 'cr'
	RETURNING namespace
`

// UpsertSkill inserts-or-updates a skills row keyed by (namespace, name).
func UpsertSkill(ctx context.Context, pool *pgxpool.Pool, row SkillRow) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertSkillTx(ctx, tx, row)
	})
}

// UpsertSkillTx — see UpsertPluginTx.
func UpsertSkillTx(ctx context.Context, tx pgx.Tx, row SkillRow) error {
	return upsertReturning(ctx, tx, upsertSkillSQL, "UpsertSkill("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name, row.StorageLocation,
		row.LastSuccessfulRefresh, row.MaxStalenessSeconds,
		row.ResourceVersion,
	)
}

// GetSkillByName reads the skills row keyed by (namespace, name).
// pgx.ErrNoRows → (nil, nil). The row is returned WITH DeletionTimestamp
// populated when set — callers MUST NOT filter on it here; the content
// service keeps serving until hard-delete by finalizer drain.
func GetSkillByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*SkillRow, error) {
	const sql = `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       deletion_timestamp, resource_version, updated_at
		  FROM skills
		 WHERE namespace = $1 AND name = $2
	`
	r := &SkillRow{}
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
		return nil, fmt.Errorf("db: GetSkillByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// ListSkills returns every live skills row in ns ordered by name ASC.
// Rows with deletion_timestamp set are excluded. Used by the platform-api
// admin inventory endpoint (read-only).
func ListSkills(ctx context.Context, pool *pgxpool.Pool, ns string) ([]SkillRow, error) {
	const sql = `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       deletion_timestamp, resource_version, updated_at
		  FROM skills
		 WHERE namespace = $1 AND deletion_timestamp IS NULL
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListSkills(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []SkillRow{}
	for rows.Next() {
		var r SkillRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.StorageLocation,
			&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
			&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("db: ListSkills(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListSkills(%s) iterate: %w", ns, err)
	}
	return out, nil
}

// SoftDeleteSkill sets deletion_timestamp = now() without removing the row.
// Idempotent on already-drained rows.
func SoftDeleteSkill(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeleteSkillSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteSkill(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SoftDeleteSkillTx — see SoftDeletePluginTx.
func SoftDeleteSkillTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeleteSkillSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteSkill(%s/%s): %w", ns, name, err)
	}
	return nil
}

const softDeleteSkillSQL = `
	UPDATE skills
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// DeleteSkill removes the skills row keyed by (namespace, name) outright.
// Called only after finalizer drain. Absence is not an error.
func DeleteSkill(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM skills WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteSkill(%s/%s): %w", ns, name, err)
	}
	return nil
}

// ResolveSkillByName resolves a context.skills entry:
//   - marketplace == "" → the skills (CRD) row for (ns, name), deletion NULL.
//   - marketplace != "" → the skill_marketplace_skills row (marketplace_name, name).
//
// (nil, nil) → caller emits 404. Mirrors ResolvePluginByName's two-arm shape.
func ResolveSkillByName(ctx context.Context, pool *pgxpool.Pool, ns, name, marketplace string) (*SkillResolution, error) {
	if marketplace == "" {
		const sql = `
			SELECT 'skill'::text AS source,
			       namespace, name, storage_location,
			       last_successful_refresh, max_staleness_seconds
			  FROM skills
			 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL`
		return scanSkillResolution(ctx, pool, sql, ns, name)
	}
	const sql = `
		SELECT 'marketplace'::text AS source,
		       marketplace_name AS namespace,
		       name, storage_location,
		       last_successful_refresh, max_staleness_seconds
		  FROM skill_marketplace_skills
		 WHERE marketplace_name = $1 AND name = $2`
	return scanSkillResolution(ctx, pool, sql, marketplace, name)
}

// scanSkillResolution runs a single-row SkillResolution query; pgx.ErrNoRows →
// (nil, nil); transient pgconn 08/57 errors propagate raw.
func scanSkillResolution(ctx context.Context, pool *pgxpool.Pool, sql, a1, a2 string) (*SkillResolution, error) {
	r := &SkillResolution{}
	if err := pool.QueryRow(ctx, sql, a1, a2).Scan(
		&r.Source, &r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ResolveSkillByName(%s/%s): %w", a1, a2, err)
	}
	return r, nil
}

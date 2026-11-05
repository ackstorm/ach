// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the prompts projection table (Phase 5 D-13,
// spec v4 §5.2 reversal). Mirrors environments.go and plugins.go shape;
// the kind-specific column is content_type (the §15.6 `Prompt.spec.contentType`
// override — NULL means caller falls back to application/octet-stream).
//
// Written by the Operator's Prompt reconciler (Plan 05-04 D-14/D-15
// extension); read by the Content Service authz pipeline (Plan 05-05) on
// every GET /content/prompt/{name}.
//
// SQL discipline mirrors environments.go: every value binds via $N; pgconn
// class 08/57 errors propagate raw; other errors wrap with non-secret
// (namespace, name) identifiers — pgErr.Message is never included
// (T-02-03-01 / T-02-03-03).

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PromptRow mirrors the prompts projection row schema after migration
// 000004 (Phase 5 D-13). (Namespace, Name) form the PRIMARY KEY.
//
// ContentType is the spec.contentType override; nil → SQL NULL → CS-06
// fallback to application/octet-stream at request time. LastSuccessfulRefresh
// is nullable (PVC-loss recovery / first-reconcile) — caller in Plan
// 05-05 emits 503 stale_cache_expired when nil (CS-10).
type PromptRow struct {
	Namespace             string     // PK part 1
	Name                  string     // PK part 2
	StorageLocation       string     // absolute path the rename(2) published to
	ContentType           *string    // spec.contentType override; nil → NULL
	LastSuccessfulRefresh *time.Time // nil on first reconcile / OP-11 reset
	MaxStalenessSeconds   int64      // spec.refresh.maxStaleness in seconds
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
}

// UpsertPrompt inserts-or-updates a row keyed by (namespace, name). The
// ON CONFLICT DO UPDATE replaces every non-PK column; updated_at is force-set
// to now() in the UPDATE branch.
//
// pgconn class 08/57 errors propagate raw (transient backoff). Other
// errors wrap with non-secret (namespace, name); pgErr.Message NEVER
// included.
const upsertPromptSQL = `
	INSERT INTO prompts
	    (namespace, name, storage_location, content_type,
	     last_successful_refresh, max_staleness_seconds,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, $7, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    storage_location        = EXCLUDED.storage_location,
	    content_type            = EXCLUDED.content_type,
	    last_successful_refresh = EXCLUDED.last_successful_refresh,
	    max_staleness_seconds   = EXCLUDED.max_staleness_seconds,
	    resource_version        = EXCLUDED.resource_version,
	    updated_at              = now(),
	    locked                  = TRUE
	WHERE prompts.origin = 'cr'
	RETURNING namespace
`

func UpsertPrompt(ctx context.Context, pool *pgxpool.Pool, row PromptRow) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertPromptTx(ctx, tx, row)
	})
}

// UpsertPromptTx — see UpsertEnvironmentTx.
func UpsertPromptTx(ctx context.Context, tx pgx.Tx, row PromptRow) error {
	return upsertReturning(ctx, tx, upsertPromptSQL, "UpsertPrompt("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name, row.StorageLocation, row.ContentType,
		row.LastSuccessfulRefresh, row.MaxStalenessSeconds,
		row.ResourceVersion,
	)
}

// GetPromptByName reads the row keyed by (namespace, name). pgx.ErrNoRows
// → (nil, nil).
func GetPromptByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*PromptRow, error) {
	const sql = `
		SELECT namespace, name, storage_location, content_type,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM prompts
		 WHERE namespace = $1 AND name = $2
	`
	r := &PromptRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation, &r.ContentType,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.ResourceVersion, &r.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetPromptByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// ListPrompts returns every prompts row in ns ordered by name ASC.
// Used by the platform-api admin inventory endpoint (read-only).
func ListPrompts(ctx context.Context, pool *pgxpool.Pool, ns string) ([]PromptRow, error) {
	const sql = `
		SELECT namespace, name, storage_location, content_type,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM prompts
		 WHERE namespace = $1
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListPrompts(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []PromptRow{}
	for rows.Next() {
		var r PromptRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.StorageLocation, &r.ContentType,
			&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
			&r.ResourceVersion, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("db: ListPrompts(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListPrompts(%s) iterate: %w", ns, err)
	}
	return out, nil
}

const deletePromptSQL = `DELETE FROM prompts WHERE namespace = $1 AND name = $2`

// DeletePrompt removes the row keyed by (namespace, name). Absence is not an
// error.
func DeletePrompt(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, deletePromptSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeletePrompt(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeletePromptTx is DeletePrompt inside an outer transaction (db.WithTxNotify).
func DeletePromptTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, deletePromptSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeletePrompt(%s/%s): %w", ns, name, err)
	}
	return nil
}

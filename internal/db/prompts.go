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
	DeletionTimestamp     *time.Time // non-nil → drain-mode (CS-09)
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
}

// UpsertPrompt inserts-or-updates a row keyed by (namespace, name). The
// ON CONFLICT DO UPDATE replaces every non-PK column EXCEPT
// deletion_timestamp (preserved per CS-09); updated_at is force-set to
// now() in the UPDATE branch.
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
// → (nil, nil). Per CS-09 the row is returned with DeletionTimestamp
// populated when set — caller MUST NOT filter on it here; the Content
// Service authz pipeline keeps serving until hard-delete by finalizer drain.
func GetPromptByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*PromptRow, error) {
	const sql = `
		SELECT namespace, name, storage_location, content_type,
		       last_successful_refresh, max_staleness_seconds,
		       deletion_timestamp, resource_version, updated_at
		  FROM prompts
		 WHERE namespace = $1 AND name = $2
	`
	r := &PromptRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation, &r.ContentType,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
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

// SoftDeletePrompt sets deletion_timestamp = now() without removing the
// row (CS-09). Idempotent: rows already in drain-mode are left untouched
// so duplicate finalizer ticks do not refresh the drain clock.
func SoftDeletePrompt(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeletePromptSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeletePrompt(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SoftDeletePromptTx — see SoftDeleteEnvironmentTx.
func SoftDeletePromptTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeletePromptSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeletePrompt(%s/%s): %w", ns, name, err)
	}
	return nil
}

const softDeletePromptSQL = `
	UPDATE prompts
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// DeletePrompt removes the row keyed by (namespace, name) outright. Called
// only after finalizer drain completes. Absence is not an error.
func DeletePrompt(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM prompts WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeletePrompt(%s/%s): %w", ns, name, err)
	}
	return nil
}

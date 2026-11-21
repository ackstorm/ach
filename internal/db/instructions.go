// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the instructions projection table. Mirrors
// environments.go and plugins.go shape.
//
// Written by the Operator's Instruction reconciler; read by the Content
// Service authz pipeline on every GET /content/instruction/{name}.
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

// InstructionRow mirrors the instructions projection row schema.
// (Namespace, Name) form the PRIMARY KEY. LastSuccessfulRefresh is nullable
// (PVC-loss recovery / first-reconcile); the Content Service answers 503
// stale_cache_expired when nil (CS-10).
type InstructionRow struct {
	Namespace             string     // PK part 1
	Name                  string     // PK part 2
	StorageLocation       string     // absolute path the rename(2) published to
	LastSuccessfulRefresh *time.Time // nil on first reconcile / OP-11 reset
	MaxStalenessSeconds   int64      // spec.refresh.maxStaleness in seconds
	ResourceVersion       string     // K8s metadata.resourceVersion at write
	UpdatedAt             time.Time  // server-set on UPSERT
	UpstreamRev           string     // external_refs.upstream_rev: the served revision
	LatestUpstreamRev     string     // external_refs.latest_upstream_rev: head of the git ref
}

// UpsertInstruction inserts-or-updates a row keyed by (namespace, name). The
// ON CONFLICT DO UPDATE replaces every non-PK column; updated_at is force-set
// to now() in the UPDATE branch.
//
// pgconn class 08/57 errors propagate raw (transient backoff). Other
// errors wrap with non-secret (namespace, name); pgErr.Message NEVER
// included.
const upsertInstructionSQL = `
	INSERT INTO instructions
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
	WHERE instructions.origin = 'cr'
	RETURNING namespace
`

// UpsertInstructionTx — see UpsertEnvironmentTx.
func UpsertInstructionTx(ctx context.Context, tx pgx.Tx, row InstructionRow) error {
	return upsertReturning(ctx, tx, upsertInstructionSQL, "UpsertInstruction("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name, row.StorageLocation,
		row.LastSuccessfulRefresh, row.MaxStalenessSeconds,
		row.ResourceVersion,
	)
}

// GetInstructionByName reads the row keyed by (namespace, name). pgx.ErrNoRows
// → (nil, nil).
func GetInstructionByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*InstructionRow, error) {
	const sql = `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM instructions
		 WHERE namespace = $1 AND name = $2
	`
	r := &InstructionRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.ResourceVersion, &r.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetInstructionByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// ListInstructions returns every instructions row in ns ordered by name ASC.
// Used by the platform-api admin inventory endpoint (read-only).
func ListInstructions(ctx context.Context, pool *pgxpool.Pool, ns string) ([]InstructionRow, error) {
	const sql = `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at,
		       COALESCE((SELECT e.upstream_rev FROM external_refs e
		                  WHERE e.kind = 'instruction' AND e.name = instructions.name), ''),
		       COALESCE((SELECT e.latest_upstream_rev FROM external_refs e
		                  WHERE e.kind = 'instruction' AND e.name = instructions.name), '')
		  FROM instructions
		 WHERE namespace = $1
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListInstructions(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []InstructionRow{}
	for rows.Next() {
		var r InstructionRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.StorageLocation,
			&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
			&r.ResourceVersion, &r.UpdatedAt, &r.UpstreamRev, &r.LatestUpstreamRev,
		); err != nil {
			return nil, fmt.Errorf("db: ListInstructions(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListInstructions(%s) iterate: %w", ns, err)
	}
	return out, nil
}

const deleteInstructionSQL = `DELETE FROM instructions WHERE namespace = $1 AND name = $2`

// DeleteInstructionTx is DeleteInstruction inside an outer transaction (db.WithTxNotify).
func DeleteInstructionTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, deleteInstructionSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteInstruction(%s/%s): %w", ns, name, err)
	}
	return nil
}

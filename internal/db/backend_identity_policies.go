// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the backend_identity_policies projection table
// (issue #34).
//
// One row per BackendIdentityPolicy CR. The operator's BIP reconciler writes
// the projection so the forwarder can resolve per-target JWT mint policy
// without an informer. ResolveWinner semantics (alphabetically LAST BIP per
// (targetKind, targetName) wins; an alpha-LAST opt-out row returns nil)
// live in internal/forwarder/bipcache — this package surfaces the raw rows
// ordered by name ASC and the cache implements the winner-picking logic.
//
// SQL discipline mirrors environments.go: origin-gated UPSERT, ErrOriginConflict
// on UI-row collision, transient pgconn 08/57 raw, other errors wrapped with
// non-secret (namespace, name).

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BIPRow mirrors the backend_identity_policies row schema after migration
// 000007. (Namespace, Name) form the PRIMARY KEY; (TargetKind, TargetName)
// is the bipcache lookup key.
type BIPRow struct {
	Namespace          string
	Name               string
	TargetKind         string // "MCPServer" | "A2AAgent"
	TargetName         string
	ForwardIdentityJWT bool
	ObservedGeneration int64
	DeletionTimestamp  *time.Time
	ResourceVersion    string
	UpdatedAt          time.Time
	Origin             string
	Locked             bool
}

const upsertBIPSQL = `
	INSERT INTO backend_identity_policies
	    (namespace, name, target_kind, target_name,
	     forward_identity_jwt, observed_generation,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, $7, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    target_kind          = EXCLUDED.target_kind,
	    target_name          = EXCLUDED.target_name,
	    forward_identity_jwt = EXCLUDED.forward_identity_jwt,
	    observed_generation  = EXCLUDED.observed_generation,
	    resource_version     = EXCLUDED.resource_version,
	    updated_at           = now(),
	    -- Resurrection: a re-applied CR is LIVE. Unlike environments.go (which
	    -- preserves deletion_timestamp for the CS-09 content-drain window), a
	    -- BIP soft-delete is purely a forwarder cache-eviction signal with no
	    -- drain consumer, so a delete-then-recreate of the SAME (namespace,name)
	    -- MUST clear the tombstone. Otherwise the resurrected policy stays
	    -- invisible to ListAllBIPs (which filters deletion_timestamp IS NULL)
	    -- and the forwarder never sees it again.
	    deletion_timestamp   = NULL,
	    locked               = TRUE
	WHERE backend_identity_policies.origin = 'cr'
	RETURNING namespace
`

// UpsertBIP inserts-or-updates the row keyed by (Namespace, Name). Returns
// ErrOriginConflict on UI-owned-row collision.
func UpsertBIP(ctx context.Context, pool *pgxpool.Pool, row BIPRow) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertBIP(%s/%s): begin: %w", row.Namespace, row.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := UpsertBIPTx(ctx, tx, row); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertBIP(%s/%s): commit: %w", row.Namespace, row.Name, err)
	}
	return nil
}

// UpsertBIPTx exposes the tx-form upsert for callers inside db.WithTxNotify.
func UpsertBIPTx(ctx context.Context, tx pgx.Tx, row BIPRow) error {
	var ns string
	err := tx.QueryRow(ctx, upsertBIPSQL,
		row.Namespace, row.Name, row.TargetKind, row.TargetName,
		row.ForwardIdentityJWT, row.ObservedGeneration, row.ResourceVersion,
	).Scan(&ns)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOriginConflict
		}
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertBIP(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// GetBIPByName returns the row keyed by (Namespace, Name) or (nil, nil) on
// absence.
func GetBIPByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*BIPRow, error) {
	const sql = `
		SELECT namespace, name, target_kind, target_name,
		       forward_identity_jwt, observed_generation,
		       deletion_timestamp, resource_version, updated_at, origin, locked
		  FROM backend_identity_policies
		 WHERE namespace = $1 AND name = $2
	`
	r := &BIPRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name, &r.TargetKind, &r.TargetName,
		&r.ForwardIdentityJWT, &r.ObservedGeneration,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt, &r.Origin, &r.Locked,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetBIPByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// ListBIPsByTarget returns every live row matching (namespace, target_kind,
// target_name) ordered by name ASC. Caller (bipcache.Resolve) picks the
// alpha-LAST winner. Rows with deletion_timestamp set are excluded.
func ListBIPsByTarget(ctx context.Context, pool *pgxpool.Pool, ns, targetKind, targetName string) ([]BIPRow, error) {
	const sql = `
		SELECT namespace, name, target_kind, target_name,
		       forward_identity_jwt, observed_generation,
		       deletion_timestamp, resource_version, updated_at, origin, locked
		  FROM backend_identity_policies
		 WHERE namespace   = $1
		   AND target_kind = $2
		   AND target_name = $3
		   AND deletion_timestamp IS NULL
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns, targetKind, targetName)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListBIPsByTarget(%s/%s/%s): %w", ns, targetKind, targetName, err)
	}
	defer rows.Close()
	out := []BIPRow{}
	for rows.Next() {
		var r BIPRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.TargetKind, &r.TargetName,
			&r.ForwardIdentityJWT, &r.ObservedGeneration,
			&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt, &r.Origin, &r.Locked,
		); err != nil {
			return nil, fmt.Errorf("db: ListBIPsByTarget(%s/%s/%s) scan: %w", ns, targetKind, targetName, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListBIPsByTarget(%s/%s/%s) iterate: %w", ns, targetKind, targetName, err)
	}
	return out, nil
}

// ListAllBIPs returns every live row in the namespace, ordered by name ASC.
// Used by the forwarder bipcache 5-minute periodic refresh.
func ListAllBIPs(ctx context.Context, pool *pgxpool.Pool, ns string) ([]BIPRow, error) {
	const sql = `
		SELECT namespace, name, target_kind, target_name,
		       forward_identity_jwt, observed_generation,
		       deletion_timestamp, resource_version, updated_at, origin, locked
		  FROM backend_identity_policies
		 WHERE namespace = $1 AND deletion_timestamp IS NULL
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListAllBIPs(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []BIPRow{}
	for rows.Next() {
		var r BIPRow
		if err := rows.Scan(
			&r.Namespace, &r.Name, &r.TargetKind, &r.TargetName,
			&r.ForwardIdentityJWT, &r.ObservedGeneration,
			&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt, &r.Origin, &r.Locked,
		); err != nil {
			return nil, fmt.Errorf("db: ListAllBIPs(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListAllBIPs(%s) iterate: %w", ns, err)
	}
	return out, nil
}

const softDeleteBIPSQL = `
	UPDATE backend_identity_policies
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// SoftDeleteBIP marks the row drain-mode. Idempotent.
func SoftDeleteBIP(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeleteBIPSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteBIP(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SoftDeleteBIPTx exposes the tx-form for callers inside db.WithTxNotify.
func SoftDeleteBIPTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeleteBIPSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteBIP(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeleteBIP removes the row outright. Called only after finalizer drain.
func DeleteBIP(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM backend_identity_policies WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteBIP(%s/%s): %w", ns, name, err)
	}
	return nil
}

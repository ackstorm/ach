// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the litellm_connections projection table (issue #34).
//
// The operator's LiteLLMConnection reconciler projects the CR spec here so
// the forwarder can boot from Postgres alone — the Secret pointed at by
// master_key_secret_namespace/name/key stays on the Kubernetes control plane
// (Secrets are not CRDs and the forwarder retains its filtered Secret
// informer for hot-reload).
//
// Steady state: a single row named 'default' in the operator's namespace.
// GetDefaultLiteLLMConnection returns (nil, nil) on absence so the forwarder
// resolveLiteLLMWithRetry path can poll until the CR is reconciled at
// cluster-up time.
//
// SQL discipline mirrors environments.go: origin-gated UPSERT via ON CONFLICT
// WHERE existing.origin = 'cr' so a UI-managed row (future) cannot be
// clobbered by the operator. ErrOriginConflict is returned on the filter
// miss; transient pgconn 08/57 errors propagate raw for controller-runtime
// backoff.

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LiteLLMConnectionRow mirrors the litellm_connections row schema after
// migration 000006. (Namespace, Name) form the PRIMARY KEY; the operator
// always writes Name='default'.
type LiteLLMConnectionRow struct {
	Namespace                string
	Name                     string
	Endpoint                 string
	MasterKeySecretNamespace string
	MasterKeySecretName      string
	MasterKeySecretKey       string
	DeletionTimestamp        *time.Time
	ResourceVersion          string
	UpdatedAt                time.Time
	Origin                   string // "cr" | "ui"
	Locked                   bool
}

const upsertLiteLLMConnectionSQL = `
	INSERT INTO litellm_connections
	    (namespace, name, endpoint,
	     master_key_secret_namespace, master_key_secret_name, master_key_secret_key,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2, $3, $4, $5, $6, $7, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    endpoint                    = EXCLUDED.endpoint,
	    master_key_secret_namespace = EXCLUDED.master_key_secret_namespace,
	    master_key_secret_name      = EXCLUDED.master_key_secret_name,
	    master_key_secret_key       = EXCLUDED.master_key_secret_key,
	    resource_version            = EXCLUDED.resource_version,
	    updated_at                  = now(),
	    locked                      = TRUE
	WHERE litellm_connections.origin = 'cr'
	RETURNING namespace
`

// UpsertLiteLLMConnection inserts-or-updates the row keyed by (Namespace,
// Name). Returns ErrOriginConflict if a UI-owned row already holds the same
// PK; transient pgconn 08/57 propagated raw for controller-runtime backoff.
func UpsertLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, row LiteLLMConnectionRow) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertLiteLLMConnection(%s/%s): begin: %w", row.Namespace, row.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := UpsertLiteLLMConnectionTx(ctx, tx, row); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertLiteLLMConnection(%s/%s): commit: %w", row.Namespace, row.Name, err)
	}
	return nil
}

// UpsertLiteLLMConnectionTx exposes the tx-form upsert for callers inside
// db.WithTxNotify.
func UpsertLiteLLMConnectionTx(ctx context.Context, tx pgx.Tx, row LiteLLMConnectionRow) error {
	var ns string
	err := tx.QueryRow(ctx, upsertLiteLLMConnectionSQL,
		row.Namespace, row.Name, row.Endpoint,
		row.MasterKeySecretNamespace, row.MasterKeySecretName, row.MasterKeySecretKey,
		row.ResourceVersion,
	).Scan(&ns)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOriginConflict
		}
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertLiteLLMConnection(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// GetDefaultLiteLLMConnection returns the (namespace, 'default') row or
// (nil, nil) on absence. Steady-state callers (forwarder boot) treat
// (nil, nil) as "not yet reconciled" and retry.
func GetDefaultLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, ns string) (*LiteLLMConnectionRow, error) {
	const sql = `
		SELECT namespace, name, endpoint,
		       master_key_secret_namespace, master_key_secret_name, master_key_secret_key,
		       deletion_timestamp, resource_version, updated_at, origin, locked
		  FROM litellm_connections
		 WHERE namespace = $1 AND name = 'default'
	`
	r := &LiteLLMConnectionRow{}
	if err := pool.QueryRow(ctx, sql, ns).Scan(
		&r.Namespace, &r.Name, &r.Endpoint,
		&r.MasterKeySecretNamespace, &r.MasterKeySecretName, &r.MasterKeySecretKey,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt, &r.Origin, &r.Locked,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetDefaultLiteLLMConnection(%s): %w", ns, err)
	}
	return r, nil
}

const softDeleteLiteLLMConnectionSQL = `
	UPDATE litellm_connections
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// SoftDeleteLiteLLMConnection marks the row drain-mode but preserves it
// (mirrors environments.go semantics). Idempotent on already-drained rows.
func SoftDeleteLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeleteLiteLLMConnectionSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteLiteLLMConnection(%s/%s): %w", ns, name, err)
	}
	return nil
}

// SoftDeleteLiteLLMConnectionTx is the tx-form for callers inside
// db.WithTxNotify.
func SoftDeleteLiteLLMConnectionTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeleteLiteLLMConnectionSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteLiteLLMConnection(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeleteLiteLLMConnection removes the row outright. Called only after
// finalizer drain completes. Absence is not an error.
func DeleteLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM litellm_connections WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteLiteLLMConnection(%s/%s): %w", ns, name, err)
	}
	return nil
}

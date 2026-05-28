// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the environments projection table (Phase 5 D-13,
// spec v4 §5.2 reversal). The Operator's Environment reconciler upserts
// the row in the same transaction as its K8s Status subresource write;
// Content Service reads via GetEnvironmentByName per request.
//
// Spec v4 §5.2 (line 13) makes Postgres authoritative for ACH CRD spec/
// status reads by Platform API, Forwarder, and Content Service — only the
// Operator watches Kubernetes. This file is the writer-side surface for
// the Environment kind; pipeline.go in internal/contentservice will read
// from it (gated by envcache.Cache per D-07).
//
// Column set is pinned by Phase 5 D-13:
//
//   - authorized_teams text[], context_prompts/plugins/artifacts text[],
//     runtime_models/mcp_servers/a2a_agents text[]: §5.1 authorization
//     surface.
//   - available_condition, access_group_synced_condition,
//     execution_resources_resolved_condition jsonb: dual-written from the
//     Operator's existing condition rollup (D-14 / D-15) for kubectl-
//     describe parity with the K8s status subresource.
//   - deletion_timestamp timestamptz NULL: §6.5 / CS-09 — Content Service
//     serves until the row is hard-deleted by finalizer drain.
//   - resource_version text, updated_at timestamptz: dual-write idempotency
//     anchors.
//
// SQL discipline mirrors external_refs.go: every value binds via $N
// (T-02-03-01 — zero string concatenation); pgconn class 08/57 errors
// return raw so controller-runtime's exponential backoff retries; other
// errors wrap via fmt.Errorf with non-secret (namespace, name) identifiers
// — pgErr.Message contents are NEVER echoed (T-02-03-03).

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvironmentRow mirrors the environments projection row schema after
// migration 000004 (Phase 5 D-13). Together (Namespace, Name) form the
// PRIMARY KEY.
//
// The three Condition columns carry raw jsonb bytes — the caller in Plan
// 05-04 JSON-marshals []metav1.Condition slices before passing. Nil
// slices map to SQL NULL via pgx's []byte/jsonb default mapping.
type EnvironmentRow struct {
	Namespace         string   // PK part 1 — K8s namespace
	Name              string   // PK part 2 — Environment metadata.name
	AuthorizedTeams   []string // spec.authorizedTeams
	ContextPrompts    []string // spec.context.prompts
	ContextPlugins    []string // spec.context.plugins
	ContextArtifacts  []string // spec.context.artifacts
	RuntimeModels     []string // spec.runtime.models
	RuntimeMCPServers []string // spec.runtime.mcpServers
	RuntimeA2AAgents  []string // spec.runtime.a2aAgents

	// Raw jsonb-encoded condition payloads (caller JSON-marshals).
	AvailableCondition                  []byte
	AccessGroupSyncedCondition          []byte
	ExecutionResourcesResolvedCondition []byte

	DeletionTimestamp *time.Time // non-nil = drain-mode (CS-09)
	ResourceVersion   string     // K8s metadata.resourceVersion at write time
	UpdatedAt         time.Time  // server-set on UPSERT
}

// UpsertEnvironment inserts-or-updates a row keyed by (namespace, name).
// The ON CONFLICT DO UPDATE replaces every non-PK column except
// deletion_timestamp, which is preserved (only SoftDeleteEnvironment writes
// it) — per CS-09 / D-14 a steady-state reconcile must not unset the drain
// marker. updated_at is force-set to now() in the UPDATE branch.
//
// Returns raw error on pgconn class 08/57 (transient) so controller-runtime
// applies exponential backoff. Other errors wrap with non-secret
// (namespace, name) identifiers; pgErr.Message is never included.
// upsertEnvironmentSQL inserts/updates CR-origin environment rows. The
// ON CONFLICT WHERE clause filters by existing.origin='cr' so a UI-managed
// row blocks the operator's UPDATE; the RETURNING + ErrNoRows mapping
// converts that filter miss into ErrOriginConflict (issue #34).
const upsertEnvironmentSQL = `
	INSERT INTO environments
	    (namespace, name,
	     authorized_teams, context_prompts, context_plugins, context_artifacts,
	     runtime_models, runtime_mcp_servers, runtime_a2a_agents,
	     available_condition, access_group_synced_condition,
	     execution_resources_resolved_condition,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2,
	        $3, $4, $5, $6,
	        $7, $8, $9,
	        $10, $11, $12,
	        $13, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    authorized_teams                       = EXCLUDED.authorized_teams,
	    context_prompts                        = EXCLUDED.context_prompts,
	    context_plugins                        = EXCLUDED.context_plugins,
	    context_artifacts                      = EXCLUDED.context_artifacts,
	    runtime_models                         = EXCLUDED.runtime_models,
	    runtime_mcp_servers                    = EXCLUDED.runtime_mcp_servers,
	    runtime_a2a_agents                     = EXCLUDED.runtime_a2a_agents,
	    available_condition                    = EXCLUDED.available_condition,
	    access_group_synced_condition          = EXCLUDED.access_group_synced_condition,
	    execution_resources_resolved_condition = EXCLUDED.execution_resources_resolved_condition,
	    resource_version                       = EXCLUDED.resource_version,
	    updated_at                             = now(),
	    locked                                 = TRUE
	WHERE environments.origin = 'cr'
	RETURNING namespace
`

func UpsertEnvironment(ctx context.Context, pool *pgxpool.Pool, row EnvironmentRow) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertEnvironment(%s/%s): begin: %w", row.Namespace, row.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertEnvironmentTx(ctx, tx, row); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertEnvironment(%s/%s): commit: %w", row.Namespace, row.Name, err)
	}
	return nil
}

// upsertEnvironmentTx is the tx-form helper that controllers use via
// db.WithTxNotify so the projection write and the pg_notify call commit
// atomically. The bare pool form above is retained for tests and callers
// without an outer transaction.
func upsertEnvironmentTx(ctx context.Context, tx pgx.Tx, row EnvironmentRow) error {
	var ns string
	err := tx.QueryRow(ctx, upsertEnvironmentSQL,
		row.Namespace, row.Name,
		row.AuthorizedTeams, row.ContextPrompts, row.ContextPlugins, row.ContextArtifacts,
		row.RuntimeModels, row.RuntimeMCPServers, row.RuntimeA2AAgents,
		row.AvailableCondition, row.AccessGroupSyncedCondition,
		row.ExecutionResourcesResolvedCondition,
		row.ResourceVersion,
	).Scan(&ns)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT WHERE existing.origin='cr' filtered the row out:
			// the existing row is non-CR origin (UI-owned).
			return ErrOriginConflict
		}
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertEnvironment(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// GetEnvironmentByName reads the row keyed by (namespace, name). On
// pgx.ErrNoRows it returns (nil, nil) — absence is not an error. The
// row is returned WITH deletion_timestamp populated when set; per CS-09
// the Content Service authz pipeline keeps serving until the row is
// hard-deleted (DeleteEnvironment) by finalizer drain — callers must not
// filter on deletion_timestamp here.
//
// Pgconn 08/57 errors propagate raw; other errors wrap with the
// (namespace, name) identifiers per the package convention.
func GetEnvironmentByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*EnvironmentRow, error) {
	const sql = `
		SELECT namespace, name,
		       authorized_teams, context_prompts, context_plugins, context_artifacts,
		       runtime_models, runtime_mcp_servers, runtime_a2a_agents,
		       available_condition, access_group_synced_condition,
		       execution_resources_resolved_condition,
		       deletion_timestamp, resource_version, updated_at
		  FROM environments
		 WHERE namespace = $1 AND name = $2
	`
	r := &EnvironmentRow{}
	if err := pool.QueryRow(ctx, sql, ns, name).Scan(
		&r.Namespace, &r.Name,
		&r.AuthorizedTeams, &r.ContextPrompts, &r.ContextPlugins, &r.ContextArtifacts,
		&r.RuntimeModels, &r.RuntimeMCPServers, &r.RuntimeA2AAgents,
		&r.AvailableCondition, &r.AccessGroupSyncedCondition,
		&r.ExecutionResourcesResolvedCondition,
		&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: GetEnvironmentByName(%s/%s): %w", ns, name, err)
	}
	return r, nil
}

// SoftDeleteEnvironment sets deletion_timestamp = now() without removing the
// row (CS-09 — Content Service serves until full removal). Idempotent: a
// row already in drain-mode (deletion_timestamp IS NOT NULL) is left
// untouched, so duplicate finalizer ticks do not refresh the drain clock.
//
// updated_at is bumped on the matching path so the Operator's downstream
// readers see fresh wall-clock state.
func SoftDeleteEnvironment(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, softDeleteEnvironmentSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteEnvironment(%s/%s): %w", ns, name, err)
	}
	return nil
}

// softDeleteEnvironmentTx mirrors SoftDeleteEnvironment for use inside an
// outer transaction (db.WithTxNotify). Same idempotent semantics.
func softDeleteEnvironmentTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, softDeleteEnvironmentSQL, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SoftDeleteEnvironment(%s/%s): %w", ns, name, err)
	}
	return nil
}

const softDeleteEnvironmentSQL = `
	UPDATE environments
	   SET deletion_timestamp = now(),
	       updated_at         = now()
	 WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
`

// DeleteEnvironment removes the row keyed by (namespace, name) outright.
// Called only after the Operator's finalizer drain completes (CS-09);
// the soft-delete path is the steady-state mechanism for K8s `kubectl
// delete environment` (SoftDeleteEnvironment + finalizer + DeleteEnvironment).
//
// Absence is not an error — DELETE of a non-existent row is a no-op.
func DeleteEnvironment(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	const sql = `DELETE FROM environments WHERE namespace = $1 AND name = $2`
	if _, err := pool.Exec(ctx, sql, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteEnvironment(%s/%s): %w", ns, name, err)
	}
	return nil
}

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
	ContextSkills     []string // spec.context.skills
	RuntimeModels     []string // spec.runtime.models
	RuntimeMCPServers []string // spec.runtime.mcpServers
	RuntimeA2AAgents  []string // spec.runtime.a2aAgents
	RuntimeGuardrails []string // spec.runtime.guardrails

	// Raw jsonb-encoded condition payloads (caller JSON-marshals).
	AvailableCondition                  []byte
	AccessGroupSyncedCondition          []byte
	ExecutionResourcesResolvedCondition []byte

	// Notice is spec.notice — optional post-hydrate advisory text (may be "").
	Notice string

	// Description is spec.description — optional catalog metadata (may be "").
	Description string

	DeletionTimestamp *time.Time // non-nil = drain-mode (CS-09)
	ResourceVersion   string     // K8s metadata.resourceVersion at write time
	UpdatedAt         time.Time  // server-set on UPSERT
}

// UpsertEnvironment inserts-or-updates a row keyed by (namespace, name).
// The ON CONFLICT DO UPDATE replaces every non-PK column. A live reconcile
// (this path only runs for a CR with no metadata.deletionTimestamp) clears
// deletion_timestamp to NULL — a recreated CR reusing a soft-deleted name
// MUST drop the stale drain marker, else env list (WHERE deletion_timestamp
// IS NULL) hides the resurrected env forever (incident 2026-06-04). The
// drain marker is owned solely by SoftDeleteEnvironment, which runs on the
// deletion branch and never overlaps this upsert. updated_at is force-set
// to now() in the UPDATE branch.
//
// Returns raw error on pgconn class 08/57 (transient) so controller-runtime
// applies exponential backoff. Other errors wrap with non-secret
// (namespace, name) identifiers; pgErr.Message is never included.
// upsertEnvironmentSQL inserts/updates the environment row from a CR reconcile.
// GitOps-wins (G2): the operator is always authoritative, so the ON CONFLICT
// DO UPDATE is UN-gated and force-sets origin='cr' — a CR applied over a
// UI-managed row (origin='ui') TAKES IT OVER (origin flips to 'cr', locked=TRUE,
// the (namespace,name) primary key preserved). The DO UPDATE therefore always
// fires and RETURNING always yields a row, so this path never returns
// ErrOriginConflict. The UI write path (internal/db/ui_objects.go) is gated the
// other way (WHERE origin='ui') so it can never clobber an operator-owned row.
const upsertEnvironmentSQL = `
	INSERT INTO environments
	    (namespace, name,
	     authorized_teams, context_prompts, context_plugins, context_artifacts,
	     runtime_models, runtime_mcp_servers, runtime_a2a_agents,
	     available_condition, access_group_synced_condition,
	     execution_resources_resolved_condition,
	     resource_version, context_skills, notice, description, runtime_guardrails, updated_at, origin, locked)
	VALUES ($1, $2,
	        $3, $4, $5, $6,
	        $7, $8, $9,
	        $10, $11, $12,
	        $13, $14, $15, $16, $17, now(), 'cr', TRUE)
	ON CONFLICT (namespace, name) DO UPDATE SET
	    authorized_teams                       = EXCLUDED.authorized_teams,
	    context_prompts                        = EXCLUDED.context_prompts,
	    context_plugins                        = EXCLUDED.context_plugins,
	    context_artifacts                      = EXCLUDED.context_artifacts,
	    context_skills                         = EXCLUDED.context_skills,
	    notice                                 = EXCLUDED.notice,
	    description                             = EXCLUDED.description,
	    runtime_models                         = EXCLUDED.runtime_models,
	    runtime_mcp_servers                    = EXCLUDED.runtime_mcp_servers,
	    runtime_a2a_agents                     = EXCLUDED.runtime_a2a_agents,
	    runtime_guardrails                     = EXCLUDED.runtime_guardrails,
	    available_condition                    = EXCLUDED.available_condition,
	    access_group_synced_condition          = EXCLUDED.access_group_synced_condition,
	    execution_resources_resolved_condition = EXCLUDED.execution_resources_resolved_condition,
	    resource_version                       = EXCLUDED.resource_version,
	    updated_at                             = now(),
	    deletion_timestamp                     = NULL,
	    origin                                 = 'cr',
	    locked                                 = TRUE
	RETURNING namespace
`

func UpsertEnvironment(ctx context.Context, pool *pgxpool.Pool, row EnvironmentRow) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertEnvironmentTx(ctx, tx, row)
	})
}

// UpsertEnvironmentTx is the tx-form helper that controllers use via
// db.WithTxNotify so the projection write and the pg_notify call commit
// atomically. The pool form above is retained for tests and callers without
// an outer transaction. GitOps-wins: a CR takes over any UI-managed row, so
// this never returns ErrOriginConflict. Transient pgconn 08/57 propagate raw;
// other errors wrap with (namespace, name).
func UpsertEnvironmentTx(ctx context.Context, tx pgx.Tx, row EnvironmentRow) error {
	return upsertReturning(ctx, tx, upsertEnvironmentSQL, "UpsertEnvironment("+row.Namespace+"/"+row.Name+")",
		row.Namespace, row.Name,
		row.AuthorizedTeams, row.ContextPrompts, row.ContextPlugins, row.ContextArtifacts,
		row.RuntimeModels, row.RuntimeMCPServers, row.RuntimeA2AAgents,
		row.AvailableCondition, row.AccessGroupSyncedCondition,
		row.ExecutionResourcesResolvedCondition,
		row.ResourceVersion,
		row.ContextSkills,
		row.Notice,
		row.Description,
		// orEmptyStrings: runtime_guardrails is `text[] NOT NULL` — a nil Go
		// slice binds to SQL NULL (the column is present in every INSERT, so
		// the DEFAULT never applies), which would violate the constraint.
		orEmptyStrings(row.RuntimeGuardrails),
	)
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
		       context_skills, notice, description,
		       runtime_models, runtime_mcp_servers, runtime_a2a_agents, runtime_guardrails,
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
		&r.ContextSkills, &r.Notice, &r.Description,
		&r.RuntimeModels, &r.RuntimeMCPServers, &r.RuntimeA2AAgents, &r.RuntimeGuardrails,
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

// ListEnvironments returns every live row in ns ordered by name ASC. Used by
// the platform-api store (B1) and the forwarder envstore (C2) periodic
// refresh. Rows with deletion_timestamp set are excluded (the steady-state
// caller — platform-api environments list, forwarder precheck — wants the
// not-draining set; the Content Service authz path uses GetEnvironmentByName
// which deliberately surfaces drain-mode rows per CS-09).
func ListEnvironments(ctx context.Context, pool *pgxpool.Pool, ns string) ([]EnvironmentRow, error) {
	const sql = `
		SELECT namespace, name,
		       authorized_teams, context_prompts, context_plugins, context_artifacts,
		       context_skills, notice, description,
		       runtime_models, runtime_mcp_servers, runtime_a2a_agents, runtime_guardrails,
		       available_condition, access_group_synced_condition,
		       execution_resources_resolved_condition,
		       deletion_timestamp, resource_version, updated_at
		  FROM environments
		 WHERE namespace = $1 AND deletion_timestamp IS NULL
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListEnvironments(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []EnvironmentRow{}
	for rows.Next() {
		var r EnvironmentRow
		if err := rows.Scan(
			&r.Namespace, &r.Name,
			&r.AuthorizedTeams, &r.ContextPrompts, &r.ContextPlugins, &r.ContextArtifacts,
			&r.ContextSkills, &r.Notice, &r.Description,
			&r.RuntimeModels, &r.RuntimeMCPServers, &r.RuntimeA2AAgents, &r.RuntimeGuardrails,
			&r.AvailableCondition, &r.AccessGroupSyncedCondition,
			&r.ExecutionResourcesResolvedCondition,
			&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("db: ListEnvironments(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListEnvironments(%s) iterate: %w", ns, err)
	}
	return out, nil
}

// ListEnvironmentsIncludingDraining returns every row in ns ordered by name
// ASC, INCLUDING drain-mode rows (deletion_timestamp IS NOT NULL). Used by the
// content-service in-memory env cache, which must keep serving environments
// under finalizer drain per CS-09 — the inverse of ListEnvironments, which
// excludes drain-mode rows for the steady-state platform-api / forwarder
// callers.
func ListEnvironmentsIncludingDraining(ctx context.Context, pool *pgxpool.Pool, ns string) ([]EnvironmentRow, error) {
	const sql = `
		SELECT namespace, name,
		       authorized_teams, context_prompts, context_plugins, context_artifacts,
		       context_skills, notice, description,
		       runtime_models, runtime_mcp_servers, runtime_a2a_agents, runtime_guardrails,
		       available_condition, access_group_synced_condition,
		       execution_resources_resolved_condition,
		       deletion_timestamp, resource_version, updated_at
		  FROM environments
		 WHERE namespace = $1
		 ORDER BY name ASC
	`
	rows, err := pool.Query(ctx, sql, ns)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListEnvironmentsIncludingDraining(%s): %w", ns, err)
	}
	defer rows.Close()
	out := []EnvironmentRow{}
	for rows.Next() {
		var r EnvironmentRow
		if err := rows.Scan(
			&r.Namespace, &r.Name,
			&r.AuthorizedTeams, &r.ContextPrompts, &r.ContextPlugins, &r.ContextArtifacts,
			&r.ContextSkills, &r.Notice, &r.Description,
			&r.RuntimeModels, &r.RuntimeMCPServers, &r.RuntimeA2AAgents, &r.RuntimeGuardrails,
			&r.AvailableCondition, &r.AccessGroupSyncedCondition,
			&r.ExecutionResourcesResolvedCondition,
			&r.DeletionTimestamp, &r.ResourceVersion, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("db: ListEnvironmentsIncludingDraining(%s) scan: %w", ns, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListEnvironmentsIncludingDraining(%s) iterate: %w", ns, err)
	}
	return out, nil
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

// SoftDeleteEnvironmentTx mirrors SoftDeleteEnvironment for use inside an
// outer transaction (db.WithTxNotify). Same idempotent semantics.
func SoftDeleteEnvironmentTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
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

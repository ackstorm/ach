// SPDX-License-Identifier: Apache-2.0

// Package db — UI-side write helpers for the GitOps-wins UI Objects API (G2).
//
// These are the symmetric counterpart of the operator's projection writers
// (environments.go etc.). Where the operator path is un-gated and takes over
// any row (origin→'cr', GitOps-wins), the UI path is gated WHERE origin='ui'
// so it can NEVER clobber an operator-owned row:
//
//   - InsertUIObject creates a row with origin='ui', locked=FALSE. A pre-
//     existing operator row → ErrConflictWithCR (409); a pre-existing UI row →
//     ErrUIAlreadyExists (409, use PATCH).
//   - UpdateUIObject / DeleteUIObject only touch origin='ui' rows. Targeting an
//     operator-owned row → ErrImmutableViaUI (403); a missing row →
//     ErrUINotFound (404).
//
// A UI-created Environment is a DRAFT: its status condition columns are NULL
// (the operator has not reconciled it), so it is not Available and hydrate will
// not serve it until it is promoted by applying the equivalent CR (which the
// operator then takes over — see upsertEnvironmentSQL). The GitOps round-trip
// is: draft in UI → export YAML (export.go) → commit + kubectl apply → operator
// takeover. v1 scopes the UI-writable set to Environment only (the external-ref
// kinds' projection stores operator-computed cache state, not the source spec,
// so they cannot be authored or exported from the projection).
//
// Each write emits the same ach_environments_changed NOTIFY the operator does,
// so the platform-api / forwarder caches refresh promptly (the 5-minute
// periodic refresh is the at-most-once safety net).

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvironmentsNotifyChannel is the LISTEN/NOTIFY channel for the environments
// projection (mirrors internal/controller/ach environmentsChannel — same
// Postgres channel contract). Exported so the UI write path and any future
// caller stay in lock-step on the name.
const EnvironmentsNotifyChannel = "ach_environments_changed"

// orEmptyStrings coalesces a nil slice to a non-nil empty slice so the
// `text[] NOT NULL` columns never receive SQL NULL on a UI insert/update.
func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// uiEnvironmentArgs is the shared positional binding for the UI insert/update
// SQL ($1..$13): the PK plus every spec-derived column. resource_version,
// origin, locked, and the operator-computed condition columns are NOT bound
// here — the SQL pins them as literals (resource_version=”, origin='ui',
// locked=FALSE) or leaves them NULL/default.
func uiEnvironmentArgs(row EnvironmentRow) []any {
	return []any{
		row.Namespace, row.Name,
		orEmptyStrings(row.AuthorizedTeams),
		orEmptyStrings(row.ContextPrompts),
		orEmptyStrings(row.ContextPlugins),
		orEmptyStrings(row.ContextArtifacts),
		orEmptyStrings(row.RuntimeModels),
		orEmptyStrings(row.RuntimeMCPServers),
		orEmptyStrings(row.RuntimeA2AAgents),
		orEmptyStrings(row.ContextSkills),
		row.Notice,
		row.Description,
		orEmptyStrings(row.RuntimeGuardrails),
	}
}

const insertUIEnvironmentSQL = `
	INSERT INTO environments
	    (namespace, name,
	     authorized_teams, context_prompts, context_plugins, context_artifacts,
	     runtime_models, runtime_mcp_servers, runtime_a2a_agents,
	     context_skills, notice, description, runtime_guardrails,
	     resource_version, updated_at, origin, locked)
	VALUES ($1, $2,
	        $3, $4, $5, $6,
	        $7, $8, $9,
	        $10, $11, $12, $13,
	        '', now(), 'ui', FALSE)
	ON CONFLICT (namespace, name) DO NOTHING
	RETURNING namespace
`

// InsertUIEnvironment creates a UI-owned (origin='ui', locked=FALSE) draft
// Environment row and NOTIFYs. On a (namespace,name) collision it returns
// ErrConflictWithCR when the existing row is operator-owned, else
// ErrUIAlreadyExists.
func InsertUIEnvironment(ctx context.Context, pool *pgxpool.Pool, row EnvironmentRow) error {
	payload := fmt.Sprintf("%s/%s", row.Namespace, row.Name)
	return WithTxNotify(ctx, pool, EnvironmentsNotifyChannel, payload, func(tx pgx.Tx) error {
		var ns string
		err := tx.QueryRow(ctx, insertUIEnvironmentSQL, uiEnvironmentArgs(row)...).Scan(&ns)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			if isTransientPgErr(err) {
				return err
			}
			return fmt.Errorf("db: InsertUIEnvironment(%s): %w", payload, err)
		}
		// DO NOTHING fired → a row already exists. Disambiguate by origin.
		origin, found, gerr := environmentOriginTx(ctx, tx, row.Namespace, row.Name)
		if gerr != nil {
			return gerr
		}
		if found && origin == "cr" {
			return ErrConflictWithCR
		}
		return ErrUIAlreadyExists
	})
}

const updateUIEnvironmentSQL = `
	UPDATE environments SET
	    authorized_teams    = $3,
	    context_prompts     = $4,
	    context_plugins     = $5,
	    context_artifacts   = $6,
	    runtime_models      = $7,
	    runtime_mcp_servers = $8,
	    runtime_a2a_agents  = $9,
	    context_skills      = $10,
	    notice              = $11,
	    description         = $12,
	    runtime_guardrails  = $13,
	    updated_at          = now()
	 WHERE namespace = $1 AND name = $2 AND origin = 'ui'
	RETURNING namespace
`

// UpdateUIEnvironment replaces the spec columns of a UI-owned Environment row
// (the platform-api applies a JSON-merge to the spec before calling this) and
// NOTIFYs. Returns ErrImmutableViaUI when the target is operator-owned, or
// ErrUINotFound when no row exists.
func UpdateUIEnvironment(ctx context.Context, pool *pgxpool.Pool, row EnvironmentRow) error {
	payload := fmt.Sprintf("%s/%s", row.Namespace, row.Name)
	return WithTxNotify(ctx, pool, EnvironmentsNotifyChannel, payload, func(tx pgx.Tx) error {
		var ns string
		err := tx.QueryRow(ctx, updateUIEnvironmentSQL, uiEnvironmentArgs(row)...).Scan(&ns)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			if isTransientPgErr(err) {
				return err
			}
			return fmt.Errorf("db: UpdateUIEnvironment(%s): %w", payload, err)
		}
		return classifyUIWriteMiss(ctx, tx, row.Namespace, row.Name)
	})
}

const deleteUIEnvironmentSQL = `
	DELETE FROM environments WHERE namespace = $1 AND name = $2 AND origin = 'ui'
`

// DeleteUIEnvironment removes a UI-owned Environment row and NOTIFYs. Returns
// ErrImmutableViaUI when the target is operator-owned, or ErrUINotFound when no
// row exists.
func DeleteUIEnvironment(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	payload := fmt.Sprintf("%s/%s", ns, name)
	return WithTxNotify(ctx, pool, EnvironmentsNotifyChannel, payload, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, deleteUIEnvironmentSQL, ns, name)
		if err != nil {
			if isTransientPgErr(err) {
				return err
			}
			return fmt.Errorf("db: DeleteUIEnvironment(%s): %w", payload, err)
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		return classifyUIWriteMiss(ctx, tx, ns, name)
	})
}

// classifyUIWriteMiss maps a 0-row UI update/delete to the right sentinel: the
// row is operator-owned (ErrImmutableViaUI/403) or absent (ErrUINotFound/404).
func classifyUIWriteMiss(ctx context.Context, tx pgx.Tx, ns, name string) error {
	origin, found, err := environmentOriginTx(ctx, tx, ns, name)
	if err != nil {
		return err
	}
	if found && origin == "cr" {
		return ErrImmutableViaUI
	}
	return ErrUINotFound
}

// environmentOriginTx reads the origin of a row inside an existing tx. Returns
// (origin, found, err); found=false when no row exists.
func environmentOriginTx(ctx context.Context, tx pgx.Tx, ns, name string) (string, bool, error) {
	var origin string
	err := tx.QueryRow(ctx, `SELECT origin FROM environments WHERE namespace = $1 AND name = $2`, ns, name).Scan(&origin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		if isTransientPgErr(err) {
			return "", false, err
		}
		return "", false, fmt.Errorf("db: environmentOrigin(%s/%s): %w", ns, name, err)
	}
	return origin, true, nil
}

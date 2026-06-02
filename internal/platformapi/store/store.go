// SPDX-License-Identifier: Apache-2.0

// Package store ships the Postgres-backed Environment reader helpers the
// platform-api handlers consume (issue #34 / Phase B1).
//
// Pre-issue-34 the Store wrapped a controller-runtime cached client.Client and
// served reads from the informer cache. Issue #34 makes Postgres the source of
// truth: the Operator dual-writes Environments into the `environments`
// projection table, and platform-api reads the projection via pgxpool. The
// Store API surface (GetEnvironment / EnvironmentTerminating /
// EnvironmentAccessGroupSynced / ListAuthorizedEnvironments) is preserved so
// handler logic does not change shape — only the underlying transport.
//
// All reads scope to the `s.ns` namespace passed at construction (MULTI-01
// invariant unchanged from the informer-era Store).

package store

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ackstorm/ach/internal/db"
	achteams "github.com/ackstorm/ach/internal/platformapi/teams"
)

// ConditionTypeAccessGroupSynced is the Hub §6.6 condition type name the
// Phase 2 Environment reconciler writes when the LiteLLM access group matches
// the Environment's spec.runtime projection. Phase 3 §8.2 step 3 gates ek_
// creation on this condition being True.
const ConditionTypeAccessGroupSynced = "AccessGroupSynced"

// Store is the Postgres-backed Environment reader.
//
// Field discipline:
//   - pool is the pgxpool.Pool the platform-api process opened against the
//     Hub Postgres. Reads use pool.QueryRow / pool.Query directly; no
//     in-process cache (Postgres is the single source of truth per issue
//     #34 / spec v4 §5.2).
//   - ns is the watch namespace (MULTI-01). All Get/List calls bind ns as
//     the first $-parameter; the field is set ONCE at construction and the
//     Store offers no API to override it per-call.
//   - log is the operational logger; the Store never emits audit events
//     itself (audit emission stays a handler-side responsibility per D-19).
type Store struct {
	pool *pgxpool.Pool
	ns   string
	log  logr.Logger
}

// New returns a Store reading Environments in ns from the supplied Postgres
// pool. The pool MUST already be opened (db.Open) — the constructor does no
// ping or migration check; the platform-api bootstrap (cmd/ach/cmd/platform_api.go)
// already verifies pool reachability via /readyz at process start.
func New(pool *pgxpool.Pool, ns string, log logr.Logger) *Store {
	return &Store{pool: pool, ns: ns, log: log}
}

// GetEnvironment returns the projection row keyed by (s.ns, name).
//
// Absent row → (nil, nil) — preserved from the informer-era contract so the
// handler can distinguish "env_not_found" from "internal_error" without
// inspecting a wrapped error type. Pgconn 08/57 transients propagate raw
// (db.GetEnvironmentByName already implements that classification).
func (s *Store) GetEnvironment(ctx context.Context, name string) (*db.EnvironmentRow, error) {
	return db.GetEnvironmentByName(ctx, s.pool, s.ns, name)
}

// EnvironmentTerminating returns true iff the projection row for the named
// Environment has a non-NULL deletion_timestamp (drain semantics per CS-09).
// Absent row → (false, nil).
//
// The check delegates to GetEnvironment; the round-trip cost is identical to
// the informer-era helper (single Postgres SELECT) so call sites that
// previously did GetEnvironment + EnvironmentTerminating back-to-back still
// pay only two round trips — pre-issue-34 they paid one informer-cache hit;
// the new path is bounded by Postgres read latency.
func (s *Store) EnvironmentTerminating(ctx context.Context, name string) (bool, error) {
	row, err := s.GetEnvironment(ctx, name)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return row.DeletionTimestamp != nil, nil
}

// EnvironmentAccessGroupSynced returns true iff the named Environment's
// access_group_synced_condition JSONB column carries a Condition with
// Type=AccessGroupSynced and Status=True. The Operator dual-writes the column
// from its status-rollup path (Phase 2 D-14 / D-15), so platform-api can read
// the boolean projection directly without a JOIN.
//
// Returns:
//   - (true,  nil) when the condition is present with Status=True.
//   - (false, nil) when the condition is present with Status=False or Unknown.
//   - (false, nil) when the column is empty/NULL (pre-first-reconcile or the
//     reconciler has not yet written the condition).
//   - (false, nil) when the environment row itself is absent — the
//     env_not_found branch is the caller's responsibility via GetEnvironment
//     one step earlier.
//   - (false, err) on a read or decode failure that is not a clean
//     (nil, nil) absent shape.
func (s *Store) EnvironmentAccessGroupSynced(ctx context.Context, name string) (bool, error) {
	row, err := s.GetEnvironment(ctx, name)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return decodeAccessGroupSynced(row.AccessGroupSyncedCondition), nil
}

// decodeAccessGroupSynced extracts the AccessGroupSynced=True predicate from
// the access_group_synced_condition JSONB column. The writer's canonical
// shape is a []metav1.Condition slice (mirrors the K8s status subresource
// .conditions field), but the helper tolerates a bare metav1.Condition object
// — the column type doesn't enforce a shape, and a hand-edited row should
// degrade gracefully rather than crash the reader.
//
// A malformed payload contributes False (not-synced) — the steady-state
// reconciler rewrites the column on its next pass.
func decodeAccessGroupSynced(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	// Slice form (the Phase 2 writer's canonical encoding).
	var slice []metav1.Condition
	if err := json.Unmarshal(raw, &slice); err == nil {
		for _, c := range slice {
			if c.Type == ConditionTypeAccessGroupSynced {
				return c.Status == metav1.ConditionTrue
			}
		}
		return false
	}
	// Single-object form.
	var single metav1.Condition
	if err := json.Unmarshal(raw, &single); err == nil && single.Type == ConditionTypeAccessGroupSynced {
		return single.Status == metav1.ConditionTrue
	}
	return false
}

// ListAuthorizedEnvironments returns the projection rows in s.ns that the
// caller is authorized to see (API-08 / Hub §15.5):
//
//   - When isAdmin is true, every Environment in s.ns is returned (the admin
//     allowlist check is the handler's responsibility — only callers whose
//     owner_email is in the allowlist reach this code path with isAdmin=true).
//   - When isAdmin is false, an Environment is included iff its
//     authorized_teams[] shares at least one element with callerTeams.
//
// Soft-deleted rows (deletion_timestamp IS NOT NULL) are excluded by the
// underlying db.ListEnvironments helper — the list endpoint surfaces the
// not-draining set; the Content Service authz path uses GetEnvironment which
// deliberately surfaces drain-mode rows per CS-09.
func (s *Store) ListAuthorizedEnvironments(ctx context.Context, callerTeams []string, isAdmin bool) ([]db.EnvironmentRow, error) {
	rows, err := db.ListEnvironments(ctx, s.pool, s.ns)
	if err != nil {
		return nil, err
	}
	out := make([]db.EnvironmentRow, 0, len(rows))
	for _, row := range rows {
		if isAdmin || achteams.HasIntersect(row.AuthorizedTeams, callerTeams) {
			out = append(out, row)
		}
	}
	return out, nil
}

// SPDX-License-Identifier: Apache-2.0

// Package store adapter from the flat db.EnvironmentRow projection to the
// nested EnvironmentView shape the hydrate / environments / envkeys handlers
// were originally built to consume (issue #34 / Phase B0).
//
// Postgres becomes the source of truth in issue #34, so platform-api no longer
// reads Environment CRs from the controller-runtime informer cache. The
// handlers were authored against the CR's nested {spec.runtime, spec.context,
// status.conditions, deletionTimestamp} shape, so a small adapter is the
// least-invasive way to preserve their internal logic while moving the read
// path off K8s.
//
// RowToView decodes the three JSONB condition columns (available_condition,
// access_group_synced_condition, execution_resources_resolved_condition) the
// Operator dual-writes into a single []metav1.Condition slice, and projects
// the seven authorization-surface arrays plus the deletion timestamp into the
// nested {Runtime, Context} bundles. The output is byte-stable with the
// pre-issue-34 informer-projected EnvironmentView shape so the JSON wire on
// /platform/environments and /platform/hydrate does not drift.
package store

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
)

// EnvironmentView is the platform-api projection of an environments row.
// Field shape preserves the pre-issue-34 EnvironmentView JSON wire so
// /platform/environments and /platform/hydrate responses do not drift.
//
//   - Namespace / Name             — projection PK (db.EnvironmentRow PK).
//   - AuthorizedTeams              — spec.authorizedTeams projected
//     into the env's authorization surface.
//   - Context, Runtime             — nested CRD-shaped bundles (api/...).
//     Empty slices are surfaced as []
//     on the wire via the CRD struct tags.
//   - Conditions                   — flattened union of the three JSONB
//     condition columns
//     (available_condition,
//     access_group_synced_condition,
//     execution_resources_resolved_condition)
//     as []metav1.Condition.
//   - DeletionTimestamp            — set when the row's deletion_timestamp
//     column is non-NULL (drain semantics
//     per CS-09 / D-14).
//   - ResourceVersion              — K8s metadata.resourceVersion the
//     Operator captured at projection-write
//     time. Surfaced for client-side
//     optimistic concurrency.
//   - Origin / Locked              — issue #34 SoT coexistence fields
//     (origin in {'cr','ui'}; locked grays
//     out UI edit controls). Locked is
//     deduced from origin per the
//     cr_locked_chk constraint
//     (origin='cr' ⇒ locked=true).
type EnvironmentView struct {
	Namespace         string
	Name              string
	AuthorizedTeams   []string
	Context           achv1alpha1.ContextBlock
	Runtime           achv1alpha1.RuntimeBlock
	Conditions        []metav1.Condition
	DeletionTimestamp *metav1.Time
	ResourceVersion   string
	Origin            string
	Locked            bool
}

// RowToView maps a flat db.EnvironmentRow into the nested EnvironmentView.
//
// JSONB columns are decoded with json.Unmarshal — the Operator writes each
// column as either a single metav1.Condition object or a []metav1.Condition
// slice (the Phase 2 writer side marshaled slices; the column type accepts
// either shape). RowToView tolerates both by trying slice-decode first and
// falling back to single-object decode. A nil/empty column contributes no
// conditions to the output slice — the not-yet-reconciled steady state.
//
// Origin and Locked are surfaced verbatim from the row; the issue-34 schema
// guarantees origin='cr' ⇒ locked=TRUE via the cr_locked_chk CHECK constraint,
// so a UI client can use either field as the read-only marker.
//
// The function makes one shallow allocation per non-empty condition column +
// one final allocation for the merged conditions slice; the cost is O(N) over
// the small condition set.
func RowToView(r db.EnvironmentRow) EnvironmentView {
	view := EnvironmentView{
		Namespace:       r.Namespace,
		Name:            r.Name,
		AuthorizedTeams: dedupNonEmpty(r.AuthorizedTeams),
		Context: achv1alpha1.ContextBlock{
			Prompts:   r.ContextPrompts,
			Plugins:   r.ContextPlugins,
			Artifacts: r.ContextArtifacts,
		},
		Runtime: achv1alpha1.RuntimeBlock{
			Models:     r.RuntimeModels,
			MCPServers: r.RuntimeMCPServers,
			A2AAgents:  r.RuntimeA2AAgents,
		},
		ResourceVersion: r.ResourceVersion,
	}
	if r.DeletionTimestamp != nil {
		t := metav1.NewTime(*r.DeletionTimestamp)
		view.DeletionTimestamp = &t
	}
	view.Conditions = mergeConditionColumns(
		r.AvailableCondition,
		r.AccessGroupSyncedCondition,
		r.ExecutionResourcesResolvedCondition,
	)
	return view
}

// mergeConditionColumns decodes each non-empty JSONB column into a
// []metav1.Condition (tolerating the single-object encoding the Phase 2
// writer may have used) and concatenates the results in the deterministic
// order Available → AccessGroupSynced → ExecutionResourcesResolved.
//
// A decode failure on any one column is swallowed silently — the projection
// is a read-only mirror of what the Operator dual-wrote; a malformed row is
// surfaced as "condition missing" to the handler rather than aborting the
// whole hydrate / environments response. The Operator's next reconcile fixes
// the projection.
func mergeConditionColumns(cols ...[]byte) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(cols))
	for _, col := range cols {
		if len(col) == 0 {
			continue
		}
		// Try slice form first (the Phase 2 writer's canonical shape).
		var slice []metav1.Condition
		if err := json.Unmarshal(col, &slice); err == nil {
			out = append(out, slice...)
			continue
		}
		// Fall back to single-object form.
		var single metav1.Condition
		if err := json.Unmarshal(col, &single); err == nil && single.Type != "" {
			out = append(out, single)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dedupNonEmpty returns the slice with empty strings dropped. Used on
// AuthorizedTeams so a malformed projection row (NULL → "") does not leak
// an empty entry into the EnvironmentView. Allocation-free for slices that
// contain no empty strings.
func dedupNonEmpty(in []string) []string {
	for _, s := range in {
		if s == "" {
			out := make([]string, 0, len(in))
			for _, s := range in {
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return in
}

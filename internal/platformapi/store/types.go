// SPDX-License-Identifier: Apache-2.0

// Plan 03-06 Task 2 — read-only projection types consumed by the
// GET /platform/environments handler (Plan 03-09) and the hydrate handler.
//
// The Environment CRD spec.runtime / spec.context shapes are imported
// verbatim from api/ach/v1alpha1 (RuntimeBlock / ContextBlock) so the
// projection matches the CRD's JSON tags exactly — no field renames, no
// reshaping. Conditions are carried verbatim from .status.conditions per
// API-08 / Hub §6.6.

package store

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// EnvironmentView is the JSON projection of an Environment for the
// GET /platform/environments response (API-08). Field shape:
//
//   - name              — metadata.name verbatim.
//   - spec              — the EnvironmentSpecView subset (authorizedTeams,
//     runtime, context). The omitted fields (TypeMeta, ObjectMeta beyond
//     name) are deployment-internal and NOT exposed to platform clients.
//   - conditions        — env.Status.Conditions copied verbatim per
//     API-08 / Hub §6.6 closed set; the slice's element type
//     (metav1.Condition) JSON-marshals with the canonical field names
//     `type`, `status`, `reason`, `message`, `lastTransitionTime`,
//     `observedGeneration`.
//   - deletionTimestamp — env.DeletionTimestamp surfaced for drain
//     visibility; nil when the Environment is not terminating. Marshaled
//     as `omitempty` so steady-state responses do not carry the field.
//
// EnvironmentView is intentionally a value type (not a pointer) so the
// handler can compose response slices with append() without aliasing
// concerns; the inner Conditions slice still shares memory with the
// source Environment, which is acceptable because the handler treats
// the projection as read-only and the controller-runtime cache returns
// fresh deep-copies on every Get/List.
type EnvironmentView struct {
	Name              string              `json:"name"`
	Spec              EnvironmentSpecView `json:"spec"`
	Conditions        []metav1.Condition  `json:"conditions"`
	DeletionTimestamp *metav1.Time        `json:"deletionTimestamp,omitempty"`
}

// EnvironmentSpecView is the spec subset surfaced to platform clients.
// AuthorizedTeams is included because Plan 03-09's handler needs to render
// it back to the caller for CLI `ach env describe` use; Runtime and Context
// preserve the CRD types verbatim so the JSON tags match the Hub spec
// §15.1 hydrate response shape exactly (a Phase 3 handler that uses this
// projection for /platform/environments can also feed it into the hydrate
// flow without reshaping).
//
// AuthorizedTeams uses `omitempty` so the JSON omits the field when an
// admin queries an Environment with no authorizedTeams entries — though
// the CRD's MinItems=1 admission rule means this branch is unreachable
// for any CR that passed validation. Keeping omitempty is the standard
// Kubernetes projection style for optional slices.
type EnvironmentSpecView struct {
	AuthorizedTeams []string                 `json:"authorizedTeams,omitempty"`
	Runtime         achv1alpha1.RuntimeBlock `json:"runtime"`
	Context         achv1alpha1.ContextBlock `json:"context"`
}

// ToEnvironmentView maps an Environment CR to its read-only JSON projection.
// The function intentionally lives in the store package (not in the handler
// package) so Plans 03-09 and 03-10 share one canonical projection without
// import cycles.
//
// Conditions are copied by reference into the projection — see
// EnvironmentView's GoDoc for the aliasing rationale (the controller-runtime
// cache returns deep-copies, so subsequent mutations on the source slice are
// safe). DeletionTimestamp is a pointer copy (metav1.Time is a tiny value
// type so the pointer indirection costs nothing meaningful).
func ToEnvironmentView(env achv1alpha1.Environment) EnvironmentView {
	return EnvironmentView{
		Name: env.Name,
		Spec: EnvironmentSpecView{
			AuthorizedTeams: env.Spec.AuthorizedTeams,
			Runtime:         env.Spec.Runtime,
			Context:         env.Spec.Context,
		},
		Conditions:        env.Status.Conditions,
		DeletionTimestamp: env.DeletionTimestamp,
	}
}

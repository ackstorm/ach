// SPDX-License-Identifier: Apache-2.0

// Plan 03-06 — informer-backed Environment reader helpers (D-20 / D-21).
//
// The Store wraps a controller-runtime cache-backed client.Client and exposes
// the four reader entry points Phase 3 handlers consume:
//
//   - GetEnvironment                — single Environment by name, nil-on-absent.
//   - EnvironmentAccessGroupSynced  — boolean projection of the
//     `AccessGroupSynced` condition (false when missing or env absent).
//   - EnvironmentTerminating        — true when env.DeletionTimestamp != nil.
//   - ListAuthorizedEnvironments    — caller-team intersection + admin override.
//
// All reads go through s.client.Get / s.client.List with
// client.InNamespace(s.ns) — namespace isolation per MULTI-01 is baked into
// the constructor and cannot be bypassed by callers.

package store

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// ConditionTypeAccessGroupSynced is the Hub §6.6 condition type name that
// Phase 2's Environment reconciler writes when the LiteLLM access group
// matches the Environment's spec.runtime projection. Phase 3 §8.2 step 3
// gates ek_ creation on this condition being True.
const ConditionTypeAccessGroupSynced = "AccessGroupSynced"

// Store is the informer-backed Environment reader. Construct via New.
//
// Field discipline:
//   - client is a controller-runtime cached client (mgr.GetClient() after
//     mgr.GetCache().WaitForCacheSync). Direct API-server clients defeat
//     the Hub §5.2 cache-served promise.
//   - ns is the watch namespace (MULTI-01). All Get/List calls are scoped
//     via client.InNamespace(ns); the field is set ONCE at construction.
//   - log is the operational logger; the Store never emits audit events
//     itself (audit emission is the handler's responsibility per D-19).
type Store struct {
	client client.Client
	ns     string
	log    logr.Logger
}

// New returns a Store reading Environments in ns. The caller MUST pass a
// cache-backed client.Client (mgr.GetClient() returns one after the manager's
// cache has synced); passing a direct REST client would silently bypass the
// Hub §5.2 discipline and hit the API server on every read.
func New(c client.Client, ns string, log logr.Logger) *Store {
	return &Store{client: c, ns: ns, log: log}
}

// GetEnvironment returns (*Environment, nil) when the named Environment
// exists in s.ns, (nil, nil) when absent, and (nil, err) on any other
// read failure. The (nil, nil) absent shape lets the handler distinguish
// "env_not_found" from "internal_error" without inspecting the underlying
// apierrors type — a deliberate ergonomic simplification per Hub §8.3.
//
// The Get hits the controller-runtime informer cache; the round-trip is
// sub-millisecond after warmup. Caller is responsible for checking
// env.DeletionTimestamp if drain semantics matter (see EnvironmentTerminating).
func (s *Store) GetEnvironment(ctx context.Context, name string) (*achv1alpha1.Environment, error) {
	env := &achv1alpha1.Environment{}
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.ns, Name: name}, env); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: GetEnvironment(%s): %w", name, err)
	}
	return env, nil
}

// EnvironmentTerminating returns true when the named Environment exists AND
// has a non-nil DeletionTimestamp. Returns (false, nil) when the Environment
// is absent — callers checking drain semantics first should have already
// invoked GetEnvironment for an explicit "env_not_found" branch (Hub §8.3).
//
// This helper does NOT distinguish "absent" from "present and not terminating"
// because the §8.2 step-2 contract treats them identically: env_not_found is
// surfaced from the GetEnvironment call site one step earlier in the flow.
func (s *Store) EnvironmentTerminating(ctx context.Context, name string) (bool, error) {
	env, err := s.GetEnvironment(ctx, name)
	if err != nil {
		return false, err
	}
	if env == nil {
		return false, nil
	}
	return env.DeletionTimestamp != nil, nil
}

// EnvironmentAccessGroupSynced returns the boolean status of the
// `AccessGroupSynced` condition in the named Environment's status.conditions
// slice (Hub §6.6 closed set). Returns:
//
//   - (true,  nil) when the condition is present with Status=True.
//   - (false, nil) when the condition is present with Status=False or Unknown.
//   - (false, nil) when the condition is missing entirely — Phase 3 §8.2
//     step 3 treats missing as not-ready (503 not_ready), matching the
//     Phase 2 Environment reconciler's pre-first-reconcile state where the
//     condition has not yet been written.
//   - (false, nil) when the Environment itself is absent — the env_not_found
//     branch is the caller's responsibility via GetEnvironment one step earlier.
//   - (false, err) on read failure.
func (s *Store) EnvironmentAccessGroupSynced(ctx context.Context, name string) (bool, error) {
	env, err := s.GetEnvironment(ctx, name)
	if err != nil {
		return false, err
	}
	if env == nil {
		return false, nil
	}
	for _, c := range env.Status.Conditions {
		if c.Type == ConditionTypeAccessGroupSynced {
			return c.Status == metav1.ConditionTrue, nil
		}
	}
	return false, nil
}

// ListAuthorizedEnvironments returns the Environments in s.ns that the
// caller is authorized to see (API-08 / Hub §15.5):
//
//   - When isAdmin is true, every Environment in s.ns is returned (the admin
//     allowlist check is the handler's responsibility — Plan 03-10 sets
//     isAdmin only AFTER verifying the caller's owner_email is in the
//     admin allowlist).
//   - When isAdmin is false, an Environment is included iff its
//     spec.authorizedTeams[] shares at least one element with callerTeams.
//
// Terminating Environments (env.DeletionTimestamp != nil) ARE included in
// the result; drain semantics are Phase 5 / CS-09 concern. The handler may
// further filter the result if its own contract requires it.
//
// The List hits the controller-runtime informer cache; iteration is
// O(len(EnvironmentList)) which is bounded by the namespace's Environment
// count (deployment concern).
func (s *Store) ListAuthorizedEnvironments(ctx context.Context, callerTeams []string, isAdmin bool) ([]achv1alpha1.Environment, error) {
	var list achv1alpha1.EnvironmentList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		return nil, fmt.Errorf("store: ListAuthorizedEnvironments: %w", err)
	}
	out := make([]achv1alpha1.Environment, 0, len(list.Items))
	for _, env := range list.Items {
		if isAdmin || hasIntersect(env.Spec.AuthorizedTeams, callerTeams) {
			out = append(out, env)
		}
	}
	return out, nil
}

// hasIntersect reports whether the two string slices share at least one
// element. Used for the authorizedTeams ∩ callerTeams check in
// ListAuthorizedEnvironments. Empty slice in either argument short-
// circuits to false — an Environment with no authorizedTeams entries is
// unreachable to non-admin callers, and a caller with no Team membership
// sees nothing (admins bypass this helper entirely).
//
// Complexity: O(len(a) + len(b)). Building a set from the smaller side would
// be a minor improvement; current call sites carry single-digit team counts.
func hasIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}

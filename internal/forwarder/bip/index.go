// SPDX-License-Identifier: Apache-2.0

// Plan 04-04 Task 1 — controller-runtime field-indexer registration
// and alpha-LAST winner lookup for BackendIdentityPolicy (Hub §9.3,
// FWD-05, OP-16, TODO.md §6).
//
// The forwarder uses this on every /mcp/<name> and /a2a/<name> request
// to decide whether to mint and attach an ACH JWT. The lookup is
// READ-ONLY against the controller-runtime cache (no apiserver round
// trip after WaitForCacheSync). The Operator NEVER writes a
// Synced=DuplicateTarget status condition — duplicates are resolved
// here, at read time, by sorting matches by metadata.name ASC and
// returning the last entry (the "alpha-LAST" contract).

package bip

import (
	"context"
	"sort"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TargetIndexKey is the controller-runtime field-indexer key used to
// look up BackendIdentityPolicy CRs by their target {kind, name} tuple.
// Format of the indexed string: "<spec.target.kind>/<spec.target.name>"
// (e.g. "MCPServer/github-mcp", "A2AAgent/openai-a2a").
const TargetIndexKey = "spec.target"

// RegisterIndex teaches the controller-runtime cache to index
// BackendIdentityPolicy by "<kind>/<name>" so request-time lookups via
// List(... MatchingFields{TargetIndexKey: "MCPServer/foo"}) hit an
// O(log N) index instead of a full namespace scan.
//
// CONTRACT: This MUST be called AFTER ctrl.NewManager and BEFORE the
// first GetInformer call on BackendIdentityPolicy — controller-runtime
// rejects late-registered indexers.
func RegisterIndex(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &achv1alpha1.BackendIdentityPolicy{},
		TargetIndexKey,
		func(obj client.Object) []string {
			b := obj.(*achv1alpha1.BackendIdentityPolicy)
			return []string{b.Spec.Target.Kind + "/" + b.Spec.Target.Name}
		})
}

// ResolveWinner returns the alphabetically-LAST BackendIdentityPolicy
// for the given (kind, name) target. Returns nil when:
//
//   - zero BIPs match, OR
//   - the alpha-LAST winner has spec.forwardIdentityJWT=false
//     (explicit opt-out is equivalent to no policy)
//
// Per TODO.md §6 + FWD-05 + Hub §9.3: multiple BIPs targeting the same
// (kind, name) tuple coexist. Operators flip precedence by renaming CRs
// (a "zz-" prefix on metadata.name makes the rename the alpha-LAST
// winner). There is NO Synced reason emitted for duplicates anywhere
// in this project; the Operator stays dumb.
//
// Per OP-16: this function reads spec.target + spec.forwardIdentityJWT
// ONLY — it MUST NOT read the .Status sub-resource (Operator is the
// sole status writer and runtime authority is decoupled from status
// write latency).
func ResolveWinner(ctx context.Context, c client.Client, kind, name, namespace string) *achv1alpha1.BackendIdentityPolicy {
	var list achv1alpha1.BackendIdentityPolicyList
	if err := c.List(ctx, &list,
		client.MatchingFields{TargetIndexKey: kind + "/" + name},
		client.InNamespace(namespace)); err != nil {
		return nil
	}
	if len(list.Items) == 0 {
		return nil
	}
	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	winner := list.Items[len(list.Items)-1]
	if !winner.Spec.ForwardIdentityJWT {
		return nil
	}
	return &winner
}

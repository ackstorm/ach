// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 2: cross-marketplace name-conflict resolution per
// OP-08 / Hub §12.3.
//
// Rules (deterministic, Unicode-code-point ordering via Go's standard
// string comparison — equivalent for ASCII, and CRD-08 forces marketplace
// metadata.name to DNS-1123 subdomain which is lowercase ASCII):
//
//  1. Plugin CRD beats marketplace: if a Plugin CR with metadata.name=X
//     exists in the namespace, no marketplace can materialize plugin name
//     X regardless of alphabetical order.
//  2. Alphabetically-lowest marketplace.metadata.name wins among
//     marketplaces exposing the same plugin name.
//
// The Plugin-CRD rule has ABSOLUTE precedence over the alphabetical
// rule — a Plugin CR named "shared" drops the marketplace-side "shared"
// even when this marketplace is the alphabetical winner.

package ach

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
)

// ConflictDecision is the per-plugin outcome of cross-marketplace name-
// conflict resolution. Kept=true means THIS marketplace materializes
// this plugin in Stage-2; Kept=false means it does not (Reason explains
// which rule fired).
//
// The Stage-2 caller distinguishes Plugin-CRD-wins ("Plugin CRD ...
// takes precedence") from marketplace-loses ("marketplace ... takes
// precedence") by inspecting Reason: marketplace-loses entries flip
// the CR-level Synced=False reason=NameConflict per Plan 02-06's
// spec-interpretation choice; Plugin-CRD-wins entries are recorded as
// informational status.message annotations without flipping Synced.
type ConflictDecision struct {
	PluginName string
	Kept       bool
	Reason     string
}

// resolveConflicts decides which of myCandidates this marketplace is
// permitted to materialize, given the other-marketplace catalogs and
// the namespace's Plugin CR name set.
//
// Inputs:
//
//   - myMarketplaceName : the metadata.name of THIS PluginMarketplace CR.
//   - myCandidates      : the post-filter plugin entries this marketplace
//     would otherwise materialize (Stage-1.5 output).
//   - otherMarketplaceCatalogs : map[other-marketplace-metadata.name][]plugin-name
//     covering every OTHER PluginMarketplace CR in the namespace, sourced
//     from their last reconcile's marketplace_plugins rows (a pragmatic
//     v1alpha1 shortcut — the optimization to re-parse+filter every other
//     marketplace per reconcile is deferred). MUST NOT include
//     myMarketplaceName as a key — the caller filters self out.
//   - allPluginCRs : set of every Plugin CR metadata.name in the namespace.
//
// The returned slice has length len(myCandidates) and preserves input
// order (1:1 with myCandidates[i] ↔ decisions[i]).
//
// Edge cases:
//
//   - Empty otherMarketplaceCatalogs + no Plugin CR conflict → every
//     candidate is Kept=true (alphabetical-lowest-of-{self} == self).
//   - Tie-breaking on metadata.name uses Go's standard string < which is
//     byte-wise — equivalent to Unicode code-point ordering for ASCII,
//     and CRD-08 forces marketplace metadata.name to DNS-1123 subdomain
//     (lowercase ASCII). Non-ASCII deployment names are theoretically
//     possible but unreachable via CRD validation.
func resolveConflicts(
	myMarketplaceName string,
	myCandidates []ClaudeCodeMarketplacePlugin,
	otherMarketplaceCatalogs map[string][]string,
	allPluginCRs map[string]struct{},
) []ConflictDecision {
	out := make([]ConflictDecision, 0, len(myCandidates))
	for _, p := range myCandidates {
		// Rule 1: Plugin CRD beats everything.
		if _, exists := allPluginCRs[p.Name]; exists {
			out = append(out, ConflictDecision{
				PluginName: p.Name,
				Kept:       false,
				Reason:     "Plugin CRD '" + p.Name + "' takes precedence",
			})
			continue
		}
		// Rule 2: alphabetical-lowest marketplace wins. Compute the set
		// of marketplaces that expose this plugin name; include self as
		// the implicit entry (we know I expose it because it's in
		// myCandidates).
		contenders := []string{myMarketplaceName}
		for otherName, otherNames := range otherMarketplaceCatalogs {
			for _, n := range otherNames {
				if n == p.Name {
					contenders = append(contenders, otherName)
					break
				}
			}
		}
		sort.Strings(contenders)
		winner := contenders[0]
		if winner == myMarketplaceName {
			out = append(out, ConflictDecision{
				PluginName: p.Name,
				Kept:       true,
			})
			continue
		}
		out = append(out, ConflictDecision{
			PluginName: p.Name,
			Kept:       false,
			Reason:     "marketplace '" + winner + "' takes precedence",
		})
	}
	return out
}

// listPluginCRNames returns the set of every Plugin CR's metadata.name in
// the namespace via the controller-runtime informer cache (sub-ms after
// warmup). Used by Stage-1.6 of the PluginMarketplace reconciler.
func listPluginCRNames(ctx context.Context, c client.Client, namespace string) (map[string]struct{}, error) {
	var plugins achv1alpha1.PluginList
	if err := c.List(ctx, &plugins, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(plugins.Items))
	for _, p := range plugins.Items {
		out[p.Name] = struct{}{}
	}
	return out, nil
}

// listOtherMarketplaceCatalogs returns a map[marketplaceName][]pluginName
// for every PluginMarketplace CR in the namespace EXCEPT selfName, drawn
// from marketplace_plugins DB rows (each other marketplace's last
// reconcile output).
//
// When dbPool is nil (Phase 1 envtest path) the function returns an empty
// map — degraded mode: every candidate is treated as the alphabetical
// winner. Plan 02-09 wires the real pool from cmd/operator/main.go.
//
// Best-effort: an error fetching one other marketplace's row set is
// silently dropped (logged via the per-row ListMarketplacePlugins
// internal logger if any) — the conflict resolver merely under-counts
// contenders for that name, biasing in favor of THIS marketplace (a
// conservative bias: we'd rather double-materialize a plugin under two
// marketplaces than starve both).
func listOtherMarketplaceCatalogs(ctx context.Context, c client.Client, dbPool *pgxpool.Pool, namespace, selfName string) (map[string][]string, error) {
	out := make(map[string][]string)
	var mps achv1alpha1.PluginMarketplaceList
	if err := c.List(ctx, &mps, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if dbPool == nil {
		return out, nil
	}
	for _, mp := range mps.Items {
		if mp.Name == selfName {
			continue
		}
		rows, err := achdb.ListMarketplacePlugins(ctx, dbPool, mp.Name)
		if err != nil {
			// Best-effort — biased toward THIS marketplace per the
			// conservative-bias rule documented above.
			continue
		}
		names := make([]string, 0, len(rows))
		for _, r := range rows {
			names = append(names, r.Name)
		}
		out[mp.Name] = names
	}
	return out, nil
}

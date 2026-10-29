// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"slices"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/snapshot"
)

// expandRuntimeGroups expands spec.runtime.mcpServerGroups / agentGroups
// against the snapshot and records the result in status.expandedRuntime.
// ok=false when groups are declared but the snapshot has never refreshed
// (group maps nil): expanding against nothing would PUT a narrower access
// group and projection — a revocation — so the caller must wait instead.
func expandRuntimeGroups(env *achv1alpha1.Environment, snap snapshot.LiteLLMSnapshot) (mcp, agents []string, ok bool) {
	rt := env.Spec.Runtime
	if len(rt.MCPServerGroups)+len(rt.A2AAgentGroups) > 0 && snap.MCPServerGroups == nil {
		return nil, nil, false
	}
	mcp = expandGroups(rt.MCPServerGroups, snap.MCPServerGroups, rt.MCPServers)
	agents = expandGroups(rt.A2AAgentGroups, snap.A2AAgentGroups, rt.A2AAgents)
	env.Status.ExpandedRuntime = nil
	if len(mcp)+len(agents) > 0 {
		env.Status.ExpandedRuntime = &achv1alpha1.ExpandedRuntime{MCPServers: mcp, A2AAgents: agents}
	}
	return mcp, agents, true
}

// expandGroups returns the sorted names carrying any of groups in byTag,
// minus the names already listed explicitly. A tag absent from byTag grants
// nothing (not an error). Nil when nothing is added.
func expandGroups(groups []string, byTag map[string][]string, explicit []string) []string {
	var out []string
	for _, g := range groups {
		for _, name := range byTag[g] {
			if !slices.Contains(explicit, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// unionSorted returns a ∪ b sorted and deduped, never nil — it feeds a
// LiteLLM whole-list field where only `[]` is a proven clear.
func unionSorted(a, b []string) []string {
	out := append(append([]string{}, a...), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

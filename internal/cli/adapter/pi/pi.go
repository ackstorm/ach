// SPDX-License-Identifier: Apache-2.0

// Package pi is the Pi platform Adapter implementation. It is the thinnest adapter:
// a 3-row ProjectionRules table plus an mcpServers-shaped RenderRuntime pointed at
// .pi/mcp.json. There is NO format conversion — plugin commands/skills are
// projected verbatim (MergeReplace): commands into Pi's project layout
// .pi/prompts/ (RemapGlobalPath moves them under ~/.pi/agent/ for --global,
// the only place pi reads them there), skills into the shared .agents/skills/, and
// MCP definitions deep-merge into .pi/mcp.json (MergeDeep).
//
// RenderRuntime writes the Environment's MCP servers into .pi/mcp.json. The
// top-level kinds without a rule (rules/, agents/, AGENTS.md) are reported as
// dropped by route.Project.
//
// Own-prefix rule (ADAPT-06 analog): this adapter emits ONLY .pi/-prefixed
// paths. Pi has four candidate MCP config locations; only the .pi/mcp.json
// project-override target is in scope here.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
package pi

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

const (
	// canonicalID is the primary registry key per the Phase 5 plan.
	canonicalID = "pi"

	// mcpJSONPath is the Pi project-override MCP config target (D-33). This
	// is the ONLY .pi/-prefixed MCP path pi emits — the other three Pi
	// MCP config locations are out of scope per the own-prefix rule.
	mcpJSONPath = ".pi/mcp.json"
)

// Adapter is the empty struct holding the pi Adapter impl.
type Adapter struct{}

// init registers the adapter with the package-level registry; the cobra
// layer blank-imports this subpackage so init() fires before Lookup/Iter.
func init() {
	adapter.Register(&Adapter{})
}

// ID returns the canonical adapter ID.
func (a *Adapter) ID() string { return canonicalID }

func (a *Adapter) Aliases() []string { return nil }

// Detect scans root for Pi signals. Each present signal boosts confidence;
// 3+ → High, 2 → Medium, 1 → Low. signals==0 → empty Match (no-match).
//
// Signals checked:
//   - .pi/ directory at root
//   - .pi/agent/ directory
//   - .pi/mcp.json file (with -g the root is $HOME, so ~/.pi/mcp.json counts)
func (a *Adapter) Detect(root string) (adapter.Match, error) {
	signals := []adapter.Signal{
		{Path: filepath.Join(root, ".pi"), Reason: "found .pi/ directory"},
		{Path: filepath.Join(root, ".pi", "agent"), Reason: "found .pi/agent/ directory"},
		{Path: filepath.Join(root, ".pi", "mcp.json"), Reason: "found .pi/mcp.json"},
	}
	return adapter.DetectFromSignals(canonicalID, signals), nil
}

// RenderRuntime emits the single .pi/mcp.json FileWrite (D-33). Merge=MergeDeep;
// Keys carries the contributed top-level keys for STATE-02 + ADAPT-05
// inverse-merge during `--sync`.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("pi: RenderRuntime called with nil manifest or runtime block")
	}

	cred := adapter.CredentialFromContext(ctx)
	// Pi reads `url` (StreamableHTTP with SSE fallback) and `headers`; it
	// defines no `type` for an HTTP server.
	content, keys, err := adapter.RenderMCPServersJSON(m, cred, "url")
	if err != nil {
		return nil, err
	}

	return []adapter.FileWrite{
		{
			Path:    mcpJSONPath,
			Content: content,
			Merge:   adapter.MergeDeep,
			Keys:    keys,
		},
	}, nil
}

// mcpDeepKeys is pi's only non-nil route.Rule.Transform, wired onto the
// mcp/**/* ProjectionRules row. It enumerates the top-level mcpServers map keys
// a plugin's mcp file contributes so the deep-merge engine can record them in
// state.files[*].keys[] (STATE-02 + ADAPT-05) and the runtime-wins drop (D-10)
// can drop ids that clash with the runtime MCP set.
//
// Pi enumerates ONLY mcpServers — it has no a2aAgents
// surface, so an a2aAgents branch in the input is intentionally ignored.
func mcpDeepKeys(srcRel string, in []byte) ([]byte, []string, error) {
	return route.MCPDeepKeys("pi", srcRel, in, "mcpServers")
}

// ProjectionRules returns the pi projection table (route.RuleProvider, the
// D-06 seam). Exactly 3 rows:
//
//   - commands/**/*.md → .pi/prompts/**/* (MergeReplace verbatim)
//   - skills/**/*      → .agents/skills/**/*  (MergeReplace verbatim)
//   - mcp/**/*         → .pi/mcp.json (MergeDeep, Transform=mcpDeepKeys; D-33)
//
// A root/**/* catch-all is OMITTED (D-36). No rules/agents/
// AGENTS.md row — those top-level kinds fall to route.Project's accumulate-once
// drop set (D-35), yielding Dropped = {rules, agents, AGENTS.md}; mcp is NOT
// dropped because it has the mcp/**/* rule (D-33). This method is pure data —
// no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".pi/prompts/**/*", Merge: adapter.MergeReplace, Prefix: route.PrefixCommand, Sep: "-"},
		{FromGlob: "skills/**/*", ToGlob: ".agents/skills/**/*", Merge: adapter.MergeReplace, Prefix: route.PrefixSkill, Sep: "-"},
		{FromGlob: "mcp/**/*", ToGlob: mcpJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
		// Root .mcp.json is Claude Code's standard plugin MCP location — merge it
		// into .pi/mcp.json too.
		{FromGlob: ".mcp.json", ToGlob: mcpJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
	}
}

// SPDX-License-Identifier: Apache-2.0

// Package claudecode is the pass-through reference Adapter
// implementation per CONTEXT.md D-05 and CLI spec §7.4 (claude-code
// adapter).
//
// The claude-code adapter is the canonical reference because:
//
//   - ADAPT-04 pins the Plugin canonical format to Claude Code. The
//     pass-through path is therefore a verbatim copy of the plugin
//     tree into .claude/plugins/<name>/ — no frontmatter rewrite, no
//     directory remapping, no silent drops.
//   - ADAPT-07 silent-drop accounting: claude-code has no source-tree
//     component it cannot translate — route.Project's drop set is
//     always empty for this adapter.
//   - The runtime-config target .claude/settings.json is where Claude
//     Code reads MCP server definitions in this adapter's contract.
//     (The official .claude/.mcp.json definition location and the
//     settings.json approval-allowlist split — `enabledMcpjsonServers`
//     — are not modeled here; see follow-up.md "Open question" for the
//     escape hatch back to .mcp.json + an allowlist write.)
//
// ADAPT-06 scope rule: this adapter emits ONLY .claude/-prefixed paths.
// Prompts and artifacts are written by the hydrator core directly (CLI
// spec §6.4); the adapter never touches them.
package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Target paths emitted by this adapter. Centralized as constants so
// the test assertions and the production code stay in lock-step.
const (
	// settingsJSONPath is where claude-code MCP server definitions are
	// written (user-directed; see the surgical-merge redesign plan). The
	// adapter merges its mcpServers entries into this file surgically,
	// preserving the user's other settings/servers.
	settingsJSONPath = ".claude/settings.json"

	// canonicalID + aliases match CLI spec §7.2 row 1 + the plan's
	// alias contract (plan must_haves: ["claude", "cc"]).
	canonicalID = "claude-code"
)

// Adapter is the empty struct holding the claude-code Adapter impl.
// Pass-through carries no state — every method is a pure function of
// its arguments (and ctx for credential propagation).
type Adapter struct{}

// init registers the adapter with the package-level registry. The
// cobra autodetection layer (plan 07-W3-05) blank-imports this
// subpackage so the init() fires before main() reaches Lookup/Iter.
func init() {
	adapter.Register(&Adapter{})
}

// ID returns the canonical adapter ID per CLI spec §7.2.
func (a *Adapter) ID() string { return canonicalID }

// Aliases returns the case-folded alternate identifiers a user may
// type at the CLI. Plan 07-W3-01 contract: ["claude", "cc"]. The
// registry case-folds at registration + Lookup, so callers passing
// "Claude" or "CC" resolve correctly.
func (a *Adapter) Aliases() []string { return []string{"claude", "cc"} }

// Detect scans root for claude-code signals. Each present signal
// boosts confidence; 3+ → High, 2 → Medium, 1 → Low. Reasons
// accumulate human-readable evidence the autodetection layer surfaces
// on multi-match exit.
//
// Signals checked (subset of spec §7.2 + plan behavior):
//   - .claude/ directory at root
//   - .claude/.mcp.json file
//   - .claude/agents/ directory
//   - .mcp.json at root (Claude Code's per-project MCP registry)
//
// Returns an empty Match (zero ID + zero Confidence) when no signals
// are seen — the autodetection layer treats that as a no-match.
func (a *Adapter) Detect(root string) (adapter.Match, error) {
	signals := 0
	reasons := make([]string, 0, 4)

	check := func(rel string, reason string) {
		full := filepath.Join(root, rel)
		if _, err := os.Stat(full); err == nil {
			signals++
			reasons = append(reasons, reason)
		}
	}

	check(".claude", "found .claude/ directory")
	check(".claude/.mcp.json", "found .claude/.mcp.json")
	check(".claude/agents", "found .claude/agents/ directory")
	check(".mcp.json", "found .mcp.json at root")

	if signals == 0 {
		return adapter.Match{}, nil
	}

	var conf adapter.Confidence
	switch {
	case signals >= 3:
		conf = adapter.ConfidenceHigh
	case signals == 2:
		conf = adapter.ConfidenceMedium
	default:
		conf = adapter.ConfidenceLow
	}

	return adapter.Match{
		ID:         canonicalID,
		Confidence: conf,
		Reasons:    reasons,
	}, nil
}

// mcpJSONShape is the document Claude Code reads at settingsJSONPath
// (.claude/settings.json). The schema mirrors what .claude/.mcp.json
// would carry — adapter renders one shape, surgical merge upserts it
// under the configured runtime path.
// We use ordered map types via explicit struct + json.Marshal sorting
// at render time so the output is deterministic (byte-identical across
// invocations with the same input) — important for FMT-05 deterministic
// re-hydrate / drift no-op detection.
//
// MCPServers carries the per-server JSON shape Claude Code consumes
// under the mcpServers key. Matches the upstream MCP server registry
// format (Hub spec §11.6 carries the same shape inside plugin .mcp.json
// files; the runtime location split is adapter-level only).
//
// A2AAgents mirrors the MCP server shape for A2A agents. CLI spec §7.4
// claude-code does not pin a fixed A2A shape (Claude Code's A2A support
// is recent + evolving), so we mirror the MCP shape under a parallel
// "a2aAgents" key. The orchestrator's autodetection layer can refine
// this later as the upstream contract solidifies.
type mcpJSONShape struct {
	MCPServers map[string]adapter.MCPServerEntry `json:"mcpServers"`
	A2AAgents  map[string]adapter.A2AAgentEntry  `json:"a2aAgents,omitempty"`
}

// renderMcpJSON builds the JSON bytes the adapter writes to
// settingsJSONPath (.claude/settings.json) from a manifest +
// credential. Returned bytes are deterministic: keys are emitted in
// sorted order because encoding/json sorts map keys lexicographically.
// The credential is embedded into each server's headers map under
// "x-ach-key" — matching the on-disk plaintext discipline CLI-04 (D-04)
// establishes.
func renderMcpJSON(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := mcpJSONShape{
		MCPServers: map[string]adapter.MCPServerEntry{},
		A2AAgents:  map[string]adapter.A2AAgentEntry{},
	}

	// Track the contributed keys for state.adapter.files[*].keys[]
	// (STATE-02 + ADAPT-05). Sorted lexicographically so the output
	// stays stable across invocations.
	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		entry := adapter.MCPServerEntry{
			Type:    "http",
			URL:     server.Endpoint,
			Headers: adapter.HeadersWithCredential(credential, m.Environment),
		}
		shape.MCPServers[server.ID] = entry
		contributedKeys = append(contributedKeys, "mcpServers."+server.ID)
	}

	for _, agent := range m.Runtime.A2AAgents {
		entry := adapter.A2AAgentEntry{
			Type:    "http",
			URL:     agent.Endpoint,
			Headers: adapter.HeadersWithCredential(credential, m.Environment),
		}
		shape.A2AAgents[agent.ID] = entry
		contributedKeys = append(contributedKeys, "a2aAgents."+agent.ID)
	}

	// Drop the a2aAgents key entirely when empty so a manifest with no
	// A2A agents round-trips to {"mcpServers": {...}} only. The
	// omitempty tag handles this for us, but only when the map is nil
	// — empty-map serializes to {} not absent. Convert empty → nil.
	if len(shape.A2AAgents) == 0 {
		shape.A2AAgents = nil
	}

	sort.Strings(contributedKeys)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(shape); err != nil {
		return nil, nil, fmt.Errorf("claudecode: encode mcpServers shape: %w", err)
	}

	return buf.Bytes(), contributedKeys, nil
}

// RenderRuntime emits the single .claude/settings.json FileWrite per
// CLI spec §7.4 claude-code section. Merge=MergeDeep; Keys carries the
// contributed top-level keys for STATE-02 + ADAPT-05 inverse-merge.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("claudecode: RenderRuntime called with nil manifest or runtime block")
	}

	cred := adapter.CredentialFromContext(ctx)
	content, keys, err := renderMcpJSON(m, cred)
	if err != nil {
		return nil, err
	}

	return []adapter.FileWrite{
		{
			Path:    settingsJSONPath,
			Content: content,
			Merge:   adapter.MergeDeep,
			Keys:    keys,
		},
	}, nil
}

// mcpDeepKeys is the claude-code adapter's only non-nil route.Rule.Transform
// (D-03/D-09). It is wired onto the `mcp/**/*` ProjectionRules row and serves a
// single purpose: enumerate the top-level MCP keys a plugin's mcp.{json,jsonc}
// contributes so the deep-merge engine can record them in state.files[*].keys[]
// (STATE-02 + ADAPT-05) and so the runtime-wins drop (plan 02, D-10) can drop
// ids that clash with the runtime MCP set.
//
// Byte discipline (D-03): mcpDeepKeys returns `in` UNCHANGED — it parses ONLY to
// read the top-level map keys, never re-encodes. Re-encoding would reorder the
// user's plugin file and break FMT-05 byte-stability / drift no-op detection.
//
// Key shape (D-09): for each top-level `mcpServers` map key id →
// "mcpServers."+id; if a top-level `a2aAgents` object is present, for each of
// its keys id → "a2aAgents."+id. Keys are sorted lexicographically, mirroring
// renderMcpJSON's contributedKeys loop, so the enumeration is deterministic.
//
// Malformed JSON returns a non-nil error so route.Project aborts that file
// (first-error discipline, T-02-07) rather than letting a server slip into the
// merge unenumerated. An input with no mcpServers object returns empty keys and
// out==in with no error.
//
// Phase-2 assumption: the plugin mcp source is treated as plain JSON. There is
// no jsonc comment-stripping helper in this codebase today; if one is added
// later (the source kind is mcp.{json,jsonc}) it should be reused here.
func mcpDeepKeys(srcRel string, in []byte) (out []byte, keys []string, err error) {
	// Decode only the top-level object so we read the contributed key names
	// without materializing or re-encoding the nested server definitions.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(in, &top); err != nil {
		return nil, nil, fmt.Errorf("claudecode: mcpDeepKeys parse %q: %w", srcRel, err)
	}

	keys = make([]string, 0)

	if raw, ok := top["mcpServers"]; ok {
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("claudecode: mcpDeepKeys parse %q mcpServers: %w", srcRel, err)
		}
		for id := range servers {
			keys = append(keys, "mcpServers."+id)
		}
	}

	if raw, ok := top["a2aAgents"]; ok {
		var agents map[string]json.RawMessage
		if err := json.Unmarshal(raw, &agents); err != nil {
			return nil, nil, fmt.Errorf("claudecode: mcpDeepKeys parse %q a2aAgents: %w", srcRel, err)
		}
		for id := range agents {
			keys = append(keys, "a2aAgents."+id)
		}
	}

	// Sort exactly like renderMcpJSON's contributedKeys so the enumeration is
	// stable across invocations.
	sort.Strings(keys)

	// D-03: return the input bytes UNCHANGED — no re-encode, no reorder.
	return in, keys, nil
}

// ProjectionRules returns the claude-code PASS-THROUGH projection table
// satisfying route.RuleProvider (the D-06 seam). claude-code is the canonical
// pass-through reference: the four file-owned resource kinds
// (rules/commands/agents/skills) route verbatim into .claude/<kind>/ as
// MergeReplace with NO field rewrite — FMT-03 is CUT (D-02): claude-code does
// NOT rewrite model/tools/permissions or any agent frontmatter; the canonical
// plugin format IS Claude Code format, so pass-through is byte-faithful.
//
// Two non-file rows complete the D-11 contract:
//   - AGENTS.md → CLAUDE.md as MergeComposite: the plugin's top-level AGENTS.md
//     prose is composited (marker-bounded) into the host CLAUDE.md memory file.
//   - mcp/**/* → settingsJSONPath as MergeDeep with Transform=mcpDeepKeys: the
//     plugin's MCP definitions deep-merge under mcpServers in the EXISTING
//     .claude/settings.json (D-08 — this is the same RenderRuntime MCP target;
//     there is NO .mcp.json filename switch and NO mcp_servers rename). The
//     Transform enumerates the contributed keys without altering the bytes.
//
// This method is pure data — no I/O. Projection runs through the Render leg
// (ProjectionRules -> route.Project).
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "rules/**/*", ToGlob: ".claude/rules/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "commands/**/*", ToGlob: ".claude/commands/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "agents/**/*", ToGlob: ".claude/agents/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".claude/skills/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "AGENTS.md", ToGlob: "CLAUDE.md", Merge: adapter.MergeComposite},
		{FromGlob: "mcp/**/*", ToGlob: settingsJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
	}
}

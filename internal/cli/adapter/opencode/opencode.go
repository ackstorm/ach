// SPDX-License-Identifier: Apache-2.0

// Package opencode is the OpenCode platform Adapter implementation per
// CLI spec §7.4 (opencode) + ADAPT-01 closed-set.
//
// OpenCode's runtime-config target is `.opencode/opencode.json` per
// spec §7.2 (Runtime-config file(s) column). The merge target inside
// that file is the top-level `mcp` key per §7.4 ("MCP merge target is
// `.opencode/opencode.json` under the `mcp` key per OpenCode's config
// format"). Plugin output root is `.opencode/plugins/<plugin-name>/`
// per §7.2, preserving the Claude-format layout so `--sync` removal
// stays simple (per the §7.4 codex/opencode shape requirement).
//
// Per §7.4 closing paragraph, any plugin component this adapter cannot
// meaningfully translate (`hooks/`, `.lsp.json`, `monitors/`, `bin/`,
// `settings.json`) is silently dropped from output. The dropped
// component names accumulate in PluginWrite.Dropped per ADAPT-07.
//
// ADAPT-06 scope rule: this adapter emits ONLY `.opencode/`-prefixed
// paths. Prompts and artifacts are written by the hydrator core
// directly (CLI spec §6.4); the adapter never touches them.
package opencode

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
//
// NOTE on plan-vs-spec divergence: the plan 07-W3-04 `<must_haves>`
// names ".opencode/config.json" as the runtime-config target. The CLI
// spec §7.2 + §7.4 (authoritative per the plan's own `<read_first>`
// directive and `<action>`'s "DO NOT guess; the spec is authoritative"
// instruction) name `.opencode/opencode.json`. We follow the spec.
// This is documented as a Rule-1 deviation in the plan's SUMMARY.
const (
	configJSONPath = ".opencode/opencode.json"

	canonicalID = "opencode"
)

// Adapter is the empty struct holding the opencode Adapter impl. The
// transformations are pure functions of their arguments (+ ctx for
// credential propagation), so no state is carried.
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
// type at the CLI. Per spec §7.2 row 4 (opencode), the Aliases column
// is `—` (no aliases) — we return an empty slice. This is consistent
// with the plan 07-W3-04 `<must_haves>` guidance: "Aliases() = []
// (no aliases per ADAPT-01 / spec §7.2 opencode row — verify against
// §7.2 at task time)".
func (a *Adapter) Aliases() []string { return []string{} }

// Detect scans root for opencode signals per spec §7.2 detection
// column (`.opencode/`, `opencode.json`) plus the plan 07-W3-04
// additional signals (`.opencode/config.json` for legacy/global mode,
// `.opencode/plugins/` directory). Each present signal boosts
// confidence; 3+ → High, 2 → Medium, 1 → Low. Reasons accumulate
// human-readable evidence the autodetection layer surfaces on multi-
// match exit.
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

	check(".opencode", "found .opencode/ directory")
	check(".opencode/opencode.json", "found .opencode/opencode.json")
	check(".opencode/plugins", "found .opencode/plugins/ directory")
	check("opencode.json", "found opencode.json at root")

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

// configJSONShape is the `.opencode/opencode.json` document OpenCode
// reads. The `mcp` top-level key holds MCP server registrations per
// spec §7.4 opencode row. We mirror the `a2aAgents` key under the
// same root to surface A2A agents symmetrically — same choice
// claudecode made in 07-W3-01.
//
// Deterministic output: encoding/json sorts map keys lexicographically,
// so the same manifest + credential always yields byte-identical bytes.
// SAFE-04 Tier 2 cascade depends on this byte-equality.
//
// MCP carries the per-server JSON shape OpenCode consumes under the
// `mcp` key. The shape mirrors the Claude Code MCP server registry
// format (type=http, url, headers) — spec §7.4 does not pin a tighter
// shape for OpenCode, so we keep the cross-adapter symmetry that
// 07-W3-01 established for claudecode.
//
// A2AAgents mirrors the MCP server shape for A2A agents. Spec §7.4 does
// not pin a fixed A2A shape for OpenCode (A2A support is recent +
// evolving across all platforms), so we mirror the MCP shape under a
// parallel `a2aAgents` top-level key — same shape choice as claudecode.
type configJSONShape struct {
	MCP       map[string]adapter.MCPServerEntry `json:"mcp"`
	A2AAgents map[string]adapter.A2AAgentEntry  `json:"a2aAgents,omitempty"`
}

// renderConfigJSON builds the `.opencode/opencode.json` bytes from a
// manifest + credential. Returned bytes are deterministic: keys are
// emitted in sorted order because encoding/json sorts map keys
// lexicographically. The credential is embedded into each server's
// headers map under "x-ach-key" — matching the on-disk plaintext
// discipline CLI-04 (D-04) establishes (and the cross-adapter
// symmetry claudecode established).
func renderConfigJSON(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := configJSONShape{
		MCP:       map[string]adapter.MCPServerEntry{},
		A2AAgents: map[string]adapter.A2AAgentEntry{},
	}

	// Track contributed top-level keys for state.adapter.files[*].keys[]
	// (STATE-02 + ADAPT-05). Sorted lexicographically so the output
	// stays stable across invocations.
	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		entry := adapter.MCPServerEntry{
			Type:    "http",
			URL:     server.Endpoint,
			Headers: adapter.HeadersWithCredential(credential),
		}
		shape.MCP[server.ID] = entry
		contributedKeys = append(contributedKeys, "mcp."+server.ID)
	}

	for _, agent := range m.Runtime.A2AAgents {
		entry := adapter.A2AAgentEntry{
			Type:    "http",
			URL:     agent.Endpoint,
			Headers: adapter.HeadersWithCredential(credential),
		}
		shape.A2AAgents[agent.ID] = entry
		contributedKeys = append(contributedKeys, "a2aAgents."+agent.ID)
	}

	// Drop the a2aAgents key entirely when empty so a manifest with no
	// A2A agents round-trips to {"mcp": {...}} only. The omitempty tag
	// handles this only when the map is nil — empty-map serializes to
	// {} not absent. Convert empty → nil.
	if len(shape.A2AAgents) == 0 {
		shape.A2AAgents = nil
	}

	sort.Strings(contributedKeys)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(shape); err != nil {
		return nil, nil, fmt.Errorf("opencode: encode opencode.json: %w", err)
	}

	return buf.Bytes(), contributedKeys, nil
}

// RenderRuntime emits the single `.opencode/opencode.json` FileWrite
// per CLI spec §7.4 opencode. Merge=MergeDeep per §7.2 row 4
// ("`merge: deep`"); Keys carries the contributed top-level paths
// (e.g. `mcp.<server-id>`, `a2aAgents.<agent-id>`) for STATE-02 +
// ADAPT-05 inverse-merge on `--sync`.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("opencode: RenderRuntime called with nil manifest or runtime block")
	}

	cred := adapter.CredentialFromContext(ctx)
	content, keys, err := renderConfigJSON(m, cred)
	if err != nil {
		return nil, err
	}

	return []adapter.FileWrite{
		{
			Path:    configJSONPath,
			Content: content,
			Merge:   adapter.MergeDeep,
			Keys:    keys,
		},
	}, nil
}

// droppableComponent reports whether the given top-level component
// name under the plugin tree is one this adapter silently drops per
// spec §7.4 closing paragraph + plan 07-W3-04 ADAPT-07 contract.
// OpenCode has no hook system; `.lsp.json`, `monitors/`, `bin/`, and
// `settings.json` are also v1alpha1-ignored per spec §7.4 common
// input table footnote.
func droppableComponent(name string) bool {
	switch name {
	case "hooks", ".lsp.json", "monitors", "bin", "settings.json":
		return true
	}
	return false
}

// TransformPlugin walks the src plugin tree and writes the platform-
// native plugin tree at dst per spec §7.4 opencode row:
//
//   - Files under known top-level components (commands/, agents/,
//     skills/, prompts/) are preserved verbatim under their original
//     relative paths inside dst (Claude layout preserved so `--sync`
//     removal stays simple per the §7.4 codex/opencode requirement).
//   - The plugin's `.mcp.json` is RECORDED as dropped under the file
//     name (it is logically consumed by RenderRuntime — not a per-
//     file plugin output). See §7.4 codex row: "Merge the plugin's
//     .mcp.json (if present) into [the runtime config]". The byte-
//     identical merge happens at orchestrator time when the staging
//     manifest is rendered; this method emits no per-file copy of
//     `.mcp.json`.
//   - `hooks/`, `.lsp.json`, `monitors/`, `bin/`, `settings.json` —
//     silently dropped per §7.4 closing paragraph + plan's ADAPT-07
//     scope rule. Each unique dropped component name accumulates in
//     PluginWrite.Dropped (no path-level granularity — the spec
//     warning is "<plugin> dropped: hooks", not "<plugin> dropped:
//     hooks/preflight.sh").
//   - `.claude-plugin/plugin.json` REQUIRED — preserved verbatim
//     under dst so OpenCode can discover plugin metadata.
//
// File mode discipline matches claudecode: every regular file is
// chmod'd to 0644; directories to 0755. SAFE-02 mirror.
func (a *Adapter) TransformPlugin(_ context.Context, src, dst string) (adapter.PluginWrite, error) {
	if src == "" || dst == "" {
		return adapter.PluginWrite{}, fmt.Errorf("opencode: TransformPlugin requires non-empty src and dst")
	}

	extracted := make([]string, 0, 16)
	droppedSet := make(map[string]struct{}, 4)

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("opencode: rel(%q, %q): %w", src, path, err)
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}

		// Identify the top-level component (first path segment under
		// src). For files at src root (e.g. `.mcp.json`), this is the
		// file name itself.
		top := adapter.TopLevelComponent(rel)

		// `.mcp.json` is consumed by RenderRuntime, never emitted as
		// a per-file plugin output. We do NOT record it as Dropped
		// (it is not "lost" — its semantic content lands in
		// `.opencode/opencode.json`).
		if rel == ".mcp.json" {
			return nil
		}

		// Silent-drop list per spec §7.4 + plan ADAPT-07.
		if droppableComponent(top) {
			droppedSet[top] = struct{}{}
			if d.IsDir() {
				// SkipDir prevents descending into the dropped tree.
				return filepath.SkipDir
			}
			return nil
		}

		dstPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		// Regular files only — symlinks, devices, FIFOs are rejected
		// by the W2-01 safe-extract layer before TransformPlugin sees
		// the tree. Defensive check: skip non-regular entries silently.
		if !d.Type().IsRegular() {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := adapter.CopyFile(path, dstPath); err != nil {
			return err
		}
		extracted = append(extracted, rel)
		return nil
	})
	if err != nil {
		return adapter.PluginWrite{}, err
	}

	sort.Strings(extracted)

	var dropped []string
	if len(droppedSet) > 0 {
		dropped = make([]string, 0, len(droppedSet))
		for k := range droppedSet {
			dropped = append(dropped, k)
		}
		sort.Strings(dropped)
	}

	return adapter.PluginWrite{
		ExtractedFiles: extracted,
		Dropped:        dropped,
	}, nil
}

// ProjectionRules returns the opencode ROUTE-04 conversion projection table
// satisfying route.RuleProvider (the D-06 seam). Per D-18/D-19 the routed
// (plural) kinds are:
//
//   - commands/**/*  → .opencode/commands/**/*  (MergeReplace, verbatim copy)
//   - agents/**/*.md → .opencode/agents/**/*.md (MergeReplace, Transform:
//     opencodeAgentTools — tools[]→{name:true}, output stays markdown)
//   - skills/**/*    → .opencode/skills/**/*    (MergeReplace, verbatim copy)
//   - mcp/**/*       → .opencode/opencode.json  (MergeDeep, Transform:
//     opencodeMCPRename — mcpServers→mcp, no header surgery, JSON output)
//
// rules/ and AGENTS.md have NO rule and fall into route.Project's dropped set
// (route.go records each unrouted top-level kind exactly once — D-12/D-19); the
// droppable runtime components (hooks/, .lsp.json, monitors/, bin/,
// settings.json) likewise have no rule and drop the same way (matching
// droppableComponent). There is deliberately no prompts/ row: D-18 routes only
// commands/agents/skills/mcp for opencode (OPENPACKAGE-MAPPING §opencode).
//
// The mcp/**/* row collapses N→1 onto the SAME runtime target
// (configJSONPath) RenderRuntime emits, deep-merged under the `mcp` key (D-21);
// the runtime encoder (renderConfigJSON) is left untouched. This method is pure
// data — no I/O. TransformPlugin is LEFT AS-IS — projection runs via the
// plan-02 Render leg (ProjectionRules -> route.Project).
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*", ToGlob: ".opencode/commands/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "agents/**/*.md", ToGlob: ".opencode/agents/**/*.md", Merge: adapter.MergeReplace, Transform: opencodeAgentTools},
		{FromGlob: "skills/**/*", ToGlob: ".opencode/skills/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "mcp/**/*", ToGlob: configJSONPath, Merge: adapter.MergeDeep, Transform: opencodeMCPRename},
	}
}

// opencodeAgentTools is the agent-frontmatter Transform (FMT-04, D-20). Task 2
// (TDD) fills the real tools[]→{name:true} re-encode; this Task-1 placeholder
// passes the source bytes through unchanged so the projection table compiles
// and the existing passthrough behavior is preserved until GREEN lands.
func opencodeAgentTools(_ string, in []byte) (out []byte, keys []string, err error) {
	return in, nil, nil
}

// opencodeMCPRename is the MCP Transform (D-21). Task 2 (TDD) fills the real
// mcpServers→mcp rename + canonical JSON emission; this Task-1 placeholder
// passes the source bytes through unchanged so the projection table compiles
// until GREEN lands.
func opencodeMCPRename(_ string, in []byte) (out []byte, keys []string, err error) {
	return in, nil, nil
}

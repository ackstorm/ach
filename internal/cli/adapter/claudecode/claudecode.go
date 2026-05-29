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
//   - ADAPT-07 silent-drop accounting is structurally available (the
//     adapter returns PluginWrite{Dropped: nil}), but claude-code has
//     no source-tree component it cannot translate — Dropped is always
//     nil.
//   - The runtime-config target .claude/.mcp.json is the Claude Code
//     MCP server registry format verbatim — the same shape the Hub's
//     Plugin .mcp.json carries.
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
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Target paths emitted by this adapter. Centralized as constants so
// the test assertions and the production code stay in lock-step.
const (
	mcpJSONPath = ".claude/.mcp.json"

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

// mcpServerEntry is the per-server JSON shape Claude Code consumes in
// .claude/.mcp.json. Matches the upstream Claude Code MCP server
// registry format (Hub spec §11.6 carries the same shape inside
// plugin .mcp.json files).
type mcpServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// a2aAgentEntry mirrors the MCP server shape for A2A agents. CLI spec
// §7.4 claude-code does not pin a fixed A2A shape (Claude Code's A2A
// support is recent + evolving), so we mirror the MCP shape under a
// parallel "a2aAgents" key. The orchestrator's autodetection layer
// can refine this later as the upstream contract solidifies.
type a2aAgentEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// mcpJSONShape is the .claude/.mcp.json document Claude Code reads.
// We use ordered map types via explicit struct + json.Marshal sorting
// at render time so the output is deterministic (byte-identical across
// invocations with the same input) — important for ResolveOutputContent
// SAFE-04 cascade equality.
type mcpJSONShape struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	A2AAgents  map[string]a2aAgentEntry  `json:"a2aAgents,omitempty"`
}

// renderMcpJSON builds the .claude/.mcp.json bytes from a manifest +
// credential. Returned bytes are deterministic: keys are emitted in
// sorted order because encoding/json sorts map keys lexicographically.
// The credential is embedded into each server's headers map under
// "x-ach-key" — matching the on-disk plaintext discipline CLI-04 (D-04)
// establishes.
func renderMcpJSON(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := mcpJSONShape{
		MCPServers: map[string]mcpServerEntry{},
		A2AAgents:  map[string]a2aAgentEntry{},
	}

	// Track the contributed keys for state.adapter.files[*].keys[]
	// (STATE-02 + ADAPT-05). Sorted lexicographically so the output
	// stays stable across invocations.
	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		entry := mcpServerEntry{
			Type:    "http",
			URL:     server.Endpoint,
			Headers: headersWithCredential(credential),
		}
		shape.MCPServers[server.ID] = entry
		contributedKeys = append(contributedKeys, "mcpServers."+server.ID)
	}

	for _, agent := range m.Runtime.A2AAgents {
		entry := a2aAgentEntry{
			Type:    "http",
			URL:     agent.Endpoint,
			Headers: headersWithCredential(credential),
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
		return nil, nil, fmt.Errorf("claudecode: encode .mcp.json: %w", err)
	}

	return buf.Bytes(), contributedKeys, nil
}

// headersWithCredential returns the per-server headers map. When the
// credential is empty (offline / dry-run / unit-test), we still emit
// the x-ach-key header with empty value so the JSON shape stays
// stable — the orchestrator's at-publication-time credential check
// (plan 07-W3-05) gates whether to attempt the write at all.
func headersWithCredential(cred string) map[string]string {
	return map[string]string{
		"x-ach-key": cred,
	}
}

// RenderRuntime emits the single .claude/.mcp.json FileWrite per
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
			Path:    mcpJSONPath,
			Content: content,
			Merge:   adapter.MergeDeep,
			Keys:    keys,
		},
	}, nil
}

// TransformPlugin copies the src plugin tree verbatim into dst. This
// is the pass-through reference impl: ADAPT-04 pins the canonical
// plugin format to Claude Code, so the claude-code adapter is a
// straight filepath.WalkDir + io.Copy loop. ADAPT-07 Dropped is always
// nil — claude-code drops nothing.
//
// File mode discipline: every regular file is chmod'd to 0644;
// directories to 0755. This mirrors SAFE-02 (the safe-extract layer
// in W2-01 masks modes the same way), so even if a plugin tarball was
// extracted with a permissive umask, the claudecode pass-through
// re-normalizes on write.
func (a *Adapter) TransformPlugin(_ context.Context, src, dst string) (adapter.PluginWrite, error) {
	if src == "" || dst == "" {
		return adapter.PluginWrite{}, fmt.Errorf("claudecode: TransformPlugin requires non-empty src and dst")
	}

	extracted := make([]string, 0, 16)

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("claudecode: rel(%q, %q): %w", src, path, err)
		}
		if rel == "." {
			// Skip src root itself — dst is the destination, src content
			// goes UNDER dst.
			return os.MkdirAll(dst, 0o755)
		}

		dstPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		// Regular files only — symlinks, devices, FIFOs are rejected by
		// the W2-01 safe-extract layer before TransformPlugin sees the
		// tree. Defensive check: skip non-regular entries silently.
		if !d.Type().IsRegular() {
			return nil
		}

		if err := copyFile(path, dstPath); err != nil {
			return err
		}
		extracted = append(extracted, rel)
		return nil
	})
	if err != nil {
		return adapter.PluginWrite{}, err
	}

	sort.Strings(extracted)

	return adapter.PluginWrite{
		ExtractedFiles: extracted,
		Dropped:        nil, // claude-code drops nothing per ADAPT-04 pass-through
	}, nil
}

// copyFile copies srcPath → dstPath with mode 0644. Parent dirs are
// expected to already exist (WalkDir order guarantees this).
func copyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath) //nolint:gosec // srcPath is under our staging dir
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) //nolint:gosec // dstPath is under our destination dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// MergeStrategies returns the per-target merge classification per
// CLI spec §7.1 + ADAPT-05. claude-code merges .claude/.mcp.json deep
// (the plugin .mcp.json files contributed by Plugins get layered onto
// the runtime-config one via deep-merge).
func (a *Adapter) MergeStrategies() map[string]adapter.MergeKind {
	return map[string]adapter.MergeKind{
		mcpJSONPath: adapter.MergeDeep,
	}
}

// ResolveOutputContent satisfies the SAFE-04 cascade Tier 2 contract
// from plan 07-W2-03. For target ".claude/.mcp.json" we recompute the
// bytes RenderRuntime would emit (so the cascade can compare against
// disk bytes without re-running the orchestrator). For any other
// target, we return (nil, nil) — the cascade falls through to Tier 3
// (source-byte read), which is the right behavior for pass-through
// plugin files (claudecode's TransformPlugin already emits source bytes
// verbatim).
func (a *Adapter) ResolveOutputContent(ctx context.Context, m *manifest.Manifest, target string) ([]byte, error) {
	if target != mcpJSONPath {
		return nil, nil
	}
	if m == nil || m.Runtime == nil {
		return nil, nil
	}
	cred := adapter.CredentialFromContext(ctx)
	content, _, err := renderMcpJSON(m, cred)
	if err != nil {
		return nil, err
	}
	return content, nil
}

// SPDX-License-Identifier: Apache-2.0

// Package codex is the OpenAI Codex CLI platform adapter per CLI spec
// §7.2 row 2 and §7.4 codex section. It is the second of four adapters
// in the v1alpha1 closed set (claude-code, codex, gemini-cli, opencode)
// and the first NON-pass-through adapter — its runtime-config target is
// TOML (not JSON) and its plugin transformation rewrites Claude-format
// agent frontmatter into the Codex agent schema, with silent-drop
// accounting for source-tree components Codex has no destination for.
//
// Per CONTEXT.md D-05 the four-adapter set must demonstrably translate
// the same Hub manifest into four distinct on-disk shapes; the codex
// adapter is the first non-trivial transformation reference that the
// gemini-cli and opencode siblings mirror (W3-03 and W3-04 land in
// parallel against this plan).
//
// Design notes:
//
//   - Runtime-config target: ".codex/config.toml" with [mcp_servers.<id>]
//     and [a2a_agents.<id>] tables (CLI spec §7.4 codex). Each table
//     carries url + headers["x-ach-key"] + transport keys. MergeDeep
//     classification; contributed top-level keys recorded under
//     "mcp_servers.<id>" / "a2a_agents.<id>" so STATE-05 inverse-merge
//     can target the right TOML subtable.
//
//   - Plugin transformation: src follows the Claude Code plugin layout
//     (.claude-plugin/plugin.json + agents/ + commands/ + prompts/ +
//     skills/ + hooks/ + .mcp.json). Codex destinations preserve the
//     Claude layout under dst (the orchestrator picks the per-plugin
//     dst, typically .codex/plugins/<plugin-name>/) but apply two
//     transformations:
//
//     1. ADAPT-07 silent-drop: src/commands/ and src/hooks/ are NOT
//     copied; their names accumulate into PluginWrite.Dropped. The
//     orchestrator (plan 07-W3-05) emits a single stderr warning at
//     end of hydration listing every dropped component across every
//     plugin; exit code is unchanged.
//
//     2. Frontmatter rewrite for src/agents/<name>.md: the YAML
//     frontmatter block at file head is parsed line-by-line; the
//     Claude `tools:` key is renamed to `allowed_tools:` per CLI
//     spec §7.4. Other keys (model, permissions, name, description)
//     pass through verbatim. The body after frontmatter is copied
//     verbatim.
//
//   - .mcp.json discipline: the plugin's optional .mcp.json is consumed
//     by the orchestrator at runtime-config rendering time (merged into
//     .codex/config.toml's [mcp_servers] tables — handled by the
//     orchestrator + a future Hub-to-runtime bridge, NOT by this
//     adapter's TransformPlugin). It is therefore NOT copied to dst and
//     NOT accumulated in Dropped (it IS consumed, just at a different
//     layer).
//
//   - ADAPT-06 scope rule: this adapter emits ONLY .codex/-prefixed
//     paths. Prompts and artifacts are written by the hydrator core
//     directly (CLI spec §6.4); the adapter never touches them.
//
//   - Credential discipline: the bearer is obtained via
//     adapter.CredentialFromContext(ctx) — never from env vars. An empty
//     credential still renders an empty header value so the TOML shape
//     stays stable for unit/dry-run paths.
package codex

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Target paths emitted by this adapter. Centralized as constants so the
// test assertions and the production code stay in lock-step.
const (
	configTOMLPath = ".codex/config.toml"

	// canonicalID matches CLI spec §7.2 row 2.
	canonicalID = "codex"
)

// Adapter is the empty struct holding the codex Adapter impl. Like the
// claudecode reference, the adapter holds no state — every method is a
// pure function of its arguments (and ctx for credential propagation).
type Adapter struct{}

// init registers the adapter with the package-level registry. The cobra
// autodetection layer (plan 07-W3-05) blank-imports this subpackage so
// the init() fires before main() reaches Lookup / Iter.
func init() {
	adapter.Register(&Adapter{})
}

// ID returns the canonical adapter ID per CLI spec §7.2 row 2.
func (a *Adapter) ID() string { return canonicalID }

// Aliases returns the case-folded alternate identifiers a user may type
// at the CLI. Plan 07-W3-02 contract: ["codex-cli"]. Note that this is
// a single alias, NOT the spec §7.2 pair ["codex-cli", "openai-codex"]
// — the plan must_haves explicitly select the shorter single-alias set,
// same parallelism-enabling discipline as claudecode's
// ["claude", "cc"]. The registry case-folds at registration + Lookup,
// so callers passing "Codex-CLI" or "CODEX-CLI" resolve correctly.
func (a *Adapter) Aliases() []string { return []string{"codex-cli"} }

// Detect scans root for codex signals. Each present signal boosts
// confidence; 3+ → High, 2 → Medium, 1 → Low. Reasons accumulate
// human-readable evidence the autodetection layer surfaces on
// multi-match exit.
//
// Signals checked (matching plan behavior section):
//
//   - .codex/ directory at root
//   - .codex/config.toml file
//   - .codex/agents/ directory
//   - $HOME/.codex/ directory (global-mode signal — Detect can return
//     Low confidence even when the local cwd has no codex artifacts IF
//     $HOME/.codex/ is present, supporting global-mode discovery per
//     CLI spec §7.3)
//
// Returns an empty Match (zero ID + zero Confidence) when no signals
// are seen — the autodetection layer treats that as a no-match.
func (a *Adapter) Detect(root string) (adapter.Match, error) {
	signals := 0
	reasons := make([]string, 0, 4)

	check := func(full string, reason string) {
		if _, err := os.Stat(full); err == nil {
			signals++
			reasons = append(reasons, reason)
		}
	}

	check(filepath.Join(root, ".codex"), "found .codex/ directory")
	check(filepath.Join(root, ".codex", "config.toml"), "found .codex/config.toml")
	check(filepath.Join(root, ".codex", "agents"), "found .codex/agents/ directory")

	// Global-mode hint: $HOME/.codex/ contributes a Low-confidence signal
	// even when the local cwd has no codex artifacts. Skipped when HOME
	// is unset (defensive — os.UserHomeDir errors on unset HOME).
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		// Avoid double-counting when root == $HOME (the .codex check
		// above already covered it).
		homeCodex := filepath.Join(home, ".codex")
		absRoot, _ := filepath.Abs(root)
		if absRoot != home {
			check(homeCodex, "found $HOME/.codex/ directory (global-mode hint)")
		}
	}

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

// mcpServerTable is the per-server TOML shape Codex consumes at
// [mcp_servers.<id>]. Field tags match CLI spec §7.4 codex literal
// keys.
type mcpServerTable struct {
	URL       string            `toml:"url"`
	Headers   map[string]string `toml:"headers"`
	Transport string            `toml:"transport"`
}

// a2aAgentTable mirrors the MCP server shape for A2A agents under
// [a2a_agents.<id>]. CLI spec §7.4 codex does not pin a fixed A2A
// shape; we mirror the MCP shape for symmetry, same posture as
// claudecode mirrors mcpServerEntry to a2aAgentEntry. If the upstream
// contract solidifies on something else, this adapter is the only impl
// that needs to change — the Adapter interface stays stable.
type a2aAgentTable struct {
	URL       string            `toml:"url"`
	Headers   map[string]string `toml:"headers"`
	Transport string            `toml:"transport"`
}

// configTOMLShape is the .codex/config.toml document Codex reads. The
// top-level keys are emitted by BurntSushi/toml in source-declaration
// order — but our values are maps, and TOML map encoding sorts keys
// lexicographically, so the output is deterministic across invocations
// with the same input. ResolveOutputContent's SAFE-04 Tier 2 byte-equal
// compare depends on this.
type configTOMLShape struct {
	MCPServers map[string]mcpServerTable `toml:"mcp_servers"`
	A2AAgents  map[string]a2aAgentTable  `toml:"a2a_agents"`
}

// renderConfigTOML builds the .codex/config.toml bytes from a manifest
// + credential. Returned bytes are deterministic.
//
// Each MCP server / A2A agent contributes a [mcp_servers.<id>] /
// [a2a_agents.<id>] table with the credential under headers["x-ach-key"]
// per the on-disk plaintext discipline CLI-04 (D-04) establishes.
func renderConfigTOML(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := configTOMLShape{
		MCPServers: map[string]mcpServerTable{},
		A2AAgents:  map[string]a2aAgentTable{},
	}

	// Track contributed top-level keys for state.adapter.files[*].keys[]
	// (STATE-02 + ADAPT-05). The "mcp_servers." / "a2a_agents." prefix
	// discipline lets STATE-05 inverse-merge target the right TOML
	// subtable.
	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		shape.MCPServers[server.ID] = mcpServerTable{
			URL:       server.Endpoint,
			Headers:   headersWithCredential(credential),
			Transport: "http",
		}
		contributedKeys = append(contributedKeys, "mcp_servers."+server.ID)
	}

	for _, agent := range m.Runtime.A2AAgents {
		shape.A2AAgents[agent.ID] = a2aAgentTable{
			URL:       agent.Endpoint,
			Headers:   headersWithCredential(credential),
			Transport: "http",
		}
		contributedKeys = append(contributedKeys, "a2a_agents."+agent.ID)
	}

	sort.Strings(contributedKeys)

	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "  "
	if err := enc.Encode(shape); err != nil {
		return nil, nil, fmt.Errorf("codex: encode config.toml: %w", err)
	}

	return buf.Bytes(), contributedKeys, nil
}

// headersWithCredential returns the per-server headers map. When the
// credential is empty (offline / dry-run / unit-test), we still emit
// the x-ach-key header with empty value so the TOML shape stays
// stable.
func headersWithCredential(cred string) map[string]string {
	return map[string]string{
		"x-ach-key": cred,
	}
}

// RenderRuntime emits the single .codex/config.toml FileWrite per
// CLI spec §7.4 codex section. Merge=MergeDeep; Keys carries the
// contributed "mcp_servers.<id>" / "a2a_agents.<id>" entries for
// STATE-02 + ADAPT-05 inverse-merge.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("codex: RenderRuntime called with nil manifest or runtime block")
	}

	cred := adapter.CredentialFromContext(ctx)
	content, keys, err := renderConfigTOML(m, cred)
	if err != nil {
		return nil, err
	}

	return []adapter.FileWrite{
		{
			Path:    configTOMLPath,
			Content: content,
			Merge:   adapter.MergeDeep,
			Keys:    keys,
		},
	}, nil
}

// dropName classifies the top-level source component the orchestrator
// silently drops per ADAPT-07. The Codex adapter cannot meaningfully
// translate `commands/` (no commands concept in Codex) or `hooks/`
// (no hook system) into the .codex/ layout, so both are recorded in
// PluginWrite.Dropped and the orchestrator emits a single end-of-hydration
// stderr warning listing every dropped name.
//
// The check is on the FIRST path element under src — a nested file like
// src/commands/foo/bar.md is still classified under "commands".
type droppedSet struct {
	seen map[string]bool
	out  []string
}

func newDroppedSet() *droppedSet {
	return &droppedSet{seen: map[string]bool{}, out: nil}
}

func (d *droppedSet) add(name string) {
	if d.seen[name] {
		return
	}
	d.seen[name] = true
	d.out = append(d.out, name)
}

// silentDropTopLevel is the set of src top-level component names that
// Codex silently drops per CLI spec §7.4 last paragraph + plan
// behavior. The orchestrator's per-plugin .mcp.json consumption is
// separate (handled at runtime-config rendering, not TransformPlugin),
// so ".mcp.json" is intentionally NOT in this set.
var silentDropTopLevel = map[string]bool{
	"commands": true,
	"hooks":    true,
}

// TransformPlugin walks the src plugin tree and writes a transformed
// codex-layout tree under dst.
//
// Behavior summary:
//
//   - Regular files outside the silent-drop top-level components are
//     copied into dst preserving src's relative path layout (mirroring
//     the Claude layout under dst, per CLI spec §7.4 codex "preserving
//     the Claude layout").
//
//   - Files under src/agents/*.md additionally have their YAML
//     frontmatter rewritten: the Claude `tools:` key is renamed to
//     `allowed_tools:`. Other frontmatter keys + body bytes pass
//     through verbatim.
//
//   - Top-level components in silentDropTopLevel (commands/, hooks/)
//     are skipped wholesale and their names accumulated into
//     PluginWrite.Dropped per ADAPT-07.
//
//   - The plugin's optional .mcp.json is consumed by the orchestrator
//     at runtime-config rendering, not by this method. It is therefore
//     NOT copied and NOT recorded in Dropped (it IS consumed, just at
//     a different layer).
//
// File mode discipline matches claudecode: every regular file is
// chmod'd to 0644; directories to 0755. SAFE-02 in W2-01 enforces the
// same mode mask on extraction; this re-normalizes on write as
// defense-in-depth.
func (a *Adapter) TransformPlugin(_ context.Context, src, dst string) (adapter.PluginWrite, error) {
	if src == "" || dst == "" {
		return adapter.PluginWrite{}, fmt.Errorf("codex: TransformPlugin requires non-empty src and dst")
	}

	extracted := make([]string, 0, 16)
	dropped := newDroppedSet()

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("codex: rel(%q, %q): %w", src, path, err)
		}
		if rel == "." {
			// Skip src root itself — dst is the destination, src content
			// goes UNDER dst.
			return os.MkdirAll(dst, 0o755)
		}

		// Determine the top-level component under src — used to drive
		// the silent-drop discipline AND the per-component routing
		// (agents → frontmatter rewrite; everything else → verbatim).
		parts := strings.Split(filepath.ToSlash(rel), "/")
		topLevel := parts[0]

		// Silent-drop: skip this entry entirely AND skip recursion into
		// the dir.
		if silentDropTopLevel[topLevel] {
			dropped.add(topLevel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// .mcp.json at src root: consumed at runtime-config rendering
		// (not here). Do NOT copy; do NOT record as Dropped (it IS
		// consumed, just at a different layer).
		if rel == ".mcp.json" {
			return nil
		}

		dstPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		// Regular files only — symlinks, devices, FIFOs are rejected by
		// the W2-01 safe-extract layer before TransformPlugin sees the
		// tree. Defensive skip for non-regular entries.
		if !d.Type().IsRegular() {
			return nil
		}

		// agents/*.md → frontmatter rewrite path.
		// Everything else → verbatim copy.
		if topLevel == "agents" && strings.HasSuffix(strings.ToLower(rel), ".md") {
			if err := writeAgentWithFrontmatterRewrite(path, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(path, dstPath); err != nil {
				return err
			}
		}
		extracted = append(extracted, rel)
		return nil
	})
	if err != nil {
		return adapter.PluginWrite{}, err
	}

	sort.Strings(extracted)
	sort.Strings(dropped.out)

	return adapter.PluginWrite{
		ExtractedFiles: extracted,
		Dropped:        dropped.out,
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

	// Per 07-W5-05 (WR-02): explicit close to surface buffered-write
	// errors that surface only at close(2) (EIO/ENOSPC). A deferred
	// `_ = out.Close()` would silently drop those errors, recording a
	// truncated file as successfully written.
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// writeAgentWithFrontmatterRewrite reads srcPath, rewrites the YAML
// frontmatter block per CLI spec §7.4 codex, and writes the result to
// dstPath at mode 0644.
//
// Frontmatter key rewrite map (Claude → Codex per spec §7.4):
//
//	tools:        → allowed_tools:  (Codex names its allow-list with the
//	                                  explicit "allowed_" prefix)
//	model:        → model:          (passes through verbatim)
//	permissions:  → permissions:    (passes through verbatim — Codex's
//	                                  permission grammar is a superset
//	                                  of Claude's at the key level; the
//	                                  contained values pass through
//	                                  unchanged at v1alpha1; the spec
//	                                  reserves room for value-level
//	                                  remap in a future release)
//	name:         → name:           (passes through)
//	description:  → description:    (passes through)
//
// The rewrite operates line-by-line on the YAML frontmatter block
// between the file-head `---` markers. The body after the closing
// `---` is copied verbatim. Files without a frontmatter block (no
// leading `---` line) are copied verbatim.
func writeAgentWithFrontmatterRewrite(srcPath, dstPath string) error {
	raw, err := os.ReadFile(srcPath) //nolint:gosec // srcPath is under our staging dir
	if err != nil {
		return err
	}

	rewritten := rewriteAgentFrontmatter(raw)

	return os.WriteFile(dstPath, rewritten, 0o644) //nolint:gosec // dstPath is under our destination dir
}

// rewriteAgentFrontmatter applies the Codex key rewrites to the YAML
// frontmatter block at the head of raw and returns the rewritten
// bytes. The body after the closing `---` is preserved verbatim.
// Files without a frontmatter block (no leading `---`) are returned
// verbatim.
//
// Implementation notes:
//
//   - We split into 3 logical regions: pre-frontmatter (empty for a
//     well-formed agent file), frontmatter (between first two `---`
//     lines), and body (after the closing `---`).
//   - Inside the frontmatter region, we operate line-by-line and
//     rewrite ONLY the leading key on each line. Nested values (under
//     a `permissions:` block, say) are left untouched at v1alpha1.
//   - Line endings are preserved verbatim. We do NOT canonicalize
//     CRLF → LF — the orchestrator's hash discipline (xxh3) requires
//     byte-stable round-trips for Tier 2 SAFE-04 compares.
func rewriteAgentFrontmatter(raw []byte) []byte {
	// Quick reject: no leading "---" means no frontmatter, return as-is.
	// Use a small slice peek instead of HasPrefix on the whole buffer
	// to avoid a copy for large files.
	if !startsWithFrontmatterFence(raw) {
		return raw
	}

	// Locate the closing "---" on its own line, starting from after the
	// opening fence + newline. We scan line-by-line.
	openEnd, closeStart, closeEnd, found := findFrontmatterFences(raw)
	if !found {
		// No closing fence — not a well-formed frontmatter block; return
		// verbatim.
		return raw
	}

	openingFence := raw[:openEnd]            // includes the opening "---\n"
	frontmatter := raw[openEnd:closeStart]   // contents (excludes both fences)
	closingFence := raw[closeStart:closeEnd] // includes the closing "---\n"
	body := raw[closeEnd:]                   // post-frontmatter body

	rewritten := rewriteFrontmatterLines(frontmatter)

	var buf bytes.Buffer
	buf.Grow(len(raw) + len(rewritten) - len(frontmatter))
	buf.Write(openingFence)
	buf.Write(rewritten)
	buf.Write(closingFence)
	buf.Write(body)
	return buf.Bytes()
}

// startsWithFrontmatterFence returns true if raw begins with a YAML
// frontmatter opening fence ("---\n" or "---\r\n"). Pure prefix check.
func startsWithFrontmatterFence(raw []byte) bool {
	if len(raw) < 4 {
		return false
	}
	if raw[0] != '-' || raw[1] != '-' || raw[2] != '-' {
		return false
	}
	// Allow "---\n" or "---\r\n".
	if raw[3] == '\n' {
		return true
	}
	if len(raw) >= 5 && raw[3] == '\r' && raw[4] == '\n' {
		return true
	}
	return false
}

// findFrontmatterFences locates the byte offsets of the opening and
// closing YAML frontmatter fences in raw. Returns:
//
//	openEnd:   byte offset of the first character AFTER the opening
//	           fence + its trailing newline (i.e. the first byte of
//	           frontmatter content).
//	closeStart: byte offset of the first character of the closing
//	           fence (typically the leading "-" of "---" on its own
//	           line).
//	closeEnd:  byte offset of the first character AFTER the closing
//	           fence + its trailing newline (i.e. the first byte of
//	           the body).
//	found:     true iff a well-formed closing fence was found.
//
// Caller guarantees raw starts with a frontmatter opening fence (call
// startsWithFrontmatterFence first).
func findFrontmatterFences(raw []byte) (openEnd, closeStart, closeEnd int, found bool) {
	// Advance past the opening fence + newline.
	// raw[0..2] = "---"; raw[3] is '\n' or '\r' per startsWithFrontmatterFence.
	switch raw[3] {
	case '\n':
		openEnd = 4
	case '\r':
		openEnd = 5
	}

	// Scan line-by-line from openEnd looking for a line whose content
	// is exactly "---".
	i := openEnd
	for i < len(raw) {
		lineStart := i
		// Find end of line.
		for i < len(raw) && raw[i] != '\n' {
			i++
		}
		// raw[lineStart..i] is one line content (excluding trailing
		// newline). Strip a trailing \r if present (CRLF).
		lineEnd := i
		lineContent := raw[lineStart:lineEnd]
		if len(lineContent) > 0 && lineContent[len(lineContent)-1] == '\r' {
			lineContent = lineContent[:len(lineContent)-1]
		}
		if bytes.Equal(lineContent, []byte("---")) {
			closeStart = lineStart
			closeEnd = i
			if closeEnd < len(raw) && raw[closeEnd] == '\n' {
				closeEnd++
			}
			return openEnd, closeStart, closeEnd, true
		}
		// Advance past the newline.
		if i < len(raw) && raw[i] == '\n' {
			i++
		}
	}
	return 0, 0, 0, false
}

// rewriteFrontmatterLines applies the per-line key rewrites inside the
// YAML frontmatter region. Each line is processed independently; only
// the leading key (text before the first `:`) at indentation level 0
// is candidate for rewriting. Nested keys (indented lines) pass
// through verbatim — Codex's value-level grammar matches Claude's
// at v1alpha1.
//
// Rewrites applied (per CLI spec §7.4 codex):
//
//	tools:        → allowed_tools:
//
// All other top-level keys pass through verbatim. Line endings are
// preserved.
func rewriteFrontmatterLines(frontmatter []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(frontmatter))

	i := 0
	for i < len(frontmatter) {
		lineStart := i
		for i < len(frontmatter) && frontmatter[i] != '\n' {
			i++
		}
		// Line content is frontmatter[lineStart..i]; the newline (if
		// any) is at frontmatter[i].
		line := frontmatter[lineStart:i]
		rewritten := rewriteFrontmatterLine(line)
		out.Write(rewritten)
		if i < len(frontmatter) && frontmatter[i] == '\n' {
			out.WriteByte('\n')
			i++
		}
	}
	return out.Bytes()
}

// rewriteFrontmatterLine returns the rewritten form of a single
// frontmatter line. Only top-level keys (no leading whitespace) are
// candidates for rewriting; indented lines (nested values) pass
// through verbatim.
func rewriteFrontmatterLine(line []byte) []byte {
	// Indented? Pass through.
	if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
		return line
	}
	// Locate the first ':' — anything before it is the key.
	colonIdx := bytes.IndexByte(line, ':')
	if colonIdx <= 0 {
		return line
	}
	key := string(line[:colonIdx])
	// CLI spec §7.4 codex: rename `tools:` → `allowed_tools:`.
	if key == "tools" {
		var out bytes.Buffer
		out.Grow(len(line) + len("allowed_") - len("tools"))
		out.WriteString("allowed_tools")
		out.Write(line[colonIdx:])
		return out.Bytes()
	}
	return line
}

// MergeStrategies returns the per-target merge classification per
// CLI spec §7.1 + ADAPT-05. codex merges .codex/config.toml deep
// (the plugin .mcp.json contributions get layered onto the
// runtime-config one via deep-merge).
func (a *Adapter) MergeStrategies() map[string]adapter.MergeKind {
	return map[string]adapter.MergeKind{
		configTOMLPath: adapter.MergeDeep,
	}
}

// ProjectionRules returns the codex Phase-1 PASSTHROUGH projection table
// satisfying route.RuleProvider (the D-06 seam). It is current-behavior-
// equivalent to the codex TransformPlugin walk: agents/ and skills/ are
// routed into .codex/<kind>/; commands/, hooks/, and rules/ have NO rule
// and therefore fall into route.Project's dropped set (matching codex's
// existing silentDropTopLevel{commands,hooks} plus the rules/ kind codex
// has never had a destination for).
//
// Phase 3 (OPENPACKAGE-MAPPING #7) reconciles codex's routed-kind/drop
// sets — routing commands/ -> .codex/prompts/ and the agents .md->.toml
// conversion. Phase 1 deliberately ships only the passthrough table;
// TransformPlugin (incl. writeAgentWithFrontmatterRewrite and
// silentDropTopLevel) is LEFT AS-IS — projection runs via the plan-02
// Render leg (ProjectionRules -> route.Project), not TransformPlugin.
// This method is pure data — no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "agents/**/*", ToGlob: ".codex/agents/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".codex/skills/**/*", Merge: adapter.MergeReplace},
	}
}

// ResolveOutputContent satisfies the SAFE-04 cascade Tier 2 contract
// from plan 07-W2-03. For target ".codex/config.toml" we recompute the
// bytes RenderRuntime would emit (so the cascade can compare against
// disk bytes without re-running the orchestrator). For any other
// target, we return (nil, nil) — the cascade falls through to Tier 3
// source-byte read, which is the right behavior for non-merged plugin
// files (codex's TransformPlugin already emits the transformed bytes
// verbatim, so the staging dir bytes ARE the canonical bytes for
// non-merged paths).
func (a *Adapter) ResolveOutputContent(ctx context.Context, m *manifest.Manifest, target string) ([]byte, error) {
	if target != configTOMLPath {
		return nil, nil
	}
	if m == nil || m.Runtime == nil {
		return nil, nil
	}
	cred := adapter.CredentialFromContext(ctx)
	content, _, err := renderConfigTOML(m, cred)
	if err != nil {
		return nil, err
	}
	return content, nil
}

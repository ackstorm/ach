// SPDX-License-Identifier: Apache-2.0

// Package gemini is the Google Gemini CLI platform Adapter implementation
// per CONTEXT.md D-07 and CLI spec §7.4 (gemini-cli adapter).
//
// Unlike the claudecode pass-through reference, the gemini-cli adapter
// performs two real transformations:
//
//   - Runtime-config: rendered into .gemini/settings.json as JSON with
//     top-level mcpServers + a2aAgents maps (CLI spec §7.4 + §7.2 row
//     for gemini-cli; MergeDeep classification per §7.1). The on-disk
//     shape mirrors the claudecode .mcp.json layout because Gemini's
//     mcpServers config follows the same MCP server registry format —
//     {type, url, headers} per server. The merge target differs (a
//     different file in a different directory), but the JSON shape on
//     each entry is identical.
//   - Plugin transformation: distributes Claude-format plugin pieces
//     into .gemini/extensions/<plugin-name>/ per the plan's
//     <must_haves.truths> contract. agents/ + prompts/ + commands/ +
//     skills/ are copied verbatim into the per-component subdir.
//     hooks/ is SILENTLY DROPPED per ADAPT-07 + CONTEXT.md D-08 (Gemini
//     has no hook system); the adapter accumulates "hooks" into
//     PluginWrite.Dropped exactly once when at least one hooks/* entry
//     is seen.
//
// ADAPT-06 scope rule: this adapter emits ONLY .gemini/-prefixed paths.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars. Empty
// credentials produce an empty header value (the orchestrator gates
// whether to write at all).
package gemini

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

// Target paths emitted by this adapter. Centralized as constants so the
// test assertions and production code stay in lock-step.
const (
	// settingsJSONPath is the gemini-cli runtime-config target per CLI
	// spec §7.2 row "gemini-cli" + §7.4 "gemini-cli adapter" paragraph.
	settingsJSONPath = ".gemini/settings.json"

	// canonicalID + the alias list match CLI spec §7.2 row 3.
	//
	// TransformPlugin distributes per-plugin pieces under
	// .gemini/extensions/<plugin-name>/ per the plan's
	// <must_haves.truths> contract for ADAPT-04. The .gemini/extensions
	// root is the orchestrator-provided dst arg; the prefix is encoded
	// at the call-site in cmd/ach-cli/cmd/hydrate.go (W3-05) — the
	// adapter itself receives an already-joined absolute path.
	canonicalID = "gemini-cli"

	// extensionManifestName is the per-extension manifest the adapter
	// writes alongside the component subdirs. The plan's <action> notes
	// that when spec §7.4 is silent on the shape, a minimal {name,
	// version, components} object suffices.
	extensionManifestName = "extension.json"
)

// Adapter is the empty struct holding the gemini-cli Adapter impl.
type Adapter struct{}

// init registers the adapter with the package-level registry. The
// cobra autodetection layer (plan 07-W3-05) blank-imports this
// subpackage so the init() fires before main() reaches Lookup/Iter.
func init() {
	adapter.Register(&Adapter{})
}

// ID returns the canonical adapter ID per CLI spec §7.2.
func (a *Adapter) ID() string { return canonicalID }

// Aliases returns the case-folded alternate identifiers the user may
// type at the CLI. Plan 07-W3-03 must_haves: ["gemini"] (spec §7.2
// lists ["gemini", "google-gemini"] but the plan trims to the shorter
// single alias for the parallel-wave contract; the cobra layer will
// surface this verbatim from Aliases()).
func (a *Adapter) Aliases() []string { return []string{"gemini"} }

// Detect scans root for gemini-cli signals. Each present signal boosts
// confidence; 3+ → High, 2 → Medium, 1 → Low. Reasons accumulate
// human-readable evidence the autodetection layer (plan 07-W3-05)
// surfaces on multi-match exit.
//
// Signals checked:
//   - .gemini/ directory at root
//   - .gemini/settings.json file
//   - .gemini/extensions/ directory
//   - $HOME/.gemini/settings.json (global-mode hint per the plan)
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

	check(".gemini", "found .gemini/ directory")
	check(".gemini/settings.json", "found .gemini/settings.json")
	check(".gemini/extensions", "found .gemini/extensions/ directory")

	// Global-mode hint: $HOME/.gemini/settings.json. This is checked
	// independently of `root` because the spec §7.5 algorithm scans
	// $HOME for global mode separately. The presence of a global
	// settings file is a Low-confidence signal that the user has
	// gemini-cli installed somewhere on this machine; we surface it
	// the same way as local signals so the autodetection layer
	// (plan 07-W3-05) can rank candidates consistently.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		full := filepath.Join(home, ".gemini", "settings.json")
		if _, err := os.Stat(full); err == nil {
			signals++
			reasons = append(reasons, "found ~/.gemini/settings.json (global mode)")
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

// settingsShape is the .gemini/settings.json document Gemini reads.
// Encoded with sorted keys (encoding/json's lexicographic map-key sort)
// so the output is deterministic — important for FMT-05 deterministic
// re-hydrate / drift no-op detection.
//
// MCPServers carries the per-server JSON shape Gemini CLI consumes under
// the "mcpServers" key per CLI spec §7.4 gemini-cli paragraph. The shape
// mirrors Claude Code's MCP server registry format — same
// {type, url, headers} surface.
//
// A2AAgents mirrors the MCP server shape under "a2aAgents". Spec §7.4
// gemini-cli does not pin a fixed A2A shape (parity with the claudecode
// reference); we mirror the MCP shape so the JSON round-trip is
// symmetric on both sides.
type settingsShape struct {
	MCPServers map[string]adapter.MCPServerEntry `json:"mcpServers"`
	A2AAgents  map[string]adapter.A2AAgentEntry  `json:"a2aAgents,omitempty"`
}

// renderSettingsJSON builds the .gemini/settings.json bytes from a
// manifest + credential. Returned bytes are deterministic: keys are
// emitted in sorted order. The credential is embedded into each
// server's headers map under "x-ach-key" (matching the on-disk
// plaintext discipline CLI-04 / D-04 establishes for the trust path).
func renderSettingsJSON(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := settingsShape{
		MCPServers: map[string]adapter.MCPServerEntry{},
		A2AAgents:  map[string]adapter.A2AAgentEntry{},
	}

	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		entry := adapter.MCPServerEntry{
			Type:    "http",
			URL:     server.Endpoint,
			Headers: adapter.HeadersWithCredential(credential),
		}
		shape.MCPServers[server.ID] = entry
		contributedKeys = append(contributedKeys, "mcpServers."+server.ID)
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

	// omitempty handles the `nil` map case; explicitly nil out an empty
	// map so the encoded output drops the "a2aAgents" key entirely when
	// there are no agents (matches the claudecode reference shape).
	if len(shape.A2AAgents) == 0 {
		shape.A2AAgents = nil
	}

	sort.Strings(contributedKeys)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(shape); err != nil {
		return nil, nil, fmt.Errorf("gemini: encode settings.json: %w", err)
	}

	return buf.Bytes(), contributedKeys, nil
}

// RenderRuntime emits the single .gemini/settings.json FileWrite per
// CLI spec §7.4 gemini-cli section. Merge=MergeDeep; Keys carries the
// contributed top-level keys for STATE-02 + ADAPT-05 inverse-merge
// during `--sync`.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("gemini: RenderRuntime called with nil manifest or runtime block")
	}

	cred := adapter.CredentialFromContext(ctx)
	content, keys, err := renderSettingsJSON(m, cred)
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

// extensionManifest is the minimal per-extension JSON manifest written
// to .gemini/extensions/<plugin>/extension.json per the plan's <action>
// fallback shape when spec §7.4 is silent on the format.
type extensionManifest struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Components []string `json:"components"`
}

// componentMapping pins which top-level src subdirs map to which
// destination subdirs under .gemini/extensions/<plugin>/. Top-level
// names not in this map are silently dropped per ADAPT-07.
//
// Kept components (verbatim copy):
//   - agents/  → agents/ (Gemini extensions support Claude-format agents per the plan)
//   - prompts/ → prompts/
//   - commands/ → commands/
//   - skills/   → skills/
//
// Dropped components (per ADAPT-07 + CONTEXT.md D-08):
//   - hooks/    — Gemini has no hook system; accumulated into PluginWrite.Dropped exactly once.
//
// .mcp.json is consumed by RenderRuntime (not by TransformPlugin) so it
// is not in the kept-or-dropped mapping; if present at src root it is
// simply ignored here (NOT added to ExtractedFiles).
//
// .claude-plugin/plugin.json is read for the extension manifest's
// version field if present; not copied to dst.
var componentKept = map[string]bool{
	"agents":   true,
	"prompts":  true,
	"commands": true,
	"skills":   true,
}

// componentDropped lists src top-level subdirs that gemini-cli silently
// drops. Each name surfaces in PluginWrite.Dropped exactly once when at
// least one entry under that subdir is encountered.
var componentDropped = map[string]bool{
	"hooks": true,
}

// TransformPlugin walks the src Claude-format plugin tree and writes
// the platform-native pieces under
// .gemini/extensions/<filepath.Base(src)>/. Kept components are copied
// verbatim; dropped components accumulate into PluginWrite.Dropped per
// ADAPT-07.
//
// File mode discipline: every regular file is chmod'd to 0644;
// directories to 0755. Symlinks/devices/FIFOs are silently skipped
// (defense-in-depth against any W2-01 safe-extract regression).
func (a *Adapter) TransformPlugin(_ context.Context, src, dst string) (adapter.PluginWrite, error) {
	if src == "" || dst == "" {
		return adapter.PluginWrite{}, fmt.Errorf("gemini: TransformPlugin requires non-empty src and dst")
	}

	pluginName := filepath.Base(src)
	if pluginName == "." || pluginName == "/" || pluginName == "" {
		return adapter.PluginWrite{}, fmt.Errorf("gemini: TransformPlugin cannot derive plugin name from src=%q", src)
	}

	// Final destination root for this plugin's pieces.
	pluginDst := filepath.Join(dst, pluginName)
	if err := os.MkdirAll(pluginDst, 0o755); err != nil {
		return adapter.PluginWrite{}, fmt.Errorf("gemini: MkdirAll(%q): %w", pluginDst, err)
	}

	extracted := make([]string, 0, 16)
	droppedSet := make(map[string]bool)

	// Read plugin metadata for the extension.json version field. Best-
	// effort: if .claude-plugin/plugin.json is absent or malformed, we
	// emit version="" rather than fail.
	version := readPluginVersion(filepath.Join(src, ".claude-plugin", "plugin.json"))

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("gemini: rel(%q, %q): %w", src, path, err)
		}
		if rel == "." {
			return nil
		}

		// Classify by top-level component (first path element).
		topLevel := adapter.TopLevelComponent(rel)

		// .claude-plugin/ metadata is consumed for version above; not
		// copied to dst.
		if topLevel == ".claude-plugin" {
			return nil
		}

		// .mcp.json at root is consumed by RenderRuntime; not part of
		// per-plugin ExtractedFiles.
		if rel == ".mcp.json" {
			return nil
		}

		// Silent-drop accounting: components in the dropped set never
		// reach dst; their top-level name is recorded once per
		// PluginWrite.
		if componentDropped[topLevel] {
			droppedSet[topLevel] = true
			return nil
		}

		// Unknown top-level components (anything not kept, not dropped,
		// not metadata) are silently dropped per ADAPT-07. We do NOT
		// record them in Dropped to keep the warning surface focused on
		// the documented-but-unsupported components (hooks for gemini).
		if !componentKept[topLevel] {
			return nil
		}

		// Kept component → copy verbatim.
		dstPath := filepath.Join(pluginDst, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		if !d.Type().IsRegular() {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := adapter.CopyFile(path, dstPath); err != nil {
			return err
		}

		// ExtractedFiles paths are recorded relative to dst (the
		// destination root the orchestrator hands us) so the
		// orchestrator's state writer can hash + index each entry
		// without re-deriving the plugin prefix.
		relToDst, err := filepath.Rel(dst, dstPath)
		if err != nil {
			return fmt.Errorf("gemini: rel(%q, %q): %w", dst, dstPath, err)
		}
		extracted = append(extracted, relToDst)
		return nil
	})
	if err != nil {
		return adapter.PluginWrite{}, err
	}

	// Sort kept-component names so the extension manifest is deterministic.
	components := make([]string, 0, len(componentKept))
	for k := range componentKept {
		// Only list components that actually have at least one file under
		// the dst tree — otherwise the manifest claims components the
		// plugin doesn't contribute.
		componentDir := filepath.Join(pluginDst, k)
		if _, err := os.Stat(componentDir); err == nil {
			components = append(components, k)
		}
	}
	sort.Strings(components)

	// Write extension.json manifest. The plan's <action> notes a minimal
	// {name, version, components} JSON object is the right shape when
	// spec §7.4 is silent on the format.
	manifestBytes, err := encodeExtensionManifest(extensionManifest{
		Name:       pluginName,
		Version:    version,
		Components: components,
	})
	if err != nil {
		return adapter.PluginWrite{}, err
	}
	manifestPath := filepath.Join(pluginDst, extensionManifestName)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return adapter.PluginWrite{}, fmt.Errorf("gemini: write extension.json: %w", err)
	}
	relManifest, err := filepath.Rel(dst, manifestPath)
	if err != nil {
		return adapter.PluginWrite{}, fmt.Errorf("gemini: rel(%q, %q): %w", dst, manifestPath, err)
	}
	extracted = append(extracted, relManifest)

	sort.Strings(extracted)

	dropped := make([]string, 0, len(droppedSet))
	for k := range droppedSet {
		dropped = append(dropped, k)
	}
	sort.Strings(dropped)
	if len(dropped) == 0 {
		dropped = nil
	}

	return adapter.PluginWrite{
		ExtractedFiles: extracted,
		Dropped:        dropped,
	}, nil
}

// readPluginVersion parses the version field from a
// .claude-plugin/plugin.json file. Best-effort: returns "" on any
// error (missing file, unreadable, malformed JSON, no version key).
func readPluginVersion(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // path is under our staging dir
	if err != nil {
		return ""
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Version
}

// encodeExtensionManifest renders the extension.json bytes
// deterministically (sorted keys via encoding/json; struct fields
// emit in declaration order).
func encodeExtensionManifest(m extensionManifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("gemini: encode extension.json: %w", err)
	}
	return buf.Bytes(), nil
}

// ProjectionRules returns the gemini-cli Phase-1 PASSTHROUGH projection
// table satisfying route.RuleProvider (the D-06 seam). It is current-
// behavior-equivalent to gemini's TransformPlugin componentKept set:
// agents/, prompts/, commands/, skills/ are routed into .gemini/<kind>/;
// hooks/ has NO rule and falls into route.Project's dropped set (Gemini
// has no hook system — matching componentDropped{hooks}).
//
// gemini-cli has no OpenPackage reference; ACH's existing gemini.go is
// canonical (PROJECT.md key decision). Phase 1 mirrors its current
// routed-kind set as the passthrough table. TransformPlugin is LEFT
// AS-IS — projection runs via the plan-02 Render leg
// (ProjectionRules -> route.Project). This method is pure data — no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "agents/**/*", ToGlob: ".gemini/agents/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "prompts/**/*", ToGlob: ".gemini/prompts/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "commands/**/*", ToGlob: ".gemini/commands/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".gemini/skills/**/*", Merge: adapter.MergeReplace},
	}
}

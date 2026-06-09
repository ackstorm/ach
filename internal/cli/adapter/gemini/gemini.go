// SPDX-License-Identifier: Apache-2.0

// Package gemini is the Google Gemini CLI platform Adapter implementation
// per CONTEXT.md D-07 and CLI spec §7.4 (gemini-cli adapter).
//
// The gemini-cli adapter handles:
//
//   - Runtime-config: rendered into .gemini/settings.json as JSON with
//     top-level mcpServers + a2aAgents maps (CLI spec §7.4 + §7.2 row
//     for gemini-cli; MergeDeep classification per §7.1). The on-disk
//     shape mirrors the claudecode .mcp.json layout because Gemini's
//     mcpServers config follows the same MCP server registry format —
//     {type, url, headers} per server. The merge target differs (a
//     different file in a different directory), but the JSON shape on
//     each entry is identical.
//   - Plugin projection: routes Claude-format plugin components into the
//     .gemini/ layout via route.Project + ProjectionRules. hooks/ has no
//     rule and falls into route.Project's dropped set (Gemini has no hook
//     system).
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
	"strings"

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
	canonicalID = "gemini-cli"
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
// geminiMCPEntry is the per-server JSON shape Gemini CLI consumes for a
// streamable-HTTP MCP server under the "mcpServers" key. Unlike Claude
// Code, gemini-cli uses "httpUrl" (a plain "url" means SSE, not HTTP) and
// has NO "type" field. The earlier {type:"http", url} shape (copied from
// claudecode) produced non-loadable config. Schema ref:
// https://github.com/google-gemini/gemini-cli (settings.json / MCP).
type geminiMCPEntry struct {
	HTTPURL string            `json:"httpUrl"`
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPServers carries the per-server JSON shape Gemini CLI consumes under
// the "mcpServers" key per CLI spec §7.4 gemini-cli paragraph
// (geminiMCPEntry: httpUrl + headers, no type).
//
// A2AAgents mirrors the MCP server shape under "a2aAgents". Spec §7.4
// gemini-cli does not pin a fixed A2A shape (parity with the claudecode
// reference); we mirror the MCP shape so the JSON round-trip is
// symmetric on both sides. A2A projection across non-claude tools is a
// separate open design question (see hydrate bug-sweep plan).
type settingsShape struct {
	MCPServers map[string]geminiMCPEntry        `json:"mcpServers"`
	A2AAgents  map[string]adapter.A2AAgentEntry `json:"a2aAgents,omitempty"`
}

// renderSettingsJSON builds the .gemini/settings.json bytes from a
// manifest + credential. Returned bytes are deterministic: keys are
// emitted in sorted order. The credential is embedded into each
// server's headers map under "x-ach-key" (matching the on-disk
// plaintext discipline CLI-04 / D-04 establishes for the trust path).
func renderSettingsJSON(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := settingsShape{
		MCPServers: map[string]geminiMCPEntry{},
		A2AAgents:  map[string]adapter.A2AAgentEntry{},
	}

	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		entry := geminiMCPEntry{
			HTTPURL: server.Endpoint,
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

// mcpDeepKeys is the gemini-cli adapter's only non-nil route.Rule.Transform
// (D-09/D-12). It is wired onto the `mcp/**/*` ProjectionRules row and serves a
// single purpose: enumerate the top-level MCP keys a plugin's mcp.{json,jsonc}
// contributes so the deep-merge engine can record them in state.files[*].keys[]
// (STATE-02 + ADAPT-05) and so the runtime-wins drop (plan 02, D-10) can drop
// ids that clash with the runtime MCP set.
//
// This is the SAME func/shape as claude-code's mcpDeepKeys (PATTERNS §gemini.go);
// the two copies are intentionally short and identical — each adapter keeps its
// own to avoid an unaccounted shared package. The only difference between the
// adapters is the ToGlob target wired on the ProjectionRules row, not this
// enumeration logic.
//
// Byte discipline (D-03): mcpDeepKeys returns `in` UNCHANGED — it parses ONLY to
// read the top-level map keys, never re-encodes. Re-encoding would reorder the
// user's plugin file and break FMT-05 byte-stability / drift no-op detection.
//
// Key shape (D-09): for each top-level `mcpServers` map key id →
// "mcpServers."+id; if a top-level `a2aAgents` object is present, for each of
// its keys id → "a2aAgents."+id. Keys are sorted lexicographically, mirroring
// renderSettingsJSON's contributedKeys loop, so the enumeration is deterministic.
//
// Malformed JSON returns a non-nil error so route.Project aborts that file
// (first-error discipline, T-02-09) rather than letting a server slip into the
// merge unenumerated. An input with no mcpServers object returns empty keys and
// out==in with no error.
func mcpDeepKeys(srcRel string, in []byte) (out []byte, keys []string, err error) {
	// Decode only the top-level object so we read the contributed key names
	// without materializing or re-encoding the nested server definitions.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(in, &top); err != nil {
		return nil, nil, fmt.Errorf("gemini: mcpDeepKeys parse %q: %w", srcRel, err)
	}

	keys = make([]string, 0)

	if raw, ok := top["mcpServers"]; ok {
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("gemini: mcpDeepKeys parse %q mcpServers: %w", srcRel, err)
		}
		for id := range servers {
			keys = append(keys, "mcpServers."+id)
		}
	}

	if raw, ok := top["a2aAgents"]; ok {
		var agents map[string]json.RawMessage
		if err := json.Unmarshal(raw, &agents); err != nil {
			return nil, nil, fmt.Errorf("gemini: mcpDeepKeys parse %q a2aAgents: %w", srcRel, err)
		}
		for id := range agents {
			keys = append(keys, "a2aAgents."+id)
		}
	}

	// Sort exactly like renderSettingsJSON's contributedKeys so the enumeration
	// is stable across invocations.
	sort.Strings(keys)

	// D-03: return the input bytes UNCHANGED — no re-encode, no reorder.
	return in, keys, nil
}

// geminiCommandTOML converts a Claude-format command (`commands/<name>.md`:
// YAML frontmatter + a markdown prompt body) into gemini-cli's native custom-
// command TOML (`.gemini/commands/<name>.toml`). gemini-cli reads custom
// commands ONLY as TOML with a required `prompt` key and an optional
// `description` (https://geminicli.com/docs/cli/custom-commands/); a verbatim
// `.md` copy is dead on arrival — hence this transform (the gemini analogue of
// codex's markdown→TOML agent lift).
//
// Mapping:
//   - frontmatter `description` (if present) → TOML `description`.
//   - body → TOML `prompt`, with Claude's `$ARGUMENTS` placeholder rewritten to
//     gemini's `{{args}}`. Claude positional placeholders ($1/$2) have no gemini
//     equivalent and are left verbatim (documented limitation).
//
// The frontmatter is line-scanned for `description:` rather than YAML-parsed:
// Claude command frontmatter is NOT reliably valid YAML (`argument-hint: [a]
// [b]` fails a strict parser), so a full Unmarshal would lose a perfectly good
// description. All other Claude command keys (argument-hint, allowed-tools,
// name, …) have no gemini equivalent and are dropped. Output is deterministic
// TOML via route.CanonicalTOML (D-24 byte-stability). keys is nil (MergeReplace,
// file-owned). A command with no frontmatter still emits `prompt` from the whole
// body.
func geminiCommandTOML(srcRel string, in []byte) (out []byte, keys []string, err error) {
	fm, body, _ := route.SplitFrontmatter(in)

	doc := map[string]any{}

	// Line-scan the frontmatter for a top-level `description:` scalar. Robust to
	// the invalid-YAML lines Claude commands carry (argument-hint: [type] …).
	for _, line := range strings.Split(string(fm), "\n") {
		if !strings.HasPrefix(line, "description:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		v = strings.Trim(v, `"'`)
		if v != "" {
			doc["description"] = v
		}
		break
	}

	// prompt = body, with Claude's $ARGUMENTS → gemini's {{args}}.
	prompt := strings.ReplaceAll(string(body), "$ARGUMENTS", "{{args}}")
	doc["prompt"] = strings.Trim(prompt, "\n")

	out, err = route.CanonicalTOML(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("gemini: geminiCommandTOML encode %q: %w", srcRel, err)
	}
	return out, nil, nil
}

// ProjectionRules returns the gemini-cli projection table satisfying
// route.RuleProvider (the D-06 seam), extended per D-12. The verbatim
// file-owned resource kinds (agents/, prompts/, skills/) route into
// .gemini/<kind>/ as MergeReplace. commands/ is NOT verbatim: gemini-cli reads
// custom commands as TOML, so commands/**/*.md is converted to
// .gemini/commands/**/*.toml via geminiCommandTOML (gemini-native commands/*.toml
// pass through).
//
// Two non-file rows complete the D-12 contract:
//   - AGENTS.md → GEMINI.md as MergeComposite: the plugin's top-level AGENTS.md
//     prose is composited (marker-bounded) into the host GEMINI.md memory file.
//   - mcp/**/* → settingsJSONPath as MergeDeep with Transform=mcpDeepKeys: the
//     plugin's MCP definitions deep-merge under mcpServers in the EXISTING
//     .gemini/settings.json (D-08 — the same RenderRuntime MCP target; there is
//     NO .mcp.json filename switch and NO OpenPackage-style key rename). The
//     Transform enumerates the contributed keys without altering the bytes.
//
// hooks/ has NO rule and falls into route.Project's dropped set (D-12 "drop
// hooks": Gemini has no hook system). gemini-cli has no OpenPackage reference;
// ACH's existing gemini.go is canonical (PROJECT.md key decision) — this
// extends it, it does NOT retrofit foreign rules. Projection runs via the
// plan-02 Render leg (ProjectionRules -> route.Project). This method is pure
// data — no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "agents/**/*", ToGlob: ".gemini/agents/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "prompts/**/*", ToGlob: ".gemini/prompts/**/*", Merge: adapter.MergeReplace},
		// gemini-cli custom commands are TOML, not markdown. A Claude-format
		// commands/<n>.md is converted to .gemini/commands/<n>.toml via
		// geminiCommandTOML; a plugin that already ships gemini-native
		// commands/<n>.toml passes through verbatim. (Mirrors the codex .md→.toml
		// agent remap.)
		{FromGlob: "commands/**/*.md", ToGlob: ".gemini/commands/**/*.toml", Merge: adapter.MergeReplace, Transform: geminiCommandTOML},
		{FromGlob: "commands/**/*.toml", ToGlob: ".gemini/commands/**/*.toml", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".gemini/skills/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "AGENTS.md", ToGlob: "GEMINI.md", Merge: adapter.MergeComposite},
		{FromGlob: "mcp/**/*", ToGlob: settingsJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
		// Root .mcp.json is Claude Code's standard plugin MCP location — deep-merge
		// it into .gemini/settings.json too.
		{FromGlob: ".mcp.json", ToGlob: settingsJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
	}
}

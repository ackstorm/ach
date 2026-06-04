// SPDX-License-Identifier: Apache-2.0

// Package pimono is the Pi (pi-mono) platform Adapter implementation — the
// optional 5th target (CONTEXT.md Phase 5, D-33). It is the thinnest adapter:
// a 3-row ProjectionRules table plus a gemini-shaped RenderRuntime pointed at
// .pi/mcp.json. There is NO format conversion — plugin commands/skills are
// projected verbatim (MergeReplace) into Pi's native .pi/agent/ layout, and
// MCP definitions deep-merge into .pi/mcp.json (MergeDeep).
//
// D-33 (this phase) makes RenderRuntime NON-EMPTY (it emits Environment runtime
// MCP servers into .pi/mcp.json, mirroring claude/gemini) and removes `mcp`
// from the Dropped set — pimono now supports MCP. The other top-level kinds
// without a rule (rules/, agents/, AGENTS.md) fall to route.Project's
// accumulate-once drop set, yielding Dropped = {rules, agents, AGENTS.md}.
//
// Own-prefix rule (ADAPT-06 analog): this adapter emits ONLY .pi/-prefixed
// paths. Pi has four candidate MCP config locations; only the .pi/mcp.json
// project-override target is in scope here.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
package pimono

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

const (
	// canonicalID is the primary registry key per the Phase 5 plan.
	canonicalID = "pimono"

	// mcpJSONPath is the Pi project-override MCP config target (D-33). This
	// is the ONLY .pi/-prefixed MCP path pimono emits — the other three Pi
	// MCP config locations are out of scope per the own-prefix rule.
	mcpJSONPath = ".pi/mcp.json"
)

// Adapter is the empty struct holding the pimono Adapter impl.
type Adapter struct{}

// init registers the adapter with the package-level registry; the cobra
// layer blank-imports this subpackage so init() fires before Lookup/Iter.
func init() {
	adapter.Register(&Adapter{})
}

// ID returns the canonical adapter ID.
func (a *Adapter) ID() string { return canonicalID }

// Aliases returns the case-folded alternate identifiers the user may type.
func (a *Adapter) Aliases() []string { return []string{"pi", "pi-mono"} }

// Detect scans root for Pi signals. Each present signal boosts confidence;
// 3+ → High, 2 → Medium, 1 → Low. signals==0 → empty Match (no-match).
//
// Signals checked:
//   - .pi/ directory at root
//   - .pi/agent/ directory
//   - .pi/mcp.json file
//   - $HOME/.pi/mcp.json (global-mode hint, checked independently of root)
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

	check(".pi", "found .pi/ directory")
	check(".pi/agent", "found .pi/agent/ directory")
	check(".pi/mcp.json", "found .pi/mcp.json")

	// Global-mode hint: $HOME/.pi/mcp.json. Checked independently of `root`
	// (mirrors gemini's $HOME/.gemini/settings.json global-settings probe).
	// A specific file — NOT the bare $HOME/.pi directory (WR-03): a populated
	// global config is a far stronger signal than the mere existence of a
	// scratch ~/.pi dir, so an unrelated cwd no longer trips a spurious
	// global-only match that could turn a clean single-match into a multi-
	// match (exit 1) for a totally different project.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		full := filepath.Join(home, ".pi", "mcp.json")
		if _, err := os.Stat(full); err == nil {
			signals++
			reasons = append(reasons, "found ~/.pi/mcp.json (global mode)")
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

// pimonoMCPEntry is the per-server JSON shape Pi consumes under the
// `mcpServers` key for an HTTP MCP server. Pi's schema is `url` (presence
// ⇒ StreamableHTTP w/ SSE fallback) + `headers`, with NO `type` field (Pi
// defines none for HTTP servers; stdio uses command/args/env/cwd). The
// earlier shared {type:"http", url, headers} shape emitted a stray `type`
// key Pi's schema does not define. Schema ref:
// https://github.com/nicobailon/pi-mcp-adapter. ACH's x-ach-key is a
// custom STATIC header → plain `headers` (not Pi's auth/bearerToken/oauth,
// which are for Bearer/OAuth flows ACH does not use here).
type pimonoMCPEntry struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// mcpShape is the .pi/mcp.json document Pi reads. Encoded with sorted keys
// (encoding/json's lexicographic map-key sort) so the output is deterministic
// — important for FMT-05 deterministic re-hydrate / drift no-op detection.
//
// Only top-level mcpServers is emitted — pimono has NO a2aAgents surface.
type mcpShape struct {
	MCPServers map[string]pimonoMCPEntry `json:"mcpServers"`
}

// renderMcpJSON builds the .pi/mcp.json bytes from a manifest + credential.
// Returned bytes are deterministic: keys are emitted in sorted order. The
// credential is embedded into each server's headers under "x-ach-key"
// (matching the on-disk plaintext discipline CLI-04 / D-04).
//
// Empty-state handling: when there are no runtime MCP servers, the encoded
// shape is a stable {"mcpServers":{}} so re-render is byte-identical and the
// merge target is re-hydrate idempotent.
func renderMcpJSON(m *manifest.Manifest, credential string) ([]byte, []string, error) {
	shape := mcpShape{
		MCPServers: map[string]pimonoMCPEntry{},
	}

	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers))

	for _, server := range m.Runtime.MCPServers {
		shape.MCPServers[server.ID] = pimonoMCPEntry{
			URL:     server.Endpoint,
			Headers: adapter.HeadersWithCredential(credential),
		}
		contributedKeys = append(contributedKeys, "mcpServers."+server.ID)
	}

	sort.Strings(contributedKeys)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(shape); err != nil {
		return nil, nil, fmt.Errorf("pimono: encode mcp.json: %w", err)
	}

	return buf.Bytes(), contributedKeys, nil
}

// RenderRuntime emits the single .pi/mcp.json FileWrite (D-33). Merge=MergeDeep;
// Keys carries the contributed top-level keys for STATE-02 + ADAPT-05
// inverse-merge during `--sync`.
//
// ADAPT-03 credential propagation: the bearer is sourced from
// adapter.CredentialFromContext(ctx) — never from env vars.
func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("pimono: RenderRuntime called with nil manifest or runtime block")
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

// mcpDeepKeys is pimono's only non-nil route.Rule.Transform, wired onto the
// mcp/**/* ProjectionRules row. It enumerates the top-level mcpServers map keys
// a plugin's mcp file contributes so the deep-merge engine can record them in
// state.files[*].keys[] (STATE-02 + ADAPT-05) and the runtime-wins drop (D-10)
// can drop ids that clash with the runtime MCP set.
//
// Unlike gemini, pimono enumerates ONLY mcpServers — it has no a2aAgents
// surface, so an a2aAgents branch in the input is intentionally ignored.
//
// Byte discipline (D-03): returns `in` UNCHANGED — parses ONLY to read the
// top-level map keys, never re-encodes. Malformed JSON returns a non-nil error
// so route.Project aborts that file (first-error discipline, T-02-09).
func mcpDeepKeys(srcRel string, in []byte) (out []byte, keys []string, err error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(in, &top); err != nil {
		return nil, nil, fmt.Errorf("pimono: mcpDeepKeys parse %q: %w", srcRel, err)
	}

	keys = make([]string, 0)

	if raw, ok := top["mcpServers"]; ok {
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("pimono: mcpDeepKeys parse %q mcpServers: %w", srcRel, err)
		}
		for id := range servers {
			keys = append(keys, "mcpServers."+id)
		}
	}

	sort.Strings(keys)

	// D-03: return the input bytes UNCHANGED — no re-encode, no reorder.
	return in, keys, nil
}

// ProjectionRules returns the pimono projection table (route.RuleProvider, the
// D-06 seam). Exactly 3 rows:
//
//   - commands/**/*.md → .pi/agent/prompts/**/* (MergeReplace verbatim)
//   - skills/**/*      → .pi/agent/skills/**/*  (MergeReplace verbatim)
//   - mcp/**/*         → .pi/mcp.json (MergeDeep, Transform=mcpDeepKeys; D-33)
//
// The OpenPackage catch-all (root/**/*) is OMITTED (D-36). No rules/agents/
// AGENTS.md row — those top-level kinds fall to route.Project's accumulate-once
// drop set (D-35), yielding Dropped = {rules, agents, AGENTS.md}; mcp is NOT
// dropped because it has the mcp/**/* rule (D-33). This method is pure data —
// no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".pi/agent/prompts/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".pi/agent/skills/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "mcp/**/*", ToGlob: mcpJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
	}
}

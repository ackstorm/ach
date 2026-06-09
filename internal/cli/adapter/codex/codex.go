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
//     and [a2a_agents.<id>] tables (CLI spec §7.4 codex). An mcp_servers
//     table carries url + http_headers["x-ach-key"] (codex infers HTTP
//     from url presence — there is NO transport key). MergeDeep
//     classification; contributed top-level keys recorded under
//     "mcp_servers.<id>" / "a2a_agents.<id>" so STATE-05 inverse-merge
//     can target the right TOML subtable. (a2a_agents tables still carry
//     the legacy url+headers+transport shape pending the separate a2a
//     design decision.)
//
//   - Plugin transformation: src follows the Claude Code plugin layout
//     (.claude-plugin/plugin.json + agents/ + commands/ + prompts/ +
//     skills/ + hooks/ + .mcp.json). Codex destinations preserve the
//     Claude layout under dst (the orchestrator picks the per-plugin
//     dst, typically .codex/plugins/<plugin-name>/) but apply two
//     transformations:
//
//     1. ADAPT-07 silent-drop: src/commands/ and src/hooks/ are NOT
//     copied; route.Project accumulates them in its drop set, and
//     the orchestrator emits a single stderr warning at end of
//     hydration listing every dropped component across every plugin;
//     exit code is unchanged.
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
//     orchestrator + a future Hub-to-runtime bridge). It is therefore
//     NOT copied to dst and NOT accumulated in Dropped (it IS consumed,
//     just at a different layer).
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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

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
// [mcp_servers.<id>]. Codex infers an HTTP server from the PRESENCE of
// `url` (no `transport` key exists) and reads static headers from
// `http_headers` (a generic `headers` table is ignored). The earlier
// {url, headers, transport:"http"} shape meant the x-ach-key auth header
// was never sent. Schema:
// https://developers.openai.com/codex/config-reference. This mirrors the
// already-correct plugin surgery path (codexMCPSurgery).
type mcpServerTable struct {
	URL         string            `toml:"url"`
	HTTPHeaders map[string]string `toml:"http_headers,omitempty"`
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
// with the same input. FMT-05 deterministic re-hydrate / drift no-op
// detection depends on this.
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
			URL:         server.Endpoint,
			HTTPHeaders: adapter.HeadersWithCredential(credential, m.Environment),
		}
		contributedKeys = append(contributedKeys, "mcp_servers."+server.ID)
	}

	for _, agent := range m.Runtime.A2AAgents {
		shape.A2AAgents[agent.ID] = a2aAgentTable{
			URL:       agent.Endpoint,
			Headers:   adapter.HeadersWithCredential(credential, m.Environment),
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

// ProjectionRules returns the codex ROUTE-03 projection table satisfying
// route.RuleProvider (the D-06 seam). It is the real Phase-3
// format-converting table (D-13/D-14), mirroring the claudecode.go /
// gemini.go rule-list-literal shape (Transform only on the converting
// rows):
//
//   - commands/**/*.md → .codex/prompts/**/*.md (MergeReplace): codex calls
//     them "prompts" (D-13). Dropped in the Phase-1 stub; now routed.
//   - skills/**/*       → .agents/skills/**/* (MergeReplace): codex skills
//     live under .agents/, NOT .codex/skills/ (the stub bug).
//   - agents/**/*.md    → .codex/agents/**/*.toml (MergeReplace) via
//     codexAgentTOML: markdown→TOML field-lift (FMT-01, D-15), exercising
//     the Wave-1 .md→.toml extension remap.
//   - mcp/**/*          → .codex/config.toml (MergeDeep) via codexMCPSurgery:
//     N→1 concrete ToGlob collapse + MCP header surgery (FMT-02, D-16).
//
// The drop set {rules, AGENTS.md, hooks} arises naturally in route.Project:
// those kinds have no matching Rule, so each is recorded once via
// dropped.add. This method is pure data — no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".codex/prompts/**/*.md", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".agents/skills/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "agents/**/*.md", ToGlob: ".codex/agents/**/*.toml", Merge: adapter.MergeReplace, Transform: codexAgentTOML},
		{FromGlob: "mcp/**/*", ToGlob: configTOMLPath, Merge: adapter.MergeDeep, Transform: codexMCPSurgery},
		// Root .mcp.json is Claude Code's standard plugin MCP location — merge it
		// into .codex/config.toml too (same header surgery as mcp/**/*).
		{FromGlob: ".mcp.json", ToGlob: configTOMLPath, Merge: adapter.MergeDeep, Transform: codexMCPSurgery},
	}
}

// codexAgentWhitelist is the exact set of agent-frontmatter keys D-15 lifts
// to the top level of the emitted .codex/agents/<name>.toml. Every OTHER
// frontmatter key (`tools`, `permissions`, `hooks`, `skills`,
// `disallowedTools`, `mcp_servers`, …) is dropped (the OpenPackage
// `$unset frontmatter` posture), so a malicious plugin cannot inject an
// unexpected codex config key via agent frontmatter (T-03-06).
//
// `mcp_servers` is deliberately NOT whitelisted (WR-01): it is a sensitive
// codex key that registers MCP server endpoints the agent will call. Allowing
// agent frontmatter to carry it would let a plugin author inject arbitrary,
// un-vetted MCP server registrations — exactly the injection class the
// whitelist exists to close. MCP servers reach codex only through the vetted
// manifest/runtime path (codexMCPSurgery + the runtime renderConfigTOML).
var codexAgentWhitelist = []string{
	"name",
	"description",
	"model",
	"model_reasoning_effort",
	"sandbox_mode",
}

// codexAgentTOML is the FMT-01 / D-15 agent markdown→TOML field-lift
// Transform (the locked D-03 seam signature, route.go:80). It re-encodes
// the source bytes, so the projected file's Hash != SourceHash (the D-23
// raison d'être).
//
// Pipeline (mirrors OPENPACKAGE-MAPPING §2a markdown→json→field-lift→toml):
//
//   - Split frontmatter+body via the Wave-1 route.SplitFrontmatter. A file
//     with no frontmatter fence is treated as an empty-frontmatter doc with
//     the whole input as body.
//   - body → developer_instructions.
//   - Whitelist-rename ONLY the six codexAgentWhitelist keys to top level;
//     drop every other frontmatter key.
//   - name defaults to filepath.Base(srcRel) sans .md; a frontmatter name
//     overrides the default. A $rename on a missing source key is a no-op
//     (the field is simply omitted).
//   - Emit deterministic TOML via route.CanonicalTOML (D-24 byte-stability).
//
// keys return == nil: this is a MergeReplace, file-owned destination with no
// dotted deep-merge keys. All output goes through the BurntSushi/toml
// encoder (NO string concatenation), so frontmatter values carrying TOML
// metacharacters are escaped/quoted and cannot break out of their field
// (T-03-05).
func codexAgentTOML(srcRel string, in []byte) (out []byte, keys []string, err error) {
	fmBytes, body, found := route.SplitFrontmatter(in)

	fm := map[string]any{}
	if found && len(bytes.TrimSpace(fmBytes)) > 0 {
		if uerr := yaml.Unmarshal(fmBytes, &fm); uerr != nil {
			return nil, nil, fmt.Errorf("codex: codexAgentTOML parse frontmatter %q: %w", srcRel, uerr)
		}
		if fm == nil {
			fm = map[string]any{}
		}
	}

	doc := map[string]any{}

	// Whitelist-lift the six keys; everything else in fm is dropped.
	for _, k := range codexAgentWhitelist {
		if v, ok := fm[k]; ok && v != nil {
			doc[k] = v
		}
	}

	// name default: filename sans .md, overridden by a frontmatter name.
	if _, ok := doc["name"]; !ok {
		base := filepath.Base(filepath.ToSlash(srcRel))
		doc["name"] = strings.TrimSuffix(base, filepath.Ext(base))
	}

	// body → developer_instructions (always present; empty body → empty string).
	doc["developer_instructions"] = string(body)

	out, err = route.CanonicalTOML(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("codex: codexAgentTOML encode %q: %w", srcRel, err)
	}
	return out, nil, nil
}

// bearerEnvRE matches an Authorization header of the exact form
// "Bearer ${env:VARNAME}"; group 1 captures the env-var NAME. Only the NAME
// is materialized — the secret VALUE is never read (T-03-04).
var bearerEnvRE = regexp.MustCompile(`^Bearer \$\{env:([A-Z_][A-Z0-9_]*)\}$`)

// envRefRE matches a bare "${env:VARNAME}" header value; group 1 captures the
// env-var NAME.
var envRefRE = regexp.MustCompile(`^\$\{env:([A-Z_][A-Z0-9_]*)\}$`)

// codexMCPSurgery is the FMT-02 / D-16 MCP header-surgery Transform — NET-NEW
// (codex's runtime mcpServerTable/renderConfigTOML path emits a DIFFERENT
// shape, {url, headers{x-ach-key}, transport}, and stays byte-unchanged per
// D-17). It re-encodes, so Hash != SourceHash (D-23).
//
// Pipeline (per OPENPACKAGE-MAPPING §2b):
//
//   - Parse the plugin mcp.json (top key mcpServers); rename top key
//     mcpServers → mcp_servers.
//   - Per server: headers.Authorization "Bearer ${env:X}" →
//     bearer_token_env_var = "X" (NAME only). A bare "${env:X}" Authorization
//     becomes an env_http_header. Any OTHER Authorization (a hardcoded literal
//     token) is DROPPED, never copied as plaintext into the config (CR-02 /
//     T-03-04 — the secret VALUE is never read).
//   - Partition remaining headers by value: "${env:Y}" → env_http_headers
//     (extract var NAME); literal → http_headers.
//   - timeout → startup_timeout_sec. Drop the original headers map.
//   - Emit deterministic TOML via route.CanonicalTOML (sorted ids, sorted
//     keys — BurntSushi sorts map keys lexicographically).
//
// keys return == ["mcp_servers.<id>", …] (sorted) matching the runtime
// encoder's contributedKeys prefix (renderConfigTOML, "mcp_servers."+id) so
// the Wave-1 generalized dropRuntimeOwnedMCP (D-10/D-17) dedups projected
// against runtime. The runtime mcpServerTable / renderConfigTOML path is NOT
// touched.
func codexMCPSurgery(srcRel string, in []byte) (out []byte, keys []string, err error) {
	var top map[string]json.RawMessage
	if uerr := json.Unmarshal(in, &top); uerr != nil {
		return nil, nil, fmt.Errorf("codex: codexMCPSurgery parse %q: %w", srcRel, uerr)
	}

	servers := map[string]json.RawMessage{}
	if raw, ok := top["mcpServers"]; ok {
		if uerr := json.Unmarshal(raw, &servers); uerr != nil {
			return nil, nil, fmt.Errorf("codex: codexMCPSurgery parse %q mcpServers: %w", srcRel, uerr)
		}
	}

	mcpServers := map[string]any{}
	keys = make([]string, 0, len(servers))

	for id, rawSrv := range servers {
		var srv map[string]any
		if uerr := json.Unmarshal(rawSrv, &srv); uerr != nil {
			return nil, nil, fmt.Errorf("codex: codexMCPSurgery parse %q server %q: %w", srcRel, id, uerr)
		}
		mcpServers[id] = surgeryServer(srv)
		keys = append(keys, "mcp_servers."+id)
	}

	sort.Strings(keys)

	doc := map[string]any{"mcp_servers": mcpServers}
	out, err = route.CanonicalTOML(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("codex: codexMCPSurgery encode %q: %w", srcRel, err)
	}
	return out, keys, nil
}

// surgeryServer applies the per-server FMT-02 header surgery to a single
// parsed mcp.json server object and returns the projected codex server table.
// The original `headers` map is consumed (dropped); `timeout` is renamed.
// Any other server key (url, transport, …) passes through verbatim. A header
// whose value is not a string (malformed but valid JSON) is dropped rather
// than coerced and emitted unvalidated (WR-05).
func surgeryServer(srv map[string]any) map[string]any {
	out := map[string]any{}

	var headers map[string]any
	for k, v := range srv {
		switch k {
		case "headers":
			if hm, ok := v.(map[string]any); ok {
				headers = hm
			}
		case "timeout":
			out["startup_timeout_sec"] = v
		default:
			out[k] = v
		}
	}

	if len(headers) == 0 {
		return out
	}

	envHeaders := map[string]any{}
	litHeaders := map[string]any{}

	for hk, hv := range headers {
		val, ok := hv.(string)
		if !ok {
			// A non-string header value (number, bool, object — malformed but
			// valid JSON) is dropped rather than coerced to "" and partitioned
			// as an unvalidated literal that the TOML encoder would emit
			// verbatim (WR-05). MCP header values are always strings; a
			// non-string is a malformed plugin shape with no safe projection.
			continue
		}
		if hk == "Authorization" {
			if m := bearerEnvRE.FindStringSubmatch(val); m != nil {
				out["bearer_token_env_var"] = m[1]
				continue
			}
			// A non-"Bearer ${env:NAME}" Authorization that is also not a bare
			// "${env:NAME}" reference is a hardcoded literal credential. DROP
			// it rather than copy the plaintext token into .codex/config.toml:
			// materializing a literal secret would violate the documented
			// T-03-04 invariant ("the secret VALUE is never read", line 776-777
			// + codex_test.go "raw secret value must never appear"). Only the
			// "${env:NAME}" form below is preserved (NAME only, value never
			// read) (CR-02).
			if envRefRE.FindStringSubmatch(val) == nil {
				continue
			}
		}
		if m := envRefRE.FindStringSubmatch(val); m != nil {
			envHeaders[hk] = m[1]
			continue
		}
		litHeaders[hk] = hv
	}

	if len(envHeaders) > 0 {
		out["env_http_headers"] = envHeaders
	}
	if len(litHeaders) > 0 {
		out["http_headers"] = litHeaders
	}
	return out
}

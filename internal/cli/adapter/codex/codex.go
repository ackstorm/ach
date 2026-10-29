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
	"fmt"
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
	return adapter.DetectFromSignals(canonicalID, []adapter.Signal{
		{Path: filepath.Join(root, ".codex"), Reason: "found .codex/ directory"},
		{Path: filepath.Join(root, ".codex", "config.toml"), Reason: "found .codex/config.toml"},
		{Path: filepath.Join(root, ".codex", "agents"), Reason: "found .codex/agents/ directory"},
	}), nil
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
	Headers   map[string]string `toml:"headers,omitempty"`
	Transport string            `toml:"transport"`
}

// configTOMLShape is the .codex/config.toml document Codex reads. The
// top-level keys are emitted by BurntSushi/toml in source-declaration
// order — but our values are maps, and TOML map encoding sorts keys
// lexicographically, so the output is deterministic across invocations
// with the same input. FMT-05 deterministic re-hydrate / drift no-op
// detection depends on this.
type configTOMLShape struct {
	ModelProvider  string                        `toml:"model_provider,omitempty"`
	ModelProviders map[string]modelProviderTable `toml:"model_providers,omitempty"`
	MCPServers     map[string]mcpServerTable     `toml:"mcp_servers,omitempty"`
	A2AAgents      map[string]a2aAgentTable      `toml:"a2a_agents,omitempty"`
}

// modelProviderTable is `[model_providers.<id>]` — the Hub as a custom model
// provider whose credential comes from a command (`[auth] command`), written
// ONLY on a person hydrate (OAuth / pk_). Codex re-runs the command every
// refresh_interval_ms; 5 min sits under the token's 1 h life.
type modelProviderTable struct {
	Name    string            `toml:"name"`
	BaseURL string            `toml:"base_url"`
	WireAPI string            `toml:"wire_api"`
	Auth    modelProviderAuth `toml:"auth"`
}

type modelProviderAuth struct {
	Command           string `toml:"command"`
	RefreshIntervalMS int    `toml:"refresh_interval_ms"`
}

// modelProviderID is the `[model_providers.<id>]` key and `model_provider` value.
const modelProviderID = "ach"

// renderConfigTOML builds the .codex/config.toml bytes from a manifest
// + credential. Returned bytes are deterministic.
//
// Each MCP server / A2A agent contributes a [mcp_servers.<id>] /
// [a2a_agents.<id>] table with the credential under headers["x-ach-key"]
// per the on-disk plaintext discipline CLI-04 (D-04) establishes.
func renderConfigTOML(m *manifest.Manifest, credential, helperBaseURL string) ([]byte, []string, error) {
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

	if helperBaseURL != "" {
		shape.ModelProvider = modelProviderID
		shape.ModelProviders = map[string]modelProviderTable{modelProviderID: {
			Name:    "ACH",
			BaseURL: strings.TrimRight(helperBaseURL, "/") + "/v1",
			WireAPI: "responses",
			Auth:    modelProviderAuth{Command: adapter.HelperCommand, RefreshIntervalMS: 300000},
		}}
		contributedKeys = append(contributedKeys, "model_provider", "model_providers."+modelProviderID)
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
	content, keys, err := renderConfigTOML(m, cred, adapter.HelperBaseURLFromContext(ctx))
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
//     codexAgentTOML (transform.go).
//   - mcp/**/*          → .codex/config.toml (MergeDeep) via codexMCPSurgery
//     (transform.go), N→1 onto the runtime config file.
//
// The drop set {rules, AGENTS.md, hooks} arises naturally in route.Project:
// those kinds have no matching Rule, so each is recorded once via
// dropped.add. This method is pure data — no I/O.
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".codex/prompts/**/*.md", Merge: adapter.MergeReplace, Prefix: route.PrefixCommand, Sep: "-"},
		{FromGlob: "skills/**/*", ToGlob: ".agents/skills/**/*", Merge: adapter.MergeReplace, Prefix: route.PrefixSkill, Sep: "-"},
		{FromGlob: "agents/**/*.md", ToGlob: ".codex/agents/**/*.toml", Merge: adapter.MergeReplace, Transform: codexAgentTOML},
		{FromGlob: "mcp/**/*", ToGlob: configTOMLPath, Merge: adapter.MergeDeep, Transform: codexMCPSurgery},
		// Root .mcp.json is Claude Code's standard plugin MCP location — merge it
		// into .codex/config.toml too (same header surgery as mcp/**/*).
		{FromGlob: ".mcp.json", ToGlob: configTOMLPath, Merge: adapter.MergeDeep, Transform: codexMCPSurgery},
	}
}

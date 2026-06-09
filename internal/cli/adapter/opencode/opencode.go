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
// component names are accumulated by route.Project per ADAPT-07.
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
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

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

	// opencode MCP server `type` discriminants (https://opencode.ai/docs/mcp-servers/).
	opencodeMCPLocal  = "local"
	opencodeMCPRemote = "remote"
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

	// Global-mode hint (WR-06): opencode's global config lives at the XDG path
	// $HOME/.config/opencode/ (remapGlobalPath, wiring.go), so a global-only
	// install with no project-relative .opencode/ footprint still matches —
	// mirroring codex's $HOME/.codex/ probe. Skipped when HOME is unset
	// (defensive — os.UserHomeDir errors on unset HOME) and when root == $HOME
	// to avoid double-counting an already-checked dir.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		absRoot, _ := filepath.Abs(root)
		if absRoot != home {
			full := filepath.Join(home, ".config", "opencode")
			if _, err := os.Stat(full); err == nil {
				signals++
				reasons = append(reasons, "found $HOME/.config/opencode/ directory (global-mode hint)")
			}
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
// opencodeMCPEntry is the per-server JSON shape OpenCode consumes under
// the `mcp` key for a remote (HTTP) MCP server. OpenCode uses
// `"type":"remote"` (NOT "http"), `url`, optional `"enabled":true`, and a
// `headers` map. The earlier {type:"http"} shape (copied from claudecode)
// was silently ignored. Schema ref: https://opencode.ai/docs/mcp-servers/.
type opencodeMCPEntry struct {
	Type    string            `json:"type"` // "remote"
	URL     string            `json:"url"`
	Enabled bool              `json:"enabled"`
	Headers map[string]string `json:"headers,omitempty"`
}

// MCP carries the per-server JSON shape OpenCode consumes under the
// `mcp` key (opencodeMCPEntry: type=remote, url, enabled, headers).
//
// A2AAgents mirrors the MCP server shape for A2A agents. Spec §7.4 does
// not pin a fixed A2A shape for OpenCode (A2A support is recent +
// evolving across all platforms), so we mirror the MCP shape under a
// parallel `a2aAgents` top-level key — same shape choice as claudecode.
// A2A projection across non-claude tools is a separate open design
// question (see hydrate bug-sweep plan).
type configJSONShape struct {
	MCP       map[string]opencodeMCPEntry      `json:"mcp"`
	A2AAgents map[string]adapter.A2AAgentEntry `json:"a2aAgents,omitempty"`
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
		MCP:       map[string]opencodeMCPEntry{},
		A2AAgents: map[string]adapter.A2AAgentEntry{},
	}

	// Track contributed top-level keys for state.adapter.files[*].keys[]
	// (STATE-02 + ADAPT-05). Sorted lexicographically so the output
	// stays stable across invocations.
	contributedKeys := make([]string, 0, len(m.Runtime.MCPServers)+len(m.Runtime.A2AAgents))

	for _, server := range m.Runtime.MCPServers {
		entry := opencodeMCPEntry{
			Type:    opencodeMCPRemote,
			URL:     server.Endpoint,
			Enabled: true,
			Headers: adapter.HeadersWithCredential(credential, m.Environment),
		}
		shape.MCP[server.ID] = entry
		contributedKeys = append(contributedKeys, "mcp."+server.ID)
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

// ProjectionRules returns the opencode ROUTE-04 conversion projection table
// satisfying route.RuleProvider (the D-06 seam). Per D-18/D-19 the routed
// (plural) kinds are:
//
//   - commands/**/*.md → .opencode/commands/**/*.md (MergeReplace, Transform:
//     opencodeCommandFrontmatter — rewrite claude command frontmatter to
//     opencode's command schema; output stays markdown, .md only)
//   - agents/**/*.md → .opencode/agents/**/*.md (MergeReplace, Transform:
//     opencodeAgentTools — tools[]→{name:true}, color/model normalize, markdown)
//   - skills/**/*    → .opencode/skills/**/*    (MergeReplace, verbatim copy)
//   - mcp/**/*       → .opencode/opencode.json  (MergeDeep, Transform:
//     opencodeMCPConvert — mcpServers→mcp + per-entry local|remote shape, JSON)
//
// rules/ and AGENTS.md have NO rule and fall into route.Project's dropped set
// (route.go records each unrouted top-level kind exactly once — D-12/D-19); the
// droppable runtime components (hooks/, .lsp.json, monitors/, bin/,
// settings.json) likewise have no rule and drop the same way via
// route.Project's KnownComponentKinds gate. There is deliberately no prompts/
// row: D-18 routes only commands/agents/skills/mcp for opencode
// (OPENPACKAGE-MAPPING §opencode).
//
// The mcp/**/* row collapses N→1 onto the SAME runtime target
// (configJSONPath) RenderRuntime emits, deep-merged under the `mcp` key (D-21);
// the runtime encoder (renderConfigJSON) is left untouched. This method is pure
// data — no I/O. Projection runs via the plan-02 Render leg
// (ProjectionRules -> route.Project).
func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".opencode/commands/**/*.md", Merge: adapter.MergeReplace, Transform: opencodeCommandFrontmatter},
		{FromGlob: "agents/**/*.md", ToGlob: ".opencode/agents/**/*.md", Merge: adapter.MergeReplace, Transform: opencodeAgentTools},
		{FromGlob: "skills/**/*", ToGlob: ".opencode/skills/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "mcp/**/*", ToGlob: configJSONPath, Merge: adapter.MergeDeep, Transform: opencodeMCPConvert},
		// Root .mcp.json is Claude Code's standard plugin MCP location — deep-merge
		// it into .opencode/opencode.json too (same conversion transform).
		{FromGlob: ".mcp.json", ToGlob: configJSONPath, Merge: adapter.MergeDeep, Transform: opencodeMCPConvert},
	}
}

// opencodeAgentTools is the agent-frontmatter Transform (FMT-04, D-20). It
// converts ONLY the frontmatter `tools` value from a YAML sequence
// (`[read, write]`) into a string→bool object (`{read: true, write: true}`) —
// the shape opencode's agent config expects. Each tool NAME is normalized via
// normalizeOpencodeToolName (lowercase + the OpenPackage rename map), because
// opencode's tool registry is lowercase and a Claude PascalCase `Read` would
// not resolve. It re-emits the document as
// markdown+frontmatter via the deterministic route.EncodeFrontmatterDoc
// (sorted keys, encoder-quoted scalars — D-24/D-05), so re-hydrate is
// byte-identical (FMT-05).
//
// Two opencode-SCHEMA fields are normalized (opencode validates them and aborts
// fatally on a bad value — it is NOT permissive for these, contrary to the
// earlier assumption here):
//
//   - color: opencode requires ^#[0-9a-fA-F]{6}$ or a theme name
//     (primary|secondary|accent|success|warning|error|info). Claude's freeform
//     names (purple, blue, …) are neither, so a known name maps to hex and an
//     unknown value is DROPPED (color is cosmetic — losing it never breaks the
//     agent). A hex/theme value passes through.
//   - model: opencode wants provider/model-id (e.g. anthropic/claude-…). A
//     claude shorthand (sonnet/opus/haiku — no "/") is DROPPED so opencode falls
//     back to the user's default instead of failing to resolve it; a
//     provider/model value passes through verbatim.
//
// Other claude-extras (`permissions`, `skills`, `hooks`, `disallowedTools`, …)
// pass through — opencode is permissive for keys it doesn't validate. The body
// after the closing fence is preserved byte-for-byte. When the input carries no
// frontmatter, it passes through unchanged (no fence is invented). When `tools`
// is absent the doc is re-encoded but no tools field is fabricated.
//
// keys is nil: the rule is MergeReplace (file-owned), so there are no dotted
// deep-merge keys to contribute. Output stays markdown — NOT JSON (D-20).
func opencodeAgentTools(srcRel string, in []byte) (out []byte, keys []string, err error) {
	fm, body, found := route.SplitFrontmatter(in)
	if !found {
		// No frontmatter — pass the document through unharmed (D-20).
		return in, nil, nil
	}

	var doc map[string]any
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, nil, fmt.Errorf("opencode: opencodeAgentTools parse %q frontmatter: %w", srcRel, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	// Convert tools sequence → {name: true} object. Only when tools is present
	// AND is a sequence; any other already-object/absent shape is left as-is
	// (a pre-converted object round-trips through the encoder unchanged).
	if rawTools, ok := doc["tools"]; ok {
		switch tv := rawTools.(type) {
		case []any:
			toolsObj := make(map[string]bool, len(tv))
			for _, e := range tv {
				name, ok := e.(string)
				if !ok {
					return nil, nil, fmt.Errorf("opencode: opencodeAgentTools %q: tools element %v (%T) is not a string", srcRel, e, e)
				}
				toolsObj[normalizeOpencodeToolName(name)] = true
			}
			doc["tools"] = toolsObj
		case map[string]any:
			// Already an object (e.g. a re-hydrate of converted output). Coerce
			// to map[string]bool so the encoder emits the stable {name:true}
			// shape and the Transform stays idempotent (FMT-05).
			obj := make(map[string]bool, len(tv))
			for k, v := range tv {
				b, ok := v.(bool)
				if !ok {
					return nil, nil, fmt.Errorf("opencode: opencodeAgentTools %q: tools[%q] value %v (%T) is not a bool", srcRel, k, v, v)
				}
				obj[normalizeOpencodeToolName(k)] = b
			}
			doc["tools"] = obj
		case string:
			// Claude also permits a comma-separated string (e.g.
			// "Read, Write, Bash") — the form upstream feature-dev agents use.
			// Split, trim, drop empties; each named tool → true. An empty
			// string yields an empty object (a tools: key with no entries).
			toolsObj := map[string]bool{}
			for _, part := range strings.Split(tv, ",") {
				name := strings.TrimSpace(part)
				if name == "" {
					continue
				}
				toolsObj[normalizeOpencodeToolName(name)] = true
			}
			doc["tools"] = toolsObj
		default:
			return nil, nil, fmt.Errorf("opencode: opencodeAgentTools %q: tools is %T, want sequence, object, or comma-separated string", srcRel, rawTools)
		}
	}

	normalizeOpencodeColorField(doc)
	normalizeOpencodeModelField(doc)

	out, err = route.EncodeFrontmatterDoc(doc, body)
	if err != nil {
		return nil, nil, fmt.Errorf("opencode: opencodeAgentTools encode %q: %w", srcRel, err)
	}
	return out, nil, nil
}

// opencodeMCPConvert is the MCP Transform (D-21). It parses the plugin
// `mcp.json`, renames the top-level `mcpServers` key to `mcp` (opencode's MCP
// merge key under .opencode/opencode.json), CONVERTS each server entry to
// opencode's strict per-server schema, and re-emits canonical JSON via
// route.CanonicalJSON (sorted keys — D-24) so re-hydrate is byte-identical.
//
// Per-entry conversion is MANDATORY — opencode validates each entry and aborts
// the whole config on a mismatch (real ConfigInvalidError observed against
// opencode 1.16.0). A Claude/universal `.mcp.json` entry is one of:
//
//   - stdio:  {command, args, env}      → {type:"local",  command:[cmd,…args],
//     enabled:true, environment:{…}}
//   - remote: {url[, type, headers]}    → {type:"remote", url, enabled:true,
//     headers:{…}}
//
// opencode's schema (https://opencode.ai/docs/mcp-servers/): local requires
// type+command (command is an ARRAY), env uses the "environment" key; remote
// requires type="remote" (NOT "http"/"streamable-http") + url, headers optional.
// `enabled` is carried (default true). Every other source field (description,
// timeout-spelling variants, …) is DROPPED so a closed-schema reject can't
// happen. Headers are copied THROUGH unchanged (any `${env:X}` literal is the
// plugin author's value; opencode resolves it at its own runtime — ACH never
// reads the secret).
//
// keys returns the contributed dotted paths `["mcp.<id>", …]` (sorted) matching
// the runtime encoder prefix (renderConfigJSON contributes `mcp.<server.ID>`),
// so the Wave-1 prefix-aware dropRuntimeOwnedMCP dedups a projected server
// against an identically-named runtime server on a `mcp.<id>` clash. The
// runtime emission (renderConfigJSON / the `json:"mcp"` shape) is UNTOUCHED.
func opencodeMCPConvert(srcRel string, in []byte) (out []byte, keys []string, err error) {
	var top map[string]any
	if err := json.Unmarshal(in, &top); err != nil {
		return nil, nil, fmt.Errorf("opencode: opencodeMCPConvert parse %q: %w", srcRel, err)
	}
	if top == nil {
		top = map[string]any{}
	}

	// Source servers live under `mcpServers` (Claude/universal) or already under
	// `mcp` (a re-hydrate of converted output — keeps the Transform idempotent).
	var raw any
	if v, ok := top["mcpServers"]; ok {
		raw = v
	} else if v, ok := top["mcp"]; ok {
		raw = v
	}
	servers, _ := raw.(map[string]any)

	converted := map[string]any{}
	keys = make([]string, 0, len(servers))
	for id, entry := range servers {
		em, ok := entry.(map[string]any)
		if !ok {
			// Non-object server value — malformed; skip rather than emit an
			// unvalidatable entry that would abort opencode's whole config.
			continue
		}
		converted[id] = convertOpencodeMCPEntry(em)
		keys = append(keys, "mcp."+id)
	}
	sort.Strings(keys)

	out, err = route.CanonicalJSON(map[string]any{"mcp": converted})
	if err != nil {
		return nil, nil, fmt.Errorf("opencode: opencodeMCPConvert encode %q: %w", srcRel, err)
	}
	return out, keys, nil
}

// opencodeMCPEntry converts a single Claude/universal MCP server entry to
// opencode's local|remote schema. Idempotent: a value already in opencode shape
// (command as an array, type already local/remote) round-trips unchanged.
func convertOpencodeMCPEntry(e map[string]any) map[string]any {
	typ, _ := e["type"].(string)
	_, hasURL := e["url"]
	isRemote := hasURL ||
		typ == opencodeMCPRemote || typ == "http" || typ == "streamable-http" || typ == "sse"

	enabled := true
	if v, ok := e["enabled"].(bool); ok {
		enabled = v
	}

	if isRemote {
		out := map[string]any{"type": opencodeMCPRemote, "enabled": enabled}
		if u, ok := e["url"].(string); ok {
			out["url"] = u
		}
		if h, ok := e["headers"].(map[string]any); ok && len(h) > 0 {
			out["headers"] = h
		}
		if t, ok := e["timeout"]; ok {
			out["timeout"] = t
		}
		return out
	}

	// Local (stdio): command must be an ARRAY [cmd, ...args].
	cmd := make([]any, 0, 4)
	switch c := e["command"].(type) {
	case string:
		if c != "" {
			cmd = append(cmd, c)
			if args, ok := e["args"].([]any); ok {
				cmd = append(cmd, args...)
			}
		}
	case []any:
		// Already an array (re-hydrate of converted output, or an author who
		// wrote the opencode form directly).
		cmd = append(cmd, c...)
	}

	out := map[string]any{"type": opencodeMCPLocal, "enabled": enabled, "command": cmd}
	// env (Claude) or environment (opencode) → environment.
	if env, ok := e["environment"].(map[string]any); ok && len(env) > 0 {
		out["environment"] = env
	} else if env, ok := e["env"].(map[string]any); ok && len(env) > 0 {
		out["environment"] = env
	}
	if t, ok := e["timeout"]; ok {
		out["timeout"] = t
	}
	return out
}

// opencodeCommandKeys is opencode's recognized custom-command frontmatter set
// (https://opencode.ai/docs/commands/). Claude command keys not in this set
// (name, argument-hint, allowed-tools, disable-model-invocation, …) are dropped
// — and argument-hint's value (`[type] [description]`) is not even valid YAML,
// so dropping it is what makes the file parse at all.
var opencodeCommandKeys = map[string]bool{
	"description": true,
	"agent":       true,
	"model":       true,
	"subtask":     true,
}

// opencodeCommandFrontmatter rewrites a claude-format command's frontmatter into
// opencode's command schema. The raw claude frontmatter is NOT reliably valid
// YAML (claude is lenient — `argument-hint: [type] [description]` fails a strict
// parser), so this transform line-scans the frontmatter for opencode's
// recognized top-level scalar keys rather than yaml.Unmarshal-ing the whole
// block, drops everything else, and re-emits a clean frontmatter via the
// deterministic encoder. The body (the prompt template — claude's $ARGUMENTS/$1
// syntax is shared with opencode) is preserved byte-for-byte.
//
// Scope: top-level scalar keys only (claude command frontmatter is flat). A
// block-scalar / nested value for a whitelisted key is not expected and not
// handled. keys is nil (MergeReplace, file-owned). When the input carries no
// frontmatter it passes through unchanged.
func opencodeCommandFrontmatter(srcRel string, in []byte) (out []byte, keys []string, err error) {
	fm, body, found := route.SplitFrontmatter(in)
	if !found {
		return in, nil, nil
	}

	doc := map[string]any{}
	for _, line := range strings.Split(string(fm), "\n") {
		key, val, ok := splitTopLevelScalar(line)
		if !ok || !opencodeCommandKeys[key] {
			continue
		}
		switch key {
		case "subtask":
			doc[key] = strings.EqualFold(val, "true")
		case "model":
			// Keep only opencode's provider/model form; drop a claude shorthand.
			if isProviderModel(val) {
				doc[key] = val
			}
		default: // description, agent
			doc[key] = val
		}
	}

	// Nothing opencode recognizes → keep just the body (the prompt template);
	// emitting an empty frontmatter fence would be meaningless.
	if len(doc) == 0 {
		return body, nil, nil
	}

	out, err = route.EncodeFrontmatterDoc(doc, body)
	if err != nil {
		return nil, nil, fmt.Errorf("opencode: opencodeCommandFrontmatter encode %q: %w", srcRel, err)
	}
	return out, nil, nil
}

// splitTopLevelScalar parses one `key: value` line at the top level (not
// indented, not a comment/blank). Returns the key, the value with any matching
// surrounding quotes stripped, and ok=false for lines that are not a bare
// top-level scalar assignment (indented continuations, comments, list items).
func splitTopLevelScalar(line string) (key, val string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '-' {
		return "", "", false
	}
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", "", false
		}
	}
	val = strings.TrimSpace(line[idx+1:])
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	return key, val, true
}

// opencode color validation: ^#[0-9a-fA-F]{6}$ or a theme name. Claude's
// freeform color names map to hex; unknowns drop.
var (
	hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

	opencodeColorThemes = map[string]bool{
		"primary": true, "secondary": true, "accent": true,
		"success": true, "warning": true, "error": true, "info": true,
	}

	// claudeColorHex maps the common claude/CSS agent color names to a hex value
	// opencode accepts. Intentionally small — an unmapped name is simply dropped.
	claudeColorHex = map[string]string{
		"red": "#e74c3c", "green": "#2ecc71", "blue": "#3498db",
		"yellow": "#f1c40f", "orange": "#e67e22", "purple": "#9b59b6",
		"pink": "#ff69b4", "cyan": "#00bcd4", "magenta": "#ff00ff",
		"gray": "#95a5a6", "grey": "#95a5a6", "white": "#ffffff",
		"black": "#000000", "brown": "#8d6e63",
	}
)

// normalizeOpencodeColorField rewrites doc["color"] in place to an
// opencode-valid value, or deletes it when the value cannot be made valid.
func normalizeOpencodeColorField(doc map[string]any) {
	raw, ok := doc["color"]
	if !ok {
		return
	}
	s, isStr := raw.(string)
	if !isStr {
		delete(doc, "color")
		return
	}
	s = strings.TrimSpace(s)
	if hexColorRe.MatchString(s) || opencodeColorThemes[strings.ToLower(s)] {
		doc["color"] = s
		return
	}
	if hex, found := claudeColorHex[strings.ToLower(s)]; found {
		doc["color"] = hex
		return
	}
	delete(doc, "color")
}

// normalizeOpencodeModelField deletes doc["model"] when it is a string not in
// opencode's provider/model form (a claude shorthand opencode cannot resolve);
// a provider/model value is left untouched.
func normalizeOpencodeModelField(doc map[string]any) {
	raw, ok := doc["model"]
	if !ok {
		return
	}
	if s, isStr := raw.(string); isStr && !isProviderModel(s) {
		delete(doc, "model")
	}
}

// isProviderModel reports whether s is in opencode's provider/model-id form
// (contains a "/"), e.g. "anthropic/claude-sonnet-4-…".
func isProviderModel(s string) bool {
	return strings.Contains(strings.TrimSpace(s), "/")
}

// opencodeToolRename maps the lowercased Claude tool names that opencode knows
// under a different identifier. Mirrors OpenPackage's claude→universal tool
// normalization (platforms.jsonc claude.import: split, lowercase, THEN replace
// these three); applied AFTER lowercasing.
var opencodeToolRename = map[string]string{
	"askuserquestion": "question",
	"notebookedit":    "notebook",
	"exitplanmode":    "exitplan",
}

// normalizeOpencodeToolName converts a Claude tool identifier to the form
// opencode expects: lowercase (opencode's tool registry is lowercase — bash,
// read, write, …; a PascalCase "Read" would not resolve), with the small rename
// map applied for OpenPackage parity. A "Bash(git *)" restriction suffix is
// stripped to its base tool. MCP tool ids ("mcp__server__tool") are returned
// unchanged — their casing is significant and they are not Claude PascalCase
// names. Idempotent: a second pass over an already-normalized name is a no-op.
func normalizeOpencodeToolName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "mcp__") {
		return name
	}
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}
	name = strings.ToLower(name)
	if mapped, ok := opencodeToolRename[name]; ok {
		return mapped
	}
	return name
}

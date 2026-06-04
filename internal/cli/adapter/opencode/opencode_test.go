// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

func TestOpencode_ID(t *testing.T) {
	a := &Adapter{}
	if got := a.ID(); got != "opencode" {
		t.Fatalf("ID() = %q, want %q", got, "opencode")
	}
}

func TestOpencode_Aliases_Empty(t *testing.T) {
	// Per CLI spec §7.2 row 4 (opencode), Aliases column is `—`. We
	// return an empty slice (length 0). The registry tolerates empty
	// alias lists.
	a := &Adapter{}
	got := a.Aliases()
	if len(got) != 0 {
		t.Errorf("Aliases() returned %d entries, want 0 (spec §7.2 opencode: `—`)", len(got))
	}
}

func TestOpencode_Detect_NoSignals_ZeroMatch(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	// Clobber HOME to a fresh dir so a real ~/.config/opencode/ on the test
	// machine cannot leak a global-mode signal (WR-06).
	t.Setenv("HOME", t.TempDir())
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "" {
		t.Errorf("Detect(empty root) returned ID=%q, want empty", got.ID)
	}
	if got.Confidence != 0 {
		t.Errorf("Detect(empty root) returned Confidence=%v, want zero", got.Confidence)
	}
}

// TestOpencode_Detect_GlobalOnly_LowConfidence proves WR-06: a global-only
// install whose ONLY footprint is the XDG dir $HOME/.config/opencode/ still
// matches (Low), mirroring codex's $HOME/.codex/ global-mode hint.
func TestOpencode_Detect_GlobalOnly_LowConfidence(t *testing.T) {
	a := &Adapter{}
	// root has no project-relative opencode artifacts.
	root := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Setenv("HOME", home)

	got, err := a.Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "opencode" {
		t.Errorf("global-only Detect ID=%q, want opencode", got.ID)
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("global-only Detect Confidence=%v, want ConfidenceLow", got.Confidence)
	}
}

func TestOpencode_Detect_OneSignal_LowConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // isolate from a real ~/.config/opencode/ (WR-06)
	if err := os.MkdirAll(filepath.Join(tmp, ".opencode"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "opencode" {
		t.Errorf("Detect returned ID=%q, want %q", got.ID, "opencode")
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("Detect with 1 signal returned Confidence=%v, want ConfidenceLow", got.Confidence)
	}
	if len(got.Reasons) != 1 {
		t.Errorf("Detect with 1 signal returned %d Reasons, want 1", len(got.Reasons))
	}
}

func TestOpencode_Detect_TwoSignals_MediumConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // isolate from a real ~/.config/opencode/ (WR-06)
	if err := os.MkdirAll(filepath.Join(tmp, ".opencode"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".opencode", "opencode.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Confidence != adapter.ConfidenceMedium {
		t.Errorf("Detect with 2 signals returned Confidence=%v, want ConfidenceMedium", got.Confidence)
	}
}

func TestOpencode_Detect_AllSignals_HighConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // isolate from a real ~/.config/opencode/ (WR-06)
	if err := os.MkdirAll(filepath.Join(tmp, ".opencode", "plugins"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".opencode", "opencode.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "opencode.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Confidence != adapter.ConfidenceHigh {
		t.Errorf("Detect with 4 signals returned Confidence=%v, want ConfidenceHigh", got.Confidence)
	}
	if len(got.Reasons) < 3 {
		t.Errorf("Detect with 4 signals returned %d Reasons, want >=3", len(got.Reasons))
	}
}

// buildManifest constructs a non-nil Manifest with 2 MCP servers + 1
// A2A agent, each carrying an Endpoint URL. Same shape used by the
// claudecode adapter tests for cross-adapter symmetry.
func buildManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			Models: []manifest.ContentRef{
				{ID: "demo-model", Endpoint: "http://localhost:8080/v1"},
			},
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp-jwt", Endpoint: "http://localhost:8080/mcp/demo-mcp-jwt"},
				{ID: "demo-mcp-nojwt", Endpoint: "http://localhost:8080/mcp/demo-mcp-nojwt"},
			},
			A2AAgents: []manifest.ContentRef{
				{ID: "demo-agent", Endpoint: "http://localhost:8080/a2a/demo-agent"},
			},
		},
		Context: &manifest.ContextBlock{},
	}
}

func TestRenderRuntime_ConfigJsonShape(t *testing.T) {
	a := &Adapter{}
	m := buildManifest()

	writes, err := a.RenderRuntime(context.Background(), m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("RenderRuntime returned %d FileWrites, want 1", len(writes))
	}
	w := writes[0]
	// Per CLI spec §7.2 row 4 + §7.4 opencode: runtime-config file is
	// `.opencode/opencode.json` (NOT `.opencode/config.json` which is
	// what the plan must_haves names — spec is authoritative per the
	// plan's <read_first> + <action> directives).
	if w.Path != ".opencode/opencode.json" {
		t.Errorf("FileWrite.Path = %q, want %q", w.Path, ".opencode/opencode.json")
	}
	if w.Merge != adapter.MergeDeep {
		t.Errorf("FileWrite.Merge = %v, want MergeDeep", w.Merge)
	}
	// 2 MCP servers + 1 A2A agent = 3 contributed top-level keys.
	if len(w.Keys) != 3 {
		t.Errorf("FileWrite.Keys count = %d, want 3", len(w.Keys))
	}

	// JSON round-trip: top-level `mcp` (NOT `mcpServers`) per spec §7.4
	// opencode row.
	var got struct {
		MCP map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Enabled bool              `json:"enabled"`
			Headers map[string]string `json:"headers"`
		} `json:"mcp"`
		A2AAgents map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"a2aAgents"`
	}
	if err := json.Unmarshal(w.Content, &got); err != nil {
		t.Fatalf("json.Unmarshal Content: %v", err)
	}
	if len(got.MCP) != 2 {
		t.Errorf("mcp map size = %d, want 2", len(got.MCP))
	}
	if got.MCP["demo-mcp-jwt"].URL != "http://localhost:8080/mcp/demo-mcp-jwt" {
		t.Errorf("MCP url = %q, want endpoint from manifest", got.MCP["demo-mcp-jwt"].URL)
	}
	// OpenCode remote MCP uses type=="remote" (NOT "http") + enabled=true.
	// Schema: https://opencode.ai/docs/mcp-servers/.
	if got.MCP["demo-mcp-jwt"].Type != "remote" {
		t.Errorf("MCP type = %q, want remote", got.MCP["demo-mcp-jwt"].Type)
	}
	if !got.MCP["demo-mcp-jwt"].Enabled {
		t.Errorf("MCP enabled = false, want true")
	}
	if len(got.A2AAgents) != 1 {
		t.Errorf("a2aAgents map size = %d, want 1", len(got.A2AAgents))
	}
	if got.A2AAgents["demo-agent"].URL != "http://localhost:8080/a2a/demo-agent" {
		t.Errorf("A2A url = %q, want endpoint from manifest", got.A2AAgents["demo-agent"].URL)
	}

	// Verify contributed Keys are prefixed `mcp.` and `a2aAgents.`
	// (NOT `mcpServers.` like claudecode) — this is the inverse-merge
	// path identifier and MUST match the on-disk JSON structure.
	wantPrefixes := map[string]bool{
		"mcp.demo-mcp-jwt":     false,
		"mcp.demo-mcp-nojwt":   false,
		"a2aAgents.demo-agent": false,
	}
	for _, k := range w.Keys {
		if _, ok := wantPrefixes[k]; ok {
			wantPrefixes[k] = true
		} else {
			t.Errorf("FileWrite.Keys[*] = %q, not in expected set", k)
		}
	}
	for k, seen := range wantPrefixes {
		if !seen {
			t.Errorf("FileWrite.Keys missing expected entry %q", k)
		}
	}
}

func TestRenderRuntime_CredentialPropagation(t *testing.T) {
	a := &Adapter{}
	m := buildManifest()

	ctx := adapter.WithCredential(context.Background(), "pk_demo")
	writes, err := a.RenderRuntime(ctx, m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	if !bytes.Contains(writes[0].Content, []byte(`"x-ach-key": "pk_demo"`)) {
		t.Errorf("rendered content missing x-ach-key credential header; got:\n%s", string(writes[0].Content))
	}
}

func TestRenderRuntime_EmptyRuntime_EmitsEmptyMcp(t *testing.T) {
	a := &Adapter{}
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime:       &manifest.RuntimeBlock{},
		Context:       &manifest.ContextBlock{},
	}

	writes, err := a.RenderRuntime(context.Background(), m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("RenderRuntime returned %d FileWrites, want 1", len(writes))
	}
	var got struct {
		MCP map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal(writes[0].Content, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(got.MCP) != 0 {
		t.Errorf("empty runtime → mcp should be empty, got %d entries", len(got.MCP))
	}
	if len(writes[0].Keys) != 0 {
		t.Errorf("empty runtime → Keys should be empty, got %d entries", len(writes[0].Keys))
	}
}

func TestRenderRuntime_NilManifest_Errors(t *testing.T) {
	a := &Adapter{}
	_, err := a.RenderRuntime(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("RenderRuntime(nil manifest) returned nil error; want error")
	}
}

func TestRegistry_RegistersOnImport(t *testing.T) {
	// This file imports github.com/ackstorm/ach/internal/cli/adapter
	// and is itself in the opencode package — so init() has fired by
	// the time this test runs.
	got, ok := adapter.Lookup("opencode")
	if !ok {
		t.Fatal("adapter.Lookup(\"opencode\") returned false; init() did not register")
	}
	if got.ID() != "opencode" {
		t.Errorf("Lookup returned adapter with ID %q, want %q", got.ID(), "opencode")
	}

	// Case-insensitive lookup of the canonical ID.
	if _, ok := adapter.Lookup("OPENCODE"); !ok {
		t.Error("adapter.Lookup(\"OPENCODE\") returned false; case-insensitive lookup missed")
	}

	// ADAPT-01 closed-set assertion: each Go test binary compiles
	// with only its package's transitive imports, so the opencode
	// test binary sees only opencode's init() side-effect — Iter()
	// length is exactly 1 here. The full 4-adapter closed-set
	// assertion (claudecode + codex + gemini + opencode) belongs to
	// W3-05 cobra wiring, where all four are blank-imported under a
	// single compilation unit. From this test we only assert the
	// per-plan invariant: the opencode adapter IS in the registry
	// after import, and Iter() contains it.
	all := adapter.Iter()
	if len(all) < 1 {
		t.Errorf("adapter.Iter() len = %d, want >= 1 (opencode)", len(all))
	}
	seenIDs := make(map[string]bool, len(all))
	for _, a := range all {
		seenIDs[a.ID()] = true
	}
	if !seenIDs["opencode"] {
		t.Error("adapter.Iter() does not include opencode")
	}
}

// TestProjectionRules_RoutesAndDrops exercises the real route.Project engine
// over a plugin tree containing every opencode-routed kind (commands/agents/
// skills/mcp) PLUS the dropped kinds (rules/, AGENTS.md) (ROUTE-04, D-18/D-19).
// It asserts: the three file kinds route to the PLURAL .opencode/<kind>/ dirs,
// mcp/ collapses onto .opencode/opencode.json, and both rules + AGENTS.md land
// in the dropped set exactly once.
func TestProjectionRules_RoutesAndDrops(t *testing.T) {
	src := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	mustWrite("commands/grunt.md", "# grunt\n")
	mustWrite("agents/cave.md", "---\nname: cave\n---\nhello\n")
	mustWrite("skills/fire/skill.md", "# fire\n")
	mustWrite("mcp/servers.json", `{"mcpServers":{"svc":{"type":"http","url":"https://svc"}}}`)
	// Dropped-by-omission kinds.
	mustWrite("rules/no.md", "# rule\n")
	mustWrite("AGENTS.md", "# agents prose\n")

	rules := (&Adapter{}).ProjectionRules()
	pr, err := route.Project(rules, src, "")
	if err != nil {
		t.Fatalf("route.Project returned error: %v", err)
	}

	// rules + AGENTS.md must be dropped (exactly once each).
	wantDropped := map[string]int{"rules": 0, "AGENTS.md": 0}
	for _, d := range pr.Dropped {
		if _, ok := wantDropped[d]; ok {
			wantDropped[d]++
		}
	}
	for kind, n := range wantDropped {
		if n != 1 {
			t.Errorf("dropped count for %q = %d, want 1 (dropped=%v)", kind, n, pr.Dropped)
		}
	}

	// Kept kinds must route to the PLURAL .opencode/<kind>/ dirs (+ the N→1
	// mcp collapse onto opencode.json).
	wantPaths := map[string]bool{
		".opencode/commands/grunt.md":    false,
		".opencode/agents/cave.md":       false,
		".opencode/skills/fire/skill.md": false,
		".opencode/opencode.json":        false,
	}
	for _, w := range pr.FileWrites {
		p := filepath.ToSlash(w.Path)
		if strings.HasPrefix(p, ".opencode/prompts/") {
			t.Errorf("FileWrite targets a .opencode/prompts/ path %q; prompts/ has no opencode rule", w.Path)
		}
		if _, ok := wantPaths[p]; ok {
			wantPaths[p] = true
		}
	}
	for p, seen := range wantPaths {
		if !seen {
			t.Errorf("expected a FileWrite for %q, got %d writes: %+v", p, len(pr.FileWrites), pr.FileWrites)
		}
	}
}

// --- Task 2: opencodeAgentTools (FMT-04, D-20) -------------------------------

// TestOpencodeAgentTools_ToolsArrayToObject: a tools array becomes a
// {name: true} object; model is preserved verbatim (NO provider/model rewrite);
// the body is intact; output stays markdown (opens with "---").
func TestOpencodeAgentTools_ToolsArrayToObject(t *testing.T) {
	in := []byte("---\ntools:\n  - read\n  - write\nmodel: anthropic/claude\n---\nB")
	out, keys, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("opencodeAgentTools: %v", err)
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil (MergeReplace, file-owned)", keys)
	}
	s := string(out)
	if !strings.HasPrefix(s, "---\n") {
		t.Errorf("output does not open with frontmatter fence:\n%s", s)
	}
	// tools must now be a nested mapping read: true / write: true.
	if !strings.Contains(s, "tools:\n") {
		t.Errorf("tools not emitted as a block mapping:\n%s", s)
	}
	if !strings.Contains(s, `"read": true`) || !strings.Contains(s, `"write": true`) {
		t.Errorf("tools array not converted to {name:true} object:\n%s", s)
	}
	if strings.Contains(s, "- read") || strings.Contains(s, "- write") {
		t.Errorf("tools still emitted as a sequence:\n%s", s)
	}
	// model preserved verbatim (no provider/model rewrite).
	if !strings.Contains(s, `model: "anthropic/claude"`) {
		t.Errorf("model not preserved verbatim:\n%s", s)
	}
	// body intact.
	if !strings.HasSuffix(s, "---\nB") {
		t.Errorf("body not preserved (want trailing '---\\nB'):\n%s", s)
	}
}

// TestOpencodeAgentTools_ToolsStringToObject proves the Claude comma-separated
// string form (tools: "Read, Write, Bash") is split + coerced to {name:true},
// the shape upstream feature-dev agents ship (anthropics/claude-plugins-official).
func TestOpencodeAgentTools_ToolsStringToObject(t *testing.T) {
	in := []byte("---\ntools: Read, Write , Bash\nmodel: anthropic/claude\n---\nB")
	out, _, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("opencodeAgentTools: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"Read": true`, `"Write": true`, `"Bash": true`} {
		if !strings.Contains(s, want) {
			t.Errorf("string tools not split/coerced (missing %s):\n%s", want, s)
		}
	}
	if strings.Contains(s, "Read, Write") {
		t.Errorf("tools still emitted as a raw string:\n%s", s)
	}
}

// TestOpencodeAgentTools_ToolsStringIsIdempotent proves a second pass over the
// converted output (now an object) stays byte-identical (FMT-05).
func TestOpencodeAgentTools_ToolsStringIsIdempotent(t *testing.T) {
	in := []byte("---\ntools: Read, Write\n---\nB")
	first, _, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, _, err := opencodeAgentTools("agents/x.md", first)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("not idempotent:\nfirst=%q\nsecond=%q", first, second)
	}
}

// TestOpencodeAgentTools_PreservesClaudeExtras: claude-only frontmatter keys
// (skills, hooks, disallowedTools) survive the re-encode.
func TestOpencodeAgentTools_PreservesClaudeExtras(t *testing.T) {
	in := []byte("---\n" +
		"tools:\n  - read\n" +
		"skills:\n  - alpha\n" +
		"hooks:\n  - preflight\n" +
		"disallowedTools:\n  - delete\n" +
		"---\nbody\n")
	out, _, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("opencodeAgentTools: %v", err)
	}
	s := string(out)
	for _, k := range []string{"skills:", "hooks:", "disallowedTools:"} {
		if !strings.Contains(s, k) {
			t.Errorf("claude-extra key %q dropped:\n%s", k, s)
		}
	}
	for _, v := range []string{`"alpha"`, `"preflight"`, `"delete"`} {
		if !strings.Contains(s, v) {
			t.Errorf("claude-extra value %q dropped:\n%s", v, s)
		}
	}
}

// TestOpencodeAgentTools_NoToolsKey: when tools is absent the doc is re-encoded
// (sorted) but no tools field is invented.
func TestOpencodeAgentTools_NoToolsKey(t *testing.T) {
	in := []byte("---\nmodel: anthropic/claude\nname: cave\n---\nbody\n")
	out, _, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("opencodeAgentTools: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "tools") {
		t.Errorf("tools field invented when source had none:\n%s", s)
	}
	if !strings.Contains(s, `model: "anthropic/claude"`) || !strings.Contains(s, `name: "cave"`) {
		t.Errorf("non-tools keys not preserved:\n%s", s)
	}
}

// TestOpencodeAgentTools_NoFrontmatter: a body with no frontmatter passes
// through unharmed (no fence invented).
func TestOpencodeAgentTools_NoFrontmatter(t *testing.T) {
	in := []byte("# just a heading\nno frontmatter here\n")
	out, _, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("opencodeAgentTools: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("no-frontmatter input mutated:\ngot:  %q\nwant: %q", out, in)
	}
}

// TestOpencodeAgentTools_Idempotent (FMT-05): a second call on the FIRST call's
// output yields byte-identical output.
func TestOpencodeAgentTools_Idempotent(t *testing.T) {
	in := []byte("---\ntools:\n  - write\n  - read\nmodel: anthropic/claude\n---\nB")
	out1, _, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	out2, _, err := opencodeAgentTools("agents/x.md", out1)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("not byte-identical on re-run (FMT-05):\nfirst:  %q\nsecond: %q", out1, out2)
	}
}

// --- Task 2: opencodeMCPRename (D-21) ----------------------------------------

// TestOpencodeMCPRename_RenamesKey: mcpServers→mcp with the per-server shape
// preserved; keys == ["mcp.<id>"]; output is JSON (no markdown fence).
func TestOpencodeMCPRename_RenamesKey(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc1":{"type":"http","url":"https://svc1"}}}`)
	out, keys, err := opencodeMCPRename("mcp/servers.json", in)
	if err != nil {
		t.Fatalf("opencodeMCPRename: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if _, ok := got["mcp"]; !ok {
		t.Errorf("top key 'mcp' missing:\n%s", out)
	}
	if _, ok := got["mcpServers"]; ok {
		t.Errorf("top key 'mcpServers' should be renamed away:\n%s", out)
	}
	mcp, _ := got["mcp"].(map[string]any)
	svc1, _ := mcp["svc1"].(map[string]any)
	if svc1["type"] != "http" || svc1["url"] != "https://svc1" {
		t.Errorf("per-server shape not preserved under mcp.svc1: %#v", svc1)
	}
	if !reflect.DeepEqual(keys, []string{"mcp.svc1"}) {
		t.Errorf("keys = %v, want [mcp.svc1]", keys)
	}
}

// TestOpencodeMCPRename_NoHeaderSurgery: a "Bearer ${env:X}" header value is
// left UNCHANGED under mcp.svc (no codex-style partition).
func TestOpencodeMCPRename_NoHeaderSurgery(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"type":"http","url":"https://svc","headers":{"Authorization":"Bearer ${env:X}"}}}}`)
	out, _, err := opencodeMCPRename("mcp/x.json", in)
	if err != nil {
		t.Fatalf("opencodeMCPRename: %v", err)
	}
	if !bytes.Contains(out, []byte(`Bearer ${env:X}`)) {
		t.Errorf("Bearer ${env:X} literal not preserved verbatim:\n%s", out)
	}
	if bytes.Contains(out, []byte("bearer_token_env_var")) || bytes.Contains(out, []byte("env_http_headers")) {
		t.Errorf("codex-style header surgery leaked into opencode output:\n%s", out)
	}
}

// TestOpencodeMCPRename_MultipleServersSortedKeys: keys are sorted mcp.<id>.
func TestOpencodeMCPRename_MultipleServersSortedKeys(t *testing.T) {
	in := []byte(`{"mcpServers":{"b":{"type":"http"},"a":{"type":"http"}}}`)
	_, keys, err := opencodeMCPRename("mcp/x.json", in)
	if err != nil {
		t.Fatalf("opencodeMCPRename: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{"mcp.a", "mcp.b"}) {
		t.Errorf("keys = %v, want sorted [mcp.a mcp.b]", keys)
	}
}

// TestOpencodeMCPRename_Idempotent (FMT-05): a second call on the FIRST call's
// output yields byte-identical output.
func TestOpencodeMCPRename_Idempotent(t *testing.T) {
	in := []byte(`{"mcpServers":{"b":{"type":"http","url":"https://b"},"a":{"type":"http","url":"https://a"}}}`)
	out1, _, err := opencodeMCPRename("mcp/x.json", in)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call sees the renamed shape (top key already 'mcp'); it must
	// still be byte-stable. opencodeMCPRename re-keys mcpServers→mcp, so on
	// the already-renamed input there is no mcpServers key — assert the
	// canonical re-encode of an mcp-keyed doc is stable across a re-run.
	out2, _, err := opencodeMCPRename("mcp/x.json", out1)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("not byte-identical on re-run (FMT-05):\nfirst:  %q\nsecond: %q", out1, out2)
	}
}

// TestOpencodeMCPRename_Malformed: invalid JSON returns a non-nil error so the
// projection aborts that file rather than emitting a half-parsed doc.
func TestOpencodeMCPRename_Malformed(t *testing.T) {
	in := []byte(`{"mcpServers": this is not json}`)
	out, keys, err := opencodeMCPRename("mcp/bad.json", in)
	if err == nil {
		t.Fatalf("expected error on malformed JSON, got nil (out=%q keys=%v)", out, keys)
	}
	if out != nil || keys != nil {
		t.Errorf("on error want out==nil keys==nil, got out=%q keys=%v", out, keys)
	}
}

// ----------------------------------------------------------------------------
// VER-01 conformance gap-fill (Phase 06): pin every literal opencode rule from
// OPENPACKAGE-MAPPING.md §opencode 2a/2b in single greppable assertions so the
// spec doc and the opencodeAgentTools / opencodeMCPRename ports cannot drift
// apart. These overlap intentionally with the FMT-04/D-21 cases above (audit,
// not duplication).
// ----------------------------------------------------------------------------

// TestOpencodeAgentTools_Conformance pins OPENPACKAGE-MAPPING.md §opencode 2a:
// tools[] → {name:true} object; model passes through verbatim (NO rewrite);
// claude-extras (skills/hooks/disallowedTools) preserved; output stays markdown.
func TestOpencodeAgentTools_Conformance(t *testing.T) {
	in := []byte("---\n" +
		"tools:\n  - read\n  - write\n" +
		"model: anthropic/claude\n" +
		"permissions:\n  bash: ask\n" +
		"skills:\n  - alpha\n" +
		"hooks:\n  - preflight\n" +
		"disallowedTools:\n  - delete\n" +
		"---\nbody")
	out, keys, err := opencodeAgentTools("agents/x.md", in)
	if err != nil {
		t.Fatalf("opencodeAgentTools: %v", err)
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil (MergeReplace, file-owned)", keys)
	}
	s := string(out)
	// Output stays markdown+frontmatter (NOT JSON).
	if !strings.HasPrefix(s, "---\n") {
		t.Errorf("output does not open with a frontmatter fence:\n%s", s)
	}
	// tools[] → {name:true} object.
	if !strings.Contains(s, `"read": true`) || !strings.Contains(s, `"write": true`) {
		t.Errorf("tools array not converted to {name:true} object:\n%s", s)
	}
	if strings.Contains(s, "- read") || strings.Contains(s, "- write") {
		t.Errorf("tools still emitted as a sequence:\n%s", s)
	}
	// model + permissions pass through as-is (NO provider/model rewrite).
	if !strings.Contains(s, `model: "anthropic/claude"`) {
		t.Errorf("model not preserved verbatim:\n%s", s)
	}
	if !strings.Contains(s, "permissions:") {
		t.Errorf("permissions not preserved:\n%s", s)
	}
	// claude-extras preserved.
	for _, k := range []string{"skills:", "hooks:", "disallowedTools:"} {
		if !strings.Contains(s, k) {
			t.Errorf("claude-extra key %q dropped:\n%s", k, s)
		}
	}
}

// TestOpencodeMCPRename_Conformance pins OPENPACKAGE-MAPPING.md §opencode 2b:
// top-level mcpServers→mcp rename ONLY (output stays JSON), AND the explicit
// NO-header-surgery contract — a "Bearer ${env:X}" header survives verbatim
// under mcp (NOT lifted to bearer_token_env_var / env_http_headers like codex).
func TestOpencodeMCPRename_Conformance(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"type":"http","url":"https://svc",` +
		`"headers":{"Authorization":"Bearer ${env:X}"}}}}`)
	out, keys, err := opencodeMCPRename("mcp/x.json", in)
	if err != nil {
		t.Fatalf("opencodeMCPRename: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{"mcp.svc"}) {
		t.Errorf("keys = %v, want [mcp.svc]", keys)
	}
	// Output is JSON with the top key renamed mcpServers→mcp.
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if _, ok := got["mcp"]; !ok {
		t.Errorf("top key 'mcp' missing:\n%s", out)
	}
	if _, ok := got["mcpServers"]; ok {
		t.Errorf("top key 'mcpServers' must be renamed away:\n%s", out)
	}
	// NO header surgery: the Bearer ${env:X} literal survives verbatim under mcp.
	if !bytes.Contains(out, []byte(`Bearer ${env:X}`)) {
		t.Errorf("Bearer ${env:X} literal not preserved verbatim (opencode does NOT do header surgery):\n%s", out)
	}
	if bytes.Contains(out, []byte("bearer_token_env_var")) || bytes.Contains(out, []byte("env_http_headers")) {
		t.Errorf("codex-style header surgery leaked into opencode output:\n%s", out)
	}
}

// TestCopyFile_SurfacesCloseError_OnDevFull asserts that copyFile
// surfaces a close(2) ENOSPC when the destination is /dev/full. Per
// 07-W5-05 + WR-02 (07-REVIEW.md): on Linux with buffered I/O,
// close(2) can return EIO/ENOSPC when the final flush fails. The
// prior `defer func() { _ = out.Close() }()` pattern silently dropped
// that error, recording a truncated file as successfully written.
// Linux-only: /dev/full is a Linux-specific device that accepts
// writes but fails on close. NOTE: the duplication of this test
// across the four adapter packages is intentional per plan
// 07-W5-05 (avoids cross-package testutil coupling for 4 ~25-line
// tests).
func TestCopyFile_SurfacesCloseError_OnDevFull(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /dev/full semantics (WR-02)")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	// 64 KiB source — enough to ensure io.Copy actually exercises the
	// write path (32 KiB default buffer flushed at least twice).
	payload := bytes.Repeat([]byte{0xAB}, 64*1024)
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	err := adapter.CopyFile(src, "/dev/full")
	if err == nil {
		t.Fatal("copyFile(/dev/full) returned nil; expected ENOSPC from close(2) — the deferred-close pattern is swallowing the error (WR-02)")
	}

	// Linux surfaces ENOSPC either as a syscall.Errno (errors.Is) or
	// as a *PathError wrapping the errno. Accept either by both
	// errors.Is and message-substring check ("no space left on device"
	// is the glibc strerror text).
	if !errors.Is(err, syscall.ENOSPC) {
		if !strings.Contains(err.Error(), "no space left on device") {
			t.Fatalf("copyFile(/dev/full) returned %v (%T); expected ENOSPC / 'no space left on device'", err, err)
		}
	}
}

// TestCopyFile_ReturnsNilOnSuccess asserts the success-path semantics
// are preserved: io.Copy + close both succeed → copyFile returns nil
// and the destination matches the source byte-for-byte.
func TestCopyFile_ReturnsNilOnSuccess(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	payload := []byte("hello world\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := adapter.CopyFile(src, dst); err != nil {
		t.Fatalf("copyFile success path returned error: %v", err)
	}
	got, err := os.ReadFile(dst) //nolint:gosec // dst is under t.TempDir()
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("dst bytes = %q, want %q", got, payload)
	}
}

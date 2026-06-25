// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

func TestCodex_ID(t *testing.T) {
	a := &Adapter{}
	if got := a.ID(); got != "codex" {
		t.Fatalf("ID() = %q, want %q", got, "codex")
	}
}

func TestCodex_Aliases(t *testing.T) {
	a := &Adapter{}
	got := a.Aliases()
	if len(got) != 1 {
		t.Fatalf("Aliases() returned %d entries, want 1", len(got))
	}
	if got[0] != "codex-cli" {
		t.Errorf("Aliases() returned %v, want [\"codex-cli\"]", got)
	}
}

func TestCodex_Detect_NoCodexDir_Zero(t *testing.T) {
	// Use a tmp dir with no .codex/ artifacts AND clobber HOME so the
	// global-mode hint cannot accidentally contribute a signal.
	t.Setenv("HOME", t.TempDir())
	a := &Adapter{}
	tmp := t.TempDir()
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if got.ID != "" {
		t.Errorf("Detect(empty root) returned ID=%q, want empty", got.ID)
	}
	if got.Confidence != 0 {
		t.Errorf("Detect(empty root) returned Confidence=%v, want zero", got.Confidence)
	}
}

// TestCodex_Detect_ProjectScope_IgnoresHome pins finding #4: a project-scope
// Detect (root = an empty cwd) must NOT match just because the user has a
// ~/.codex/ on this machine. Detection is root-relative only.
func TestCodex_Detect_ProjectScope_IgnoresHome(t *testing.T) {
	a := &Adapter{}
	project := t.TempDir() // empty — no .codex artifacts
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Setenv("HOME", home)

	got, err := a.Detect(project)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "" || got.Confidence != 0 {
		t.Errorf("project-scope Detect leaked $HOME: got ID=%q Confidence=%v, want zero-match", got.ID, got.Confidence)
	}
}

func TestCodex_Detect_FullArtifacts_High(t *testing.T) {
	// Clobber HOME so the global-mode hint doesn't add an extra signal
	// (we want the high-confidence ranking driven by local cwd
	// artifacts).
	t.Setenv("HOME", t.TempDir())

	a := &Adapter{}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".codex", "agents"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".codex", "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "codex" {
		t.Errorf("Detect with all signals returned ID=%q, want %q", got.ID, "codex")
	}
	if got.Confidence != adapter.ConfidenceHigh {
		t.Errorf("Detect with 3 signals returned Confidence=%v, want ConfidenceHigh", got.Confidence)
	}
	if len(got.Reasons) < 3 {
		t.Errorf("Detect with 3 signals returned %d Reasons, want >=3", len(got.Reasons))
	}
}

func TestCodex_Detect_TwoSignals_Medium(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	a := &Adapter{}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".codex", "config.toml"), []byte(""), 0o644); err != nil {
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

// buildManifest constructs a non-nil Manifest with 2 MCP servers + 1
// A2A agent, each carrying an Endpoint URL. The shape mirrors what
// internal/cli/manifest.Decode produces against examples/hydrate.json.
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

// decodedTOML is the round-trip-target shape used by TestRenderRuntime_TomlShape
// to decode the emitted bytes via BurntSushi/toml and assert keys.
type decodedTOML struct {
	MCPServers map[string]struct {
		URL         string            `toml:"url"`
		HTTPHeaders map[string]string `toml:"http_headers"`
		Headers     map[string]string `toml:"headers"` // legacy/generic — must be ABSENT
		Transport   string            `toml:"transport"`
	} `toml:"mcp_servers"`
	A2AAgents map[string]struct {
		URL       string            `toml:"url"`
		Headers   map[string]string `toml:"headers"`
		Transport string            `toml:"transport"`
	} `toml:"a2a_agents"`
}

func TestRenderRuntime_TomlShape(t *testing.T) {
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
	if w.Path != ".codex/config.toml" {
		t.Errorf("FileWrite.Path = %q, want %q", w.Path, ".codex/config.toml")
	}
	if w.Merge != adapter.MergeDeep {
		t.Errorf("FileWrite.Merge = %v, want MergeDeep", w.Merge)
	}
	// Keys contributed: 2 MCP servers + 1 A2A agent = 3
	if len(w.Keys) != 3 {
		t.Errorf("FileWrite.Keys count = %d, want 3", len(w.Keys))
	}
	// Keys prefix discipline: each MCP key under "mcp_servers."; each
	// A2A key under "a2a_agents.".
	mcpKeys := 0
	a2aKeys := 0
	for _, k := range w.Keys {
		switch {
		case strings.HasPrefix(k, "mcp_servers."):
			mcpKeys++
		case strings.HasPrefix(k, "a2a_agents."):
			a2aKeys++
		default:
			t.Errorf("FileWrite.Keys[%q] missing mcp_servers./a2a_agents. prefix", k)
		}
	}
	if mcpKeys != 2 {
		t.Errorf("MCP-prefixed Keys count = %d, want 2", mcpKeys)
	}
	if a2aKeys != 1 {
		t.Errorf("A2A-prefixed Keys count = %d, want 1", a2aKeys)
	}

	// Decode the emitted TOML and verify shape.
	var got decodedTOML
	if _, err := toml.Decode(string(w.Content), &got); err != nil {
		t.Fatalf("toml.Decode Content: %v\nbytes:\n%s", err, string(w.Content))
	}
	if len(got.MCPServers) != 2 {
		t.Errorf("mcp_servers table count = %d, want 2", len(got.MCPServers))
	}
	if got.MCPServers["demo-mcp-jwt"].URL != "http://localhost:8080/mcp/demo-mcp-jwt" {
		t.Errorf("mcp_servers.demo-mcp-jwt.url = %q, want endpoint from manifest", got.MCPServers["demo-mcp-jwt"].URL)
	}
	// Codex infers HTTP from the presence of `url`; there is NO `transport`
	// key and static headers live under `http_headers` (a generic `headers`
	// table is ignored). Schema:
	// https://developers.openai.com/codex/config-reference.
	if got.MCPServers["demo-mcp-jwt"].Transport != "" {
		t.Errorf("mcp_servers.demo-mcp-jwt.transport must be absent (codex has no transport key); got %q", got.MCPServers["demo-mcp-jwt"].Transport)
	}
	if len(got.MCPServers["demo-mcp-jwt"].Headers) != 0 {
		t.Errorf("mcp_servers.demo-mcp-jwt.headers (generic) must be absent; got %v", got.MCPServers["demo-mcp-jwt"].Headers)
	}
	if _, ok := got.MCPServers["demo-mcp-jwt"].HTTPHeaders["x-ach-key"]; !ok {
		t.Errorf("mcp_servers.demo-mcp-jwt.http_headers must carry x-ach-key; got %v", got.MCPServers["demo-mcp-jwt"].HTTPHeaders)
	}
	if len(got.A2AAgents) != 1 {
		t.Errorf("a2a_agents table count = %d, want 1", len(got.A2AAgents))
	}
	if got.A2AAgents["demo-agent"].URL != "http://localhost:8080/a2a/demo-agent" {
		t.Errorf("a2a_agents.demo-agent.url = %q, want endpoint from manifest", got.A2AAgents["demo-agent"].URL)
	}
	if got.A2AAgents["demo-agent"].Transport != "http" {
		t.Errorf("a2a_agents.demo-agent.transport = %q, want \"http\"", got.A2AAgents["demo-agent"].Transport)
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

	// Decode and assert the credential lands in every entry's
	// headers["x-ach-key"]. We can't assert literal substring at the
	// byte level because TOML emitters MAY quote / order differently;
	// the round-trip decode is the contract.
	var got decodedTOML
	if _, err := toml.Decode(string(writes[0].Content), &got); err != nil {
		t.Fatalf("toml.Decode: %v\nbytes:\n%s", err, string(writes[0].Content))
	}
	for id, srv := range got.MCPServers {
		if srv.HTTPHeaders["x-ach-key"] != "pk_demo" {
			t.Errorf("mcp_servers.%s.http_headers[x-ach-key] = %q, want %q", id, srv.HTTPHeaders["x-ach-key"], "pk_demo")
		}
		if srv.HTTPHeaders["x-ach-environment"] != "demo" {
			t.Errorf("mcp_servers.%s.http_headers[x-ach-environment] = %q, want %q", id, srv.HTTPHeaders["x-ach-environment"], "demo")
		}
	}
	for id, ag := range got.A2AAgents {
		if ag.Headers["x-ach-key"] != "pk_demo" {
			t.Errorf("a2a_agents.%s.headers[x-ach-key] = %q, want %q", id, ag.Headers["x-ach-key"], "pk_demo")
		}
		if ag.Headers["x-ach-environment"] != "demo" {
			t.Errorf("a2a_agents.%s.headers[x-ach-environment] = %q, want %q", id, ag.Headers["x-ach-environment"], "demo")
		}
	}
}

func TestRenderRuntime_EmptyRuntime_EmitsEmptyTables(t *testing.T) {
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
	if len(writes[0].Keys) != 0 {
		t.Errorf("empty runtime → Keys should be empty, got %d entries", len(writes[0].Keys))
	}
	var got decodedTOML
	if _, err := toml.Decode(string(writes[0].Content), &got); err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}
	if len(got.MCPServers) != 0 {
		t.Errorf("empty runtime → mcp_servers should be empty, got %d entries", len(got.MCPServers))
	}
	if len(got.A2AAgents) != 0 {
		t.Errorf("empty runtime → a2a_agents should be empty, got %d entries", len(got.A2AAgents))
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
	// and is itself in the codex package — so init() has fired by the
	// time this test runs.
	got, ok := adapter.Lookup("codex")
	if !ok {
		t.Fatal("adapter.Lookup(\"codex\") returned false; init() did not register")
	}
	if got.ID() != "codex" {
		t.Errorf("Lookup returned adapter with ID %q, want %q", got.ID(), "codex")
	}

	// Alias resolves.
	if _, ok := adapter.Lookup("codex-cli"); !ok {
		t.Error("adapter.Lookup(\"codex-cli\") returned false; alias did not register")
	}
	// Case-insensitive alias resolution.
	if _, ok := adapter.Lookup("CODEX-CLI"); !ok {
		t.Error("adapter.Lookup(\"CODEX-CLI\") returned false; case-insensitive alias missed")
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

// ----------------------------------------------------------------------------
// Task 1: ROUTE-03 ProjectionRules table (D-13/D-14)
// ----------------------------------------------------------------------------

// writePluginTree writes a map of rel-path → content under root, creating
// parent dirs as needed. A nil/empty content writes an empty file (used to
// materialize directory-bearing kinds).
func writePluginTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}
}

func TestCodex_ProjectionRules_RouteTargets(t *testing.T) {
	a := &Adapter{}
	rules := a.ProjectionRules()

	// Index the rules by FromGlob to assert each route target + Transform.
	type want struct {
		to        string
		merge     adapter.MergeKind
		transform bool
	}
	wants := map[string]want{
		"commands/**/*.md": {to: ".codex/prompts/**/*.md", merge: adapter.MergeReplace, transform: false},
		"skills/**/*":      {to: ".agents/skills/**/*", merge: adapter.MergeReplace, transform: false},
		"agents/**/*.md":   {to: ".codex/agents/**/*.toml", merge: adapter.MergeReplace, transform: true},
		"mcp/**/*":         {to: configTOMLPath, merge: adapter.MergeDeep, transform: true},
		".mcp.json":        {to: configTOMLPath, merge: adapter.MergeDeep, transform: true},
	}
	if len(rules) != len(wants) {
		t.Fatalf("ProjectionRules() = %d rules, want %d: %+v", len(rules), len(wants), rules)
	}
	for _, r := range rules {
		w, ok := wants[r.FromGlob]
		if !ok {
			t.Errorf("unexpected rule FromGlob=%q", r.FromGlob)
			continue
		}
		if r.ToGlob != w.to {
			t.Errorf("%s ToGlob = %q, want %q", r.FromGlob, r.ToGlob, w.to)
		}
		if r.Merge != w.merge {
			t.Errorf("%s Merge = %v, want %v", r.FromGlob, r.Merge, w.merge)
		}
		if (r.Transform != nil) != w.transform {
			t.Errorf("%s Transform present = %v, want %v", r.FromGlob, r.Transform != nil, w.transform)
		}
	}

	// skills MUST NOT route into .codex/skills (the Phase-1 stub bug).
	for _, r := range rules {
		if r.FromGlob == "skills/**/*" && strings.Contains(r.ToGlob, ".codex/skills") {
			t.Errorf("skills route target %q must not be under .codex/skills", r.ToGlob)
		}
	}
}

func TestCodex_Project_DroppedSet(t *testing.T) {
	src := t.TempDir()
	writePluginTree(t, src, map[string]string{
		// Routed kinds.
		"commands/foo.md":     "# foo command\n",
		"skills/bar/SKILL.md": "skill body\n",
		"agents/baz.md":       "---\nname: baz\n---\nbody\n",
		"mcp/mcp.json":        `{"mcpServers":{"svc":{"url":"https://x"}}}`,
		// Dropped kinds (no rule).
		"rules/style.md": "rule body\n",
		"AGENTS.md":      "agents prose\n",
		"hooks/pre.sh":   "#!/bin/sh\n",
	})

	a := &Adapter{}
	pr, err := route.Project(a.ProjectionRules(), src, "")
	if err != nil {
		t.Fatalf("route.Project: %v", err)
	}

	got := append([]string(nil), pr.Dropped...)
	sort.Strings(got)
	want := []string{"AGENTS.md", "hooks", "rules"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped = %v, want %v", got, want)
	}
}

// ----------------------------------------------------------------------------
// Task 2: codexAgentTOML (FMT-01) + codexMCPSurgery (FMT-02)
// ----------------------------------------------------------------------------

// decodeTOML parses TOML bytes into a generic map for assertion.
func decodeTOML(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		t.Fatalf("toml.Unmarshal(%q): %v", b, err)
	}
	return m
}

func TestCodexAgentTOML_FieldLift(t *testing.T) {
	in := []byte("---\nname: x\nmodel: y\ntools:\n  - a\n  - b\n---\nB")
	out, keys, err := codexAgentTOML("agents/x.md", in)
	if err != nil {
		t.Fatalf("codexAgentTOML: %v", err)
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil (MergeReplace, file-owned)", keys)
	}
	m := decodeTOML(t, out)
	if m["name"] != "x" {
		t.Errorf("name = %v, want x", m["name"])
	}
	if m["model"] != "y" {
		t.Errorf("model = %v, want y", m["model"])
	}
	if m["developer_instructions"] != "B" {
		t.Errorf("developer_instructions = %v, want B", m["developer_instructions"])
	}
	if _, ok := m["tools"]; ok {
		t.Errorf("tools must be dropped, got %v", m["tools"])
	}
}

func TestCodexAgentTOML_NameDefaultsToFilename(t *testing.T) {
	in := []byte("---\nmodel: y\n---\nbody text")
	out, _, err := codexAgentTOML("agents/foo.md", in)
	if err != nil {
		t.Fatalf("codexAgentTOML: %v", err)
	}
	m := decodeTOML(t, out)
	if m["name"] != "foo" {
		t.Errorf("name = %v, want foo (filename sans .md)", m["name"])
	}
}

func TestCodexAgentTOML_NoFrontmatter(t *testing.T) {
	in := []byte("just a body, no fence")
	out, _, err := codexAgentTOML("agents/plain.md", in)
	if err != nil {
		t.Fatalf("codexAgentTOML: %v", err)
	}
	m := decodeTOML(t, out)
	if m["name"] != "plain" {
		t.Errorf("name = %v, want plain", m["name"])
	}
	if m["developer_instructions"] != "just a body, no fence" {
		t.Errorf("developer_instructions = %v, want whole body", m["developer_instructions"])
	}
}

func TestCodexAgentTOML_DropsNonWhitelistKeys(t *testing.T) {
	// mcp_servers is included here (WR-01): an agent-frontmatter mcp_servers
	// key must be DROPPED, not lifted — registering MCP endpoints via agent
	// frontmatter is the injection vector the whitelist closes.
	in := []byte("---\nname: a\npermissions: deny\nhooks: x\nskills: y\ndisallowedTools:\n  - z\nmcp_servers:\n  evil:\n    url: https://attacker\n---\nbody")
	out, _, err := codexAgentTOML("agents/a.md", in)
	if err != nil {
		t.Fatalf("codexAgentTOML: %v", err)
	}
	m := decodeTOML(t, out)
	for _, k := range []string{"permissions", "hooks", "skills", "disallowedTools", "mcp_servers"} {
		if _, ok := m[k]; ok {
			t.Errorf("key %q must be dropped, got %v", k, m[k])
		}
	}
	// The injected endpoint must not appear anywhere in the emitted bytes.
	if bytes.Contains(out, []byte("attacker")) {
		t.Errorf("injected mcp_servers endpoint leaked into output: %q", out)
	}
}

func TestCodexAgentTOML_WhitelistKeysPassThrough(t *testing.T) {
	in := []byte("---\nname: a\ndescription: d\nmodel: m\nmodel_reasoning_effort: high\nsandbox_mode: read-only\n---\nbody")
	out, _, err := codexAgentTOML("agents/a.md", in)
	if err != nil {
		t.Fatalf("codexAgentTOML: %v", err)
	}
	m := decodeTOML(t, out)
	if m["model_reasoning_effort"] != "high" {
		t.Errorf("model_reasoning_effort = %v, want high", m["model_reasoning_effort"])
	}
	if m["sandbox_mode"] != "read-only" {
		t.Errorf("sandbox_mode = %v, want read-only", m["sandbox_mode"])
	}
}

func TestCodexAgentTOML_Idempotent(t *testing.T) {
	in := []byte("---\nname: x\nmodel: y\n---\nB")
	out1, _, err := codexAgentTOML("agents/x.md", in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	out2, _, err := codexAgentTOML("agents/x.md", in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("not byte-identical (FMT-05):\nfirst:  %q\nsecond: %q", out1, out2)
	}
}

func TestCodexMCPSurgery_BearerEnv(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"url":"https://x","headers":{"Authorization":"Bearer ${env:TOKEN}"}}}}`)
	out, keys, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("codexMCPSurgery: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{"mcp_servers.svc"}) {
		t.Errorf("keys = %v, want [mcp_servers.svc]", keys)
	}
	m := decodeTOML(t, out)
	servers := m["mcp_servers"].(map[string]any)
	svc := servers["svc"].(map[string]any)
	if svc["bearer_token_env_var"] != "TOKEN" {
		t.Errorf("bearer_token_env_var = %v, want TOKEN", svc["bearer_token_env_var"])
	}
	// The raw secret value must never appear.
	if bytes.Contains(out, []byte("Bearer")) {
		t.Errorf("output must not contain the literal Bearer header: %q", out)
	}
	// No literal Authorization header remains.
	if hh, ok := svc["http_headers"].(map[string]any); ok {
		if _, present := hh["Authorization"]; present {
			t.Errorf("Authorization must not remain as http_header: %v", hh)
		}
	}
}

// TestCodexMCPSurgery_LiteralAuthorizationDropped proves CR-02: a hardcoded
// literal Authorization token (one that is NOT "Bearer ${env:NAME}" and NOT a
// bare "${env:NAME}" reference) is DROPPED rather than materialized into the
// on-disk .codex/config.toml. Materializing the plaintext token would violate
// the documented T-03-04 "the secret VALUE is never read" invariant.
func TestCodexMCPSurgery_LiteralAuthorizationDropped(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"url":"https://x","headers":{"Authorization":"Bearer sk-live-abc123"}}}}`)
	out, _, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("codexMCPSurgery: %v", err)
	}
	// The raw secret value must never appear in the emitted bytes.
	if bytes.Contains(out, []byte("sk-live-abc123")) {
		t.Errorf("literal Authorization secret leaked into output: %q", out)
	}
	m := decodeTOML(t, out)
	svc := m["mcp_servers"].(map[string]any)["svc"].(map[string]any)
	// No Authorization survives in any partition.
	if _, ok := svc["bearer_token_env_var"]; ok {
		t.Errorf("literal token must not produce bearer_token_env_var: %v", svc)
	}
	if hh, ok := svc["http_headers"].(map[string]any); ok {
		if _, present := hh["Authorization"]; present {
			t.Errorf("literal Authorization must be dropped, not kept as http_header: %v", hh)
		}
	}
}

// TestCodexMCPSurgery_NonStringHeaderDropped proves WR-05: a header whose value
// is not a string (number/bool/object — malformed but valid JSON) is dropped,
// not coerced to "" and emitted as an unvalidated literal.
func TestCodexMCPSurgery_NonStringHeaderDropped(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"url":"https://x","headers":{"X-Num":42,"X-Obj":{"k":"v"},"X-Ok":"literal"}}}}`)
	out, _, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("codexMCPSurgery: %v", err)
	}
	m := decodeTOML(t, out)
	svc := m["mcp_servers"].(map[string]any)["svc"].(map[string]any)
	lit, _ := svc["http_headers"].(map[string]any)
	// The string header survives; the non-string headers are dropped.
	if lit["X-Ok"] != "literal" {
		t.Errorf("string header X-Ok = %v, want literal", lit["X-Ok"])
	}
	if _, present := lit["X-Num"]; present {
		t.Errorf("non-string header X-Num must be dropped: %v", lit)
	}
	if _, present := lit["X-Obj"]; present {
		t.Errorf("non-string header X-Obj must be dropped: %v", lit)
	}
}

func TestCodexMCPSurgery_EnvAndLiteralHeaders(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"url":"https://x","headers":{"X-Foo":"${env:BAR}","X-Lit":"literal"}}}}`)
	out, _, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("codexMCPSurgery: %v", err)
	}
	m := decodeTOML(t, out)
	svc := m["mcp_servers"].(map[string]any)["svc"].(map[string]any)
	env := svc["env_http_headers"].(map[string]any)
	if env["X-Foo"] != "BAR" {
		t.Errorf("env_http_headers.X-Foo = %v, want BAR", env["X-Foo"])
	}
	lit := svc["http_headers"].(map[string]any)
	if lit["X-Lit"] != "literal" {
		t.Errorf("http_headers.X-Lit = %v, want literal", lit["X-Lit"])
	}
}

func TestCodexMCPSurgery_TimeoutRename(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{"url":"https://x","timeout":30,"headers":{"X-A":"literal"}}}}`)
	out, _, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("codexMCPSurgery: %v", err)
	}
	m := decodeTOML(t, out)
	svc := m["mcp_servers"].(map[string]any)["svc"].(map[string]any)
	// startup_timeout_sec present; timeout absent; headers map absent.
	if _, ok := svc["startup_timeout_sec"]; !ok {
		t.Errorf("startup_timeout_sec missing: %v", svc)
	}
	if _, ok := svc["timeout"]; ok {
		t.Errorf("timeout must be renamed away: %v", svc)
	}
	if _, ok := svc["headers"]; ok {
		t.Errorf("original headers map must be dropped: %v", svc)
	}
}

func TestCodexMCPSurgery_KeysAndIdempotent(t *testing.T) {
	in := []byte(`{"mcpServers":{"b":{"url":"https://b"},"a":{"url":"https://a"}}}`)
	out1, keys1, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !reflect.DeepEqual(keys1, []string{"mcp_servers.a", "mcp_servers.b"}) {
		t.Errorf("keys = %v, want sorted [mcp_servers.a mcp_servers.b]", keys1)
	}
	out2, _, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("not byte-identical (FMT-05):\nfirst:  %q\nsecond: %q", out1, out2)
	}
}

// ----------------------------------------------------------------------------
// VER-01 conformance gap-fill (Phase 06): pin every literal codex rule from
// OPENPACKAGE-MAPPING.md §codex 2a/2b in single greppable assertions so the
// spec doc and the codexAgentTOML / codexMCPSurgery ports cannot drift apart.
// These are the per-rule audit anchors the plan's <action> checklist requires;
// they overlap intentionally with the FMT-01/FMT-02 cases above (audit, not
// duplication) and cite the exact output keys the acceptance criteria greps for.
// ----------------------------------------------------------------------------

// TestCodexFieldLift_Conformance pins OPENPACKAGE-MAPPING.md §codex 2a in one
// assertion: body→developer_instructions, the six-key whitelist lift, the
// name-defaults-to-filename rule, and the $unset-frontmatter drop of every
// non-whitelist key (tools/permissions/hooks/skills/disallowedTools/mcp_servers).
func TestCodexFieldLift_Conformance(t *testing.T) {
	// All whitelist keys present + every non-whitelist key present.
	in := []byte("---\n" +
		"name: cave\n" +
		"description: d\n" +
		"model: m\n" +
		"model_reasoning_effort: high\n" +
		"sandbox_mode: read-only\n" +
		"tools:\n  - bash\n" +
		"permissions:\n  bash: ask\n" +
		"hooks: x\n" +
		"skills: y\n" +
		"disallowedTools:\n  - z\n" +
		"mcp_servers:\n  evil:\n    url: https://attacker\n" +
		"---\nbody text")
	out, keys, err := codexAgentTOML("agents/cave.md", in)
	if err != nil {
		t.Fatalf("codexAgentTOML: %v", err)
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil (MergeReplace, file-owned)", keys)
	}
	m := decodeTOML(t, out)

	// body → developer_instructions (§codex 2a literal output key).
	if m["developer_instructions"] != "body text" {
		t.Errorf("developer_instructions = %v, want %q", m["developer_instructions"], "body text")
	}
	// Whitelist (exactly the named keys) lifted to top level.
	for k, want := range map[string]any{
		"name": "cave", "description": "d", "model": "m",
		"model_reasoning_effort": "high", "sandbox_mode": "read-only",
	} {
		if m[k] != want {
			t.Errorf("whitelist key %q = %v, want %v", k, m[k], want)
		}
	}
	// $unset frontmatter: every non-whitelist key dropped.
	for _, k := range []string{"tools", "permissions", "hooks", "skills", "disallowedTools", "mcp_servers"} {
		if _, ok := m[k]; ok {
			t.Errorf("non-whitelist key %q must be dropped, got %v", k, m[k])
		}
	}
	// The injected mcp_servers endpoint must not leak anywhere in the bytes.
	if bytes.Contains(out, []byte("attacker")) {
		t.Errorf("injected mcp_servers endpoint leaked into output: %q", out)
	}
}

// TestCodexMCPSurgery_Conformance pins OPENPACKAGE-MAPPING.md §codex 2b in one
// assertion citing every literal output key: bearer_token_env_var (NAME only),
// env_http_headers (env-ref NAME extraction), http_headers (literal partition),
// startup_timeout_sec (timeout rename), and the dropped original headers map.
func TestCodexMCPSurgery_Conformance(t *testing.T) {
	in := []byte(`{"mcpServers":{"svc":{` +
		`"url":"https://x",` +
		`"timeout":30,` +
		`"headers":{` +
		`"Authorization":"Bearer ${env:TOKEN}",` +
		`"X-Env":"${env:BAR}",` +
		`"X-Lit":"literal"` +
		`}}}}`)
	out, keys, err := codexMCPSurgery("mcp/mcp.json", in)
	if err != nil {
		t.Fatalf("codexMCPSurgery: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{"mcp_servers.svc"}) {
		t.Errorf("keys = %v, want [mcp_servers.svc]", keys)
	}
	m := decodeTOML(t, out)
	svc := m["mcp_servers"].(map[string]any)["svc"].(map[string]any)

	// bearer_token_env_var = NAME only (the secret VALUE is never read, T-03-04).
	if svc["bearer_token_env_var"] != "TOKEN" {
		t.Errorf("bearer_token_env_var = %v, want TOKEN", svc["bearer_token_env_var"])
	}
	if bytes.Contains(out, []byte("Bearer")) {
		t.Errorf("literal Bearer header leaked into output: %q", out)
	}
	// env_http_headers: ${env:Y} value → NAME extraction.
	env := svc["env_http_headers"].(map[string]any)
	if env["X-Env"] != "BAR" {
		t.Errorf("env_http_headers.X-Env = %v, want BAR", env["X-Env"])
	}
	// http_headers: literal value passes through.
	lit := svc["http_headers"].(map[string]any)
	if lit["X-Lit"] != "literal" {
		t.Errorf("http_headers.X-Lit = %v, want literal", lit["X-Lit"])
	}
	// startup_timeout_sec: timeout renamed; original key + headers map dropped.
	if _, ok := svc["startup_timeout_sec"]; !ok {
		t.Errorf("startup_timeout_sec missing: %v", svc)
	}
	if _, ok := svc["timeout"]; ok {
		t.Errorf("timeout must be renamed to startup_timeout_sec: %v", svc)
	}
	if _, ok := svc["headers"]; ok {
		t.Errorf("original headers map must be dropped: %v", svc)
	}
}

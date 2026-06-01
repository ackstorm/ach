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

func TestCodex_Detect_NoCodexDir_Low_GlobalHint(t *testing.T) {
	// Spoof $HOME to a dir containing a .codex/ subdir → low-confidence
	// global-mode hint even with no local artifacts.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Setenv("HOME", home)

	a := &Adapter{}
	tmp := t.TempDir()
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "codex" {
		t.Errorf("Detect with global hint returned ID=%q, want %q", got.ID, "codex")
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("Detect with global hint only returned Confidence=%v, want ConfidenceLow", got.Confidence)
	}
	if len(got.Reasons) != 1 {
		t.Errorf("Detect with global hint returned %d Reasons, want 1", len(got.Reasons))
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
		URL       string            `toml:"url"`
		Headers   map[string]string `toml:"headers"`
		Transport string            `toml:"transport"`
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
	if got.MCPServers["demo-mcp-jwt"].Transport != "http" {
		t.Errorf("mcp_servers.demo-mcp-jwt.transport = %q, want \"http\"", got.MCPServers["demo-mcp-jwt"].Transport)
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
		if srv.Headers["x-ach-key"] != "pk_demo" {
			t.Errorf("mcp_servers.%s.headers[x-ach-key] = %q, want %q", id, srv.Headers["x-ach-key"], "pk_demo")
		}
	}
	for id, ag := range got.A2AAgents {
		if ag.Headers["x-ach-key"] != "pk_demo" {
			t.Errorf("a2a_agents.%s.headers[x-ach-key] = %q, want %q", id, ag.Headers["x-ach-key"], "pk_demo")
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

func TestTransformPlugin_DistributesPrompts(t *testing.T) {
	a := &Adapter{}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	// Seed src with a realistic Claude-format plugin tree containing
	// every component the codex adapter cares about.
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "caveman", "version": "1.0.0"}`,
		"agents/cave-agent.md":       "---\nname: cave\ntools:\n  - bash\n  - read\n---\nhello body",
		"commands/grunt.md":          "# grunt",   // D-14: now COPIED verbatim by the legacy walk
		"prompts/intro.md":           "# intro",   // preserved verbatim
		"skills/fire/skill.md":       "# fire",    // preserved verbatim
		"hooks/preflight.sh":         "#!/bin/sh", // silent-dropped
		".mcp.json":                  `{"mcpServers": {}}`,
	}
	for rel, body := range files {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}

	pw, err := a.TransformPlugin(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("TransformPlugin: %v", err)
	}

	// D-14: "commands" is NO LONGER silent-dropped by the legacy walk
	// (commands are now ROUTED via ProjectionRules). Only "hooks" remains in
	// the legacy walk's silentDropTopLevel set.
	wantDrop := map[string]bool{"hooks": true}
	if len(pw.Dropped) != len(wantDrop) {
		t.Errorf("Dropped count = %d, want %d (got %v)", len(pw.Dropped), len(wantDrop), pw.Dropped)
	}
	for _, d := range pw.Dropped {
		if !wantDrop[d] {
			t.Errorf("Dropped contains unexpected entry %q", d)
		}
		delete(wantDrop, d)
	}
	if len(wantDrop) != 0 {
		t.Errorf("Dropped missing entries: %v", wantDrop)
	}

	// ExtractedFiles MUST include exactly the non-dropped, non-.mcp.json
	// files: .claude-plugin/plugin.json, agents/cave-agent.md,
	// commands/grunt.md (D-14: now copied), prompts/intro.md,
	// skills/fire/skill.md.
	wantExtracted := []string{
		".claude-plugin/plugin.json",
		"agents/cave-agent.md",
		"commands/grunt.md",
		"prompts/intro.md",
		"skills/fire/skill.md",
	}
	// Normalize to OS path separator.
	for i := range wantExtracted {
		wantExtracted[i] = filepath.FromSlash(wantExtracted[i])
	}
	sort.Strings(wantExtracted)
	gotExtracted := append([]string{}, pw.ExtractedFiles...)
	sort.Strings(gotExtracted)
	if len(gotExtracted) != len(wantExtracted) {
		t.Fatalf("ExtractedFiles count = %d, want %d (got %v / want %v)",
			len(gotExtracted), len(wantExtracted), gotExtracted, wantExtracted)
	}
	for i := range wantExtracted {
		if gotExtracted[i] != wantExtracted[i] {
			t.Errorf("ExtractedFiles[%d] = %q, want %q", i, gotExtracted[i], wantExtracted[i])
		}
	}

	// Verify the verbatim-copied files are byte-identical on disk.
	verbatim := map[string]string{
		".claude-plugin/plugin.json": files[".claude-plugin/plugin.json"],
		"prompts/intro.md":           files["prompts/intro.md"],
		"skills/fire/skill.md":       files["skills/fire/skill.md"],
	}
	for rel, want := range verbatim {
		fullDst := filepath.Join(dst, rel)
		actual, err := os.ReadFile(fullDst)
		if err != nil {
			t.Errorf("ReadFile %s: %v", rel, err)
			continue
		}
		if string(actual) != want {
			t.Errorf("file %s: content mismatch\ngot:  %q\nwant: %q", rel, actual, want)
		}
		info, err := os.Stat(fullDst)
		if err != nil {
			t.Errorf("Stat %s: %v", rel, err)
			continue
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("file %s: mode = %o, want 0644", rel, info.Mode().Perm())
		}
	}

	// Verify the dropped component (hooks only, post-D-14) was NOT written to
	// dst. commands/ is now copied (see ExtractedFiles above), so it is no
	// longer in the drop set.
	for _, dropped := range []string{"hooks"} {
		path := filepath.Join(dst, dropped)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("dropped component %s should not exist at %s", dropped, path)
		}
	}

	// .mcp.json is consumed at runtime-config rendering, NOT copied to
	// dst.
	if _, err := os.Stat(filepath.Join(dst, ".mcp.json")); err == nil {
		t.Error(".mcp.json should not be copied to dst — it is consumed at runtime-config rendering")
	}
}

func TestTransformPlugin_FrontmatterRewrite_AgentsKeys(t *testing.T) {
	a := &Adapter{}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	const claudeFrontmatter = "---\nname: cave\nmodel: claude-opus-4\ntools:\n  - bash\n  - read\npermissions:\n  bash: ask\n---\nhello body content\n"

	full := filepath.Join(src, "agents", "cave.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(claudeFrontmatter), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pw, err := a.TransformPlugin(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("TransformPlugin: %v", err)
	}
	if len(pw.ExtractedFiles) != 1 {
		t.Fatalf("ExtractedFiles count = %d, want 1 (got %v)", len(pw.ExtractedFiles), pw.ExtractedFiles)
	}

	out, err := os.ReadFile(filepath.Join(dst, "agents", "cave.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Claude `tools:` key MUST be renamed to `allowed_tools:` per CLI
	// spec §7.4 codex.
	if !bytes.Contains(out, []byte("allowed_tools:")) {
		t.Errorf("rewritten frontmatter missing `allowed_tools:` key\nout:\n%s", string(out))
	}
	// The Claude `tools:` key (without the `allowed_` prefix) MUST be
	// gone from the frontmatter as a top-level key. We look for a line
	// starting with exactly "tools:" at column 0.
	for _, line := range bytes.Split(out, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("tools:")) {
			t.Errorf("rewritten frontmatter still contains Claude `tools:` top-level key on line %q", string(line))
		}
	}
	// Non-rewritten keys MUST pass through verbatim.
	for _, want := range [][]byte{
		[]byte("name: cave"),
		[]byte("model: claude-opus-4"),
		[]byte("permissions:"),
	} {
		if !bytes.Contains(out, want) {
			t.Errorf("rewritten frontmatter missing pass-through key %q\nout:\n%s", string(want), string(out))
		}
	}
	// Body MUST be preserved verbatim.
	if !bytes.Contains(out, []byte("hello body content")) {
		t.Errorf("rewritten file missing body content\nout:\n%s", string(out))
	}
	// Nested `tools:` value items must NOT be molested (the `- bash` /
	// `- read` lines are indented under the rewritten `allowed_tools:`).
	if !bytes.Contains(out, []byte("  - bash")) || !bytes.Contains(out, []byte("  - read")) {
		t.Errorf("rewritten frontmatter dropped indented tools list values\nout:\n%s", string(out))
	}
}

func TestTransformPlugin_FrontmatterRewrite_NoFrontmatter_VerbatimCopy(t *testing.T) {
	// Agent file with no leading frontmatter fence should pass through
	// byte-identical.
	a := &Adapter{}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	const noFrontmatter = "# just a markdown agent file\n\nno YAML here\n"

	full := filepath.Join(src, "agents", "plain.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(noFrontmatter), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := a.TransformPlugin(context.Background(), src, dst); err != nil {
		t.Fatalf("TransformPlugin: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(dst, "agents", "plain.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(out) != noFrontmatter {
		t.Errorf("file without frontmatter should pass through byte-identical\ngot:  %q\nwant: %q", out, noFrontmatter)
	}
}

func TestTransformPlugin_EmptySrc_NoFiles(t *testing.T) {
	a := &Adapter{}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	pw, err := a.TransformPlugin(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("TransformPlugin: %v", err)
	}
	if len(pw.ExtractedFiles) != 0 {
		t.Errorf("empty src → ExtractedFiles should be empty, got %d entries", len(pw.ExtractedFiles))
	}
	if len(pw.Dropped) != 0 {
		t.Errorf("Dropped should be empty for empty src, got %v", pw.Dropped)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst dir not created: %v", err)
	}
}

func TestTransformPlugin_EmptyPaths_Errors(t *testing.T) {
	a := &Adapter{}
	if _, err := a.TransformPlugin(context.Background(), "", "/tmp/dst"); err == nil {
		t.Error("TransformPlugin(empty src) returned nil error; want error")
	}
	if _, err := a.TransformPlugin(context.Background(), "/tmp/src", ""); err == nil {
		t.Error("TransformPlugin(empty dst) returned nil error; want error")
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
	_, dropped, err := route.Project(a.ProjectionRules(), src, "")
	if err != nil {
		t.Fatalf("route.Project: %v", err)
	}

	got := append([]string(nil), dropped...)
	sort.Strings(got)
	want := []string{"AGENTS.md", "hooks", "rules"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped = %v, want %v", got, want)
	}
}

func TestCodex_SilentDropTopLevel_NoCommands(t *testing.T) {
	if silentDropTopLevel["commands"] {
		t.Error("silentDropTopLevel still contains \"commands\" — D-14 removed it (commands are now routed)")
	}
	if !silentDropTopLevel["hooks"] {
		t.Error("silentDropTopLevel must still contain \"hooks\"")
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

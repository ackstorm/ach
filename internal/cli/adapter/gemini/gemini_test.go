// SPDX-License-Identifier: Apache-2.0

package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

func TestGemini_ID(t *testing.T) {
	a := &Adapter{}
	if got := a.ID(); got != "gemini-cli" {
		t.Fatalf("ID() = %q, want %q", got, "gemini-cli")
	}
}

func TestGemini_Aliases(t *testing.T) {
	a := &Adapter{}
	got := a.Aliases()
	if len(got) != 1 {
		t.Fatalf("Aliases() returned %d entries, want 1", len(got))
	}
	if got[0] != "gemini" {
		t.Errorf("Aliases()[0] = %q, want %q", got[0], "gemini")
	}
}

func TestGemini_Detect_NoSignals_ZeroMatch(t *testing.T) {
	a := &Adapter{}
	// Use a tmp dir AND override HOME so the global-mode check does not
	// pick up a real user's ~/.gemini/settings.json.
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

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

func TestGemini_Detect_OneSignal_LowConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if err := os.MkdirAll(filepath.Join(tmp, ".gemini"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "gemini-cli" {
		t.Errorf("Detect returned ID=%q, want %q", got.ID, "gemini-cli")
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("Detect with 1 signal returned Confidence=%v, want ConfidenceLow", got.Confidence)
	}
	if len(got.Reasons) != 1 {
		t.Errorf("Detect with 1 signal returned %d Reasons, want 1", len(got.Reasons))
	}
}

func TestGemini_Detect_TwoSignals_MediumConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if err := os.MkdirAll(filepath.Join(tmp, ".gemini"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".gemini", "settings.json"), []byte("{}"), 0o644); err != nil {
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

func TestGemini_Detect_HighConfidence_AllSignals(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Local signals: .gemini/, .gemini/settings.json, .gemini/extensions/
	if err := os.MkdirAll(filepath.Join(tmp, ".gemini", "extensions"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".gemini", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Global signal: $HOME/.gemini/settings.json
	if err := os.MkdirAll(filepath.Join(fakeHome, ".gemini"), 0o755); err != nil {
		t.Fatalf("MkdirAll fakeHome: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeHome, ".gemini", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile fakeHome: %v", err)
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
// A2A agent — same shape as the claudecode tests use.
func buildManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			Models: []manifest.ContentRef{
				{ID: "demo-model", Endpoint: "http://localhost:8080/gemini"},
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

func TestRenderRuntime_SettingsJsonShape(t *testing.T) {
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
	if w.Path != ".gemini/settings.json" {
		t.Errorf("FileWrite.Path = %q, want %q", w.Path, ".gemini/settings.json")
	}
	if w.Merge != adapter.MergeDeep {
		t.Errorf("FileWrite.Merge = %v, want MergeDeep", w.Merge)
	}
	if len(w.Keys) != 3 {
		t.Errorf("FileWrite.Keys count = %d, want 3 (2 mcpServers + 1 a2aAgents)", len(w.Keys))
	}

	// JSON round-trip: decode the content and verify shape.
	var got struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
		A2AAgents map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"a2aAgents"`
	}
	if err := json.Unmarshal(w.Content, &got); err != nil {
		t.Fatalf("json.Unmarshal Content: %v", err)
	}
	if len(got.MCPServers) != 2 {
		t.Errorf("mcpServers map size = %d, want 2", len(got.MCPServers))
	}
	if got.MCPServers["demo-mcp-jwt"].URL != "http://localhost:8080/mcp/demo-mcp-jwt" {
		t.Errorf("MCP url = %q, want endpoint from manifest", got.MCPServers["demo-mcp-jwt"].URL)
	}
	if got.MCPServers["demo-mcp-jwt"].Type != "http" {
		t.Errorf("MCP type = %q, want http", got.MCPServers["demo-mcp-jwt"].Type)
	}
	if len(got.A2AAgents) != 1 {
		t.Errorf("a2aAgents map size = %d, want 1", len(got.A2AAgents))
	}
	if got.A2AAgents["demo-agent"].URL != "http://localhost:8080/a2a/demo-agent" {
		t.Errorf("A2A url = %q, want endpoint from manifest", got.A2AAgents["demo-agent"].URL)
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

func TestRenderRuntime_EmptyRuntime_EmitsEmptyMcpServers(t *testing.T) {
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
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(writes[0].Content, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(got.MCPServers) != 0 {
		t.Errorf("empty runtime → mcpServers should be empty, got %d entries", len(got.MCPServers))
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

func TestTransformPlugin_ExtensionLayout(t *testing.T) {
	a := &Adapter{}

	src := filepath.Join(t.TempDir(), "caveman")
	dst := filepath.Join(t.TempDir(), "extensions")

	// Seed src with a realistic Claude-format plugin tree.
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "caveman", "version": "1.2.3"}`,
		"agents/cave-agent.md":       "---\nname: cave\n---\nhello",
		"commands/grunt.md":          "# grunt",
		"prompts/intro.md":           "# intro",
		"skills/fire/skill.md":       "# fire",
		"hooks/preflight.sh":         "#!/bin/sh\necho hi",
		"hooks/postflight.sh":        "#!/bin/sh\necho bye",
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

	// dst/caveman/ must contain agents/, prompts/, commands/, skills/.
	pluginDst := filepath.Join(dst, "caveman")
	for _, comp := range []string{"agents", "prompts", "commands", "skills"} {
		info, err := os.Stat(filepath.Join(pluginDst, comp))
		if err != nil || !info.IsDir() {
			t.Errorf("missing component dir %s/ — got err=%v", comp, err)
		}
	}

	// hooks/ MUST NOT appear under dst — silent drop per ADAPT-07.
	if _, err := os.Stat(filepath.Join(pluginDst, "hooks")); err == nil {
		t.Errorf("hooks/ subdir present under dst/caveman/ — gemini-cli must silently drop hooks")
	}

	// .mcp.json MUST NOT appear under dst — consumed by RenderRuntime.
	if _, err := os.Stat(filepath.Join(pluginDst, ".mcp.json")); err == nil {
		t.Errorf(".mcp.json appeared under dst — should be consumed by RenderRuntime")
	}

	// agents/cave-agent.md round-trips byte-for-byte.
	got, err := os.ReadFile(filepath.Join(pluginDst, "agents", "cave-agent.md"))
	if err != nil {
		t.Fatalf("ReadFile agents/cave-agent.md: %v", err)
	}
	if string(got) != files["agents/cave-agent.md"] {
		t.Errorf("agents/cave-agent.md content mismatch\ngot:  %q\nwant: %q", got, files["agents/cave-agent.md"])
	}

	// extension.json manifest must exist and carry name + version + components.
	manifestBytes, err := os.ReadFile(filepath.Join(pluginDst, "extension.json"))
	if err != nil {
		t.Fatalf("ReadFile extension.json: %v", err)
	}
	var manifestGot struct {
		Name       string   `json:"name"`
		Version    string   `json:"version"`
		Components []string `json:"components"`
	}
	if err := json.Unmarshal(manifestBytes, &manifestGot); err != nil {
		t.Fatalf("Unmarshal extension.json: %v", err)
	}
	if manifestGot.Name != "caveman" {
		t.Errorf("extension.json name = %q, want %q", manifestGot.Name, "caveman")
	}
	if manifestGot.Version != "1.2.3" {
		t.Errorf("extension.json version = %q, want %q", manifestGot.Version, "1.2.3")
	}
	expectedComponents := []string{"agents", "commands", "prompts", "skills"}
	gotComponents := append([]string{}, manifestGot.Components...)
	sort.Strings(gotComponents)
	if len(gotComponents) != len(expectedComponents) {
		t.Errorf("extension.json components count = %d, want %d", len(gotComponents), len(expectedComponents))
	}
	for i := range expectedComponents {
		if i >= len(gotComponents) || gotComponents[i] != expectedComponents[i] {
			t.Errorf("extension.json components[%d] = %q, want %q",
				i, safeIdx(gotComponents, i), expectedComponents[i])
		}
	}

	// ExtractedFiles must contain agents/prompts/commands/skills entries
	// PLUS extension.json — all relative to dst, all under caveman/.
	for _, want := range []string{
		filepath.Join("caveman", "agents", "cave-agent.md"),
		filepath.Join("caveman", "prompts", "intro.md"),
		filepath.Join("caveman", "commands", "grunt.md"),
		filepath.Join("caveman", "skills", "fire", "skill.md"),
		filepath.Join("caveman", "extension.json"),
	} {
		if !containsString(pw.ExtractedFiles, want) {
			t.Errorf("ExtractedFiles missing %q; got %v", want, pw.ExtractedFiles)
		}
	}
	// Ensure no hooks/ paths leaked into ExtractedFiles. filepath's
	// HasPrefix is deprecated (does not respect separator boundaries);
	// use a separator-aware check instead.
	hooksPrefix := filepath.Join("caveman", "hooks") + string(filepath.Separator)
	for _, f := range pw.ExtractedFiles {
		if f == filepath.Join("caveman", "hooks") ||
			(len(f) > len(hooksPrefix) && f[:len(hooksPrefix)] == hooksPrefix) {
			t.Errorf("ExtractedFiles leaked a hooks/ entry: %q", f)
		}
	}
}

func TestTransformPlugin_Hooks_Dropped(t *testing.T) {
	a := &Adapter{}

	src := filepath.Join(t.TempDir(), "caveman")
	dst := filepath.Join(t.TempDir(), "extensions")

	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "caveman", "version": "0.0.1"}`,
		"prompts/intro.md":           "# intro",
		"hooks/preflight.sh":         "#!/bin/sh",
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

	// Dropped MUST contain "hooks" exactly once.
	if len(pw.Dropped) != 1 {
		t.Fatalf("PluginWrite.Dropped = %v, want exactly [\"hooks\"]", pw.Dropped)
	}
	if pw.Dropped[0] != "hooks" {
		t.Errorf("PluginWrite.Dropped[0] = %q, want %q", pw.Dropped[0], "hooks")
	}
}

func TestTransformPlugin_NoHooks_DroppedNil(t *testing.T) {
	a := &Adapter{}

	src := filepath.Join(t.TempDir(), "caveman")
	dst := filepath.Join(t.TempDir(), "extensions")

	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "caveman", "version": "0.0.1"}`,
		"prompts/intro.md":           "# intro",
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
	if pw.Dropped != nil {
		t.Errorf("PluginWrite.Dropped = %v, want nil when no hooks present", pw.Dropped)
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

func TestMergeStrategies_SettingsJsonIsDeep(t *testing.T) {
	a := &Adapter{}
	got := a.MergeStrategies()
	if got[".gemini/settings.json"] != adapter.MergeDeep {
		t.Errorf("MergeStrategies()[\".gemini/settings.json\"] = %v, want MergeDeep", got[".gemini/settings.json"])
	}
}

func TestResolveOutputContent_SettingsJsonRoundTrip(t *testing.T) {
	a := &Adapter{}
	m := buildManifest()
	ctx := adapter.WithCredential(context.Background(), "pk_demo")

	writes, err := a.RenderRuntime(ctx, m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	wantBytes := writes[0].Content

	gotBytes, err := a.ResolveOutputContent(ctx, m, ".gemini/settings.json")
	if err != nil {
		t.Fatalf("ResolveOutputContent: %v", err)
	}
	if !bytes.Equal(wantBytes, gotBytes) {
		t.Errorf("ResolveOutputContent bytes != RenderRuntime bytes\ngot:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestResolveOutputContent_UnknownTarget_ReturnsNilNil(t *testing.T) {
	a := &Adapter{}
	m := buildManifest()
	got, err := a.ResolveOutputContent(context.Background(), m, ".gemini/extensions/caveman/agents/x.md")
	if err != nil {
		t.Fatalf("ResolveOutputContent: %v", err)
	}
	if got != nil {
		t.Errorf("ResolveOutputContent(unknown target) = non-nil; want nil so cascade Tier 3 source-byte read takes over")
	}
}

func TestResolveOutputContent_NilManifest_ReturnsNilNil(t *testing.T) {
	a := &Adapter{}
	got, err := a.ResolveOutputContent(context.Background(), nil, ".gemini/settings.json")
	if err != nil {
		t.Fatalf("ResolveOutputContent: %v", err)
	}
	if got != nil {
		t.Errorf("ResolveOutputContent(nil manifest) = non-nil; want nil")
	}
}

func TestRegistry_RegistersOnImport(t *testing.T) {
	// This file imports github.com/ackstorm/ach/internal/cli/adapter
	// and is itself in the gemini package — so init() has fired by the
	// time this test runs.
	got, ok := adapter.Lookup("gemini-cli")
	if !ok {
		t.Fatal("adapter.Lookup(\"gemini-cli\") returned false; init() did not register")
	}
	if got.ID() != "gemini-cli" {
		t.Errorf("Lookup returned adapter with ID %q, want %q", got.ID(), "gemini-cli")
	}

	// Alias should resolve case-insensitively.
	if _, ok := adapter.Lookup("gemini"); !ok {
		t.Error("adapter.Lookup(\"gemini\") returned false; alias did not register")
	}
	if _, ok := adapter.Lookup("GEMINI"); !ok {
		t.Error("adapter.Lookup(\"GEMINI\") returned false; case-insensitive alias missed")
	}
}

// containsString reports whether haystack contains needle.
func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func safeIdx(s []string, i int) string {
	if i < 0 || i >= len(s) {
		return "<out-of-bounds>"
	}
	return s[i]
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

	err := copyFile(src, "/dev/full")
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
	if err := copyFile(src, dst); err != nil {
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

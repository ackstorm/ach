// SPDX-License-Identifier: Apache-2.0

package opencode

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

func TestOpencode_Detect_OneSignal_LowConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
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
	if got.MCP["demo-mcp-jwt"].Type != "http" {
		t.Errorf("MCP type = %q, want http", got.MCP["demo-mcp-jwt"].Type)
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

func TestTransformPlugin_DistributesToOpencode(t *testing.T) {
	a := &Adapter{}

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	// Seed src with a realistic Claude-format plugin tree containing
	// every component category — kept files (.claude-plugin,
	// commands, agents, prompts, skills, subdir) AND drop-list
	// entries (hooks/, .lsp.json, monitors/, bin/, settings.json) AND
	// `.mcp.json` (consumed by RenderRuntime, not emitted per-file).
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "caveman", "version": "1.0.0"}`,
		"agents/cave-agent.md":       "---\nname: cave\n---\nhello",
		"commands/grunt.md":          "# grunt",
		"prompts/intro.md":           "# intro",
		"skills/fire/skill.md":       "# fire",
		"subdir/nested/file.txt":     "nested",
		// Drop-listed components — all should appear in Dropped.
		"hooks/preflight.sh": "#!/bin/sh",
		".lsp.json":          `{"lsp": {}}`,
		"monitors/m1.json":   `{}`,
		"bin/helper.sh":      "#!/bin/sh",
		"settings.json":      `{}`,
		// Consumed by RenderRuntime — neither emitted nor recorded as Dropped.
		".mcp.json": `{"mcpServers": {}}`,
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

	// Assert dst/<plugin>/{agents,prompts,commands,skills}/* present
	// — i.e. Claude layout preserved verbatim under dst per spec §7.4.
	expectedKept := []string{
		".claude-plugin/plugin.json",
		"agents/cave-agent.md",
		"commands/grunt.md",
		"prompts/intro.md",
		"skills/fire/skill.md",
		"subdir/nested/file.txt",
	}
	for _, rel := range expectedKept {
		full := filepath.Join(dst, rel)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected kept file not present in dst: %s: %v", rel, err)
		}
	}

	// ExtractedFiles MUST exactly match the kept set (Claude-layout
	// preservation), sorted.
	wantExtracted := make([]string, 0, len(expectedKept))
	for _, k := range expectedKept {
		wantExtracted = append(wantExtracted, filepath.FromSlash(k))
	}
	sort.Strings(wantExtracted)
	got := append([]string{}, pw.ExtractedFiles...)
	sort.Strings(got)
	if len(got) != len(wantExtracted) {
		t.Errorf("ExtractedFiles count = %d, want %d (got=%v)", len(got), len(wantExtracted), got)
	}
	for i := range wantExtracted {
		if i >= len(got) || got[i] != wantExtracted[i] {
			t.Errorf("ExtractedFiles[%d] = %q, want %q", i, safeIdx(got, i), wantExtracted[i])
		}
	}

	// Every kept file's content must round-trip byte-for-byte at mode 0644.
	for _, rel := range expectedKept {
		want := files[rel]
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

	// Drop-listed components MUST NOT appear under dst at all.
	for _, dropped := range []string{
		"hooks/preflight.sh",
		".lsp.json",
		"monitors/m1.json",
		"bin/helper.sh",
		"settings.json",
		".mcp.json",
	} {
		full := filepath.Join(dst, dropped)
		if _, err := os.Stat(full); err == nil {
			t.Errorf("drop-listed component leaked into dst: %s", dropped)
		}
	}
}

func TestTransformPlugin_HooksDropped(t *testing.T) {
	a := &Adapter{}

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	// Seed only drop-list components. Dropped must include every
	// top-level dropped name (de-duplicated, sorted).
	files := map[string]string{
		"hooks/preflight.sh":  "#!/bin/sh",
		"hooks/postcommit.sh": "#!/bin/sh", // SAME `hooks` top-level — dedup
		".lsp.json":           `{"lsp": {}}`,
		"monitors/m1.json":    `{}`,
		"bin/helper.sh":       "#!/bin/sh",
		"settings.json":       `{}`,
		// Non-dropped keeper so the test asserts the diff, not nothing.
		"prompts/keep.md": "# keep",
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

	wantDropped := []string{".lsp.json", "bin", "hooks", "monitors", "settings.json"}
	if len(pw.Dropped) != len(wantDropped) {
		t.Errorf("Dropped count = %d, want %d; got=%v want=%v",
			len(pw.Dropped), len(wantDropped), pw.Dropped, wantDropped)
	}
	for i := range wantDropped {
		if i >= len(pw.Dropped) || pw.Dropped[i] != wantDropped[i] {
			t.Errorf("Dropped[%d] = %q, want %q", i, safeIdx(pw.Dropped, i), wantDropped[i])
		}
	}

	// The non-dropped keeper must be in ExtractedFiles.
	if len(pw.ExtractedFiles) != 1 || pw.ExtractedFiles[0] != filepath.FromSlash("prompts/keep.md") {
		t.Errorf("ExtractedFiles = %v, want [prompts/keep.md]", pw.ExtractedFiles)
	}
}

func TestTransformPlugin_EmptySrc_NoFilesNoDropped(t *testing.T) {
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
	if pw.Dropped != nil {
		t.Errorf("empty src → Dropped should be nil, got %v", pw.Dropped)
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

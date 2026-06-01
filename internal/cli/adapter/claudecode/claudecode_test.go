// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

func TestClaudeCode_ID(t *testing.T) {
	a := &Adapter{}
	if got := a.ID(); got != "claude-code" {
		t.Fatalf("ID() = %q, want %q", got, "claude-code")
	}
}

func TestClaudeCode_Aliases(t *testing.T) {
	a := &Adapter{}
	got := a.Aliases()
	if len(got) != 2 {
		t.Fatalf("Aliases() returned %d entries, want 2", len(got))
	}
	want := map[string]bool{"claude": true, "cc": true}
	for _, alias := range got {
		if !want[alias] {
			t.Errorf("Aliases() includes unexpected entry %q", alias)
		}
	}
}

func TestClaudeCode_Detect_NoClaudeDir_ZeroMatch(t *testing.T) {
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

func TestClaudeCode_Detect_OneSignal_LowConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "claude-code" {
		t.Errorf("Detect returned ID=%q, want %q", got.ID, "claude-code")
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("Detect with 1 signal returned Confidence=%v, want ConfidenceLow", got.Confidence)
	}
	if len(got.Reasons) != 1 {
		t.Errorf("Detect with 1 signal returned %d Reasons, want 1", len(got.Reasons))
	}
}

func TestClaudeCode_Detect_TwoSignals_MediumConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".claude", ".mcp.json"), []byte("{}"), 0o644); err != nil {
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

func TestClaudeCode_Detect_AllSignals_HighConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".claude", "agents"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".claude", ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".mcp.json"), []byte("{}"), 0o644); err != nil {
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

func TestRenderRuntime_EmitsMcpJson(t *testing.T) {
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
	if w.Path != ".claude/settings.json" {
		t.Errorf("FileWrite.Path = %q, want %q", w.Path, ".claude/settings.json")
	}
	if w.Merge != adapter.MergeDeep {
		t.Errorf("FileWrite.Merge = %v, want MergeDeep", w.Merge)
	}
	// Keys contributed: 2 MCP servers + 1 A2A agent = 3
	if len(w.Keys) != 3 {
		t.Errorf("FileWrite.Keys count = %d, want 3", len(w.Keys))
	}

	// Decode the content and verify shape.
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

func TestRenderRuntime_EmptyRuntime_EmitsEmptyMcpJson(t *testing.T) {
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

func TestTransformPlugin_PassThrough(t *testing.T) {
	a := &Adapter{}

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	// Seed src with a realistic Claude-format plugin tree.
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "caveman", "version": "1.0.0"}`,
		"agents/cave-agent.md":       "---\nname: cave\n---\nhello",
		"commands/grunt.md":          "# grunt",
		"prompts/intro.md":           "# intro",
		"skills/fire/skill.md":       "# fire",
		"hooks/preflight.sh":         "#!/bin/sh",
		".mcp.json":                  `{"mcpServers": {}}`,
		"subdir/nested/file.txt":     "nested",
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

	// Dropped MUST be nil — claude-code drops nothing per ADAPT-04
	// pass-through.
	if pw.Dropped != nil {
		t.Errorf("PluginWrite.Dropped = %v, want nil for claude-code pass-through", pw.Dropped)
	}

	// All 8 source files must appear in ExtractedFiles.
	if len(pw.ExtractedFiles) != len(files) {
		t.Errorf("ExtractedFiles count = %d, want %d", len(pw.ExtractedFiles), len(files))
	}
	expected := make([]string, 0, len(files))
	for k := range files {
		expected = append(expected, filepath.FromSlash(k))
	}
	sort.Strings(expected)
	got := append([]string{}, pw.ExtractedFiles...)
	sort.Strings(got)
	for i := range expected {
		if i >= len(got) || got[i] != expected[i] {
			t.Errorf("ExtractedFiles[%d] = %q, want %q", i, safeIdx(got, i), expected[i])
		}
	}

	// Every file's content must round-trip byte-for-byte.
	for rel, want := range files {
		fullDst := filepath.Join(dst, rel)
		actual, err := os.ReadFile(fullDst)
		if err != nil {
			t.Errorf("ReadFile %s: %v", rel, err)
			continue
		}
		if string(actual) != want {
			t.Errorf("file %s: content mismatch\ngot:  %q\nwant: %q", rel, actual, want)
		}
		// Mode must be 0644 (regular file).
		info, err := os.Stat(fullDst)
		if err != nil {
			t.Errorf("Stat %s: %v", rel, err)
			continue
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("file %s: mode = %o, want 0644", rel, info.Mode().Perm())
		}
	}
}

func safeIdx(s []string, i int) string {
	if i < 0 || i >= len(s) {
		return "<out-of-bounds>"
	}
	return s[i]
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
	if pw.Dropped != nil {
		t.Errorf("Dropped should be nil for empty src, got %v", pw.Dropped)
	}
	// dst should still exist (we MkdirAll it).
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
	// and is itself in the claudecode package — so init() has fired by
	// the time this test runs.
	got, ok := adapter.Lookup("claude-code")
	if !ok {
		t.Fatal("adapter.Lookup(\"claude-code\") returned false; init() did not register")
	}
	if got.ID() != "claude-code" {
		t.Errorf("Lookup returned adapter with ID %q, want %q", got.ID(), "claude-code")
	}

	// Aliases should also resolve.
	if _, ok := adapter.Lookup("claude"); !ok {
		t.Error("adapter.Lookup(\"claude\") returned false; alias did not register")
	}
	if _, ok := adapter.Lookup("CC"); !ok {
		t.Error("adapter.Lookup(\"CC\") returned false; case-insensitive alias missed")
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
		// Fall back to message check (the strerror text varies
		// slightly across libc versions but always contains the phrase).
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

// TestProjectionRules_Rows (D-11/D-02) asserts the claude-code ProjectionRules
// table: the four file-owned kinds stay verbatim MergeReplace with NO Transform
// (FMT-03 cut), AGENTS.md composites into CLAUDE.md, and mcp/**/* deep-merges
// into settingsJSONPath with the mcpDeepKeys Transform wired.
func TestProjectionRules_Rows(t *testing.T) {
	rules := (&Adapter{}).ProjectionRules()

	// Build a lookup keyed by FromGlob for membership + field assertions.
	type rowFields struct {
		to        string
		merge     adapter.MergeKind
		hasXform  bool
		seenTwice bool
	}
	byFrom := map[string]*rowFields{}
	for _, r := range rules {
		if _, dup := byFrom[r.FromGlob]; dup {
			byFrom[r.FromGlob].seenTwice = true
			continue
		}
		byFrom[r.FromGlob] = &rowFields{
			to:       r.ToGlob,
			merge:    r.Merge,
			hasXform: r.Transform != nil,
		}
	}

	// The four file-owned kinds: MergeReplace, no Transform (FMT-03 cut, D-02).
	fileKinds := map[string]string{
		"rules/**/*":    ".claude/rules/**/*",
		"commands/**/*": ".claude/commands/**/*",
		"agents/**/*":   ".claude/agents/**/*",
		"skills/**/*":   ".claude/skills/**/*",
	}
	for from, wantTo := range fileKinds {
		row, ok := byFrom[from]
		if !ok {
			t.Fatalf("ProjectionRules missing file-kind row %q", from)
		}
		if row.to != wantTo {
			t.Errorf("row %q ToGlob = %q, want %q", from, row.to, wantTo)
		}
		if row.merge != adapter.MergeReplace {
			t.Errorf("row %q Merge = %v, want MergeReplace", from, row.merge)
		}
		if row.hasXform {
			t.Errorf("row %q has a non-nil Transform; file kinds must be verbatim (FMT-03 cut)", from)
		}
	}

	// AGENTS.md -> CLAUDE.md as MergeComposite, no Transform.
	comp, ok := byFrom["AGENTS.md"]
	if !ok {
		t.Fatalf("ProjectionRules missing AGENTS.md composite row")
	}
	if comp.to != "CLAUDE.md" {
		t.Errorf("AGENTS.md ToGlob = %q, want CLAUDE.md", comp.to)
	}
	if comp.merge != adapter.MergeComposite {
		t.Errorf("AGENTS.md Merge = %v, want MergeComposite", comp.merge)
	}
	if comp.hasXform {
		t.Errorf("AGENTS.md composite row must have nil Transform")
	}

	// mcp/**/* -> settingsJSONPath as MergeDeep WITH a non-nil Transform.
	mcp, ok := byFrom["mcp/**/*"]
	if !ok {
		t.Fatalf("ProjectionRules missing mcp/**/* deep-merge row")
	}
	if mcp.to != settingsJSONPath {
		t.Errorf("mcp/**/* ToGlob = %q, want settingsJSONPath %q", mcp.to, settingsJSONPath)
	}
	if mcp.merge != adapter.MergeDeep {
		t.Errorf("mcp/**/* Merge = %v, want MergeDeep", mcp.merge)
	}
	if !mcp.hasXform {
		t.Errorf("mcp/**/* row must wire a non-nil Transform (mcpDeepKeys)")
	}
}

// TestMcpDeepKeys_Enumerates (D-09): a plugin mcp.json with two mcpServers
// enumerates sorted "mcpServers.<id>" keys and returns the input bytes exactly.
func TestMcpDeepKeys_Enumerates(t *testing.T) {
	in := []byte(`{"mcpServers":{"b":{"type":"http","url":"https://b"},"a":{"type":"http","url":"https://a"}}}`)

	out, keys, err := mcpDeepKeys("mcp/servers.json", in)
	if err != nil {
		t.Fatalf("mcpDeepKeys returned error: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("out bytes differ from in: got %q, want %q (no byte conversion)", out, in)
	}
	want := []string{"mcpServers.a", "mcpServers.b"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want sorted %v", keys, want)
	}
}

// TestMcpDeepKeys_A2A (D-09): input carrying both mcpServers and a2aAgents
// enumerates both families (sorted), bytes unchanged.
func TestMcpDeepKeys_A2A(t *testing.T) {
	in := []byte(`{"mcpServers":{"srv":{"type":"http"}},"a2aAgents":{"agt":{"type":"http"}}}`)

	out, keys, err := mcpDeepKeys("mcp/x.json", in)
	if err != nil {
		t.Fatalf("mcpDeepKeys returned error: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("out bytes differ from in: got %q", out)
	}
	want := []string{"a2aAgents.agt", "mcpServers.srv"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want sorted %v", keys, want)
	}
}

// TestMcpDeepKeys_Empty: an input with no mcpServers object returns empty keys,
// no error, and out==in.
func TestMcpDeepKeys_Empty(t *testing.T) {
	in := []byte(`{}`)

	out, keys, err := mcpDeepKeys("mcp/empty.json", in)
	if err != nil {
		t.Fatalf("mcpDeepKeys returned error on empty object: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("out bytes differ from in: got %q, want %q", out, in)
	}
	if len(keys) != 0 {
		t.Errorf("keys = %v, want empty", keys)
	}
}

// TestMcpDeepKeys_Malformed (T-02-07): invalid JSON returns a non-nil error so
// the projection aborts that file rather than silently dropping servers.
func TestMcpDeepKeys_Malformed(t *testing.T) {
	in := []byte(`{"mcpServers": this is not json}`)

	out, keys, err := mcpDeepKeys("mcp/bad.json", in)
	if err == nil {
		t.Fatalf("expected error on malformed JSON, got nil (out=%q keys=%v)", out, keys)
	}
	if out != nil || keys != nil {
		t.Errorf("on error want out==nil keys==nil, got out=%q keys=%v", out, keys)
	}
}

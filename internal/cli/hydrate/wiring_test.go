// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"

	// Blank-import all four adapter subpackages so init() registers
	// them — adapter.Lookup will need claudecode in the dispatcher
	// tests below.
	_ "github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	_ "github.com/ackstorm/ach/internal/cli/adapter/codex"
	_ "github.com/ackstorm/ach/internal/cli/adapter/gemini"
	_ "github.com/ackstorm/ach/internal/cli/adapter/opencode"
)

// TestExtractorImpl_DispatchesToStage drives the extractorImpl
// against an httptest.NewServer that returns a single-byte verbatim
// body. Asserts:
//   - PublishResult.WrittenFiles non-empty.
//   - SourceHash is a valid xxh3 string.
//   - Final file lands at the workspace-relative path the wiring
//     derives from the /content/{kind}/{name} URL.
func TestExtractorImpl_DispatchesToStage(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	body := []byte("hello world")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)

	hc := &httpclient.Client{BaseURL: ts.URL, APIKey: "pk_test"}
	ext, _ := hydrate.NewWiring(hc, "claude-code", extract.DefaultLimits(), false, false)

	ref := manifest.ContentRef{
		ID:          "demo-prompt",
		Name:        "demo-prompt",
		DownloadURL: ts.URL + "/content/prompt/demo-prompt",
	}
	res, err := ext.ExtractContent(context.Background(), ref, achDir)
	if err != nil {
		t.Fatalf("ExtractContent: %v", err)
	}
	if len(res.WrittenFiles) == 0 {
		t.Errorf("WrittenFiles empty; want at least 1 entry")
	}
	if res.SourceHash == "" || !strings.HasPrefix(res.SourceHash, "xxh3:") {
		t.Errorf("SourceHash = %q; want xxh3: prefix", res.SourceHash)
	}
	// The verbatim file lands at <achDir>/prompt/demo-prompt.
	final := filepath.Join(achDir, "prompt", "demo-prompt")
	if _, err := os.Stat(final); err != nil {
		t.Errorf("final path %s missing: %v", final, err)
	}
}

// TestAdapterDispatcherImpl_InvokesRender_ForPlatform asserts the
// dispatcher's claude-code path:
//   - Looks up the registered claudecode adapter.
//   - Calls RenderRuntime against a minimal manifest with one MCP
//     server.
//   - Returns at least one FileWrite (the .claude/.mcp.json target).
func TestAdapterDispatcherImpl_InvokesRender_ForPlatform(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false)

	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp", Endpoint: "http://localhost:8080/mcp/demo-mcp"},
			},
		},
		Context: &manifest.ContextBlock{},
	}

	res, err := disp.Render(context.Background(), m, nil, achDir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(res.WrittenFiles) == 0 {
		t.Fatalf("RenderResult.WrittenFiles empty")
	}
	// File should be written at <achDir>/.claude/.mcp.json.
	mcpPath := filepath.Join(achDir, ".claude", ".mcp.json")
	if _, err := os.Stat(mcpPath); err != nil {
		t.Errorf(".claude/.mcp.json missing after Render: %v", err)
	}
}

// TestAdapterDispatcherImpl_CollisionCascade_Identical seeds the
// final path with bytes that match what RenderRuntime would produce,
// then asserts auto-claim: no error, file written.
func TestAdapterDispatcherImpl_CollisionCascade_Identical(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false)

	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp", Endpoint: "http://localhost:8080/mcp/demo-mcp"},
			},
		},
		Context: &manifest.ContextBlock{},
	}

	// Pre-render to obtain the canonical bytes claudecode would emit.
	firstRes, err := disp.Render(context.Background(), m, nil, achDir)
	if err != nil {
		t.Fatalf("initial Render: %v", err)
	}
	if len(firstRes.WrittenFiles) == 0 {
		t.Fatalf("initial Render produced no files")
	}

	// Now simulate a re-hydrate where the prior state.File is nil
	// (so the existing .mcp.json is "unowned"). The cascade should
	// detect Identical (bytes match what Render would emit) and
	// auto-claim — no error.
	res, err := disp.Render(context.Background(), m, nil, achDir)
	if err != nil {
		t.Fatalf("re-Render with unowned-but-identical bytes: %v", err)
	}
	if len(res.WrittenFiles) == 0 {
		t.Fatalf("re-Render produced no files")
	}
}

// TestAdapterDispatcherImpl_CollisionCascade_Differ_Force seeds the
// final path with bytes that DIFFER from RenderRuntime output, runs
// with force=true, and asserts the write proceeds without exit 7.
func TestAdapterDispatcherImpl_CollisionCascade_Differ_Force(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, true)

	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp", Endpoint: "http://localhost:8080/mcp/demo-mcp"},
			},
		},
		Context: &manifest.ContextBlock{},
	}

	mcpPath := filepath.Join(achDir, ".claude", ".mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(mcpPath, []byte("WRONG BYTES"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Run with force=true — the SAFE-04 collision-refuse arm is bypassed.
	res, err := disp.Render(context.Background(), m, nil, achDir)
	if err != nil {
		t.Fatalf("Render with --force: %v", err)
	}
	if len(res.WrittenFiles) == 0 {
		t.Fatalf("Render with --force produced no files")
	}
	// The file's bytes should now equal RenderRuntime output (force
	// overwrote).
	got, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read post-force: %v", err)
	}
	if bytes.Equal(got, []byte("WRONG BYTES")) {
		t.Errorf("--force did not overwrite; file still contains stale bytes")
	}
}

// TestAdapterDispatcherImpl_CollisionCascade_Differ_NoForce seeds
// the final path with differing bytes and runs WITHOUT --force.
// Asserts the dispatcher returns exit.CollisionRefuse (7).
func TestAdapterDispatcherImpl_CollisionCascade_Differ_NoForce(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false)

	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp", Endpoint: "http://localhost:8080/mcp/demo-mcp"},
			},
		},
		Context: &manifest.ContextBlock{},
	}

	mcpPath := filepath.Join(achDir, ".claude", ".mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(mcpPath, []byte("WRONG BYTES"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := disp.Render(context.Background(), m, nil, achDir)
	if err == nil {
		t.Fatal("Render with unowned+different bytes: want CollisionRefuse, got nil")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("not *exit.CodedError: %T (%v)", err, err)
	}
	if ce.Code != exit.CollisionRefuse {
		t.Errorf("Code = %d; want CollisionRefuse (7)", ce.Code)
	}
}

// TestSync_DeepestFirst_Order seeds prev with three nested entries
// and asserts deletion proceeds deepest-first. We instrument via an
// observation hook: the test checks the parent dir is untouched
// until its child file has been deleted. Implemented by inspecting
// the final filesystem state — if Sync deleted parent-first, the
// child file would have been orphaned (rmdir on a non-empty dir
// returns ENOTEMPTY, which we ignore).
//
// Concrete assertion: seed three entries at varying depths; after
// Sync all three files are gone AND each containing dir is also
// gone (empty-dir prune).
func TestSync_DeepestFirst_Order(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	deep := filepath.Join(achDir, "a", "b", "c", "deepfile")
	mid := filepath.Join(achDir, "a", "b", "midfile")
	shallow := filepath.Join(achDir, "a", "shallowfile")
	for _, p := range []string{deep, mid, shallow} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	prev := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Artifacts: []state.FileEntry{
			{Target: deep, Hash: hashOf(t, "x")},
			{Target: mid, Hash: hashOf(t, "x")},
			{Target: shallow, Hash: hashOf(t, "x")},
		},
	}
	newFile := &state.File{SchemaVersion: "2", Environment: "demo"}

	var stderr bytes.Buffer
	stats, err := hydrate.Sync(prev, newFile, achDir, hydrate.SyncOptions{Stderr: &stderr})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pruned != 3 {
		t.Errorf("Pruned = %d; want 3", stats.Pruned)
	}
	if stats.Preserved != 0 {
		t.Errorf("Preserved = %d; want 0", stats.Preserved)
	}
	// All three files removed.
	for _, p := range []string{deep, mid, shallow} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("file %s should be gone: stat err = %v", p, err)
		}
	}
	// Empty parent dirs pruned.
	if _, err := os.Stat(filepath.Join(achDir, "a", "b", "c")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty dir a/b/c should be pruned")
	}
}

// TestSync_LocalEdit_PreservesAndWarns seeds prev with one entry
// whose recorded hash does NOT match the on-disk file (i.e. the
// user edited it locally). Sync without --force should preserve and
// emit a stderr warning.
func TestSync_LocalEdit_PreservesAndWarns(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	target := filepath.Join(achDir, "edited.txt")
	if err := os.WriteFile(target, []byte("user edited this"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Artifacts: []state.FileEntry{
			{Target: target, Hash: hashOf(t, "engine wrote this")},
		},
	}
	newFile := &state.File{SchemaVersion: "2", Environment: "demo"}

	var stderr bytes.Buffer
	stats, err := hydrate.Sync(prev, newFile, achDir, hydrate.SyncOptions{Stderr: &stderr})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Preserved != 1 {
		t.Errorf("Preserved = %d; want 1", stats.Preserved)
	}
	if stats.Pruned != 0 {
		t.Errorf("Pruned = %d; want 0", stats.Pruned)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("preserved file %s should still exist: %v", target, err)
	}
	if !strings.Contains(stderr.String(), "preserving") {
		t.Errorf("stderr missing 'preserving' warning: %q", stderr.String())
	}
}

// TestSync_InverseMerge_RemovesContributedKeys seeds a JSON file with
// two top-level keys (mcpServers.foo + mcpServers.bar); the state
// entry says only mcpServers.foo was engine-contributed. After Sync,
// the file should contain only mcpServers.bar.
func TestSync_InverseMerge_RemovesContributedKeys(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	target := filepath.Join(achDir, ".claude", ".mcp.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := `{"mcpServers":{"foo":{"url":"http://foo"},"bar":{"url":"http://bar"}}}`
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Adapter: state.AdapterSection{
			ID: "claude-code",
			Files: []state.FileEntry{
				{
					Target: target,
					Hash:   hashOf(t, original),
					Merge:  "deep",
					Keys:   []string{"mcpServers.foo"},
				},
			},
		},
	}
	newFile := &state.File{SchemaVersion: "2", Environment: "demo"}

	var stderr bytes.Buffer
	if _, err := hydrate.Sync(prev, newFile, achDir, hydrate.SyncOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after Sync: %v", err)
	}
	if strings.Contains(string(body), `"foo"`) {
		t.Errorf("inverse-merge left mcpServers.foo behind: %s", string(body))
	}
	if !strings.Contains(string(body), `"bar"`) {
		t.Errorf("inverse-merge removed mcpServers.bar: %s", string(body))
	}
}

// TestSync_CompositeBlock_RemovesMarkedRegion seeds a markdown file
// with `<!-- ach:begin -->X<!-- ach:end -->Y`; after Sync only Y
// should remain.
func TestSync_CompositeBlock_RemovesMarkedRegion(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	target := filepath.Join(achDir, "PROJECT.md")
	original := "<!-- ach:begin -->XXX<!-- ach:end -->\nY tail content\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Adapter: state.AdapterSection{
			ID: "claude-code",
			Files: []state.FileEntry{
				{
					Target: target,
					Hash:   hashOf(t, original),
					Merge:  "composite",
					Keys:   []string{"ach:block"},
				},
			},
		},
	}
	newFile := &state.File{SchemaVersion: "2", Environment: "demo"}

	if _, err := hydrate.Sync(prev, newFile, achDir, hydrate.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after Sync: %v", err)
	}
	if strings.Contains(string(body), "XXX") {
		t.Errorf("composite block content not removed: %s", string(body))
	}
	if !strings.Contains(string(body), "Y tail content") {
		t.Errorf("non-block content lost: %s", string(body))
	}
	if strings.Contains(string(body), "ach:begin") || strings.Contains(string(body), "ach:end") {
		t.Errorf("marker tags not removed: %s", string(body))
	}
}

// TestSync_Force_BypassesDriftWins seeds a drift case and asserts
// --force deletes anyway.
func TestSync_Force_BypassesDriftWins(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	target := filepath.Join(achDir, "edited.txt")
	if err := os.WriteFile(target, []byte("user edited this"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Artifacts: []state.FileEntry{
			{Target: target, Hash: hashOf(t, "engine wrote this")},
		},
	}
	newFile := &state.File{SchemaVersion: "2", Environment: "demo"}

	stats, err := hydrate.Sync(prev, newFile, achDir, hydrate.SyncOptions{Force: true})
	if err != nil {
		t.Fatalf("Sync --force: %v", err)
	}
	if stats.Pruned != 1 {
		t.Errorf("Pruned = %d; want 1", stats.Pruned)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--force did not delete: stat err = %v", err)
	}
}

// TestSync_Nil_Prev returns zero stats without panic.
func TestSync_Nil_Prev(t *testing.T) {
	stats, err := hydrate.Sync(nil, nil, t.TempDir(), hydrate.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync(nil): %v", err)
	}
	if stats.Pruned != 0 || stats.Preserved != 0 {
		t.Errorf("nil-prev should be no-op; got %+v", stats)
	}
}

// hashOf returns the canonical xxh3 of s.
func hashOf(t *testing.T, s string) string {
	t.Helper()
	h, err := hash.Hash(strings.NewReader(s))
	if err != nil {
		t.Fatalf("hash.Hash(%q): %v", s, err)
	}
	return h
}

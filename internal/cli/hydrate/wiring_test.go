// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	res, err := ext.ExtractContent(context.Background(), ref, achDir, nil)
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

	res, err := disp.Render(context.Background(), m, nil, achDir, achDir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(res.WrittenFiles) == 0 {
		t.Fatalf("RenderResult.WrittenFiles empty")
	}
	// File should be written at <achDir>/.claude/settings.json (the
	// surgical-merge target; toolRoot == achDir in this test).
	settingsPath := filepath.Join(achDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf(".claude/settings.json missing after Render: %v", err)
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
	firstRes, err := disp.Render(context.Background(), m, nil, achDir, achDir)
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
	res, err := disp.Render(context.Background(), m, nil, achDir, achDir)
	if err != nil {
		t.Fatalf("re-Render with unowned-but-identical bytes: %v", err)
	}
	if len(res.WrittenFiles) == 0 {
		t.Fatalf("re-Render produced no files")
	}
}

// TestAdapterDispatcherImpl_SurgicalMerge_PreservesUserKeys seeds the
// target config with a user-authored MCP server plus an unrelated setting
// and asserts both SURVIVE alongside ACH's entries after Render — the
// redesign's core coexistence guarantee (we never clobber a config we do
// not own).
func TestAdapterDispatcherImpl_SurgicalMerge_PreservesUserKeys(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false)
	m := dispMiniManifest()

	settingsPath := filepath.Join(achDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const seed = `{"mcpServers":{"user-srv":{"type":"http","url":"https://user"}},"permissions":{"allow":["Read"]}}`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := disp.Render(context.Background(), m, nil, achDir, achDir); err != nil {
		t.Fatalf("Render: %v", err)
	}

	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode merged: %v", err)
	}
	ms, _ := got["mcpServers"].(map[string]any)
	if _, ok := ms["user-srv"]; !ok {
		t.Errorf("user server clobbered; want preserved. mcpServers=%v", ms)
	}
	if _, ok := ms["demo-mcp"]; !ok {
		t.Errorf("ACH server not merged in. mcpServers=%v", ms)
	}
	if _, ok := got["permissions"]; !ok {
		t.Errorf("user 'permissions' key clobbered; want preserved")
	}
}

// TestAdapterDispatcherImpl_PerKeyDrift_RefusesUserEditOfOurKey renders
// once (establishing prior state), then simulates a user hand-edit of OUR
// key on disk. Re-rendering with that prior state and NO --force must
// refuse with exit.Drift (§8.4 LocalEditPreserve, applied per-key). With
// --force the edit is overwritten. The user's OTHER keys are never the
// subject of this comparison.
func TestAdapterDispatcherImpl_PerKeyDrift_RefusesUserEditOfOurKey(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()
	m := dispMiniManifest()

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false)
	first, err := disp.Render(context.Background(), m, nil, achDir, achDir)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	prior := dispPriorState("demo", first)

	settingsPath := filepath.Join(achDir, ".claude", "settings.json")
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read after first: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	doc["mcpServers"].(map[string]any)["demo-mcp"].(map[string]any)["url"] = "https://EDITED"
	edited, _ := json.Marshal(doc)
	if err := os.WriteFile(settingsPath, edited, 0o644); err != nil {
		t.Fatalf("write edit: %v", err)
	}

	// No --force → drift refuse (exit 2).
	if _, err := disp.Render(context.Background(), m, prior, achDir, achDir); err == nil {
		t.Fatal("re-render after user edit of our key: want drift error, got nil")
	} else {
		var ce *exit.CodedError
		if !errors.As(err, &ce) || ce.Code != exit.Drift {
			t.Fatalf("want exit.Drift, got %v (%T)", err, err)
		}
	}

	// --force → overwrite our key (edit gone).
	_, dispF := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, true)
	if _, err := dispF.Render(context.Background(), m, prior, achDir, achDir); err != nil {
		t.Fatalf("force re-render: %v", err)
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read after force: %v", err)
	}
	if bytes.Contains(after, []byte("EDITED")) {
		t.Errorf("--force did not overwrite our edited key")
	}
}

// dispMiniManifest is a minimal one-MCP-server manifest for the dispatcher
// tests.
func dispMiniManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp", Endpoint: "http://localhost:8080/mcp/demo-mcp"},
			},
		},
		Context: &manifest.ContextBlock{},
	}
}

// dispPriorState converts a RenderResult into the *state.File a subsequent
// hydrate would load (Adapter.Files mirror the just-written entries).
func dispPriorState(env string, r hydrate.RenderResult) *state.File {
	files := make([]state.FileEntry, 0, len(r.WrittenFiles))
	for _, w := range r.WrittenFiles {
		files = append(files, state.FileEntry{
			Target:     w.Target,
			Hash:       w.Hash,
			SourceHash: w.SourceHash,
			Merge:      w.Merge,
			Keys:       w.Keys,
		})
	}
	return &state.File{
		SchemaVersion: "2",
		Environment:   env,
		Adapter:       state.AdapterSection{ID: "claude-code", Files: files},
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

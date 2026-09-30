// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/state"
)

// executeUninstall drives newUninstallCmd through the shared
// executeCommand helper.
func executeUninstall(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	return executeCommand(t, newUninstallCmd(), args...)
}

// swapUninstallSyncFn replaces uninstallSyncFn for the lifetime of t and
// records the (prev, scopedEmpty) inputs the leaf passes through.
type recordedSync struct {
	called      bool
	prev        *state.File
	scopedEmpty *state.File
	achDir      string
	toolRoot    string
	opts        hydrate.SyncOptions
}

func swapUninstallSyncFn(t *testing.T, rec *recordedSync, stats hydrate.SyncStats, err error) {
	t.Helper()
	prevFn := uninstallSyncFn
	uninstallSyncFn = func(
		prev, newFile *state.File, achDir, toolRoot string, opts hydrate.SyncOptions,
	) (hydrate.SyncStats, error) {
		rec.called = true
		rec.prev = prev
		rec.scopedEmpty = newFile
		rec.achDir = achDir
		rec.toolRoot = toolRoot
		rec.opts = opts
		return stats, err
	}
	t.Cleanup(func() { uninstallSyncFn = prevFn })
}

// writeState writes a minimal valid v2 state.json into the per-environment
// <dir>/.ach/<f.Environment>/ layout (env required — namespaced per spec §8.1).
func writeState(t *testing.T, workspace string, f *state.File) string {
	t.Helper()
	if f.Environment == "" {
		t.Fatalf("writeState: state.File needs a non-empty Environment (namespaced layout)")
	}
	achDir := filepath.Join(workspace, ".ach", f.Environment)
	if err := os.MkdirAll(achDir, 0o755); err != nil {
		t.Fatalf("mkdir .ach/%s: %v", f.Environment, err)
	}
	statePath := filepath.Join(achDir, "state.json")
	if err := state.Save(statePath, f); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return statePath
}

func TestUninstall_DryRunWritesNothing(t *testing.T) {
	ws := t.TempDir()
	prev := &state.File{
		SchemaVersion: "3",
		Environment:   "prod",
		Plugins: []state.FileEntry{
			{Target: "CLAUDE.md", Hash: "h", SourceHash: "s", Merge: "composite", Keys: []string{"foo"}},
		},
		RuntimeFiles: []state.FileEntry{
			{Target: ".mcp.json", Hash: "h2", SourceHash: "s2", Merge: "deep", Keys: []string{"mcpServers.x"}},
		},
	}
	statePath := writeState(t, ws, prev)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state before: %v", err)
	}

	var rec recordedSync
	swapUninstallSyncFn(t, &rec, hydrate.SyncStats{Pruned: 1, Preserved: 0}, nil)

	stdout, _, code, err := executeUninstall(t, "--dir", ws, "prod", "--dry-run")
	if err != nil || code != exit.OK {
		t.Fatalf("dry-run uninstall: code=%d err=%v", code, err)
	}
	if !rec.called {
		t.Fatal("Sync seam was not invoked")
	}
	// Default scope removes everything hydrate wrote: runtime AND context.
	if n := len(state.WalkEntries(rec.scopedEmpty)); n != 0 {
		t.Fatalf("default scope must empty every bucket, got %d rows", n)
	}
	// prev passed through verbatim.
	if rec.prev == nil || len(rec.prev.Plugins) != 1 {
		t.Fatalf("prev not passed through to Sync: %+v", rec.prev)
	}
	// --dry-run must leave state.json byte-identical.
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("--dry-run mutated state.json\nbefore: %s\nafter:  %s", before, after)
	}
	if !strings.Contains(stdout, "dry-run") {
		t.Fatalf("expected dry-run notice in stdout, got %q", stdout)
	}
}

func TestUninstall_MissingStateExitsZero(t *testing.T) {
	ws := t.TempDir() // no .ach/state.json

	var rec recordedSync
	swapUninstallSyncFn(t, &rec, hydrate.SyncStats{}, nil)

	stdout, _, code, err := executeUninstall(t, "--dir", ws, "prod")
	if err != nil {
		t.Fatalf("missing-state uninstall returned error: %v", err)
	}
	if code != exit.OK {
		t.Fatalf("missing-state uninstall code=%d, want 0", code)
	}
	if rec.called {
		t.Fatal("Sync must not be invoked when nothing is installed")
	}
	if !strings.Contains(stdout, "nothing installed") {
		t.Fatalf("expected 'nothing installed' message, got %q", stdout)
	}
}

func TestUninstall_RemovedFlags(t *testing.T) {
	c := newUninstallCmd()
	for _, name := range []string{"include-runtime", "target", "output"} {
		if c.Flags().Lookup(name) != nil {
			t.Errorf("--%s must be removed from env uninstall", name)
		}
	}
	if f := c.Flags().ShorthandLookup("g"); f == nil || f.Name != "global" {
		t.Errorf("-g must be the shorthand for --global, got %v", f)
	}
	if c.Flags().Lookup("dir") == nil {
		t.Error("--dir must be registered")
	}
}

func TestUninstall_FullTeardownRemovesState(t *testing.T) {
	ws := t.TempDir()
	prev := &state.File{
		SchemaVersion: "3",
		Environment:   "prod",
		Plugins:       []state.FileEntry{{Target: "CLAUDE.md", Hash: "h", SourceHash: "s"}},
		RuntimeFiles:  []state.FileEntry{{Target: ".mcp.json", Hash: "h2", SourceHash: "s2"}},
	}
	statePath := writeState(t, ws, prev)

	var rec recordedSync
	swapUninstallSyncFn(t, &rec, hydrate.SyncStats{Pruned: 2}, nil)

	// Default scope = full teardown (symmetric with hydrate).
	_, _, code, err := executeUninstall(t, "--dir", ws, "prod")
	if err != nil || code != exit.OK {
		t.Fatalf("full teardown: code=%d err=%v", code, err)
	}
	// Full teardown: scopedEmpty has zero entries everywhere.
	if len(state.WalkEntries(rec.scopedEmpty)) != 0 {
		t.Fatalf("full teardown scopedEmpty must be empty, got %+v", rec.scopedEmpty)
	}
	if rec.opts.Force {
		t.Fatal("Force should be false without --force")
	}
	// D-28: state.json removed after full teardown.
	if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
		t.Fatalf("state.json must be removed after full teardown, stat err=%v", statErr)
	}
}

func TestUninstall_ScopedRewritesStateRetainingSurvivors(t *testing.T) {
	ws := t.TempDir()
	prev := &state.File{
		SchemaVersion: "3",
		Environment:   "prod",
		Plugins:       []state.FileEntry{{Target: "CLAUDE.md", Hash: "h", SourceHash: "s"}},
		RuntimeFiles:  []state.FileEntry{{Target: ".mcp.json", Hash: "h2", SourceHash: "s2"}},
	}
	statePath := writeState(t, ws, prev)

	var rec recordedSync
	swapUninstallSyncFn(t, &rec, hydrate.SyncStats{Pruned: 1}, nil)

	// --only-runtime: removes runtime, retains context.
	_, _, code, err := executeUninstall(t, "--dir", ws, "prod", "--only-runtime")
	if err != nil || code != exit.OK {
		t.Fatalf("scoped uninstall: code=%d err=%v", code, err)
	}
	// state.json still present and now holds only the survivor (context) rows.
	got, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if got == nil {
		t.Fatal("scoped uninstall must retain state.json")
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("context survivor rows must be retained in state, got %d", len(got.Plugins))
	}
	if len(got.RuntimeFiles) != 0 {
		t.Fatalf("runtime rows must be dropped from state, got %d", len(got.RuntimeFiles))
	}
}

func TestUninstall_ForceFlagThreadsThrough(t *testing.T) {
	ws := t.TempDir()
	writeState(t, ws, &state.File{
		SchemaVersion: "3",
		Environment:   "prod",
		Plugins:       []state.FileEntry{{Target: "CLAUDE.md", Hash: "h", SourceHash: "s"}},
	})

	var rec recordedSync
	swapUninstallSyncFn(t, &rec, hydrate.SyncStats{}, nil)

	_, _, code, err := executeUninstall(t, "--dir", ws, "prod", "--force")
	if err != nil || code != exit.OK {
		t.Fatalf("force uninstall: code=%d err=%v", code, err)
	}
	if !rec.opts.Force {
		t.Fatal("--force must set SyncOptions.Force=true")
	}
}

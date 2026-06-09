// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestResolvePath_Workspace asserts the workspace-scope formula (now
// per-environment namespaced): <workspaceCwd>/.ach/<environment>/state.json.
func TestResolvePath_Workspace(t *testing.T) {
	cwd := t.TempDir()
	got, err := state.ResolvePath(cwd, "demo", false)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	want := filepath.Join(cwd, ".ach", "demo", "state.json")
	if got != want {
		t.Fatalf("ResolvePath(workspace) = %q, want %q", got, want)
	}
}

// TestResolvePath_WorkspaceNamespacesEnvironment asserts workspace scope is now
// per-environment: distinct environments resolve to distinct dirs, and an empty
// environment is rejected (ErrInvalidPath) — the namespace segment is required.
func TestResolvePath_WorkspaceNamespacesEnvironment(t *testing.T) {
	cwd := t.TempDir()
	a, err := state.ResolvePath(cwd, "env-a", false)
	if err != nil {
		t.Fatalf("ResolvePath env-a: %v", err)
	}
	b, err := state.ResolvePath(cwd, "env-b", false)
	if err != nil {
		t.Fatalf("ResolvePath env-b: %v", err)
	}
	if a == b {
		t.Fatalf("distinct environments must resolve to distinct dirs; both = %q", a)
	}
	if _, err := state.ResolvePath(cwd, "", false); !errors.Is(err, state.ErrInvalidPath) {
		t.Fatalf("empty environment in workspace scope: err = %v, want ErrInvalidPath", err)
	}
}

// TestResolvePath_WorkspaceEmptyCwd_Errors asserts the defensive
// guard against an empty workspaceCwd in workspace scope. Maps to
// ErrInvalidPath so callers can distinguish from os errors.
func TestResolvePath_WorkspaceEmptyCwd_Errors(t *testing.T) {
	_, err := state.ResolvePath("", "", false)
	if !errors.Is(err, state.ErrInvalidPath) {
		t.Fatalf("ResolvePath(\"\", \"\", false): err = %v, want errors.Is(..., ErrInvalidPath)", err)
	}
}

// TestResolvePath_Global asserts the global-scope formula:
// $HOME/.ach/<environment>/state.json. Uses t.Setenv to inject HOME.
func TestResolvePath_Global(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// UserConfigDir / UserHomeDir on linux/darwin honor HOME; on
	// windows the lookup uses USERPROFILE. Skip on platforms where
	// HOME is not the source of truth.
	if home, err := os.UserHomeDir(); err != nil || home != tmp {
		t.Skipf("UserHomeDir did not honor HOME on this platform (got %q, err %v)", home, err)
	}

	got, err := state.ResolvePath("", "engineering-prod", true)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	want := filepath.Join(tmp, ".ach", "engineering-prod", "state.json")
	if got != want {
		t.Fatalf("ResolvePath(global) = %q, want %q", got, want)
	}
}

// TestResolvePath_GlobalEmptyEnv_Errors asserts the §8.1 invariant:
// global scope MUST have a non-empty environment (the directory is
// namespaced by Environment per spec). An empty env in global scope
// surfaces ErrInvalidPath.
func TestResolvePath_GlobalEmptyEnv_Errors(t *testing.T) {
	_, err := state.ResolvePath("", "", true)
	if !errors.Is(err, state.ErrInvalidPath) {
		t.Fatalf("ResolvePath(\"\", \"\", true): err = %v, want errors.Is(..., ErrInvalidPath)", err)
	}
}

// TestResolvePlatformPath asserts the per-platform state file lives beside the
// env state under <ach-dir>/state-<platform>.json (same <ach-dir> as the legacy
// path, so the content cache is shared), and that a bad platform segment is
// rejected (defense-in-depth against a crafted id reaching the filename).
func TestResolvePlatformPath(t *testing.T) {
	cwd := t.TempDir()
	got, err := state.ResolvePlatformPath(cwd, "demo", "claude-code", false)
	if err != nil {
		t.Fatalf("ResolvePlatformPath: %v", err)
	}
	want := filepath.Join(cwd, ".ach", "demo", "state-claude-code.json")
	if got != want {
		t.Fatalf("ResolvePlatformPath = %q, want %q", got, want)
	}
	// <ach-dir> must match the legacy env path's dir (shared content cache).
	base, _ := state.ResolvePath(cwd, "demo", false)
	if filepath.Dir(got) != filepath.Dir(base) {
		t.Errorf("per-platform state dir %q != env dir %q", filepath.Dir(got), filepath.Dir(base))
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../x", `a\b`} {
		if _, err := state.ResolvePlatformPath(cwd, "demo", bad, false); !errors.Is(err, state.ErrInvalidPath) {
			t.Errorf("ResolvePlatformPath(platform=%q): err=%v, want ErrInvalidPath", bad, err)
		}
	}
}

// TestListStatePaths enumerates every per-platform state-<platform>.json plus a
// legacy flat state.json for an environment — this is what `env status` /
// `env uninstall` use so a multi-target hydrate's targets are all visible and
// none is orphaned.
func TestListStatePaths(t *testing.T) {
	cwd := t.TempDir()
	envDir := filepath.Join(cwd, ".ach", "demo")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two per-platform states + a legacy flat state + an unrelated file.
	for _, n := range []string{"state-codex.json", "state-opencode.json", "state.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(envDir, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := state.ListStatePaths(cwd, "demo", false)
	if err != nil {
		t.Fatalf("ListStatePaths: %v", err)
	}
	want := []string{
		filepath.Join(envDir, "state-codex.json"),
		filepath.Join(envDir, "state-opencode.json"),
		filepath.Join(envDir, "state.json"),
	}
	if len(got) != len(want) {
		t.Fatalf("ListStatePaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListStatePaths[%d] = %q, want %q (sorted)", i, got[i], want[i])
		}
	}

	// Absent env dir → empty, no error.
	empty, err := state.ListStatePaths(cwd, "nonesuch", false)
	if err != nil {
		t.Fatalf("ListStatePaths(absent): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListStatePaths(absent) = %v, want empty", empty)
	}
}

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

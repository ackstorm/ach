// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestResolvePath_Workspace asserts the workspace-scope formula:
// <workspaceCwd>/.ach/state.json.
func TestResolvePath_Workspace(t *testing.T) {
	cwd := t.TempDir()
	got, err := state.ResolvePath(cwd, "", false)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	want := filepath.Join(cwd, ".ach", "state.json")
	if got != want {
		t.Fatalf("ResolvePath(workspace) = %q, want %q", got, want)
	}
}

// TestResolvePath_WorkspaceIgnoresEnvironment asserts that the
// environment string is irrelevant in workspace scope: workspace
// state lives under the workspace's own .ach/ dir, not under any
// per-Environment global path.
func TestResolvePath_WorkspaceIgnoresEnvironment(t *testing.T) {
	cwd := t.TempDir()
	withEnv, err := state.ResolvePath(cwd, "engineering-prod", false)
	if err != nil {
		t.Fatalf("ResolvePath with env: %v", err)
	}
	without, err := state.ResolvePath(cwd, "", false)
	if err != nil {
		t.Fatalf("ResolvePath without env: %v", err)
	}
	if withEnv != without {
		t.Fatalf("workspace scope must ignore environment\n withEnv: %q\n no env:  %q", withEnv, without)
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

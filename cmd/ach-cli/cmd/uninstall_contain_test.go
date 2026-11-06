// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/state"
)

func containWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func assertPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestUninstall_UnsafeStateTargetsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target func(victim string) string
		args   []string
	}{
		{name: "absolute", target: func(v string) string { return v }},
		{name: "dotdot", target: func(v string) string {
			return filepath.Join("..", filepath.Base(filepath.Dir(v)), "victim")
		}},
		{name: "dry run", target: func(v string) string { return v }, args: []string{"--dry-run"}},
		{name: "force", target: func(v string) string { return v }, args: []string{"--force"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			victim := filepath.Join(outside, "victim")
			containWrite(t, victim, "x")
			writeState(t, ws, &state.File{SchemaVersion: "3", Environment: "prod",
				Plugins: []state.FileEntry{{Target: tc.target(victim), Merge: "replace"}}})

			_, _, code, err := executeUninstall(t, append([]string{"--dir", ws, "prod"}, tc.args...)...)
			if !errors.Is(err, state.ErrNotContained) || code != exit.ConfigFile {
				t.Fatalf("uninstall: code=%d err=%v, want ConfigFile + ErrNotContained", code, err)
			}
			assertPresent(t, victim)
		})
	}
}

// TestUninstall_UnsafeStateStopsEveryTarget: one bad per-platform state file
// stops the run before a sound sibling is torn down.
func TestUninstall_UnsafeStateStopsEveryTarget(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	own := filepath.Join(ws, ".claude", "own.md")
	containWrite(t, own, "o")
	victim := filepath.Join(outside, "victim")
	containWrite(t, victim, "x")
	sound := writeState(t, ws, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: ".claude/own.md", Merge: "replace"}}})
	if err := state.Save(filepath.Join(filepath.Dir(sound), "state-gemini-cli.json"), &state.File{
		SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: victim, Merge: "replace"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := executeUninstall(t, "--dir", ws, "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
	assertPresent(t, own)
	assertPresent(t, victim)
	assertPresent(t, sound)
}

func TestUninstall_SymlinkedTargetDirRefused(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "x.md")
	containWrite(t, victim, "x")
	containLink(t, outside, filepath.Join(ws, ".claude"))
	statePath := writeState(t, ws, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: ".claude/x.md", Merge: "replace"}}})

	if _, _, _, err := executeUninstall(t, "--dir", ws, "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
	assertPresent(t, victim)
	assertPresent(t, statePath)
}

func TestUninstall_SymlinkedLockRefused(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	statePath := writeState(t, ws, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: ".claude/x.md", Merge: "replace"}}})
	created := filepath.Join(outside, "created")
	containLink(t, created, filepath.Join(filepath.Dir(statePath), "lock"))

	if _, _, _, err := executeUninstall(t, "--dir", ws, "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("lock open reached outside the workspace: %v", err)
	}
}

func TestUninstall_SymlinkedAchDirRefused(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	writeState(t, outside, &state.File{SchemaVersion: "3", Environment: "prod"})
	containLink(t, filepath.Join(outside, ".ach"), filepath.Join(ws, ".ach"))

	if _, _, _, err := executeUninstall(t, "--dir", ws, "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
	entries, err := os.ReadDir(filepath.Join(outside, ".ach", "prod"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("outside .ach changed: %v %v", entries, err)
	}
}

// relativeRepo creates <parent>/repo, enters <parent>, and returns the
// absolute workspace plus a directory outside it.
func relativeRepo(t *testing.T) (abs, outside string) {
	t.Helper()
	parent := t.TempDir()
	abs = filepath.Join(parent, "repo")
	if err := os.Mkdir(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)
	return abs, t.TempDir()
}

func TestUninstall_RelativeDir_SymlinkedTargetDirRefused(t *testing.T) {
	for _, dir := range []string{"repo", "./repo", "repo/../repo"} {
		t.Run(dir, func(t *testing.T) {
			abs, outside := relativeRepo(t)
			victim := filepath.Join(outside, "x.md")
			containWrite(t, victim, "x")
			containLink(t, outside, filepath.Join(abs, ".claude"))
			statePath := writeState(t, abs, &state.File{SchemaVersion: "3", Environment: "prod",
				Plugins: []state.FileEntry{{Target: ".claude/x.md", Merge: "replace"}}})

			if _, _, _, err := executeUninstall(t, "--dir", dir, "prod"); !errors.Is(err, state.ErrNotContained) {
				t.Fatalf("uninstall = %v, want ErrNotContained", err)
			}
			assertPresent(t, victim)
			assertPresent(t, statePath)
		})
	}
}

func TestUninstall_RelativeDir_SymlinkedLockRefused(t *testing.T) {
	abs, outside := relativeRepo(t)
	statePath := writeState(t, abs, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: ".claude/x.md", Merge: "replace"}}})
	created := filepath.Join(outside, "created")
	containLink(t, created, filepath.Join(filepath.Dir(statePath), "lock"))

	if _, _, _, err := executeUninstall(t, "--dir", "repo", "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("lock open reached outside the workspace: %v", err)
	}
}

func TestUninstall_RelativeDir_SymlinkedAchDirRefused(t *testing.T) {
	abs, outside := relativeRepo(t)
	writeState(t, outside, &state.File{SchemaVersion: "3", Environment: "prod"})
	containLink(t, filepath.Join(outside, ".ach"), filepath.Join(abs, ".ach"))

	if _, _, _, err := executeUninstall(t, "--dir", "repo", "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
}

func TestUninstall_RelativeDir_UnsafeStateTargetRefused(t *testing.T) {
	abs, outside := relativeRepo(t)
	victim := filepath.Join(outside, "victim")
	containWrite(t, victim, "x")
	writeState(t, abs, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: victim, Merge: "replace"}}})

	_, _, code, err := executeUninstall(t, "--dir", "repo", "prod")
	if !errors.Is(err, state.ErrNotContained) || code != exit.ConfigFile {
		t.Fatalf("uninstall: code=%d err=%v, want ConfigFile + ErrNotContained", code, err)
	}
	assertPresent(t, victim)
}

// TestUninstall_RelativeDir_SoundWorkspaceRemoves: the checks do not refuse a
// relative workspace with nothing wrong in it.
func TestUninstall_RelativeDir_SoundWorkspaceRemoves(t *testing.T) {
	abs, _ := relativeRepo(t)
	own := filepath.Join(abs, ".claude", "own.md")
	containWrite(t, own, "o")
	writeState(t, abs, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: ".claude/own.md", Merge: "replace"}}})

	if _, _, code, err := executeUninstall(t, "--dir", "repo", "prod"); err != nil || code != exit.OK {
		t.Fatalf("uninstall: code=%d err=%v", code, err)
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatalf("own file still present: %v", err)
	}
}

// TestUninstall_Global_SymlinkedDirStillWorks: dotfile managers link ~/.claude.
func TestUninstall_Global_SymlinkedDirStillWorks(t *testing.T) {
	home, dotfiles := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	managed := filepath.Join(dotfiles, "x.md")
	containWrite(t, managed, "x")
	containLink(t, dotfiles, filepath.Join(home, ".claude"))
	writeState(t, home, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: ".claude/x.md", Merge: "replace"}}})

	if _, _, code, err := executeUninstall(t, "-g", "prod"); err != nil || code != exit.OK {
		t.Fatalf("global uninstall: code=%d err=%v", code, err)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed file still present: %v", err)
	}
}

func TestUninstall_Global_UnsafeStateTargetRefused(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	victim := filepath.Join(outside, "victim")
	containWrite(t, victim, "x")
	writeState(t, home, &state.File{SchemaVersion: "3", Environment: "prod",
		Plugins: []state.FileEntry{{Target: victim, Merge: "replace"}}})

	if _, _, _, err := executeUninstall(t, "-g", "prod"); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("uninstall = %v, want ErrNotContained", err)
	}
	assertPresent(t, victim)
}

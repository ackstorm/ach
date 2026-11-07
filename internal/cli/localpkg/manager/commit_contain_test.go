// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/cli/state"
)

// linkedClaude returns a project root whose .claude is a symlink to an
// outside directory, and that directory.
func linkedClaude(t *testing.T) (root, outside string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func assertUntouched(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q untouched", path, got, err, want)
	}
}

func TestCommit_RefusesSymlinkedDir(t *testing.T) {
	root, outside := linkedClaude(t)
	target := filepath.Join(outside, "skills", "a.md")

	_, err := Commit(root, false, "p", []PlannedWrite{
		{Path: ".claude/skills/a.md", Content: []byte("x"), Merge: adapter.MergeReplace},
	})
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	if _, serr := os.Stat(target); !os.IsNotExist(serr) {
		t.Fatalf("outside file created: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(outside, "skills")); !os.IsNotExist(serr) {
		t.Fatalf("outside dir created: %v", serr)
	}
}

func TestCommit_RefusesOverwriteThroughSymlink(t *testing.T) {
	root, outside := linkedClaude(t)
	target := filepath.Join(outside, "a.md")
	if err := os.WriteFile(target, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Commit(root, false, "p", []PlannedWrite{
		{Path: ".claude/a.md", Content: []byte("x"), Merge: adapter.MergeReplace},
	})
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	assertUntouched(t, target, "precious")
}

func TestCommit_RefusesSymlinkedMergeTarget(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "mcp.json")
	if err := os.WriteFile(target, []byte(`{"mine":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	_, err := Commit(root, false, "p", []PlannedWrite{{
		Path: ".mcp.json", Content: []byte(`{"mcpServers":{"x":{}}}`),
		Merge: adapter.MergeDeep, Keys: []string{"mcpServers.x"},
	}})
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	assertUntouched(t, target, `{"mine":1}`)
}

func TestUninstall_RefusesSymlinkedDir(t *testing.T) {
	root, outside := linkedClaude(t)
	if err := os.MkdirAll(filepath.Join(outside, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "skills", "a.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs := []store.FileRec{{RelPath: ".claude/skills/a.md", Hash: hash.HashBytes([]byte("x"))}}

	_, err := Uninstall(root, false, recs)
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	assertUntouched(t, target, "x")
	if _, serr := os.Stat(filepath.Join(outside, "skills")); serr != nil {
		t.Fatalf("outside dir pruned: %v", serr)
	}
}

func TestCommitUninstall_GlobalAllowsLinkedRoot(t *testing.T) {
	root, outside := linkedClaude(t)
	recs, err := Commit(root, true, "p", []PlannedWrite{
		{Path: ".claude/a.md", Content: []byte("x"), Merge: adapter.MergeReplace},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	assertUntouched(t, filepath.Join(outside, "a.md"), "x")
	if _, err := Uninstall(root, true, recs); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(outside, "a.md")); !os.IsNotExist(serr) {
		t.Fatalf("file not removed: %v", serr)
	}
}

// linkedAndSafe returns a root with a real dir "safe" and a link "linked" to an
// outside dir.
func linkedAndSafe(t *testing.T) (root, outside string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestCommit_RefusalWritesNothing(t *testing.T) {
	root, _ := linkedAndSafe(t)
	_, err := Commit(root, false, "p", []PlannedWrite{
		{Path: "safe/a.md", Content: []byte("x"), Merge: adapter.MergeReplace},
		{Path: "linked/b.md", Content: []byte("x"), Merge: adapter.MergeReplace},
	})
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	if _, serr := os.Stat(filepath.Join(root, "safe", "a.md")); !os.IsNotExist(serr) {
		t.Fatalf("earlier file was written: %v", serr)
	}
}

func TestUninstall_RefusalDeletesNothing(t *testing.T) {
	root, outside := linkedAndSafe(t)
	for _, p := range []string{filepath.Join(root, "safe", "a.md"), filepath.Join(outside, "b.md")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := hash.HashBytes([]byte("x"))
	recs := []store.FileRec{{RelPath: "safe/a.md", Hash: h}, {RelPath: "linked/b.md", Hash: h}}
	if _, err := Uninstall(root, false, recs); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	assertUntouched(t, filepath.Join(root, "safe", "a.md"), "x")
	assertUntouched(t, filepath.Join(outside, "b.md"), "x")
}

func TestUninstallPlan_RefusesSymlinkedDir(t *testing.T) {
	root, _ := linkedClaude(t)
	recs := []store.FileRec{{RelPath: ".claude/a.md", Hash: hash.HashBytes([]byte("x"))}}
	if _, err := UninstallPlan(root, false, recs); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("err = %v, want ErrNotContained", err)
	}
	if _, err := UninstallPlan(root, true, recs); err != nil {
		t.Fatalf("global plan: %v", err)
	}
}

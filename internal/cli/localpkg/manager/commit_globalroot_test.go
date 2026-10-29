// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// TestCommit_AbsoluteDest proves Commit writes a redirected ABSOLUTE
// destination outside root, and records it verbatim so Uninstall finds it.
func TestCommit_AbsoluteDest(t *testing.T) {
	root := t.TempDir()
	cfg := t.TempDir()

	dest := filepath.Join(cfg, "skills", "demo", "SKILL.md")
	writes := []PlannedWrite{{Path: dest, Content: []byte("# demo\n"), Merge: adapter.MergeReplace}}

	recs, err := Commit(root, "demo", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, serr := os.Stat(dest); serr != nil {
		t.Fatalf("expected file at %s: %v", dest, serr)
	}
	if len(recs) != 1 || recs[0].RelPath != dest {
		t.Fatalf("recs = %+v; want one RelPath == %q", recs, dest)
	}

	// Round-trip: Uninstall must resolve the absolute RelPath, not join it.
	skipped, uerr := Uninstall(root, recs)
	if uerr != nil {
		t.Fatalf("Uninstall: %v", uerr)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v; want none", skipped)
	}
	if _, serr := os.Stat(dest); !os.IsNotExist(serr) {
		t.Fatalf("expected %s removed; stat err = %v", dest, serr)
	}
}

// TestUninstallPlan_AbsoluteDest: the --dry-run preview must agree with the act.
func TestUninstallPlan_AbsoluteDest(t *testing.T) {
	root := t.TempDir()
	cfg := t.TempDir()
	dest := filepath.Join(cfg, "a.md")
	body := []byte("x")
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := Commit(root, "demo", []PlannedWrite{
		{Path: dest, Content: body, Merge: adapter.MergeReplace},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan, perr := UninstallPlan(root, recs)
	if perr != nil {
		t.Fatalf("UninstallPlan: %v", perr)
	}
	if len(plan) != 1 || plan[0].Op != "remove" {
		t.Fatalf("plan = %+v; want one remove", plan)
	}
}

// TestCommit_RelativeDestUnchanged pins the default (non-redirected) behavior.
func TestCommit_RelativeDestUnchanged(t *testing.T) {
	root := t.TempDir()
	recs, err := Commit(root, "demo", []PlannedWrite{
		{Path: ".claude/skills/a.md", Content: []byte("x"), Merge: adapter.MergeReplace},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, ".claude/skills/a.md")); serr != nil {
		t.Fatalf("expected file under root: %v", serr)
	}
	if recs[0].RelPath != ".claude/skills/a.md" {
		t.Errorf("RelPath = %q; want the relative form", recs[0].RelPath)
	}
	_ = store.FileRec{}
}

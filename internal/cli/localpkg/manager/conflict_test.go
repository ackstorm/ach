// SPDX-License-Identifier: Apache-2.0

package manager_test

import (
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/localpkg/manager"
)

func TestResolveConflicts(t *testing.T) {
	const target = ".claude/commands/review.md"
	owners := map[string]string{target: "gemini@ackstorm"}

	replace := []manager.PlannedWrite{{Path: target, Merge: adapter.MergeReplace}}

	t.Run("no owner passes through", func(t *testing.T) {
		out, acts, err := manager.ResolveConflicts(replace, map[string]string{}, manager.ConflictNamespace, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Path != target || len(acts) != 0 {
			t.Errorf("expected passthrough no-action; got out=%v acts=%v", out, acts)
		}
	})

	t.Run("additive merge never clashes", func(t *testing.T) {
		deep := []manager.PlannedWrite{{Path: ".claude/settings.json", Merge: adapter.MergeDeep}}
		ownersDeep := map[string]string{".claude/settings.json": "other@r"}
		out, acts, err := manager.ResolveConflicts(deep, ownersDeep, manager.ConflictNamespace, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Path != ".claude/settings.json" || len(acts) != 0 {
			t.Errorf("deep-merge write must pass through unchanged; got out=%v acts=%v", out, acts)
		}
	})

	t.Run("overwrite keeps write, no action", func(t *testing.T) {
		out, acts, err := manager.ResolveConflicts(replace, owners, manager.ConflictOverwrite, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Path != target || len(acts) != 0 {
			t.Errorf("overwrite: want write kept + no action; got out=%v acts=%v", out, acts)
		}
	})

	t.Run("skip drops write, records action", func(t *testing.T) {
		out, acts, err := manager.ResolveConflicts(replace, owners, manager.ConflictSkip, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 0 {
			t.Errorf("skip: want 0 writes; got %v", out)
		}
		if len(acts) != 1 || acts[0].Policy != manager.ConflictSkip || acts[0].Owner != "gemini@ackstorm" {
			t.Errorf("skip: want one skip action owned by gemini@ackstorm; got %v", acts)
		}
	})

	t.Run("refuse errors", func(t *testing.T) {
		_, _, err := manager.ResolveConflicts(replace, owners, manager.ConflictRefuse, "codex", "")
		if err == nil {
			t.Fatal("refuse: want error on clash, got nil")
		}
	})

	t.Run("namespace renames leaf, records action", func(t *testing.T) {
		out, acts, err := manager.ResolveConflicts(replace, owners, manager.ConflictNamespace, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		want := ".claude/commands/codex-review.md"
		if len(out) != 1 || out[0].Path != want {
			t.Errorf("namespace: want renamed to %q; got %v", want, out)
		}
		if len(acts) != 1 || acts[0].NewPath != want || acts[0].Path != target {
			t.Errorf("namespace: want action %s→%s; got %v", target, want, acts)
		}
	})
}

func TestResolveConflicts_AbsoluteRootContainingSkills(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	target := filepath.Join(root, ".claude", "commands", "review.md")
	owners := map[string]string{target: "other@repo"}
	writes := []manager.PlannedWrite{{Path: target, Merge: adapter.MergeReplace}}

	out, actions, err := manager.ResolveConflicts(writes, owners, manager.ConflictNamespace, "codex", root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".claude", "commands", "codex-review.md")
	if len(out) != 1 || out[0].Path != want {
		t.Fatalf("namespaced path = %v; want %q", out, want)
	}
	if len(actions) != 1 || actions[0].NewPath != want {
		t.Fatalf("actions = %v; want new path %q", actions, want)
	}
}

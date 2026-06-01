// SPDX-License-Identifier: Apache-2.0

package route

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

// writeTree materializes a map of rel-path -> contents under a fresh
// temp dir and returns the dir. Parent dirs are created as needed.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	return root
}

// TestProject_NestedSubdirPreserved is the must-have: a recursive **
// glob remap preserves nested subdirs.
func TestProject_NestedSubdirPreserved(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/foo/bar.md": "RULE BODY",
	})
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	fws, dropped, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(dropped) != 0 {
		t.Fatalf("expected no drops, got %v", dropped)
	}
	if len(fws) != 1 {
		t.Fatalf("expected 1 FileWrite, got %d (%v)", len(fws), fws)
	}
	got := fws[0]
	if got.Path != ".claude/rules/foo/bar.md" {
		t.Errorf("Path: got %q, want %q", got.Path, ".claude/rules/foo/bar.md")
	}
	if string(got.Content) != "RULE BODY" {
		t.Errorf("Content: got %q, want %q", string(got.Content), "RULE BODY")
	}
	if got.Merge != adapter.MergeReplace {
		t.Errorf("Merge: got %v, want %v", got.Merge, adapter.MergeReplace)
	}
}

// TestProject_DropDedup: an unrouted top-level kind appears exactly once
// in the dropped slice even when multiple files exist under it.
func TestProject_DropDedup(t *testing.T) {
	src := writeTree(t, map[string]string{
		"hooks/a.json":  "{}",
		"hooks/b.json":  "{}",
		"rules/keep.md": "x",
	})
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	fws, dropped, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(fws) != 1 {
		t.Fatalf("expected 1 routed file, got %d", len(fws))
	}
	want := []string{"hooks"}
	if !reflect.DeepEqual(dropped, want) {
		t.Errorf("dropped: got %v, want %v", dropped, want)
	}
}

// TestProject_Deterministic: output []FileWrite is sorted by Path and
// dropped []string is sorted; two runs yield identical slices.
func TestProject_Deterministic(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/z.md":    "z",
		"rules/a.md":    "a",
		"rules/m/n.md":  "n",
		"hooks/x.json":  "{}",
		"unknown/y.txt": "y",
	})
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	fws1, dropped1, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project run 1: %v", err)
	}
	fws2, dropped2, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project run 2: %v", err)
	}

	if !reflect.DeepEqual(fws1, fws2) {
		t.Errorf("FileWrites not deterministic:\n run1=%v\n run2=%v", fws1, fws2)
	}
	if !reflect.DeepEqual(dropped1, dropped2) {
		t.Errorf("dropped not deterministic:\n run1=%v\n run2=%v", dropped1, dropped2)
	}

	// Assert FileWrites are sorted by Path.
	for i := 1; i < len(fws1); i++ {
		if fws1[i-1].Path > fws1[i].Path {
			t.Errorf("FileWrites not sorted by Path at %d: %q > %q", i, fws1[i-1].Path, fws1[i].Path)
		}
	}
	// Assert dropped is sorted.
	for i := 1; i < len(dropped1); i++ {
		if dropped1[i-1] > dropped1[i] {
			t.Errorf("dropped not sorted at %d: %q > %q", i, dropped1[i-1], dropped1[i])
		}
	}
}

// TestProject_EmptyDirTree: a dir-only tree yields no FileWrite, no panic.
func TestProject_EmptyDirTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rules", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	fws, dropped, err := Project(rules, root, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(fws) != 0 {
		t.Errorf("expected no FileWrites for dir-only tree, got %v", fws)
	}
	if len(dropped) != 0 {
		t.Errorf("expected no drops for dir-only tree, got %v", dropped)
	}
}

// TestProject_ProvenanceGate: a rule gated "claude-plugin" matches only
// when source == "claude-plugin"; Phase-1 both arms copy bytes verbatim.
func TestProject_ProvenanceGate(t *testing.T) {
	files := map[string]string{
		"agents/x.md": "AGENT BODY",
	}

	gated := []Rule{
		{FromGlob: "agents/**/*.md", ToGlob: ".claude/agents/**/*.md", Merge: adapter.MergeReplace, ProvenanceGate: "claude-plugin"},
	}

	// source == gate: rule applies.
	srcMatch := writeTree(t, files)
	fwsMatch, _, err := Project(gated, srcMatch, "claude-plugin")
	if err != nil {
		t.Fatalf("Project (match): %v", err)
	}
	if len(fwsMatch) != 1 {
		t.Fatalf("gated rule with matching source: expected 1 FileWrite, got %d", len(fwsMatch))
	}
	if string(fwsMatch[0].Content) != "AGENT BODY" {
		t.Errorf("gated arm content not verbatim: got %q", string(fwsMatch[0].Content))
	}

	// source != gate: rule does NOT apply, agents dropped.
	srcMiss := writeTree(t, files)
	fwsMiss, droppedMiss, err := Project(gated, srcMiss, "other-source")
	if err != nil {
		t.Fatalf("Project (miss): %v", err)
	}
	if len(fwsMiss) != 0 {
		t.Fatalf("gated rule with non-matching source: expected 0 FileWrites, got %d", len(fwsMiss))
	}
	if len(droppedMiss) != 1 || droppedMiss[0] != "agents" {
		t.Errorf("expected agents dropped on gate miss, got %v", droppedMiss)
	}

	// Ungated rule + verbatim assertion: content identical to gated arm.
	ungated := []Rule{
		{FromGlob: "agents/**/*.md", ToGlob: ".claude/agents/**/*.md", Merge: adapter.MergeReplace},
	}
	srcUngated := writeTree(t, files)
	fwsUngated, _, err := Project(ungated, srcUngated, "anything")
	if err != nil {
		t.Fatalf("Project (ungated): %v", err)
	}
	if len(fwsUngated) != 1 {
		t.Fatalf("ungated rule: expected 1 FileWrite, got %d", len(fwsUngated))
	}
	if string(fwsUngated[0].Content) != string(fwsMatch[0].Content) {
		t.Errorf("Phase-1 transform-vs-passthrough arms differ: gated=%q ungated=%q",
			string(fwsMatch[0].Content), string(fwsUngated[0].Content))
	}
}

// TestProject_TraversalGuard (T-01-01): a crafted ToGlob that would
// produce a dest path escaping the destination root makes Project error.
func TestProject_TraversalGuard(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/evil.md": "x",
	})
	// ToGlob anchored on a parent-escape: the recursive-glob remap will
	// produce a dest beginning with "../", which the guard must reject.
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: "../escape/**/*.md", Merge: adapter.MergeReplace},
	}

	_, _, err := Project(rules, src, "")
	if err == nil {
		t.Fatalf("expected traversal guard error for ToGlob escaping dest root, got nil")
	}
}

// TestResolveTarget_NestedAndGuard exercises the remap helper directly.
func TestResolveTarget_NestedAndGuard(t *testing.T) {
	dest, err := resolveRecursiveGlobTarget("rules/**/*.md", ".claude/rules/**/*.md", "rules/foo/bar.md")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dest != ".claude/rules/foo/bar.md" {
		t.Errorf("dest: got %q, want %q", dest, ".claude/rules/foo/bar.md")
	}

	// Absolute ToGlob anchor must be rejected.
	if _, err := resolveRecursiveGlobTarget("rules/**/*.md", "/etc/**/*.md", "rules/foo/bar.md"); err == nil {
		t.Errorf("expected error for absolute dest anchor")
	}

	// `..` in the resulting dest must be rejected.
	if _, err := resolveRecursiveGlobTarget("rules/**/*.md", "../x/**/*.md", "rules/foo/bar.md"); err == nil {
		t.Errorf("expected error for `..` dest segment")
	}
}

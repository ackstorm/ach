// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/state"
)

// TestPublishFile_RedirectedGlobalDest proves the whole --global chain: with
// CLAUDE_CONFIG_DIR pointing outside $HOME, publishFile writes into the
// redirected dir (NOT under toolRoot) and records the absolute destination so
// Sync/pruneMissing can find it again.
func TestPublishFile_RedirectedGlobalDest(t *testing.T) {
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	d := &adapterDispatcherImpl{platformID: "claude-code", global: true}

	fw := adapter.FileWrite{
		Path:    ".claude/skills/demo/SKILL.md",
		Content: []byte("# demo\n"),
		Merge:   adapter.MergeReplace,
	}
	fw.Path = adapter.RemapGlobalPath(d.platformID, home, fw.Path)

	entry, err := d.publishFile(fw, nil, home)
	if err != nil {
		t.Fatalf("publishFile: %v", err)
	}

	wantAbs := filepath.Join(cfg, "skills/demo/SKILL.md")
	if _, serr := os.Stat(wantAbs); serr != nil {
		t.Fatalf("expected file at redirected dest %s: %v", wantAbs, serr)
	}
	if _, serr := os.Stat(filepath.Join(home, ".claude/skills/demo/SKILL.md")); serr == nil {
		t.Fatal("file was ALSO written under $HOME; redirect did not take effect")
	}
	if entry.Target != wantAbs {
		t.Errorf("recorded Target = %q; want the absolute redirected dest %q", entry.Target, wantAbs)
	}
}

// TestPublishFile_NoRedirectStaysUnderHome pins the unchanged default path.
func TestPublishFile_NoRedirectStaysUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	d := &adapterDispatcherImpl{platformID: "claude-code", global: true}
	fw := adapter.FileWrite{
		Path:    ".claude/skills/demo/SKILL.md",
		Content: []byte("# demo\n"),
		Merge:   adapter.MergeReplace,
	}
	fw.Path = adapter.RemapGlobalPath(d.platformID, home, fw.Path)

	entry, err := d.publishFile(fw, nil, home)
	if err != nil {
		t.Fatalf("publishFile: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(home, ".claude/skills/demo/SKILL.md")); serr != nil {
		t.Fatalf("expected file under $HOME: %v", serr)
	}
	if entry.Target != ".claude/skills/demo/SKILL.md" {
		t.Errorf("recorded Target = %q; want the relative form", entry.Target)
	}
}

// TestPruneMissing_AbsoluteTarget proves pruneMissing resolves a redirected
// absolute Target against itself, not against base — a present file must NOT
// be pruned just because it lives outside toolRoot.
func TestPruneMissing_AbsoluteTarget(t *testing.T) {
	base := t.TempDir()
	cfg := t.TempDir()
	live := filepath.Join(cfg, "skills", "a.md")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &commit{}
	entries := []state.FileEntry{
		{Target: live},
		{Target: filepath.Join(cfg, "skills", "gone.md")},
	}
	kept, pruned := c.pruneMissing(entries, base, 0)
	if pruned != 1 {
		t.Errorf("pruned = %d; want 1", pruned)
	}
	if len(kept) != 1 || kept[0].Target != live {
		t.Errorf("kept = %+v; want only %q", kept, live)
	}
}

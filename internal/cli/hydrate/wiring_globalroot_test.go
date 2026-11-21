// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
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

// TestPublishFile_AntigravityGlobal proves --global writes every .agents/ path
// of the antigravity adapter under ~/.gemini/config, records relative targets,
// and that the recorded state passes ValidateState.
func TestPublishFile_AntigravityGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	d := &adapterDispatcherImpl{platformID: "antigravity", global: true}
	writes := []adapter.FileWrite{
		{Path: ".agents/skills/demo/SKILL.md", Content: []byte("# demo\n"), Merge: adapter.MergeReplace},
		{Path: ".agents/mcp_config.json", Content: []byte(`{"mcpServers":{"echo":{"serverUrl":"https://x"}}}`),
			Merge: adapter.MergeDeep, Keys: []string{"mcpServers.echo"}},
	}
	f := &state.File{SchemaVersion: "3", Environment: "demo"}
	for _, fw := range writes {
		fw.Path = adapter.RemapGlobalPath(d.platformID, home, fw.Path)
		entry, err := d.publishFile(fw, nil, home)
		if err != nil {
			t.Fatalf("publishFile %s: %v", fw.Path, err)
		}
		f.Plugins = append(f.Plugins, state.FileEntry{Target: entry.Target, Merge: "replace"})
	}

	for _, rel := range []string{".gemini/config/skills/demo/SKILL.md", ".gemini/config/mcp_config.json"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("expected %s under $HOME: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); err == nil {
		t.Error("antigravity wrote under ~/.agents")
	}
	if err := ValidateState(f, "antigravity", home, true); err != nil {
		t.Errorf("ValidateState: %v", err)
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

// TestSync_RedirectedDestDoesNotPruneOutsideToolRoot proves --sync cleanup
// cannot remove the configured external adapter root after deleting its last
// managed file. The external root is a destination boundary, not a directory
// owned by the hydrate engine.
func TestSync_RedirectedDestDoesNotPruneOutsideToolRoot(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	redirectedRoot := t.TempDir()
	target := filepath.Join(redirectedRoot, "skills", "gone.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("managed\n")
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}

	prev := &state.File{
		SchemaVersion: "3",
		Plugins:       []state.FileEntry{{Target: target, Hash: hash.HashBytes(content)}},
	}
	if _, err := Sync(prev, &state.File{SchemaVersion: "3"}, achDir, toolRoot, SyncOptions{Global: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("managed file should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(redirectedRoot); err != nil {
		t.Fatalf("redirected root must remain intact, stat err = %v", err)
	}
}

func TestSkillNameFromPath_RedirectedRootContainingSkills(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	path := filepath.Join(root, ".claude", "skills", "demo", "SKILL.md")
	if got, want := skillNameFromPath(path, root), "demo"; got != want {
		t.Fatalf("skillNameFromPath(%q, %q) = %q; want %q", path, root, got, want)
	}
}

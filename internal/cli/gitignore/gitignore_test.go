// SPDX-License-Identifier: Apache-2.0

package gitignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTopLevelEntry(t *testing.T) {
	cases := []struct{ in, want string }{
		{".claude/agents/x.md", ".claude/"},
		{".codex/config.toml", ".codex/"},
		{".mcp.json", ".mcp.json"},
		{"./.claude/skills/s/SKILL.md", ".claude/"},
		{".ach/", ".ach/"},
		{"", ""},
		{"/abs/path", ""},
	}
	for _, c := range cases {
		if got := TopLevelEntry(c.in); got != c.want {
			t.Errorf("TopLevelEntry(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// readGI is a tiny helper returning the .gitignore contents in dir.
func readGI(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	return string(b)
}

func TestEnsure_CreatesBlockWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	wrote, err := Ensure(dir, []string{".mcp.json", ".claude/", ".ach/"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !wrote {
		t.Fatal("Ensure: want wrote=true on first call")
	}
	want := beginMarker + "\n.ach/\n.claude/\n.mcp.json\n" + endMarker + "\n"
	if got := readGI(t, dir); got != want {
		t.Errorf("gitignore = %q\nwant %q", got, want)
	}
	// Mode is non-secret 0644 (gitignore is meant to be committed).
	info, _ := os.Stat(filepath.Join(dir, ".gitignore"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestEnsure_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, []string{".claude/", ".ach/"}); err != nil {
		t.Fatal(err)
	}
	first := readGI(t, dir)
	wrote, err := Ensure(dir, []string{".ach/", ".claude/"})
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("Ensure: want wrote=false on identical second call")
	}
	if got := readGI(t, dir); got != first {
		t.Errorf("file changed on idempotent call:\n got %q\nwant %q", got, first)
	}
}

func TestEnsure_AccumulatesUnion(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, []string{".claude/", ".mcp.json"}); err != nil {
		t.Fatal(err)
	}
	wrote, err := Ensure(dir, []string{".codex/"})
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("want wrote=true when adding a new entry")
	}
	want := beginMarker + "\n.claude/\n.codex/\n.mcp.json\n" + endMarker + "\n"
	if got := readGI(t, dir); got != want {
		t.Errorf("gitignore = %q\nwant %q", got, want)
	}
}

func TestEnsure_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	existing := "# my stuff\nnode_modules/\n*.log\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, []string{".claude/"}); err != nil {
		t.Fatal(err)
	}
	want := existing + "\n" + beginMarker + "\n.claude/\n" + endMarker + "\n"
	if got := readGI(t, dir); got != want {
		t.Errorf("gitignore = %q\nwant %q", got, want)
	}
}

// TestEnsure_PreservesContentAfterBlock proves a re-run keeps user lines that
// follow our block untouched and re-merges in place (no duplicate block).
func TestEnsure_PreservesContentAfterBlock(t *testing.T) {
	dir := t.TempDir()
	seed := "top.txt\n" + beginMarker + "\n.claude/\n" + endMarker + "\nbottom.txt\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, []string{".codex/"}); err != nil {
		t.Fatal(err)
	}
	want := "top.txt\n" + beginMarker + "\n.claude/\n.codex/\n" + endMarker + "\nbottom.txt\n"
	if got := readGI(t, dir); got != want {
		t.Errorf("gitignore = %q\nwant %q", got, want)
	}
}

func TestEnsure_EmptyEntriesNoOp(t *testing.T) {
	dir := t.TempDir()
	wrote, err := Ensure(dir, []string{"", "  "})
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("want wrote=false for all-empty entries")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("gitignore should not have been created; stat err=%v", err)
	}
}

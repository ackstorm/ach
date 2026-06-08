// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// ---------- remapGlobalPath --------------------------------------------------

func TestRemapGlobalPath(t *testing.T) {
	tests := []struct {
		adapterID string
		path      string
		want      string
	}{
		{"opencode", ".opencode/opencode.json", ".config/opencode/opencode.json"},
		{"opencode", ".opencode/agents/a.md", ".config/opencode/agents/a.md"},
		{"opencode", ".opencode/commands/foo.md", ".config/opencode/commands/foo.md"},
		{"opencode", ".claude/settings.json", ".claude/settings.json"}, // not opencode prefix
		{"claude-code", ".opencode/agents/a.md", ".opencode/agents/a.md"},
		{"codex", ".codex/config.toml", ".codex/config.toml"},
	}
	for _, tc := range tests {
		got := remapGlobalPath(tc.adapterID, tc.path)
		if got != tc.want {
			t.Errorf("remapGlobalPath(%q, %q) = %q; want %q", tc.adapterID, tc.path, got, tc.want)
		}
	}
}

// ---------- Commit: MergeReplace ---------------------------------------------

func TestCommit_MergeReplace(t *testing.T) {
	root := t.TempDir()
	content := []byte("hello localpkg")
	writes := []PlannedWrite{
		{Path: "subdir/file.txt", Content: content, Merge: adapter.MergeReplace},
	}

	recs, err := Commit(root, false, "claude-code", "test-plugin", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 FileRec, got %d", len(recs))
	}

	abs := filepath.Join(root, "subdir/file.txt")
	ondisk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(ondisk) != string(content) {
		t.Errorf("on-disk content %q != written %q", ondisk, content)
	}

	wantHash := hash.HashBytes(content)
	if recs[0].Hash != wantHash {
		t.Errorf("FileRec.Hash = %q; want %q", recs[0].Hash, wantHash)
	}
	if recs[0].RelPath != "subdir/file.txt" {
		t.Errorf("FileRec.RelPath = %q; want %q", recs[0].RelPath, "subdir/file.txt")
	}
}

// ---------- Commit: MergeDeep ------------------------------------------------

func TestCommit_MergeDeep(t *testing.T) {
	root := t.TempDir()

	// Pre-create settings.json with an existing key.
	settingsDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := []byte(`{"existing": 1}`)
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), existing, 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	ours := []byte(`{"mcpServers": {"x": {}}}`)
	writes := []PlannedWrite{
		{
			Path:    ".claude/settings.json",
			Content: ours,
			Merge:   adapter.MergeDeep,
			Keys:    []string{"mcpServers.x"},
		},
	}

	recs, err := Commit(root, false, "claude-code", "test-plugin", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 FileRec, got %d", len(recs))
	}

	finalBytes, err := os.ReadFile(filepath.Join(settingsDir, "settings.json"))
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}

	var merged map[string]any
	if err := json.Unmarshal(finalBytes, &merged); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	// Both the original "existing" key and the new "mcpServers" key must be present.
	if _, ok := merged["existing"]; !ok {
		t.Error("existing key lost after MergeDeep")
	}
	if mcpRaw, ok := merged["mcpServers"]; !ok {
		t.Error("mcpServers key missing after MergeDeep")
	} else {
		mcp, ok := mcpRaw.(map[string]any)
		if !ok || mcp["x"] == nil {
			t.Errorf("mcpServers.x missing: %v", mcpRaw)
		}
	}

	// FileRec.Hash must equal the hash of the FINAL on-disk bytes (merged),
	// not the hash of the ours input.
	wantHash := hash.HashBytes(finalBytes)
	if recs[0].Hash != wantHash {
		t.Errorf("FileRec.Hash = %q; want hash of final bytes %q", recs[0].Hash, wantHash)
	}
	// The ours-only hash must differ (regression guard: we're hashing merged).
	oursHash := hash.HashBytes(ours)
	if recs[0].Hash == oursHash {
		t.Error("FileRec.Hash equals hash of ours-only bytes — MergeDeep hash not tracking merged content")
	}
}

// ---------- Commit: MergeComposite -------------------------------------------

func TestCommit_MergeComposite(t *testing.T) {
	root := t.TempDir()
	block := []byte("<!-- ach:begin:myplugin -->\nsome content\n<!-- ach:end:myplugin -->\n")
	writes := []PlannedWrite{
		{Path: "CLAUDE.md", Content: block, Merge: adapter.MergeComposite},
	}

	recs, err := Commit(root, false, "claude-code", "myplugin", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 FileRec, got %d", len(recs))
	}

	finalBytes, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}

	if !strings.Contains(string(finalBytes), "<!-- ach:begin:myplugin -->") {
		t.Errorf("begin marker not found in CLAUDE.md: %s", finalBytes)
	}
	if !strings.Contains(string(finalBytes), "<!-- ach:end:myplugin -->") {
		t.Errorf("end marker not found in CLAUDE.md: %s", finalBytes)
	}

	wantHash := hash.HashBytes(finalBytes)
	if recs[0].Hash != wantHash {
		t.Errorf("FileRec.Hash = %q; want %q", recs[0].Hash, wantHash)
	}
}

// ---------- Commit: opencode global remap ------------------------------------

func TestCommit_OpencodeGlobalRemap(t *testing.T) {
	root := t.TempDir()
	content := []byte("# agent\n")
	writes := []PlannedWrite{
		{Path: ".opencode/agents/a.md", Content: content, Merge: adapter.MergeReplace},
	}

	recs, err := Commit(root, true, "opencode", "myplugin", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 FileRec, got %d", len(recs))
	}

	// Must be written to the remapped path, not the original.
	remappedAbs := filepath.Join(root, ".config/opencode/agents/a.md")
	ondisk, err := os.ReadFile(remappedAbs)
	if err != nil {
		t.Fatalf("read remapped path: %v", err)
	}
	if string(ondisk) != string(content) {
		t.Errorf("remapped content %q != %q", ondisk, content)
	}

	// Original path must NOT exist.
	originalAbs := filepath.Join(root, ".opencode/agents/a.md")
	if _, err := os.Stat(originalAbs); !os.IsNotExist(err) {
		t.Errorf("original path %s should not exist", originalAbs)
	}

	if recs[0].RelPath != ".config/opencode/agents/a.md" {
		t.Errorf("RelPath = %q; want .config/opencode/agents/a.md", recs[0].RelPath)
	}
}

// ---------- Uninstall --------------------------------------------------------

func TestUninstall_RemovesFiles(t *testing.T) {
	root := t.TempDir()
	content := []byte("to be removed")
	writes := []PlannedWrite{
		{Path: "subdir/a.txt", Content: content, Merge: adapter.MergeReplace},
		{Path: "subdir/b.txt", Content: content, Merge: adapter.MergeReplace},
	}

	recs, err := Commit(root, false, "claude-code", "myplugin", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	skipped, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped: %v", skipped)
	}

	// Files must be gone.
	for _, rec := range recs {
		abs := filepath.Join(root, rec.RelPath)
		if _, err := os.Stat(abs); !os.IsNotExist(err) {
			t.Errorf("file %s still exists after Uninstall", rec.RelPath)
		}
	}

	// Empty subdir must be pruned.
	subdirAbs := filepath.Join(root, "subdir")
	if _, err := os.Stat(subdirAbs); !os.IsNotExist(err) {
		t.Errorf("empty subdir %s still exists after Uninstall", subdirAbs)
	}
}

func TestUninstall_SkipsModifiedFiles(t *testing.T) {
	root := t.TempDir()
	content := []byte("original content")
	writes := []PlannedWrite{
		{Path: "file.txt", Content: content, Merge: adapter.MergeReplace},
	}

	recs, err := Commit(root, false, "claude-code", "myplugin", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Modify the file so the hash no longer matches.
	abs := filepath.Join(root, "file.txt")
	if err := os.WriteFile(abs, []byte("user modified content"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	skipped, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(skipped) != 1 || skipped[0] != "file.txt" {
		t.Errorf("skipped = %v; want [file.txt]", skipped)
	}

	// Modified file must still exist.
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("modified file should still exist: %v", err)
	}
}

func TestUninstall_MissingFileIsNoOp(t *testing.T) {
	root := t.TempDir()
	files := []store.FileRec{
		{RelPath: "nonexistent/file.txt", Hash: "xxh3:deadbeef"},
	}
	skipped, err := Uninstall(root, files)
	if err != nil {
		t.Fatalf("Uninstall with missing file: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v; want []", skipped)
	}
}

func TestUninstall_EmptyDirPruned(t *testing.T) {
	root := t.TempDir()
	content := []byte("content")

	// Create a nested structure: root/a/b/file.txt
	if err := os.MkdirAll(filepath.Join(root, "a/b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	abs := filepath.Join(root, "a/b/file.txt")
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	recs := []store.FileRec{
		{RelPath: "a/b/file.txt", Hash: hash.HashBytes(content)},
	}

	_, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// Both a/b and a should be pruned since they are now empty.
	for _, dir := range []string{"a/b", "a"} {
		if _, err := os.Stat(filepath.Join(root, dir)); !os.IsNotExist(err) {
			t.Errorf("dir %s should have been pruned", dir)
		}
	}
}

func TestUninstall_NonEmptyDirPreserved(t *testing.T) {
	root := t.TempDir()
	content := []byte("content")

	// Create root/subdir/file1.txt and root/subdir/file2.txt (user's own)
	subdirAbs := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdirAbs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdirAbs, "file1.txt"), content, 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	userFile := filepath.Join(subdirAbs, "userfile.txt")
	if err := os.WriteFile(userFile, []byte("user data"), 0o644); err != nil {
		t.Fatalf("write userfile: %v", err)
	}

	recs := []store.FileRec{
		{RelPath: "subdir/file1.txt", Hash: hash.HashBytes(content)},
	}

	_, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// subdir must still exist because userfile.txt is in it.
	if _, err := os.Stat(subdirAbs); err != nil {
		t.Errorf("subdir should still exist (non-empty): %v", err)
	}
	// userfile.txt must not be touched.
	if _, err := os.Stat(userFile); err != nil {
		t.Errorf("user file should still exist: %v", err)
	}
}

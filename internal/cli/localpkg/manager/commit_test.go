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
	// Commit receives RAW (unwrapped) content — Commit must wrap it in the
	// per-id composite markers itself (Bug I-a: it previously passed raw
	// content straight to WriteComposite, so no markers were written).
	content := []byte("some content")
	writes := []PlannedWrite{
		{Path: "CLAUDE.md", Content: content, Merge: adapter.MergeComposite, Keys: []string{"myplugin"}},
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
	// The raw content must be inside the markers exactly once (no double-wrap).
	if c := strings.Count(string(finalBytes), "<!-- ach:begin:myplugin -->"); c != 1 {
		t.Errorf("begin marker count = %d; want 1 (no double-wrap): %s", c, finalBytes)
	}
	if !strings.Contains(string(finalBytes), "\nsome content\n") {
		t.Errorf("raw content not wrapped verbatim: %s", finalBytes)
	}

	wantHash := hash.HashBytes(finalBytes)
	if recs[0].Hash != wantHash {
		t.Errorf("FileRec.Hash = %q; want %q", recs[0].Hash, wantHash)
	}
	// Merge metadata must be recorded for inverse-merge on uninstall.
	if recs[0].Merge != "composite" {
		t.Errorf("FileRec.Merge = %q; want composite", recs[0].Merge)
	}
	if len(recs[0].Keys) != 1 || recs[0].Keys[0] != "myplugin" {
		t.Errorf("FileRec.Keys = %v; want [myplugin]", recs[0].Keys)
	}
}

// TestCommit_MergeComposite_CompositeIDFallback asserts that when the rule
// carries no Keys, Commit falls back to the caller's compositeID for the
// marker id and records it.
func TestCommit_MergeComposite_CompositeIDFallback(t *testing.T) {
	root := t.TempDir()
	writes := []PlannedWrite{
		{Path: "CLAUDE.md", Content: []byte("body"), Merge: adapter.MergeComposite},
	}
	recs, err := Commit(root, false, "claude-code", "fallback-id", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	finalBytes, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(finalBytes), "<!-- ach:begin:fallback-id -->") {
		t.Errorf("fallback compositeID marker not found: %s", finalBytes)
	}
	if len(recs[0].Keys) != 1 || recs[0].Keys[0] != "fallback-id" {
		t.Errorf("FileRec.Keys = %v; want [fallback-id]", recs[0].Keys)
	}
}

// TestCommit_MergeDeep_RecordsMergeMeta asserts the deep case records the
// contributed dotted keys for inverse-merge.
func TestCommit_MergeDeep_RecordsMergeMeta(t *testing.T) {
	root := t.TempDir()
	writes := []PlannedWrite{
		{
			Path:    ".claude/settings.json",
			Content: []byte(`{"mcpServers":{"demo":{}}}`),
			Merge:   adapter.MergeDeep,
			Keys:    []string{"mcpServers.demo"},
		},
	}
	recs, err := Commit(root, false, "claude-code", "demo", writes)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if recs[0].Merge != "deep" {
		t.Errorf("FileRec.Merge = %q; want deep", recs[0].Merge)
	}
	if len(recs[0].Keys) != 1 || recs[0].Keys[0] != "mcpServers.demo" {
		t.Errorf("FileRec.Keys = %v; want [mcpServers.demo]", recs[0].Keys)
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

// ---------- Uninstall: composite inverse-merge -------------------------------

// TestUninstall_CompositeInverseMerge asserts uninstalling ONE plugin's
// composite block strips only that block, preserving another plugin's block
// AND interleaved user prose (Bug I-b: the old path deleted/skipped the whole
// co-owned file).
func TestUninstall_CompositeInverseMerge(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "CLAUDE.md")
	body := "# user heading\n" +
		"<!-- ach:begin:plugA -->\nA content\n<!-- ach:end:plugA -->\n" +
		"user middle line\n" +
		"<!-- ach:begin:plugB -->\nB content\n<!-- ach:end:plugB -->\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	// Uninstall plugA only. Recorded hash is intentionally stale (the file was
	// co-owned and has since changed) — composite must NOT hash-skip.
	recs := []store.FileRec{
		{RelPath: "CLAUDE.md", Hash: "xxh3:stale", Merge: "composite", Keys: []string{"plugA"}},
	}
	skipped, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped: %v", skipped)
	}

	out, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read CLAUDE.md after uninstall: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "plugA") || strings.Contains(got, "A content") {
		t.Errorf("plugA block not stripped: %q", got)
	}
	if !strings.Contains(got, "<!-- ach:begin:plugB -->") || !strings.Contains(got, "B content") {
		t.Errorf("plugB block lost: %q", got)
	}
	if !strings.Contains(got, "# user heading") || !strings.Contains(got, "user middle line") {
		t.Errorf("user prose lost: %q", got)
	}
}

// TestUninstall_CompositeEmptiesFileDeletes asserts that stripping the LAST
// block (leaving only whitespace) deletes the file and prunes the dir.
func TestUninstall_CompositeEmptiesFileDeletes(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "CLAUDE.md")
	body := "<!-- ach:begin:only -->\ncontent\n<!-- ach:end:only -->\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs := []store.FileRec{
		{RelPath: "CLAUDE.md", Hash: "xxh3:stale", Merge: "composite", Keys: []string{"only"}},
	}
	if _, err := Uninstall(root, recs); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md should be deleted when last block stripped")
	}
}

// ---------- Uninstall: deep inverse-merge ------------------------------------

// TestUninstall_DeepInverseMergeJSON asserts uninstalling one plugin's deep
// keys removes only those keys, preserving a sibling server and user content.
func TestUninstall_DeepInverseMergeJSON(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"mcpServers":{"srvA":{"command":"a"},"srvB":{"command":"b"}},"userKey":1}`
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs := []store.FileRec{
		{RelPath: ".claude/settings.json", Hash: "xxh3:stale", Merge: "deep", Keys: []string{"mcpServers.srvA"}},
	}
	skipped, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped: %v", skipped)
	}
	out, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcp, _ := doc["mcpServers"].(map[string]any)
	if mcp == nil {
		t.Fatalf("mcpServers missing: %v", doc)
	}
	if _, ok := mcp["srvA"]; ok {
		t.Errorf("srvA not removed: %v", mcp)
	}
	if _, ok := mcp["srvB"]; !ok {
		t.Errorf("srvB lost: %v", mcp)
	}
	if doc["userKey"] == nil {
		t.Errorf("userKey lost: %v", doc)
	}
}

// TestUninstall_DeepInverseMergeTOML asserts the same inverse-merge for a
// .codex/config.toml deep file.
func TestUninstall_DeepInverseMergeTOML(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "[mcp_servers.srvA]\ncommand = \"a\"\n\n[mcp_servers.srvB]\ncommand = \"b\"\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs := []store.FileRec{
		{RelPath: ".codex/config.toml", Hash: "xxh3:stale", Merge: "deep", Keys: []string{"mcp_servers.srvA"}},
	}
	if _, err := Uninstall(root, recs); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	out, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "srvA") {
		t.Errorf("srvA not removed from TOML: %q", got)
	}
	if !strings.Contains(got, "srvB") {
		t.Errorf("srvB lost from TOML: %q", got)
	}
}

// TestUninstall_DeepEmptiesFileDeletes asserts removing the only key empties
// the doc, deleting the file.
func TestUninstall_DeepEmptiesFileDeletes(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"mcpServers":{"only":{}}}`
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Removing both the leaf and its now-empty parent empties the doc.
	recs := []store.FileRec{
		{RelPath: ".claude/settings.json", Hash: "xxh3:stale", Merge: "deep", Keys: []string{"mcpServers"}},
	}
	if _, err := Uninstall(root, recs); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("settings.json should be deleted when doc emptied")
	}
}

// TestUninstall_DeepNonParseableSkips asserts a deep file the user broke into
// invalid JSON is skipped, not corrupted/deleted.
func TestUninstall_DeepNonParseableSkips(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs := []store.FileRec{
		{RelPath: ".claude/settings.json", Hash: "xxh3:stale", Merge: "deep", Keys: []string{"mcpServers.x"}},
	}
	skipped, err := Uninstall(root, recs)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != ".claude/settings.json" {
		t.Errorf("skipped = %v; want [.claude/settings.json]", skipped)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("non-parseable file should be preserved: %v", err)
	}
}

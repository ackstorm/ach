// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

// TestCommitUninstall_KeepsNarrowFileMode asserts a 0600 co-owned file is
// never widened by an install merge or by the uninstall rewrite.
func TestCommitUninstall_KeepsNarrowFileMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"mine":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err := Commit(root, false, "p", []PlannedWrite{{
		Path:    ".mcp.json",
		Content: []byte(`{"mcpServers":{"x":{}}}`),
		Merge:   adapter.MergeDeep,
		Keys:    []string{"mcpServers.x"},
	}})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	assertMode(t, path, 0o600)
	if _, err := Uninstall(root, false, recs); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

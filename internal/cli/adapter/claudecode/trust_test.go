// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderUntrusted(t *testing.T) {
	cfg, dir := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if FolderUntrusted(dir) {
		t.Fatal("no config file: nothing is known, no warning")
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"projects":{"` + filepath.Dir(dir) + `":{"hasTrustDialogAccepted":true}}}`)
	if !FolderUntrusted(dir) {
		t.Fatal("a trusted parent does not trust the folder")
	}
	write(`{"projects":{"` + dir + `":{"hasTrustDialogAccepted":true}}}`)
	if FolderUntrusted(dir) {
		t.Fatal("trusted folder reported untrusted")
	}
	write(`not json`)
	if FolderUntrusted(dir) {
		t.Fatal("unreadable config: nothing is known, no warning")
	}
}

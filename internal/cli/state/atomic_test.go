// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestWriteAtomic_TargetExistsAfterWrite asserts invariant (a) per
// the plan: post-WriteAtomic the target path exists with the bytes
// just written. The simplest observable correctness gate.
func TestWriteAtomic_TargetExistsAfterWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	payload := []byte(`{"schemaVersion":"2","environment":"e","deployment":"d"}`)

	if err := state.WriteAtomic(path, payload); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("bytes mismatch\n got: %s\nwant: %s", got, payload)
	}
}

// TestWriteAtomic_NoTmpRemnant asserts invariant (b) per the plan:
// after a successful WriteAtomic, no `.state-*.json.tmp` files
// remain in the target's parent dir. This validates the cleanup
// closure + os.Rename ordering.
func TestWriteAtomic_NoTmpRemnant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := state.WriteAtomic(path, []byte(`{}`)); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") && strings.HasSuffix(e.Name(), ".json.tmp") {
			t.Fatalf("tmp remnant left behind: %s", e.Name())
		}
	}
}

// TestWriteAtomic_NonexistentParentDir_TargetUntouched asserts
// invariant (c) per the plan: if WriteAtomic fails (e.g. tmp creation
// fails because the parent dir does not exist), the target path is
// left untouched (still absent OR still old content).
func TestWriteAtomic_NonexistentParentDir_TargetUntouched(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "does-not-exist")
	path := filepath.Join(parent, "state.json")

	// Pre-condition: target absent.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pre-condition: target should be absent; stat err = %v", err)
	}

	err := state.WriteAtomic(path, []byte(`{}`))
	if err == nil {
		t.Fatalf("WriteAtomic on nonexistent parent dir: err = nil; want non-nil")
	}

	// Post-condition: target still absent (no partial write leaked).
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("post-condition: target should still be absent; stat err = %v", err)
	}
}

// TestWriteAtomic_OverwritesExistingTarget asserts the rename-onto-
// existing-target branch: WriteAtomic replaces the old file's bytes
// in-place (POSIX rename(2) is atomic on the same fs).
func TestWriteAtomic_OverwritesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := os.WriteFile(path, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	newPayload := []byte(`{"new":true}`)
	if err := state.WriteAtomic(path, newPayload); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(newPayload) {
		t.Fatalf("not overwritten\n got: %s\nwant: %s", got, newPayload)
	}
}

// TestWriteAtomic_FileMode asserts the chmod-to-0644 step. Skipped
// on Windows (no POSIX mode bits) and root (which bypasses checks).
func TestWriteAtomic_FileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not POSIX on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX file mode")
	}

	// Set a restrictive umask via mode argument is not portable;
	// instead, seed the parent dir then assert the resulting file's
	// mode after WriteAtomic publishes it.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := state.WriteAtomic(path, []byte(`{}`)); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o644 {
		t.Errorf("file mode = %#o, want 0644", got)
	}
}

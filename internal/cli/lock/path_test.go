// SPDX-License-Identifier: Apache-2.0

package lock_test

import (
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/lock"
)

// TestPath_JoinsLockUnderAchDir asserts the basic <ach-dir>/lock
// join shape. This is the contract every hydrate caller depends on
// (STATE-06): the lock file lives next to state.json inside the
// resolved `<ach-dir>`.
func TestPath_JoinsLockUnderAchDir(t *testing.T) {
	got := lock.Path("/tmp/.ach")
	want := filepath.Join("/tmp/.ach", "lock")
	if got != want {
		t.Errorf("Path(/tmp/.ach) = %q, want %q", got, want)
	}
}

// TestPath_EmptyAchDir documents the degenerate case: an empty
// ach-dir falls back to a bare "lock" relative path. The hydrate
// command's flag/env resolver always provides a non-empty path, so
// this case is for paranoia (a malformed call should not panic).
func TestPath_EmptyAchDir(t *testing.T) {
	got := lock.Path("")
	want := "lock"
	if got != want {
		t.Errorf("Path(\"\") = %q, want %q", got, want)
	}
}

// TestPath_NestedAchDir asserts no collapse / cleanup happens — Path
// is a thin wrapper around filepath.Join and inherits its semantics
// (no symlink resolution, no Clean). A nested path is preserved
// verbatim minus filepath.Join's adjacent-separator normalization.
func TestPath_NestedAchDir(t *testing.T) {
	in := filepath.Join("/var", "lib", "ach")
	got := lock.Path(in)
	want := filepath.Join(in, "lock")
	if got != want {
		t.Errorf("Path(%q) = %q, want %q", in, got, want)
	}
}

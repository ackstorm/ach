// SPDX-License-Identifier: Apache-2.0

package cachefs_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ackstorm/ach/internal/cachefs"
)

// expectedSubdirs mirrors cachefs.SubDirs but is duplicated here so the test
// asserts the §10.3 contract from the OUTSIDE — a refactor that drops a
// subdirectory from cachefs.SubDirs would silently break the contract if the
// test only iterated the package's own variable.
var expectedSubdirs = []string{"prompt", "plugin", "marketplace", "artifact", ".tmp"}

// TestEnsureLayoutCreatesAllFiveSubdirs asserts the §10.3 contract: after a
// single EnsureLayout call on an empty directory, every OP-10 subdirectory
// exists as a directory (not a file, not a symlink-to-file).
func TestEnsureLayoutCreatesAllFiveSubdirs(t *testing.T) {
	root := t.TempDir()

	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout returned error: %v", err)
	}

	for _, sub := range expectedSubdirs {
		path := filepath.Join(root, sub)
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected subdir %q to exist, stat err: %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("expected %q to be a directory, mode=%s", sub, st.Mode())
		}
	}
}

// TestEnsureLayoutIdempotent asserts D-13: calling EnsureLayout twice on the
// same root is a no-op on the second call. All five subdirectories must still
// exist afterward.
func TestEnsureLayoutIdempotent(t *testing.T) {
	root := t.TempDir()

	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("first EnsureLayout returned error: %v", err)
	}
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("second EnsureLayout returned error: %v", err)
	}

	for _, sub := range expectedSubdirs {
		path := filepath.Join(root, sub)
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("after idempotent re-run, subdir %q missing: %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("after idempotent re-run, %q is not a directory", sub)
		}
	}
}

// TestEnsureLayoutEmptyRootReturnsError asserts the empty-string guard:
// EnsureLayout("") returns ErrCacheRootMissing. Verified via errors.Is so the
// caller can do an idiomatic sentinel match.
func TestEnsureLayoutEmptyRootReturnsError(t *testing.T) {
	err := cachefs.EnsureLayout("")
	if !errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("EnsureLayout(\"\") err = %v, want ErrCacheRootMissing", err)
	}
}

// TestEnsureLayoutNonExistentRootReturnsError asserts a missing PVC mount is
// detected: a path that does not exist on disk returns ErrCacheRootMissing.
// The path is deliberately wild — no PVC will ever be mounted there.
func TestEnsureLayoutNonExistentRootReturnsError(t *testing.T) {
	err := cachefs.EnsureLayout("/this/path/does/not/exist/at/all")
	if !errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("EnsureLayout(<nonexistent>) err = %v, want ErrCacheRootMissing", err)
	}
}

// TestEnsureLayoutFileNotDirReturnsError asserts the IsDir guard: when root
// exists but is a regular file (not a directory), EnsureLayout returns
// ErrCacheRootMissing. This catches the misconfiguration where someone
// points ACH_CACHE_ROOT at a file.
func TestEnsureLayoutFileNotDirReturnsError(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	err := cachefs.EnsureLayout(file)
	if !errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("EnsureLayout(<file>) err = %v, want ErrCacheRootMissing", err)
	}
}

// TestEnsureLayoutSurvivesExistingSubdirs asserts the idempotency contract
// from the partial-init angle: if some subdirs already exist (e.g. PVC was
// previously initialized then the .tmp/ orphan-sweep deleted a subdir), the
// call still succeeds and ALL five subdirs exist afterward.
func TestEnsureLayoutSurvivesExistingSubdirs(t *testing.T) {
	root := t.TempDir()

	// Pre-create one subdir so EnsureLayout has to skip over an existing
	// directory before creating the rest.
	if err := os.MkdirAll(filepath.Join(root, "prompt"), 0o755); err != nil {
		t.Fatalf("failed to pre-create subdir: %v", err)
	}

	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout returned error on partial init: %v", err)
	}

	for _, sub := range expectedSubdirs {
		path := filepath.Join(root, sub)
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected subdir %q to exist after partial-init run: %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("expected %q to be a directory after partial-init run", sub)
		}
	}
}

// TestEnsureLayoutPermissionDeniedReturnsError asserts that MkdirAll failure
// is surfaced: when root is read+exec but NOT writable, EnsureLayout returns
// a non-nil error. We do not assert errors.Is(err, os.ErrPermission) because
// the wrapping shape may vary by Go version; instead we assert err != nil
// and err != ErrCacheRootMissing (the path exists and is a directory, so the
// guard at the top of EnsureLayout passes; the failure must come from
// MkdirAll on a child path).
//
// Skipped on Windows because chmod 0500 semantics differ — Windows ACLs are
// not modeled by os.Chmod and the test would falsely pass.
//
// Skipped when running as root (e.g. some CI containers) because UID 0
// bypasses file-mode permission checks on Unix and the MkdirAll would
// succeed against the 0o500 directory.
func TestEnsureLayoutPermissionDeniedReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows; skipping permission-denied path")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: UID 0 bypasses mode checks on Unix; skipping permission-denied path")
	}

	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatalf("failed to chmod test root: %v", err)
	}
	// Restore writable mode on cleanup so t.TempDir can rm -rf the tree.
	t.Cleanup(func() {
		_ = os.Chmod(root, 0o755)
	})

	err := cachefs.EnsureLayout(root)
	if err == nil {
		t.Fatalf("EnsureLayout on read-only root returned nil, want non-nil error")
	}
	if errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("EnsureLayout on read-only root returned ErrCacheRootMissing; want underlying os error (MkdirAll failure, not the IsDir guard)")
	}
}

// SPDX-License-Identifier: Apache-2.0

package cachefs_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cachefs"
)

// TestIsEmpty_FreshLayout asserts that a root containing only the
// EnsureLayout subdirectory skeleton (all empty) reports as empty —
// the canonical Plan 02-09 OP-11 trigger.
func TestIsEmpty_FreshLayout(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	empty, err := cachefs.IsEmpty(root)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("IsEmpty(fresh layout) = false, want true")
	}
}

// TestIsEmpty_OnePluginFile asserts the defensive predicate from
// threat T-02-04-05: a single file under any non-.tmp subdir flips
// the root to "populated" and IsEmpty returns false.
func TestIsEmpty_OnePluginFile(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin", "foo.tar.gz"),
		[]byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	empty, err := cachefs.IsEmpty(root)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if empty {
		t.Fatalf("IsEmpty(with plugin file) = true, want false")
	}
}

// TestIsEmpty_OnlyTmpPopulated asserts the .tmp/ exemption: files
// under .tmp/ are operator-internal scratch and MUST NOT count
// against the OP-11 empty-cache predicate.
func TestIsEmpty_OnlyTmpPopulated(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tmp", "stg-xyz"),
		[]byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	empty, err := cachefs.IsEmpty(root)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("IsEmpty(only .tmp populated) = false, want true")
	}
}

// TestIsEmpty_MissingRoot asserts the ErrCacheRootMissing sentinel
// for a path that does not exist on disk (matches the bootstrap.go
// guard semantics).
func TestIsEmpty_MissingRoot(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "does-not-exist")
	empty, err := cachefs.IsEmpty(bogus)
	if !errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("IsEmpty(<missing>) err = %v, want ErrCacheRootMissing", err)
	}
	if empty {
		t.Fatalf("IsEmpty(<missing>) returned empty=true; expected false on error")
	}
}

// TestIsEmpty_RootIsFile asserts the IsDir guard: root must be a
// directory, not a regular file (catches misconfiguration where
// ACH_CACHE_ROOT points at a file).
func TestIsEmpty_RootIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "imfile")
	if err := os.WriteFile(file, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	empty, err := cachefs.IsEmpty(file)
	if !errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("IsEmpty(<file>) err = %v, want ErrCacheRootMissing", err)
	}
	if empty {
		t.Fatalf("IsEmpty(<file>) returned empty=true; expected false on error")
	}
}

// TestIsEmpty_EmptyString asserts the empty-string guard returns
// ErrCacheRootMissing rather than panicking or stat'ing the CWD.
func TestIsEmpty_EmptyString(t *testing.T) {
	empty, err := cachefs.IsEmpty("")
	if !errors.Is(err, cachefs.ErrCacheRootMissing) {
		t.Fatalf("IsEmpty(\"\") err = %v, want ErrCacheRootMissing", err)
	}
	if empty {
		t.Fatalf("IsEmpty(\"\") returned empty=true; expected false on error")
	}
}

// TestIsEmpty_EmptyNestedSubdir asserts WR-08: a structure like
// plugin/abc/ (an empty nested subdir under an otherwise-empty
// plugin/) MUST classify as empty. The previous one-level os.ReadDir
// short-circuited to "populated" because plugin/ contained [abc].
// Production reconcilers create files (not empty subdirs), but the
// asymmetry was surprising and a stray empty subdir would silently
// skip OP-11 recovery.
func TestIsEmpty_EmptyNestedSubdir(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plugin", "abc"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	empty, err := cachefs.IsEmpty(root)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("IsEmpty(plugin/abc/ empty nested subdir) = false; want true")
	}
}

// TestIsEmpty_DeeplyNestedFile asserts WR-08's positive-population
// case: a file two levels deep flips IsEmpty to false. Counterpart
// to TestIsEmpty_EmptyNestedSubdir.
func TestIsEmpty_DeeplyNestedFile(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "marketplace", "mp1", "plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "marketplace", "mp1", "plugin", "x.tar.gz"),
		[]byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	empty, err := cachefs.IsEmpty(root)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if empty {
		t.Fatalf("IsEmpty(deeply nested file present) = true; want false")
	}
}

// TestIsEmpty_StrayTmpFile asserts IN-04: a top-level regular FILE
// named ".tmp" must NOT mask population — it counts as user data
// because the .tmp exemption is for the operator's staging
// DIRECTORY, not arbitrary entries with that name.
func TestIsEmpty_StrayTmpFile(t *testing.T) {
	root := t.TempDir()
	// Note: deliberately do NOT call EnsureLayout, since EnsureLayout
	// creates .tmp/ as a directory. We want a stray regular file at
	// the top level named .tmp.
	if err := os.WriteFile(filepath.Join(root, ".tmp"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	empty, err := cachefs.IsEmpty(root)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if empty {
		t.Fatalf("IsEmpty(stray top-level .tmp file) = true; want false (must NOT mask population)")
	}
}

// TestSweepTmp_RemovesOldFiles asserts the §10.3 orphan-sweep core
// behavior: files older than maxAge are removed; younger files are
// retained.
func TestSweepTmp_RemovesOldFiles(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	tmp := filepath.Join(root, ".tmp")

	oldA := filepath.Join(tmp, "stg-old-a")
	oldB := filepath.Join(tmp, "stg-old-b")
	fresh := filepath.Join(tmp, "stg-fresh")
	for _, p := range []string{oldA, oldB, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", p, err)
		}
	}
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{oldA, oldB} {
		if err := os.Chtimes(p, twoHoursAgo, twoHoursAgo); err != nil {
			t.Fatalf("Chtimes %s: %v", p, err)
		}
	}
	// fresh keeps its default ModTime (≈ now).

	if err := cachefs.SweepTmp(root, time.Hour); err != nil {
		t.Fatalf("SweepTmp: %v", err)
	}

	for _, p := range []string{oldA, oldB} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s to be removed; stat err=%v", p, err)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("expected fresh file to survive; stat err=%v", err)
	}
}

// TestSweepTmp_MissingTmpDir asserts the benign absent-dir branch:
// the operator's hourly Runnable may call SweepTmp before
// EnsureLayout has run; absent .tmp/ MUST return nil.
func TestSweepTmp_MissingTmpDir(t *testing.T) {
	root := t.TempDir()
	// Deliberately do NOT call EnsureLayout — .tmp/ is absent.
	if err := cachefs.SweepTmp(root, time.Hour); err != nil {
		t.Fatalf("SweepTmp on root with no .tmp/ = %v, want nil", err)
	}
}

// TestSweepTmp_RaceTolerance asserts the threat T-02-04-03
// mitigation: a concurrent Remove (modeling a reconciler's
// rename(2) winning the race) MUST NOT cause SweepTmp to return
// non-nil. Whoever wins, SweepTmp's per-entry Remove tolerates
// ErrNotExist silently.
func TestSweepTmp_RaceTolerance(t *testing.T) {
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	tmp := filepath.Join(root, ".tmp")
	victim := filepath.Join(tmp, "stg-racy")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Set ModTime well in the past so the 1-nanosecond cutoff still
	// classifies it as old.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(victim, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Race the sweeper: try to remove the victim ourselves.
		// Whoever wins, both are no-ops or ErrNotExist; SweepTmp
		// tolerates both per threat T-02-04-03 mitigation.
		_ = os.Remove(victim)
	}()

	if err := cachefs.SweepTmp(root, time.Nanosecond); err != nil {
		t.Fatalf("SweepTmp under race = %v, want nil", err)
	}
	wg.Wait()

	if _, err := os.Stat(victim); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected victim removed by SOMEONE; stat err=%v", err)
	}
}

// SPDX-License-Identifier: Apache-2.0

package cachefs

import (
	"io/fs"
	"os"
	"path/filepath"
)

// IsEmpty returns true when root exists and contains no regular files
// OTHER THAN those under the .tmp/ staging directory. Used by
// cmd/operator/main.go (Plan 02-09) to drive the OP-11 cache-loss
// recovery branch: when IsEmpty returns true, ACH NULLs the
// last_successful_refresh columns in external_refs and
// marketplace_plugins so every reconciler reissues the upstream fetch
// on the next reconcile.
//
// Returns ErrCacheRootMissing if root is empty, missing, or not a
// directory. Does NOT propagate ReadDir errors inside subdirectories
// — those are treated as "populated" defensively to err on the side
// of preserving any data the reconcilers may have written.
//
// WR-08: scans recursively under each non-.tmp top-level entry. The
// previous one-level os.ReadDir would mis-classify a structure like
// `plugin/abc/` (empty nested subdir, no files) as "populated" — the
// outer ReadDir returns [abc] and len > 0 short-circuited to false.
// In production the reconcilers create FILES (not empty subdirs),
// but the asymmetry was surprising and a stray empty subdir would
// silently skip OP-11 recovery. The recursive scan stops at the
// first regular file found per top-level subtree, so cost stays
// O(1) on the populated case.
//
// IN-04: the .tmp exemption is keyed on (entry.IsDir() && name ==
// ".tmp"). A stray top-level regular file or symlink named ".tmp"
// counts as user data and flips IsEmpty to false.
//
// Edge cases:
//   - root contains only the .tmp/ directory (possibly with files
//     inside): returns (true, nil). The .tmp/ staging dir is
//     operator-internal scratch (orphan staging files), not user data,
//     and is excluded from the emptiness check. No sweeper runs — atomic
//     rename(2) makes orphans rare and the PVC is bounded.
//   - root contains a stray regular file at the top level (e.g. a
//     lost+found from fsck): the entry is detected as a non-directory
//     immediately and IsEmpty returns (false, nil).
func IsEmpty(root string) (bool, error) {
	if root == "" {
		return false, ErrCacheRootMissing
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return false, ErrCacheRootMissing
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		// IN-04: tighten the .tmp exemption to "directory named .tmp".
		// A stray top-level regular file or symlink named ".tmp" no
		// longer masks population.
		if entry.IsDir() && entry.Name() == ".tmp" {
			// .tmp/ presence (and any contents inside it) is operator-
			// internal scratch, not user data. No sweeper runs; orphan
			// staging files are rare (atomic rename) and the PVC is
			// bounded.
			continue
		}
		// A top-level non-directory entry is immediately "user data"
		// — no recursion needed.
		if !entry.IsDir() {
			return false, nil
		}
		// WR-08: recurse into the subtree until we find a regular file
		// OR exhaust it. Empty intermediate directories do not flip
		// "populated".
		populated, walkErr := subtreeHasFile(filepath.Join(root, entry.Name()))
		if walkErr != nil {
			// Defensive: an error scanning the subtree means we cannot
			// prove the cache is empty. Err toward preserving data
			// (the OP-11 reset is destructive of staleness state).
			return false, nil
		}
		if populated {
			return false, nil
		}
	}
	return true, nil
}

// subtreeHasFile reports whether dir or any descendant contains a regular
// (non-directory) entry. Walks via filepath.WalkDir, short-circuiting with
// fs.SkipAll on the first non-dir entry. A walk error propagates so the
// caller (IsEmpty) can apply its defensive "any error ⇒ populated"
// classification.
func subtreeHasFile(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

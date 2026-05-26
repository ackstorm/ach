// SPDX-License-Identifier: Apache-2.0

package cachefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
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
//     operator-internal; orphan files there are not "user data" and
//     SweepTmp handles them on a separate cadence.
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
			// internal scratch, not user data. SweepTmp handles its
			// lifecycle on a separate cadence.
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

// subtreeHasFile recursively scans dir and returns true as soon as it
// finds a regular file (or any non-directory entry). Returns false
// when dir and all transitive descendants contain only empty
// directories. ReadDir errors propagate to the caller, which converts
// them to the defensive "populated" classification.
func subtreeHasFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return true, nil
		}
		populated, err := subtreeHasFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false, err
		}
		if populated {
			return true, nil
		}
	}
	return false, nil
}

// SweepTmp removes regular files under root/.tmp/ whose ModTime is
// older than maxAge. Hub §10.3 — orphan staging-file sweep against
// crashed reconciles that exited between os.CreateTemp and
// rename(2). Idempotent; best-effort; concurrent rename(2) calls
// from live reconciles race benignly (Remove returns ErrNotExist
// which SweepTmp silently ignores).
//
// Returns nil if .tmp/ does not exist (the operator's hourly Runnable
// may call this before EnsureLayout has run; absent dir = nothing to
// do). Returns nil unconditionally after iteration — partial Remove
// failures (e.g. a concurrent rename winning the race) are silently
// ignored on a per-entry basis so a single anomaly does not abort
// the sweep of subsequent entries.
func SweepTmp(root string, maxAge time.Duration) error {
	tmp := filepath.Join(root, ".tmp")
	entries, err := os.ReadDir(tmp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			// File disappeared between ReadDir and Info — a benign
			// race against a concurrent rename(2) from a live
			// reconcile. Skip this entry and continue.
			continue
		}
		if info.ModTime().Before(cutoff) {
			// Ignore Remove error: ErrNotExist means a concurrent
			// rename(2) won the race (the file is already at its
			// production path); other errors are best-effort and
			// will be retried on the next tick.
			_ = os.Remove(filepath.Join(tmp, entry.Name()))
		}
	}
	return nil
}

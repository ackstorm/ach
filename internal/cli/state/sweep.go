// SPDX-License-Identifier: Apache-2.0

package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// SweepTmp removes every subentry under <achDir>/tmp/ unconditionally.
// Called by the hydrate engine at step 2 of the §6.7 14-step sequence
// (D-02 atomic boundary — "tmp/ contents are dead on hydrate start by
// definition; the prior invocation either committed via rename or
// crashed before commit, in which case the staged bytes are
// unreferenced state").
//
// Contract:
//
//   - NO maxAge parameter. D-02 mandates the unconditional sweep — a
//     hydrate cannot tell which tmp entries belong to a recovering
//     prior process and which are its own (none, since this runs
//     before any tmp writes). The CLI's tmp/ has no concurrent writers
//     between locks, so an unconditional wipe is safe.
//   - Subentries are <rand>/ STAGING SUBTREES (directories), not flat
//     files — use os.RemoveAll, not os.Remove.
//   - Per-entry errors are swallowed silently per spec §6.7 step 2
//     ("benign cleanup, never aborts hydrate"). A single misbehaving
//     entry does not block subsequent sweep work.
//
// Idempotent: if <achDir>/tmp/ does not exist (fresh workspace —
// first hydrate), returns nil with no work. The hydrate orchestrator
// MAY call this before creating the tmp/ directory.
//
// Concurrency: the §6.7 lock (W1-03) is held by the caller around the
// full 14-step sequence; SweepTmp is therefore single-writer per
// <ach-dir>. No file-system races with concurrent hydrate processes.
func SweepTmp(achDir string) error {
	tmp := filepath.Join(achDir, "tmp")
	entries, err := os.ReadDir(tmp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Idempotent: absent tmp/ is the fresh-workspace branch.
			return nil
		}
		return err
	}
	for _, entry := range entries {
		// Per-entry errors are swallowed — spec §6.7 step 2 is
		// "benign cleanup, never aborts hydrate". A stuck entry
		// (permission denied, EBUSY) is logged at the caller layer
		// at most; SweepTmp itself returns nil so the hydrate
		// sequence proceeds.
		_ = os.RemoveAll(filepath.Join(tmp, entry.Name()))
	}
	return nil
}

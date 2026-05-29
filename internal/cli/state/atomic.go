// SPDX-License-Identifier: Apache-2.0

package state

import (
	"os"
	"path/filepath"
	"runtime"
)

// WriteAtomic publishes `data` at `path` atomically following the
// STATE-07 / spec §8.7 four-step contract:
//
//  1. CreateTemp(".state-*.json.tmp") in the same directory as path
//     so the rename(2) stays on a single filesystem.
//  2. Write(data) + Sync(fd) — flush kernel page cache to durable
//     storage before the rename. The fsync(fd) is the addition over
//     internal/cli/config.Save which omits it; spec §8.7 mandates it
//     so a kernel crash between Write and Rename cannot leave a tmp
//     file whose bytes are not yet on disk.
//  3. Chmod(0644) + Close + os.Rename(tmp, path) — POSIX rename is
//     atomic on the same filesystem, so an observer either sees the
//     prior state.json or the new one, never a partial.
//  4. Open(parent_dir) + Sync — fsync(parent_dir) ensures the
//     directory entry pointing at the renamed inode is itself
//     durable. Skipped on Windows where NTFS does not honor fsync on
//     directories (best-effort posture).
//
// Cleanup: every error path after CreateTemp removes the tmp file so
// no `.state-*.json.tmp` orphans accumulate in <ach-dir>/. The
// hydrate-startup `SweepTmp` is the long-term safety net (D-02).
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	// STATE-07 step 2 — fsync(fd) BEFORE rename so the bytes are
	// durable. Without this, a kernel crash between Write and Rename
	// could leave an empty tmp file (rename is atomic for the
	// directory entry, but the inode's data pages may not have hit
	// disk yet).
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	// state.json has no secrets — 0644 is the right mode (vs config's
	// 0600). Chmod before Close so the rename publishes the correct
	// mode atomically.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}

	// STATE-07 step 4 — fsync(parent_dir) so the rename's directory-
	// entry update is itself durable. Skipped on Windows: NTFS does
	// not honor fsync on directories, and golang's os.File.Sync on a
	// directory handle returns an error there. Linux/macOS honor it.
	if runtime.GOOS != "windows" {
		if d, derr := os.Open(dir); derr == nil {
			_ = d.Sync()
			_ = d.Close()
		}
		// Open failure is best-effort: the rename has succeeded; the
		// caller has the new bytes at path. The parent-dir fsync is
		// a durability hardening, not a correctness gate.
	}

	return nil
}

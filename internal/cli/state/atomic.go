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
//  3. Chmod(mode) + Close + os.Rename(tmp, path) — POSIX rename is
//     atomic on the same filesystem, so an observer either sees the
//     prior state.json or the new one, never a partial.
//  4. Open(parent_dir) + Sync — fsync(parent_dir) ensures the
//     directory entry pointing at the renamed inode is itself
//     durable. Skipped on Windows where NTFS does not honor fsync on
//     directories (best-effort posture).
//
// File mode is an explicit per-caller policy. The `mode` parameter is
// required (no package default) because WriteAtomic is the
// publication primitive for BOTH `state.json` (no secrets — 0o644 is
// correct) AND adapter runtime-config files (`.claude/.mcp.json`,
// `.codex/config.toml`, `.gemini/settings.json`, `.opencode/opencode.json`)
// that embed plaintext `x-ach-key` bearer credentials in headers
// maps. Credential-bearing callers MUST pass 0o600 so other local
// UIDs on multi-user hosts cannot read the bearer; only callers
// publishing non-secret files (state.Save) pass 0o644. The signature
// is required-mode (not a default + variant) so a future
// credential-bearing caller cannot silently regress to 0o644 by
// omitting the arg — see CR-01 in 07-REVIEW.md and verifier finding
// gaps[1] in 07-VERIFICATION.md.
//
// Cleanup: every error path after CreateTemp removes the tmp file so
// no `.state-*.json.tmp` orphans accumulate in <ach-dir>/. The
// hydrate-startup `SweepTmp` is the long-term safety net (D-02).
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
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

	// Per-caller mode policy: callers writing credential-bearing
	// adapter runtime-config files pass 0o600 (refuse world-readable
	// on multi-user hosts per CR-01); state.Save passes 0o644 for the
	// no-secret state.json. Chmod before Close so the rename
	// publishes the correct mode atomically (the temp file is the
	// rename source — by the instant rename(2) completes the final
	// path is already at the target mode, closing T-07-W5-02-02's
	// TOCTOU window between rename and the next caller's open).
	if err := tmp.Chmod(mode); err != nil {
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

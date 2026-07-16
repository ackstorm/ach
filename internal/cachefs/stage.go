// SPDX-License-Identifier: Apache-2.0

package cachefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrOversize marks a staged body that exceeded the caller's size cap.
// Callers translate it into their kind-specific oversize error.
var ErrOversize = errors.New("cachefs: staged content exceeds size cap")

// StageAtomic writes r to a fsync'd, closed temp file under
// <cacheRoot>/.tmp and returns its path for the caller to (optionally
// verify and) os.Rename onto the final path — the shared §10.3 staging
// half of the atomic-publish contract. capBytes <= 0 disables the cap;
// on overshoot the temp file is removed and err wraps ErrOversize with
// n = bytes read (cap+1). Any error removes the temp file.
//
// fsync-before-rename: rename(2) preserves only what reached the
// platter; without the Sync a power cut can leave a zero-length file at
// the published path. Explicit Close before rename keeps strict
// rename-of-open-fd platforms (FUSE) correct.
func StageAtomic(cacheRoot string, r io.Reader, capBytes int64) (string, int64, error) {
	tmpDir := filepath.Join(cacheRoot, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir staging dir: %w", err)
	}
	f, err := os.CreateTemp(tmpDir, "stg-")
	if err != nil {
		return "", 0, fmt.Errorf("create staging file: %w", err)
	}
	path := f.Name()
	fail := func(werr error) (string, int64, error) {
		_ = f.Close()
		_ = os.Remove(path)
		return "", 0, werr
	}

	src := r
	if capBytes > 0 {
		// Cap+1 so overshoot is detected exactly.
		src = io.LimitReader(r, capBytes+1)
	}
	n, copyErr := io.Copy(f, src)
	if copyErr != nil {
		return fail(fmt.Errorf("staging copy: %w", copyErr))
	}
	if capBytes > 0 && n > capBytes {
		_ = f.Close()
		_ = os.Remove(path)
		return "", n, fmt.Errorf("%w (read %d > cap %d)", ErrOversize, n, capBytes)
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("staging fsync: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("staging close: %w", err)
	}
	return path, n, nil
}

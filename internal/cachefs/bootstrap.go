// SPDX-License-Identifier: Apache-2.0

package cachefs

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrCacheRootMissing is returned by EnsureLayout when the cache root is
// empty, does not exist on disk, or exists but is not a directory.
//
// Per D-13 the operator main treats this as a fatal startup error: the PVC
// mount must already be present before EnsureLayout is called, and a missing
// mount means the Pod was scheduled without its storage and should crash so
// Kubernetes reschedules it.
var ErrCacheRootMissing = errors.New("cachefs: ACH_CACHE_ROOT directory does not exist or is unwritable")

// SubDirs are the OP-10 cache subdirectories created under the PVC mount root.
// Order is fixed so EnsureLayout is deterministic for tests.
//
// The leading dot on ".tmp" is intentional — §10.3 specifies it as a hidden
// staging directory whose contents are never served. All entries live on the
// same filesystem (the same PVC mount) by construction so the rename(2) from
// .tmp/<random> to the final publish path is atomic per POSIX.
//
// "skill" / "skill-marketplace" back the agent-skill content kind + its
// marketplace; without "skill" pre-created a standalone Skill's §10.3 rename(2)
// fails ("no such file or directory" on the publish parent).
var SubDirs = []string{"prompt", "plugin", "marketplace", "artifact", "skill", "skill-marketplace", ".tmp"}

// EnsureLayout creates the OP-10 cache directory tree under root.
//
// Idempotent: re-running on an already-initialized root is a no-op (os.MkdirAll
// returns nil when the target directory already exists). Returns
// ErrCacheRootMissing when root is empty, missing, or not a directory; returns
// the underlying os error (e.g. wrapped *PathError on permission denied or
// ENOSPC) on any MkdirAll failure.
//
// This function does not log. Errors flow to the caller (the operator main)
// which is responsible for surfacing structured startup-failure context.
func EnsureLayout(root string) error {
	if root == "" {
		return ErrCacheRootMissing
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return ErrCacheRootMissing
	}
	for _, sub := range SubDirs {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

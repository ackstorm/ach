// SPDX-License-Identifier: Apache-2.0

package state

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// ErrConcurrentEdit reports a file that changed on every attempt to rewrite
// it; the file is left as its other writer last wrote it.
var ErrConcurrentEdit = errors.New("file kept changing while ach-cli was updating it; rerun when it is saved")

// rewriteAttempts bounds RewriteFile's re-runs.
const rewriteAttempts = 3

// RewriteFile is the read-modify-write for a file the user may also edit
// (tool config, CLAUDE.md, .gitignore). It reads path, passes the bytes to fn
// (nil when the file is absent, a non-nil empty slice when it is empty) and
// publishes fn's output with WriteAtomic. Just before publishing it reads the
// file again: if another writer (an editor save) changed it since fn saw it,
// fn runs again on the new bytes, so that edit is merged instead of lost.
// After rewriteAttempts changed reads it returns ErrConcurrentEdit and writes
// nothing. fn returning write=false publishes nothing.
//
// The remaining window is between the second read and the rename. Closing it
// would need the other writer to honour a lock, which editors do not.
func RewriteFile(path string, mode os.FileMode, fn func(cur []byte) (out []byte, write bool, err error)) error {
	for range rewriteAttempts {
		cur, err := readIfExists(path)
		if err != nil {
			return err
		}
		out, write, err := fn(cur)
		if err != nil || !write {
			return err
		}
		now, err := readIfExists(path)
		if err != nil {
			return err
		}
		if (now == nil) == (cur == nil) && bytes.Equal(now, cur) {
			return WriteAtomic(path, out, mode)
		}
	}
	return fmt.Errorf("%s: %w", path, ErrConcurrentEdit)
}

// readIfExists returns nil for an absent file and a non-nil slice otherwise.
func readIfExists(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if b == nil {
		b = []byte{}
	}
	return b, nil
}

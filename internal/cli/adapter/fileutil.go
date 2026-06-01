// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyFile copies srcPath → dstPath with mode 0644. Parent dirs are
// expected to already exist (WalkDir order guarantees this for the
// adapters that drive directory creation themselves).
func CopyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath) //nolint:gosec // srcPath is under our staging dir
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) //nolint:gosec // dstPath is under our destination dir
	if err != nil {
		return err
	}

	// Per 07-W5-05 (WR-02): explicit close to surface buffered-write
	// errors that surface only at close(2) (EIO/ENOSPC). A deferred
	// `_ = out.Close()` would silently drop those errors, recording a
	// truncated file as successfully written.
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// TopLevelComponent returns the first path segment of rel (e.g.
// "agents/foo.md" → "agents"; ".claude-plugin/plugin.json" →
// ".claude-plugin"; ".mcp.json" → ".mcp.json"). For a root-level entry
// with no separator, rel itself is returned; an empty rel yields "".
// Used to classify walked plugin-tree entries by their top-level
// component name.
func TopLevelComponent(rel string) string {
	if rel == "" {
		return ""
	}
	return strings.Split(filepath.ToSlash(rel), "/")[0]
}

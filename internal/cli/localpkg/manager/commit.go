// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/cli/merge"
	"github.com/ackstorm/ach/internal/cli/state"
)

// opencodeProjectPrefix and opencodeGlobalPrefix mirror the constants in
// internal/cli/hydrate/wiring.go for the global path remap.
const (
	localOpencodeProjectPrefix = ".opencode/"
	localOpencodeGlobalPrefix  = ".config/opencode/"
)

// remapGlobalPath adjusts a workspace-relative path for --global scope.
// If adapterID == "opencode" and path starts with ".opencode/", the prefix
// is replaced with ".config/opencode/". All other paths are returned unchanged.
func remapGlobalPath(adapterID, path string) string {
	if adapterID == "opencode" && strings.HasPrefix(path, localOpencodeProjectPrefix) {
		return localOpencodeGlobalPrefix + strings.TrimPrefix(path, localOpencodeProjectPrefix)
	}
	return path
}

// Commit writes planned writes under root (the tool root: project cwd or $HOME
// for --global), honoring MergeKind, and returns the relative paths written
// (for uninstall tracking). adapterID is used for the opencode global path
// remap. compositeID is used as the marker id for MergeComposite writes
// (typically the plugin/skill name).
func Commit(root string, global bool, adapterID, compositeID string, writes []PlannedWrite) ([]store.FileRec, error) {
	recs := make([]store.FileRec, 0, len(writes))

	for _, w := range writes {
		rel := w.Path
		if global {
			rel = remapGlobalPath(adapterID, w.Path)
		}
		abs := filepath.Join(root, rel)

		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, fmt.Errorf("commit: mkdir parent for %s: %w", rel, err)
		}

		var finalBytes []byte

		switch w.Merge {
		case adapter.MergeDeep:
			// MergeForward reads existing, deep-merges ours in, writes atomically,
			// and returns the merged bytes — we must NOT write again.
			merged, err := merge.MergeForward(abs, w.Content, 0o644)
			if err != nil {
				return nil, fmt.Errorf("commit: merge deep %s: %w", rel, err)
			}
			finalBytes = merged

		case adapter.MergeComposite:
			// WriteComposite inserts/replaces a marker-bounded block in the file.
			// We read back the file after writing to compute the hash of final bytes.
			id := compositeID
			if id == "" {
				// Fall back to the rel path as a stable composite id.
				id = rel
			}
			if err := merge.WriteComposite(abs, id, w.Content, 0o644); err != nil {
				return nil, fmt.Errorf("commit: merge composite %s: %w", rel, err)
			}
			var err error
			finalBytes, err = os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("commit: read back composite %s: %w", rel, err)
			}

		default:
			// MergeReplace (and the zero value): write verbatim.
			if err := state.WriteAtomic(abs, w.Content, 0o644); err != nil {
				return nil, fmt.Errorf("commit: write replace %s: %w", rel, err)
			}
			finalBytes = w.Content
		}

		recs = append(recs, store.FileRec{
			RelPath: rel,
			Hash:    hash.HashBytes(finalBytes),
		})
	}

	return recs, nil
}

// Uninstall removes recorded files under root. For each file it verifies the
// on-disk content hash matches the recorded hash; on mismatch it skips the file
// (to avoid clobbering user edits) and records it in the returned skipped list.
// Missing files are treated as already-removed (no error). Empty ancestor dirs
// are pruned after removal (up to but not including root).
// Returns the list of skipped (user-modified) RelPaths and any unexpected error.
func Uninstall(root string, files []store.FileRec) (skipped []string, err error) {
	for _, f := range files {
		abs := filepath.Join(root, f.RelPath)
		body, rerr := os.ReadFile(abs)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				// Already absent — treat as removed.
				continue
			}
			return skipped, fmt.Errorf("uninstall: read %s: %w", f.RelPath, rerr)
		}

		if hash.HashBytes(body) != f.Hash {
			// User-modified — skip, do not delete.
			skipped = append(skipped, f.RelPath)
			continue
		}

		if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return skipped, fmt.Errorf("uninstall: remove %s: %w", f.RelPath, rerr)
		}

		// Prune now-empty ancestor directories (stop at root).
		pruneEmptyDirs(root, filepath.Dir(abs))
	}
	return skipped, nil
}

// pruneEmptyDirs walks up the directory tree from dir toward root (exclusive),
// removing each directory that is now empty. Stops when a non-empty directory
// is encountered or when dir reaches root.
func pruneEmptyDirs(root, dir string) {
	// Normalize both paths so we can compare reliably.
	cleanRoot := filepath.Clean(root)
	cur := filepath.Clean(dir)
	for {
		if cur == cleanRoot || !strings.HasPrefix(cur, cleanRoot+string(filepath.Separator)) {
			// Reached or escaped root — stop.
			break
		}
		if err := os.Remove(cur); err != nil {
			// os.Remove fails on non-empty dirs with ENOTEMPTY (or EEXIST on
			// some platforms) — that is the intended stop condition. Any other
			// error (e.g. permission denied) is swallowed; we do a best-effort
			// prune, not a guaranteed cleanup.
			break
		}
		// Ascend one level.
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
}

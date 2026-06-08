// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"bytes"
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

// FileRec.Merge discriminators recorded by Commit and dispatched on by
// Uninstall. The empty string is the replace/back-compat default (see
// store.FileRec doc) and is deliberately NOT a named constant — absence of a
// discriminator IS the replace case.
const (
	mergeKindDeep      = "deep"
	mergeKindComposite = "composite"
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

		var (
			finalBytes []byte
			recMerge   string
			recKeys    []string
		)

		switch w.Merge {
		case adapter.MergeDeep:
			// MergeForward reads existing, deep-merges ours in, writes atomically,
			// and returns the merged bytes — we must NOT write again. Record the
			// contributed dotted keys so Uninstall can inverse-merge (remove only
			// our keys, preserving other plugins' and the user's entries).
			merged, err := merge.MergeForward(abs, w.Content, 0o644)
			if err != nil {
				return nil, fmt.Errorf("commit: merge deep %s: %w", rel, err)
			}
			finalBytes = merged
			recMerge = mergeKindDeep
			recKeys = append([]string(nil), w.Keys...)

		case adapter.MergeComposite:
			// Determine the marker id: the rule's first key (dispatcher-threaded
			// plugin name) wins; fall back to the caller's compositeID, then the
			// rel path as a last resort. Wrap the RAW content in markers via the
			// shared CompositeBlock helper (WriteComposite inserts/replaces the
			// block AS-IS — it does NOT wrap), then record the id so Uninstall can
			// strip exactly this plugin's marked region.
			id := compositeID
			if len(w.Keys) > 0 && w.Keys[0] != "" {
				id = w.Keys[0]
			}
			if id == "" {
				id = rel
			}
			block := merge.CompositeBlock(id, w.Content)
			if err := merge.WriteComposite(abs, id, block, 0o644); err != nil {
				return nil, fmt.Errorf("commit: merge composite %s: %w", rel, err)
			}
			var err error
			finalBytes, err = os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("commit: read back composite %s: %w", rel, err)
			}
			recMerge = mergeKindComposite
			recKeys = []string{id}

		default:
			// MergeReplace (and the zero value): write verbatim. No merge metadata
			// — Uninstall hash-checks and deletes the whole file.
			if err := state.WriteAtomic(abs, w.Content, 0o644); err != nil {
				return nil, fmt.Errorf("commit: write replace %s: %w", rel, err)
			}
			finalBytes = w.Content
		}

		recs = append(recs, store.FileRec{
			RelPath: rel,
			Hash:    hash.HashBytes(finalBytes),
			Merge:   recMerge,
			Keys:    recKeys,
		})
	}

	return recs, nil
}

// Uninstall removes a package's recorded files under root, dispatching on each
// FileRec's Merge kind so CO-OWNED files (deep-merged JSON/TOML, composite
// marker blocks) are inverse-merged rather than deleted wholesale:
//
//   - "composite": strip only this plugin's marked region (Keys[0]) from the
//     host-memory file, preserving other plugins' blocks and the user's prose;
//     delete the file only when nothing but whitespace remains.
//   - "deep": remove only this plugin's contributed dotted Keys from the
//     JSON/TOML document, preserving sibling entries; delete the file only when
//     the document becomes empty.
//   - "" / replace: the legacy whole-file path — hash-check, then delete; on
//     drift skip (recorded in the returned skipped list) to avoid clobbering
//     user edits.
//
// Co-owned (deep/composite) files are NOT hash-skipped: another install/edit
// legitimately changes the whole file, but removing only OUR contribution is
// always safe. A parse failure on a deep file (the user broke the JSON/TOML
// out from under us) is recorded in skipped rather than corrupting the file.
// Missing files are treated as already-removed (no error). Empty ancestor dirs
// are pruned after a file is fully removed (up to but not including root).
// Returns the list of skipped RelPaths and any unexpected error.
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

		var (
			removed bool
			skip    bool
			uerr    error
		)
		switch f.Merge {
		case mergeKindComposite:
			removed, uerr = uninstallComposite(abs, f, body)
		case mergeKindDeep:
			removed, skip, uerr = uninstallDeep(abs, f, body)
		default:
			removed, skip = uninstallReplace(abs, f, body)
		}
		if uerr != nil {
			return skipped, uerr
		}
		if skip {
			skipped = append(skipped, f.RelPath)
			continue
		}
		if removed {
			// Prune now-empty ancestor directories (stop at root).
			pruneEmptyDirs(root, filepath.Dir(abs))
		}
	}
	return skipped, nil
}

// uninstallReplace is the legacy whole-file path: delete when the on-disk hash
// matches the recorded hash, otherwise skip (drift). Returns (removed, skip).
func uninstallReplace(abs string, f store.FileRec, body []byte) (removed, skip bool) {
	if hash.HashBytes(body) != f.Hash {
		// User-modified — skip, do not delete.
		return false, true
	}
	if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		// Best-effort: a remove failure here is non-fatal to the overall
		// uninstall — treat as not-removed so we do not prune above it.
		return false, false
	}
	return true, false
}

// uninstallComposite strips this plugin's marked region (Keys[0]) from a
// host-memory file. When nothing but whitespace remains, the file is deleted.
// Co-owned files are intentionally NOT hash-checked — removing only the marked
// block is always safe. A FileRec with no Keys falls back to the hash-gated
// whole-file delete (defensive; should not occur for recorded composites).
// Returns (removed, error).
func uninstallComposite(abs string, f store.FileRec, body []byte) (bool, error) {
	if len(f.Keys) == 0 || f.Keys[0] == "" {
		removed, _ := uninstallReplace(abs, f, body)
		return removed, nil
	}
	re := merge.PluginMarkerRE(f.Keys[0])
	stripped := re.ReplaceAll(body, nil)
	if len(bytes.TrimSpace(stripped)) == 0 {
		if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return false, fmt.Errorf("uninstall: remove composite %s: %w", f.RelPath, rerr)
		}
		return true, nil
	}
	if werr := state.WriteAtomic(abs, stripped, 0o644); werr != nil {
		return false, fmt.Errorf("uninstall: rewrite composite %s: %w", f.RelPath, werr)
	}
	return false, nil
}

// uninstallDeep removes this plugin's contributed dotted Keys from a JSON/TOML
// document. When the document becomes empty, the file is deleted. Co-owned
// files are NOT hash-checked. If the file became non-parseable out from under
// us, it is skipped (skip=true) rather than corrupted. Returns
// (removed, skip, error).
func uninstallDeep(abs string, f store.FileRec, body []byte) (removed, skip bool, err error) {
	isTOML := strings.HasSuffix(f.RelPath, ".toml")
	doc, perr := merge.ParseDoc(body, isTOML)
	if perr != nil {
		// File became non-parseable (user broke it) — skip rather than corrupt.
		return false, true, nil
	}
	for _, k := range f.Keys {
		merge.RemoveDottedKey(doc, k)
	}
	if len(doc) == 0 {
		if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return false, false, fmt.Errorf("uninstall: remove deep %s: %w", f.RelPath, rerr)
		}
		return true, false, nil
	}
	out, eerr := merge.EncodeDoc(doc, isTOML)
	if eerr != nil {
		return false, false, fmt.Errorf("uninstall: encode deep %s: %w", f.RelPath, eerr)
	}
	if werr := state.WriteAtomic(abs, out, 0o644); werr != nil {
		return false, false, fmt.Errorf("uninstall: rewrite deep %s: %w", f.RelPath, werr)
	}
	return false, false, nil
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

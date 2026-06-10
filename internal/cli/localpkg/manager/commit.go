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

// uninstallOp is the verdict classifyUninstall produces for one recorded file
// given its current on-disk body: what uninstalling it WOULD do.
type uninstallOp int

const (
	opAbsent uninstallOp = iota // file is gone from disk (handled by the caller)
	opRemove                    // delete the whole file
	opModify                    // rewrite with the inverse-merged content (co-owned)
	opSkip                      // user-modified replace file — leave it untouched
)

// String renders the op for the user-facing uninstall plan (--dry-run).
func (o uninstallOp) String() string {
	switch o {
	case opRemove:
		return "remove"
	case opModify:
		return "modify"
	case opSkip:
		return "skip"
	default:
		return "absent"
	}
}

// classifyUninstall decides — purely, with no I/O beyond the already-read body —
// what uninstalling FileRec f means for its current on-disk body, dispatching on
// the Merge kind:
//
//   - "composite": strip only this plugin's marked region (Keys[0]); opRemove
//     when nothing but whitespace remains, else opModify with the stripped body.
//   - "deep": remove only this plugin's contributed dotted Keys from the
//     JSON/TOML document; opRemove when the document becomes empty, else opModify
//     with the re-encoded body. A non-parseable file (user broke it) → opSkip.
//   - "" / replace: hash-check; opRemove on match, opSkip on drift (user-edited).
//
// Co-owned (deep/composite) files are NOT hash-checked — removing only OUR
// contribution is always safe. For opModify the rewritten content is returned.
// Shared by Uninstall (which performs the I/O) and UninstallPlan (which only
// reports), so the act path and the --dry-run preview can never drift.
func classifyUninstall(f store.FileRec, body []byte) (uninstallOp, []byte, error) {
	switch f.Merge {
	case mergeKindComposite:
		if len(f.Keys) == 0 || f.Keys[0] == "" {
			// Defensive: a recorded composite should always carry its marker id;
			// fall back to the hash-gated whole-file verdict.
			return classifyReplace(f, body), nil, nil
		}
		stripped := merge.PluginMarkerRE(f.Keys[0]).ReplaceAll(body, nil)
		if len(bytes.TrimSpace(stripped)) == 0 {
			return opRemove, nil, nil
		}
		return opModify, stripped, nil

	case mergeKindDeep:
		isTOML := strings.HasSuffix(f.RelPath, ".toml")
		doc, perr := merge.ParseDoc(body, isTOML)
		if perr != nil {
			return opSkip, nil, nil
		}
		for _, k := range f.Keys {
			merge.RemoveDottedKey(doc, k)
		}
		if len(doc) == 0 {
			return opRemove, nil, nil
		}
		out, eerr := merge.EncodeDoc(doc, isTOML)
		if eerr != nil {
			return opSkip, nil, fmt.Errorf("encode deep %s: %w", f.RelPath, eerr)
		}
		return opModify, out, nil

	default:
		return classifyReplace(f, body), nil, nil
	}
}

// classifyReplace is the whole-file verdict: opRemove when the on-disk hash
// matches the recorded hash, opSkip on drift (user-modified — never clobber).
func classifyReplace(f store.FileRec, body []byte) uninstallOp {
	if hash.HashBytes(body) != f.Hash {
		return opSkip
	}
	return opRemove
}

// Uninstall removes a package's recorded files under root, applying the
// classifyUninstall verdict for each: opRemove deletes the file (pruning now-
// empty ancestor dirs up to but not including root), opModify rewrites the
// inverse-merged content (co-owned deep/composite files), opSkip records the
// RelPath as skipped (drift — never clobbered). Missing files are treated as
// already-removed. A remove failure is best-effort (non-fatal, no prune above
// it). Returns the list of skipped RelPaths and any unexpected error.
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

		op, newContent, cerr := classifyUninstall(f, body)
		if cerr != nil {
			return skipped, fmt.Errorf("uninstall: %w", cerr)
		}
		switch op {
		case opSkip:
			skipped = append(skipped, f.RelPath)
		case opModify:
			if werr := state.WriteAtomic(abs, newContent, 0o644); werr != nil {
				return skipped, fmt.Errorf("uninstall: rewrite %s: %w", f.RelPath, werr)
			}
		case opRemove:
			if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				// Best-effort: a remove failure is non-fatal to the overall
				// uninstall — skip pruning above it and move on.
				continue
			}
			pruneEmptyDirs(root, filepath.Dir(abs))
		}
	}
	return skipped, nil
}

// UninstallVerdict is one entry of an UninstallPlan: the read-only classification
// of what Uninstall WOULD do to a recorded file, for the `--dry-run` preview.
type UninstallVerdict struct {
	RelPath string
	// Op is one of: remove | modify | skip | absent.
	Op string
}

// UninstallPlan classifies each recorded file WITHOUT touching disk — the read-
// only twin of Uninstall, sharing classifyUninstall so the preview matches the
// act exactly. A missing file reports "absent".
func UninstallPlan(root string, files []store.FileRec) ([]UninstallVerdict, error) {
	out := make([]UninstallVerdict, 0, len(files))
	for _, f := range files {
		body, rerr := os.ReadFile(filepath.Join(root, f.RelPath))
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				out = append(out, UninstallVerdict{RelPath: f.RelPath, Op: opAbsent.String()})
				continue
			}
			return nil, fmt.Errorf("uninstall plan: read %s: %w", f.RelPath, rerr)
		}
		op, _, cerr := classifyUninstall(f, body)
		if cerr != nil {
			return nil, fmt.Errorf("uninstall plan: %w", cerr)
		}
		out = append(out, UninstallVerdict{RelPath: f.RelPath, Op: op.String()})
	}
	return out, nil
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

// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// tarEntrySafe rejects a tar entry that the CLI hydrate extractor
// (internal/cli/extract, the SAFE-01 policy) would refuse under ANY policy —
// so the operator never marks a Plugin/Skill Synced=True for a tarball that is
// guaranteed to fail at hydrate extract time (F3). Without this, the verifiers
// returned on the first recognized entry and a tar carrying both a valid
// manifest/SKILL.md AND a path-traversal / hardlink / device / fifo / unknown
// entry would pass operator validation, reach Available=True, then break the
// CLI extractor — a Synced-but-unhydratable gap.
//
// It mirrors extract.checkSafeRel (lexically — the operator has no destination
// dir) plus the unconditional typeflag rejections. In-tree symlinks are
// ADMITTED: the CLI accepts them under --allow-symlinks, so rejecting them
// operator-side would false-fail content that is legitimately hydratable. Only
// out-of-tree symlink targets (absolute or escaping), which the CLI rejects
// under EVERY policy, fail here. All rejections wrap sources.ErrUpstreamInvalid
// (→ ReasonUpstreamInvalid).
func tarEntrySafe(hdr *tar.Header) error {
	if err := safeRelLexical(hdr.Name); err != nil {
		return fmt.Errorf("unsafe tar entry %q: %v: %w", hdr.Name, err, sources.ErrUpstreamInvalid)
	}
	// PAX path injection: archive/tar applies a `path` record to the NEXT
	// entry's Name, but a global/edge-case record may still be visible here —
	// check it defensively, matching extract.paxInjectedPath.
	if hdr.PAXRecords != nil {
		if p, ok := hdr.PAXRecords["path"]; ok {
			if err := safeRelLexical(p); err != nil {
				return fmt.Errorf("unsafe pax path %q: %v: %w", p, err, sources.ErrUpstreamInvalid)
			}
		}
	}
	switch hdr.Typeflag {
	case tar.TypeReg, tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader:
		return nil
	case tar.TypeSymlink:
		return symlinkTargetSafe(hdr)
	case tar.TypeLink:
		return fmt.Errorf("unsafe tar entry %q: hardlink rejected: %w", hdr.Name, sources.ErrUpstreamInvalid)
	case tar.TypeChar, tar.TypeBlock:
		return fmt.Errorf("unsafe tar entry %q: device file rejected: %w", hdr.Name, sources.ErrUpstreamInvalid)
	case tar.TypeFifo:
		return fmt.Errorf("unsafe tar entry %q: fifo rejected: %w", hdr.Name, sources.ErrUpstreamInvalid)
	default:
		return fmt.Errorf("unsafe tar entry %q: unsupported typeflag %d: %w", hdr.Name, hdr.Typeflag, sources.ErrUpstreamInvalid)
	}
}

// safeRelLexical is the operator-side, destination-less analog of
// extract.checkSafeRel: it rejects empty, absolute, and `..`-escaping tar
// entry names. tar names are forward-slash by spec, so an absolute path begins
// with "/"; the cleaned path escaping the root means a leading "..".
func safeRelLexical(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if strings.HasPrefix(name, "/") {
		return errors.New("absolute path")
	}
	cleaned := path.Clean(strings.TrimPrefix(name, "./"))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("dotdot escapes root")
	}
	return nil
}

// symlinkTargetSafe rejects a symlink whose target the CLI extractor rejects
// under EVERY policy (extract.writeSymlink): empty, absolute, or a target that
// lexically escapes the extraction root. In-tree relative targets are admitted.
func symlinkTargetSafe(hdr *tar.Header) error {
	ln := hdr.Linkname
	if ln == "" {
		return fmt.Errorf("unsafe tar entry %q: empty symlink target: %w", hdr.Name, sources.ErrUpstreamInvalid)
	}
	if strings.HasPrefix(ln, "/") {
		return fmt.Errorf("unsafe tar entry %q: absolute symlink target %q: %w", hdr.Name, ln, sources.ErrUpstreamInvalid)
	}
	parent := path.Dir(path.Clean(strings.TrimPrefix(hdr.Name, "./")))
	resolved := path.Clean(path.Join(parent, ln))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("unsafe tar entry %q: symlink target %q escapes root: %w", hdr.Name, ln, sources.ErrUpstreamInvalid)
	}
	return nil
}

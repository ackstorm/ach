// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// Operator-side full-walk caps (F3 review). verifyPluginContents /
// verifySkillContents now walk every entry, so a decompression bomb or a
// million-header tar could monopolize a reconcile. These bound the walk well
// above any legitimate plugin/skill (real content is MB-scale and a handful of
// files) yet stop adversarial archives before the CLI's own caps would.
//
// Declared as var (effectively const) only so tests can lower them cheaply
// instead of building gigabyte/100k-entry fixtures.
var (
	maxVerifyEntries                 = 100_000
	maxVerifyDecompressedBytes int64 = 1 << 30 // 1 GiB
)

// countingReader tallies bytes read so a verify walk can bound cumulative
// DECOMPRESSED output against maxVerifyDecompressedBytes.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// cappedTarReader wraps a gzip stream in a decompression-bounded tar.Reader:
// the LimitReader hard-caps total decompressed bytes (bomb defense) and the
// countingReader exposes the running total for an explicit over-cap error.
func cappedTarReader(gz io.Reader) (*tar.Reader, *countingReader) {
	cr := &countingReader{r: io.LimitReader(gz, maxVerifyDecompressedBytes+1)}
	return tar.NewReader(cr), cr
}

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
	case tar.TypeReg, tar.TypeSymlink:
		// A "."/"./" regular-file or symlink entry resolves to the extraction
		// root itself, which the CLI extractor cannot materialize (it collides
		// with the existing dst). Reject so it never passes operator validation
		// (F3 review). A "." DIRECTORY marker is harmless and handled below.
		if path.Clean(hdr.Name) == "." {
			return fmt.Errorf("unsafe tar entry %q: resolves to extraction root: %w", hdr.Name, sources.ErrUpstreamInvalid)
		}
		if hdr.Typeflag == tar.TypeSymlink {
			return symlinkTargetSafe(hdr)
		}
		return nil
	case tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader:
		return nil
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
// entry names.
//
// F3 review fixes:
//   - Clean the RAW name (do NOT TrimPrefix "./" first): trimming turned
//     ".//../evil" into "/../evil" → "/evil", bypassing the traversal check.
//     path.Clean(".//../evil") == "../evil" is correctly rejected.
//   - Reject backslashes and Windows volume prefixes (`C:\`, `C:`): a Windows
//     hydrate client's filepath treats `\` as a separator and `..\escape` /
//     `C:\abs` as traversal/absolute, so the operator must reject them too or
//     it would OK a tar that client cannot extract.
func safeRelLexical(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if strings.ContainsRune(name, '\\') {
		return errors.New("backslash in path (rejected for Windows-client parity)")
	}
	if hasWindowsVolume(name) {
		return errors.New("windows volume path")
	}
	cleaned := path.Clean(name)
	if strings.HasPrefix(cleaned, "/") {
		return errors.New("absolute path")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("dotdot escapes root")
	}
	return nil
}

// hasWindowsVolume reports whether name begins with a Windows drive-letter
// volume (e.g. "C:" / "c:\\foo"), which a Windows filepath treats as absolute.
func hasWindowsVolume(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	c := name[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// symlinkTargetSafe rejects a symlink whose target the CLI extractor rejects
// under EVERY policy (extract.writeSymlink): empty, absolute, backslash/volume
// (Windows-client parity), or a target that lexically escapes the extraction
// root. In-tree relative targets are admitted.
func symlinkTargetSafe(hdr *tar.Header) error {
	ln := hdr.Linkname
	if ln == "" {
		return fmt.Errorf("unsafe tar entry %q: empty symlink target: %w", hdr.Name, sources.ErrUpstreamInvalid)
	}
	if strings.HasPrefix(ln, "/") || strings.ContainsRune(ln, '\\') || hasWindowsVolume(ln) {
		return fmt.Errorf("unsafe tar entry %q: absolute/non-portable symlink target %q: %w", hdr.Name, ln, sources.ErrUpstreamInvalid)
	}
	parent := path.Dir(path.Clean(hdr.Name))
	resolved := path.Clean(path.Join(parent, ln))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("unsafe tar entry %q: symlink target %q escapes root: %w", hdr.Name, ln, sources.ErrUpstreamInvalid)
	}
	return nil
}

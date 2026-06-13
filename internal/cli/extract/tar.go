// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/ach/internal/cli/hash"
)

// Sentinel errors. The hydrate orchestrator (07-W2-02 staging layer)
// matches on these via errors.Is to discard partial output and emit
// the right *exit.CodedError.
var (
	// ErrUnsafeTarEntry is returned for any SAFE-01 rejection:
	// absolute paths, `..` segments, paths normalized outside dst,
	// hardlinks, devices, FIFOs, sockets, symlinks under default
	// policy, symlinks whose resolved target escapes dst, and pax-
	// extended-header path injections.
	ErrUnsafeTarEntry = errors.New("extract: unsafe tar entry")

	// ErrBombCapExceeded is returned when an entry's body would push
	// the cumulative uncompressed bytes past Limits.MaxBytesForKind(k).
	// The check fires BEFORE writing the offending bytes (SAFE-03 /
	// CLAUDE.md "Decompression-bomb caps" — bomb-defense ordering).
	// The partially-written file is removed; the caller discards the
	// entire staging dir.
	ErrBombCapExceeded = errors.New("extract: bomb cap exceeded")

	// ErrTooManyEntries is returned when the archive's entry count
	// would exceed Limits.MaxEntries. The check fires BEFORE the
	// offending entry's body is read.
	ErrTooManyEntries = errors.New("extract: too many entries")
)

// FileWrite records one materialized file. The hash is the canonical
// `xxh3:<32hex>` digest of the bytes written to disk; the path is
// relative to dst.
type FileWrite struct {
	RelPath string
	Hash    string
	Mode    os.FileMode
}

// Result is the per-call outcome. WrittenFiles preserves the order
// the archive listed entries (callers downstream depend on insertion
// order for the state ledger's deterministic file table).
type Result struct {
	WrittenFiles []FileWrite
	BytesWritten int64
}

// Extract decompresses a gzipped tar stream into dst with the
// hand-rolled SAFE-01..06 policy.
//
// Parameters:
//
//   - ctx: cancellable context; checked between entries.
//   - gzReader: the raw gzipped tar bytes (e.g. the HTTP response body
//     from the Content Service). Wrapped via compress/gzip.NewReader →
//     archive/tar.NewReader so the archive is NEVER fully buffered.
//   - dst: the destination directory. MUST exist and MUST be empty for
//     the caller's safety; this function does not enforce emptiness
//     but the W2-02 staging layer does.
//   - kind: ResourceKind routing for the per-entry bomb-cap limit.
//   - limits: bomb-defense configuration (per-kind byte cap +
//     entry-count cap).
//   - allowSymlinks: when true, in-tree-resolved symlinks are admitted;
//     out-of-tree symlinks remain rejected. Default false rejects all.
//
// Returns an error wrapping one of the sentinel errors above, or a
// raw I/O error from the underlying reader/writer.
//
// The function holds no global state; concurrent calls with disjoint
// dst directories are safe.
func Extract(
	ctx context.Context,
	gzReader io.Reader,
	dst string,
	kind ResourceKind,
	limits Limits,
	allowSymlinks bool,
) (Result, error) {
	gz, err := gzip.NewReader(gzReader)
	if err != nil {
		return Result{}, fmt.Errorf("extract: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	maxBytes := limits.MaxBytesForKind(kind)

	var (
		res         Result
		entriesSeen int
	)
	for {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("extract: context cancelled: %w", err)
		}

		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, fmt.Errorf("extract: tar header: %w", err)
		}

		// Entry-count check fires BEFORE we touch the entry body.
		entriesSeen++
		if entriesSeen > limits.MaxEntries {
			return res, fmt.Errorf("%w: count %d exceeds cap %d",
				ErrTooManyEntries, entriesSeen, limits.MaxEntries)
		}

		// Pax-extended-header path injection: tar.Reader transparently
		// applies PAX records to the *next* entry's Name, so by the time
		// Next() returns, hdr.Name already reflects the injection. We
		// still defensively check the raw PAXRecords map (some archives
		// carry global PAX headers that the reader merges later).
		if injected, ok := paxInjectedPath(hdr); ok {
			if err := checkSafeRel(dst, injected); err != nil {
				return res, fmt.Errorf("%w (pax path %q): %v",
					ErrUnsafeTarEntry, injected, err)
			}
		}

		// Path safety on hdr.Name regardless of Typeflag.
		if err := checkSafeRel(dst, hdr.Name); err != nil {
			return res, fmt.Errorf("%w (%s): %v",
				ErrUnsafeTarEntry, hdr.Name, err)
		}

		// Resolve target on disk; checkSafeRel guarantees it is within dst.
		target := filepath.Join(dst, filepath.Clean(hdr.Name))

		switch hdr.Typeflag {
		case tar.TypeDir:
			// Forced 0o0755 per SAFE-02 — recorded mode ignored.
			if err := os.MkdirAll(target, 0o0755); err != nil {
				return res, fmt.Errorf("extract: mkdir %s: %w", target, err)
			}

		case tar.TypeReg:
			// tar.TypeRegA ('\x00') has been an alias for TypeReg since Go 1.1
			// and the package itself normalizes any pre-USTAR null-byte
			// typeflag entries into TypeReg before returning; no need for a
			// separate case (the constant is now staticcheck-deprecated).
			//
			// Pass the *remaining* budget (cap minus bytes already written
			// this archive) so the cap is cumulative across entries — N
			// files each just under the per-file cap can no longer extract
			// N × maxBytes. (#4)
			remaining := maxBytes - res.BytesWritten
			if maxBytes <= 0 {
				remaining = 0 // 0 => capWriter treats as "no cap" (existing semantics)
			}
			fw, written, err := writeRegular(target, hdr, tr, remaining)
			if err != nil {
				return res, err
			}
			res.WrittenFiles = append(res.WrittenFiles, fw)
			res.BytesWritten += written

		case tar.TypeSymlink:
			if !allowSymlinks {
				return res, fmt.Errorf("%w (%s): symlinks rejected by default policy",
					ErrUnsafeTarEntry, hdr.Name)
			}
			if err := writeSymlink(dst, target, hdr); err != nil {
				return res, err
			}

		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			// PAX extended headers carry no on-disk artifact directly;
			// the tar.Reader transparently merges them into the *next*
			// entry. The path-injection check happened above via
			// paxInjectedPath. Continue to the next entry without
			// writing or counting toward the body cap.
			continue

		case tar.TypeLink:
			return res, fmt.Errorf("%w (%s): hardlinks rejected unconditionally",
				ErrUnsafeTarEntry, hdr.Name)

		case tar.TypeChar, tar.TypeBlock:
			return res, fmt.Errorf("%w (%s): device files rejected unconditionally",
				ErrUnsafeTarEntry, hdr.Name)

		case tar.TypeFifo:
			return res, fmt.Errorf("%w (%s): FIFOs rejected unconditionally",
				ErrUnsafeTarEntry, hdr.Name)

		default:
			// TypeCont, TypeGNULongName, TypeGNULongLink, TypeGNUSparse,
			// sockets, any unknown — all unconditionally rejected.
			return res, fmt.Errorf("%w (%s): unsupported typeflag %d",
				ErrUnsafeTarEntry, hdr.Name, hdr.Typeflag)
		}
	}

	return res, nil
}

// paxInjectedPath returns (path, true) when the PAX record map carries
// a `path` key that would override hdr.Name on the next entry. The
// returned path is the raw injected string; checkSafeRel inspects it
// for SAFE-01 violation.
func paxInjectedPath(hdr *tar.Header) (string, bool) {
	if hdr.PAXRecords == nil {
		return "", false
	}
	p, ok := hdr.PAXRecords["path"]
	return p, ok
}

// checkSafeRel validates a tar-entry path string against dst per SAFE-01:
// rejects absolute, `..` segments, anything that resolves outside dst.
// Empty / whitespace-only / "." / ".." names are also rejected to keep
// the policy strict.
func checkSafeRel(dst, name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if strings.HasPrefix(name, "/") {
		return errors.New("absolute path")
	}
	// Defense in depth: archive/tar produces forward-slash names but
	// filepath.IsAbs on Windows would catch C:\ etc.
	if filepath.IsAbs(name) {
		return errors.New("absolute path (filepath.IsAbs)")
	}
	cleaned := filepath.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return errors.New("dotdot segment escapes dst")
	}
	// `filepath.Join(dst, cleaned)` would silently collapse "../" with
	// no error; verify the resolved absolute path stays under dst.
	resolved := filepath.Join(dst, cleaned)
	// Compare with a trailing separator so /tmp/dstevil does NOT match /tmp/dst.
	dstWithSep := dst + string(os.PathSeparator)
	if resolved != dst && !strings.HasPrefix(resolved, dstWithSep) {
		return errors.New("resolved path escapes dst")
	}
	return nil
}

// maskMode applies SAFE-02 — masks to mode & 0o0755, unconditionally
// stripping setuid (0o4000), setgid (0o2000), sticky (0o1000),
// group-write (0o0020), world-write (0o0002). The result is one of
// the standard 8-mode triads {755, 754, 750, 744, 740, 700, 644, 640, ...}.
func maskMode(recorded int64) os.FileMode {
	return os.FileMode(recorded) & 0o0755
}

// capWriter wraps an io.Writer and tracks cumulative byte count
// against maxBytes. If maxBytes is 0, the cap is disabled (prompts
// path). A Write that would push count past maxBytes returns
// ErrBombCapExceeded BEFORE the underlying writer is touched —
// guaranteeing no bytes from the offending entry land on disk.
type capWriter struct {
	w        io.Writer
	maxBytes int64
	written  int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.maxBytes > 0 && c.written+int64(len(p)) > c.maxBytes {
		return 0, fmt.Errorf("%w: would write %d bytes past cap %d",
			ErrBombCapExceeded, c.written+int64(len(p)), c.maxBytes)
	}
	n, err := c.w.Write(p)
	c.written += int64(n)
	return n, err
}

// writeRegular streams hdr's body into dst-relative target with mode
// masking and bomb-cap enforcement. The file is removed on any failure
// (partial write must not survive). The xxh3 hash is computed by
// reading the materialized file after the stream completes; this
// keeps the write path tight (a single io.Copy through capWriter) and
// matches the W1-04 hash package's streaming contract.
func writeRegular(
	target string,
	hdr *tar.Header,
	tr *tar.Reader,
	maxBytes int64,
) (FileWrite, int64, error) {
	// Ensure the parent directory exists (tar archives don't always
	// include leading directory entries). Use 0o0755 per SAFE-02.
	if err := os.MkdirAll(filepath.Dir(target), 0o0755); err != nil {
		return FileWrite{}, 0, fmt.Errorf("extract: mkdir parent %s: %w",
			filepath.Dir(target), err)
	}
	mode := maskMode(hdr.Mode)
	// O_EXCL refuses to overwrite an existing file; the W2-02 staging
	// layer guarantees a fresh dst so any pre-existing file is the
	// archive trying to clobber itself (or worse, the caller's tmp).
	f, err := os.OpenFile(target,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, mode)
	if err != nil {
		return FileWrite{}, 0, fmt.Errorf("extract: create %s: %w", target, err)
	}

	cap := &capWriter{w: f, maxBytes: maxBytes}
	_, copyErr := io.Copy(cap, tr)
	closeErr := f.Close()

	if copyErr != nil {
		_ = os.Remove(target)
		return FileWrite{}, 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return FileWrite{}, 0, fmt.Errorf("extract: close %s: %w", target, closeErr)
	}
	// Mtime/atime NOT preserved per SAFE-02 — explicitly do not call os.Chtimes.

	// Compute the xxh3 digest by re-reading the freshly-written file.
	rf, openErr := os.Open(target)
	if openErr != nil {
		_ = os.Remove(target)
		return FileWrite{}, 0, fmt.Errorf("extract: re-open %s for hash: %w", target, openErr)
	}
	digest, hashErr := hash.Hash(rf)
	_ = rf.Close()
	if hashErr != nil {
		_ = os.Remove(target)
		return FileWrite{}, 0, fmt.Errorf("extract: hash %s: %w", target, hashErr)
	}

	// Compute RelPath as the cleaned, slash-style path used in state ledger.
	relPath := filepath.Clean(hdr.Name)
	return FileWrite{
		RelPath: relPath,
		Hash:    digest,
		Mode:    mode,
	}, cap.written, nil
}

// writeSymlink materializes an in-tree symlink. Out-of-tree targets
// (absolute, ../escape, or any path whose Lexically-resolved value
// lands outside dst) are rejected even when allowSymlinks=true per
// the SAFE-01 in-tree-only guarantee.
//
// The resolved-target check works on the LEXICAL resolution of
// Linkname relative to the symlink's parent dir; we deliberately do
// NOT call filepath.EvalSymlinks here because the symlink target may
// not yet exist on disk at extraction time, and resolution would
// follow real-fs links that the caller cannot have planted yet.
func writeSymlink(dst, linkPath string, hdr *tar.Header) error {
	if hdr.Linkname == "" {
		return fmt.Errorf("%w (%s): symlink with empty Linkname",
			ErrUnsafeTarEntry, hdr.Name)
	}
	if filepath.IsAbs(hdr.Linkname) {
		return fmt.Errorf("%w (%s -> %s): symlink with absolute target",
			ErrUnsafeTarEntry, hdr.Name, hdr.Linkname)
	}
	// Lexically resolve Linkname relative to the symlink's parent dir,
	// then verify the result is still under dst.
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), hdr.Linkname))
	dstWithSep := dst + string(os.PathSeparator)
	if resolved != dst && !strings.HasPrefix(resolved, dstWithSep) {
		return fmt.Errorf("%w (%s -> %s): symlink target escapes dst",
			ErrUnsafeTarEntry, hdr.Name, hdr.Linkname)
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o0755); err != nil {
		return fmt.Errorf("extract: mkdir parent for symlink %s: %w", linkPath, err)
	}
	if err := os.Symlink(hdr.Linkname, linkPath); err != nil {
		return fmt.Errorf("extract: symlink %s -> %s: %w", linkPath, hdr.Linkname, err)
	}
	return nil
}

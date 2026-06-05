// SPDX-License-Identifier: Apache-2.0

package sources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"
)

// DefaultArchiveIngressCap bounds the in-memory buffer used when narrowing a
// REST repo-archive to a subtree. The git transport narrows on-disk during the
// tar step (git.tarSubtree) and never reaches this path; only the legacy REST
// transport buffers here. Mirrors git's gitDefaultMaxCloneBytes ceiling — the
// user-facing per-CR size cap is enforced downstream by the reconciler.
const DefaultArchiveIngressCap = 512 << 20

// NarrowArchiveSubtree reads a gzip-tar archive from body (bounded by capBytes),
// strips at most one archive-root wrapper directory (the "<repo>-<sha>/" prefix
// the GitHub/GitLab/Bitbucket REST archive endpoints add — git-protocol
// archives have none), and narrows to the path at relPath. It mirrors the
// on-disk git.tarSubtree so a downstream consumer is transport-agnostic (F1):
//
//   - relPath names a DIRECTORY → a fresh gzip-tar RE-ROOTED at that
//     directory's CONTENTS (the relPath prefix stripped entirely).
//   - relPath names a single regular FILE → that file's RAW bytes (no tar
//     wrapper) — Prompt / Artifact scope=object name a single file via path.
//   - relPath empty/root → the buffered bytes unchanged.
//
// A relPath that matches nothing returns a wrapped ErrUpstreamInvalid; an
// archive exceeding capBytes returns ErrUpstreamInvalid.
func NarrowArchiveSubtree(body io.Reader, relPath string, capBytes int64) ([]byte, error) {
	if capBytes <= 0 {
		capBytes = DefaultArchiveIngressCap
	}
	raw, err := io.ReadAll(io.LimitReader(body, capBytes+1))
	if err != nil {
		return nil, fmt.Errorf("sources: read archive: %w", err)
	}
	if int64(len(raw)) > capBytes {
		return nil, fmt.Errorf("sources: archive exceeds %d-byte ingress cap: %w", capBytes, ErrUpstreamInvalid)
	}

	sub := normalizeSubtree(relPath)
	if sub == "" {
		return raw, nil
	}

	names, err := archiveRegularNames(raw)
	if err != nil {
		return nil, fmt.Errorf("sources: %w", err)
	}
	root := archiveWrapperRoot(names)

	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("sources: gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	var buf bytes.Buffer
	outGz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(outGz)
	prefix := sub + "/"
	wrote := false
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("sources: tar read: %w", e)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			continue // skip symlinks / devices / etc — git.tarSubtree does the same
		}
		rel := stripWrapper(hdr.Name, root)
		if rel == sub {
			if hdr.Typeflag == tar.TypeReg {
				// relPath names a single file → return its RAW bytes (no tar).
				fileBytes, ferr := io.ReadAll(io.LimitReader(tr, capBytes+1))
				if ferr != nil {
					return nil, fmt.Errorf("sources: read file %q: %w", relPath, ferr)
				}
				return fileBytes, nil
			}
			continue // the subtree's own dir entry — its CONTENTS re-root to "."
		}
		if !strings.HasPrefix(rel, prefix) {
			continue
		}
		rerooted := strings.TrimPrefix(rel, prefix)
		if rerooted == "" {
			continue
		}
		nh := &tar.Header{Name: rerooted, Mode: hdr.Mode, Size: hdr.Size, Typeflag: hdr.Typeflag, ModTime: hdr.ModTime}
		if hdr.Typeflag == tar.TypeDir {
			nh.Name = rerooted + "/"
		}
		if err := tw.WriteHeader(nh); err != nil {
			return nil, fmt.Errorf("sources: write header %s: %w", rerooted, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.Copy(tw, tr); err != nil { //nolint:gosec // bounded: raw already capped at capBytes
				return nil, fmt.Errorf("sources: copy %s: %w", rel, err)
			}
		}
		wrote = true
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := outGz.Close(); err != nil {
		return nil, err
	}
	if !wrote {
		return nil, fmt.Errorf("sources: path %q not found in archive: %w", relPath, ErrUpstreamInvalid)
	}
	return buf.Bytes(), nil
}

// normalizeSubtree cleans a spec.<git>.path into a slash-trimmed relative path
// ("" for empty/root). Mirrors the controller's normSubPath.
func normalizeSubtree(p string) string {
	p = path.Clean(strings.Trim(strings.TrimSpace(p), "/"))
	if p == "." || p == "/" {
		return ""
	}
	return p
}

// stripWrapper removes the optional single archive-root wrapper segment from a
// tar entry name and cleans it.
func stripWrapper(name, root string) string {
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if root != "" {
		clean = strings.TrimPrefix(clean, root+"/")
	}
	return clean
}

// archiveWrapperRoot returns the single common leading segment shared by ALL
// regular-file entries (the REST "<repo>-<sha>/" wrapper), or "" when entries
// are already root-relative (git transport) or span multiple top-level dirs.
func archiveWrapperRoot(names []string) string {
	root := ""
	for _, n := range names {
		clean := path.Clean(strings.TrimPrefix(n, "./"))
		i := strings.IndexByte(clean, '/')
		if i < 0 {
			return "" // a file sits at top level → no single wrapper root
		}
		first := clean[:i]
		if root == "" {
			root = first
		} else if root != first {
			return ""
		}
	}
	return root
}

// archiveRegularNames returns the names of all regular-file entries in one
// streaming pass so archiveWrapperRoot can run before the re-root pass.
func archiveRegularNames(tarball []byte) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("tar read: %w", e)
		}
		if hdr.Typeflag == tar.TypeReg {
			names = append(names, hdr.Name)
		}
	}
	return names, nil
}

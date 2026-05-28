// SPDX-License-Identifier: Apache-2.0

package pluginpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// manifestMaxBytes caps the size of the `.claude-plugin/plugin.json`
// entry the filter is willing to buffer in memory. Mirrors the
// marketplace_extract.go marketplaceJSONInTarballMaxBytes constant
// (5 MiB — enough for any sane plugin manifest).
const manifestMaxBytes = 5 << 20

// pluginTarballMaxEntries bounds the inbound tar walk. Mirrors the
// marketplaceTarballMaxEntries constant (50000 — two orders of
// magnitude headroom over real-world Plugin tarballs).
const pluginTarballMaxEntries = 50000

// manifestRelPath is the location inside the tarball where the
// `.claude-plugin/plugin.json` manifest must live. The Plugin CR
// fetcher (git.tarSubtree) strips the subtree prefix, so the manifest
// always sits at the tar root.
const manifestRelPath = ".claude-plugin/plugin.json"

// Filter reads a gzipped tar (the upstream-fetched Plugin tarball
// body) and returns a fresh gzipped tar containing only the runtime-
// relevant subset (see the package doc for the whitelist edges).
//
// Errors:
//
//   - gzip header malformed → wraps sources.ErrUpstreamInvalid.
//   - more than pluginTarballMaxEntries entries scanned → wraps
//     sources.ErrUpstreamInvalid.
//   - `.claude-plugin/plugin.json` absent → ErrManifestMissing
//     wrapping sources.ErrUpstreamInvalid.
//   - plugin.json JSON parse failure → wraps sources.ErrUpstreamInvalid.
//   - manifest reference escapes plugin root (`..` or absolute) →
//     wraps sources.ErrUpstreamInvalid (NOT ErrManifestMissing).
//
// The function is pure — no I/O outside the in-memory byte buffer.
func Filter(in []byte) ([]byte, error) {
	// First pass: decompress, walk the tar, collect every entry we
	// might keep, locate plugin.json.
	gz, err := gzip.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, fmt.Errorf("pluginpack: gzip header: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)

	// collected captures the bits of a source tar entry we need to
	// re-emit later. Regular file bodies are buffered in memory; dir
	// entries carry just the header metadata.
	type collected struct {
		name     string
		typeflag byte
		hdr      tar.Header
		body     []byte // regular files only
	}

	var entries []collected
	var manifestBytes []byte
	manifestSeen := false

	scanned := 0
	for {
		if scanned >= pluginTarballMaxEntries {
			return nil, fmt.Errorf("pluginpack: exceeded %d tar entries: %w", pluginTarballMaxEntries, sources.ErrUpstreamInvalid)
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pluginpack: tar header: %v: %w", err, sources.ErrUpstreamInvalid)
		}
		scanned++

		// Reject `..`-bearing entries outright (TOCTOU-ish defense;
		// we don't extract, but a `..`-name would never match the
		// whitelist anyway).
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		// Only regular files and directories are considered — drop
		// symlinks, devices, FIFOs.
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			continue
		}

		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		// path.Clean strips trailing slashes; restore the explicit dir
		// marker for TypeDir entries so the include predicate matches
		// the prefix shape `<name>/`.
		if hdr.Typeflag == tar.TypeDir && !strings.HasSuffix(name, "/") {
			name += "/"
		}

		c := collected{
			name:     name,
			typeflag: hdr.Typeflag,
			hdr: tar.Header{
				Name:     name,
				Mode:     hdr.Mode,
				Size:     hdr.Size,
				ModTime:  hdr.ModTime,
				Typeflag: hdr.Typeflag,
			},
		}

		if hdr.Typeflag == tar.TypeReg {
			// Bound the per-entry read at manifestMaxBytes when this
			// is the manifest file; otherwise read in full. The
			// fetcher already capped the whole-tarball size via the
			// git engine's MaxCloneBytes.
			if name == manifestRelPath {
				if hdr.Size < 0 || hdr.Size > manifestMaxBytes {
					return nil, fmt.Errorf("pluginpack: %s header claims %d bytes (cap %d): %w",
						manifestRelPath, hdr.Size, manifestMaxBytes, sources.ErrUpstreamInvalid)
				}
				body, readErr := io.ReadAll(io.LimitReader(tr, manifestMaxBytes+1))
				if readErr != nil {
					return nil, fmt.Errorf("pluginpack: read %s: %v: %w", manifestRelPath, readErr, sources.ErrUpstreamInvalid)
				}
				if int64(len(body)) > manifestMaxBytes {
					return nil, fmt.Errorf("pluginpack: %s body exceeds cap %d: %w",
						manifestRelPath, manifestMaxBytes, sources.ErrUpstreamInvalid)
				}
				manifestBytes = body
				manifestSeen = true
				c.body = body
			} else {
				body, readErr := io.ReadAll(tr)
				if readErr != nil {
					return nil, fmt.Errorf("pluginpack: read entry %q: %v: %w", name, readErr, sources.ErrUpstreamInvalid)
				}
				c.body = body
			}
		}

		entries = append(entries, c)
	}

	if !manifestSeen {
		return nil, fmt.Errorf("%w: %w", ErrManifestMissing, sources.ErrUpstreamInvalid)
	}

	// Parse the manifest + extract the parent-dir transitive set.
	manifestTree, err := parsePluginJSON(manifestBytes)
	if err != nil {
		return nil, err
	}
	parentDirs, err := extractReferences(manifestTree)
	if err != nil {
		return nil, err
	}

	// Build the include predicate.
	includeName := func(name string) bool {
		// Whitelist exacts.
		if name == manifestRelPath {
			return true
		}
		if name == "README.md" {
			return true
		}
		if isRootLicense(name) {
			return true
		}
		// Root convention dirs (root-level only — `agents/foo` yes,
		// `plugins/x/agents/foo` no).
		if matchesRootConventionDir(name) {
			return true
		}
		// Manifest-referenced parent dirs.
		for parent := range parentDirs {
			// Match parent itself (with or without trailing slash)
			// or any descendant.
			if name == parent ||
				name == parent+"/" ||
				strings.HasPrefix(name, parent+"/") {
				return true
			}
		}
		return false
	}

	// Determine the set of directories we need to emit as explicit
	// tar.TypeDir entries (defensive — content-service streams via
	// sendfile, but downstream consumers like CLI hydrate / debug
	// tools may extract).
	includedDirs := map[string]struct{}{}
	includedFiles := map[string]bool{}
	for _, c := range entries {
		// Strip trailing slash from dir-names for inclusion testing.
		test := strings.TrimSuffix(c.name, "/")
		if test == "" {
			continue
		}
		// Re-form the predicate input so that a dir entry tested via
		// its with-slash form matches `<name>/`. Use the trailing-
		// slash form for dirs (so prefix tests work) and the bare
		// form for files.
		var pname string
		if c.typeflag == tar.TypeDir {
			pname = test + "/"
		} else {
			pname = test
		}
		if !includeName(pname) {
			continue
		}
		if c.typeflag == tar.TypeDir {
			includedDirs[test] = struct{}{}
		} else {
			includedFiles[test] = true
			// Synthesize the file's parent directory chain into the
			// includedDirs set so the emitted tar always has the
			// `parent/` headers preceding the file headers.
			for d := path.Dir(test); d != "." && d != "/"; d = path.Dir(d) {
				includedDirs[d] = struct{}{}
			}
		}
	}

	// Second pass: emit a fresh tar.gz. Directories first (sorted
	// lexicographically so parents precede children), then files in
	// the order they appeared in the source — keeps a deterministic
	// output without sorting overhead on the file pass.
	var outBuf bytes.Buffer
	outGz := gzip.NewWriter(&outBuf)
	outTw := tar.NewWriter(outGz)

	// Emit explicit dir entries.
	for _, d := range sortedKeys(includedDirs) {
		hdr := &tar.Header{
			Name:     d + "/",
			Mode:     0o755,
			Typeflag: tar.TypeDir,
		}
		// Try to mirror the source dir's mode + mtime when we saw an
		// explicit entry for it.
		for _, c := range entries {
			if c.typeflag != tar.TypeDir {
				continue
			}
			if strings.TrimSuffix(c.name, "/") == d {
				hdr.Mode = c.hdr.Mode
				hdr.ModTime = c.hdr.ModTime
				break
			}
		}
		if err := outTw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("pluginpack: write dir header %q: %w", d, err)
		}
	}

	// Emit regular files.
	for _, c := range entries {
		if c.typeflag != tar.TypeReg {
			continue
		}
		if !includedFiles[c.name] {
			continue
		}
		hdr := &tar.Header{
			Name:     c.name,
			Mode:     c.hdr.Mode,
			Size:     int64(len(c.body)),
			ModTime:  c.hdr.ModTime,
			Typeflag: tar.TypeReg,
		}
		if err := outTw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("pluginpack: write file header %q: %w", c.name, err)
		}
		if _, err := outTw.Write(c.body); err != nil {
			return nil, fmt.Errorf("pluginpack: write file body %q: %w", c.name, err)
		}
	}

	if err := outTw.Close(); err != nil {
		return nil, fmt.Errorf("pluginpack: close tar writer: %w", err)
	}
	if err := outGz.Close(); err != nil {
		return nil, fmt.Errorf("pluginpack: close gzip writer: %w", err)
	}
	return outBuf.Bytes(), nil
}

// isRootLicense reports whether name is a root-level LICENSE file.
// Accepts bare `LICENSE` and any extension (LICENSE.txt, LICENSE.md,
// LICENSE-MIT) at the tar root only.
func isRootLicense(name string) bool {
	if strings.Contains(name, "/") {
		return false
	}
	if name == "LICENSE" {
		return true
	}
	return strings.HasPrefix(name, "LICENSE.") || strings.HasPrefix(name, "LICENSE-")
}

// matchesRootConventionDir reports whether name lives under a
// root-level Claude Code convention directory.
func matchesRootConventionDir(name string) bool {
	for _, prefix := range []string{"commands/", "agents/", "skills/", "hooks/", "mcpServers/"} {
		if strings.HasPrefix(name, prefix) || name == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

// sortedKeys returns the map keys sorted lexicographically.
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Insertion sort — set sizes here are tiny (≤ a few dozen entries).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

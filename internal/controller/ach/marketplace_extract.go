// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// marketplaceJSONInTarballMaxBytes caps the marketplace.json entry size
// when extracted from a gzipped repo tarball. The outer body is already
// capped by marketplaceJSONMaxBytes (5 MiB); this is the per-entry cap
// to defend against a tar entry that claims its own oversized size.
const marketplaceJSONInTarballMaxBytes = 5 << 20 // 5 MiB

// marketplaceTarballMaxEntries bounds the tar walk so an adversarial
// upstream cannot stall the reconciler with millions of small headers.
// 50k entries comfortably covers Anthropic's plugins repo (~250 entries)
// with two orders of magnitude headroom.
const marketplaceTarballMaxEntries = 50000

// marketplaceJSONRelPath is the file path inside the upstream repo where
// the Claude Code marketplace declares itself. GitHub/GitLab/Bitbucket
// tarballs always wrap their contents in a single root directory named
// `<repo>-<shortsha>/`, so we match by suffix instead of expecting a
// known prefix.
const marketplaceJSONRelPath = "/.claude-plugin/marketplace.json"

// extractMarketplaceJSON walks a gzipped tar stream and returns the bytes
// of the first regular file whose path ends with marketplaceJSONRelPath.
// Used by the PluginMarketplace reconciler for git-tarball source types
// (github, gitlab, bitbucket) where the fetcher returns the whole repo
// archive (Hub §10.1, Path-subset extraction deferred to v1beta1).
//
// Returns wrapped sources.ErrUpstreamInvalid on:
//   - gzip header malformed
//   - tar header malformed
//   - more than marketplaceTarballMaxEntries entries scanned without a hit
//   - matching file exceeds marketplaceJSONInTarballMaxBytes
//   - no matching entry found in the archive
//
// The reader r is fully consumed up to the first match (or end of stream).
// Callers MUST close their underlying fetch body separately.
func extractMarketplaceJSON(r io.Reader) ([]byte, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("marketplace tarball: gzip header: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	scanned := 0
	for {
		if scanned >= marketplaceTarballMaxEntries {
			return nil, fmt.Errorf("marketplace tarball: exceeded %d tar entries without finding %s: %w",
				marketplaceTarballMaxEntries, marketplaceJSONRelPath, sources.ErrUpstreamInvalid)
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("marketplace tarball: tar header: %v: %w", err, sources.ErrUpstreamInvalid)
		}
		scanned++

		// Only regular files are considered. Symlinks / dirs / device
		// entries are skipped — extraction follows no links (TOCTOU /
		// path-traversal defense).
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Match by suffix to be agnostic of the GitHub-style
		// `<repo>-<shortsha>/` wrapper prefix. The git-protocol
		// fetcher (internal/gitfetch/fetcher.go: tarSubtree)
		// strips the root prefix from entry names, so the tarball
		// it produces lists the file as `.claude-plugin/marketplace.json`
		// (no leading `/`). Accept that bare form alongside the
		// wrapper-prefixed form GitHub/GitLab/Bitbucket archive APIs
		// emit. Reject any name containing `..` to defend against
		// crafted tar entries even though we don't write to disk —
		// pure-paranoia gate.
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		if !strings.HasSuffix(hdr.Name, marketplaceJSONRelPath) &&
			hdr.Name != strings.TrimPrefix(marketplaceJSONRelPath, "/") {
			continue
		}
		// Per-entry cap. hdr.Size is upstream-supplied and must be
		// validated even though we LimitReader below — a negative or
		// gigantic value should be rejected up front so we don't
		// allocate a giant buffer hint.
		if hdr.Size < 0 || hdr.Size > marketplaceJSONInTarballMaxBytes {
			return nil, fmt.Errorf("marketplace tarball: %s header claims %d bytes (cap %d): %w",
				marketplaceJSONRelPath, hdr.Size, marketplaceJSONInTarballMaxBytes, sources.ErrUpstreamInvalid)
		}
		body, err := io.ReadAll(io.LimitReader(tr, marketplaceJSONInTarballMaxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("marketplace tarball: read %s: %v: %w", marketplaceJSONRelPath, err, sources.ErrUpstreamInvalid)
		}
		if int64(len(body)) > marketplaceJSONInTarballMaxBytes {
			return nil, fmt.Errorf("marketplace tarball: %s body exceeds cap %d: %w",
				marketplaceJSONRelPath, marketplaceJSONInTarballMaxBytes, sources.ErrUpstreamInvalid)
		}
		return body, nil
	}
	return nil, fmt.Errorf("marketplace tarball: %s not found: %w", marketplaceJSONRelPath, sources.ErrUpstreamInvalid)
}

// isTarballSourceType reports whether spec.Type returns a gzipped repo
// tarball whose `.claude-plugin/marketplace.json` must be extracted
// before Stage-1 parse. Mirrors the Hub §10.1 fetcher contract.
func isTarballSourceType(t string) bool {
	switch t {
	case "github", "gitlab", "bitbucket":
		return true
	default:
		return false
	}
}

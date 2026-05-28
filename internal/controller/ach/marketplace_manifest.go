// SPDX-License-Identifier: Apache-2.0

// Post-fetch plugin.json existence check. The marketplace dispatcher
// returns a gzipped tar of the plugin's contents (whole worktree or a
// subtree slice). Before persisting the tar via rename(2), the
// materialize step calls verifyPluginManifest to ensure
// `.claude-plugin/plugin.json` is actually present in the stream — a
// fetched tar that lacks the manifest indicates the upstream entry
// doesn't point at a real plugin (e.g. wrong path, repo renamed,
// manifest moved). The contents of the manifest are NOT parsed; only
// presence is checked (issue #15 Pregunta 3).
//
// A missing manifest surfaces as a wrapped sources.ErrUpstreamInvalid,
// which classifyFetchErrorMarketplace already maps to
// ReasonUpstreamInvalid → per-entry pluginFailure.
//
// Tar layout assumption: git.tarSubtree (the only producer of plugin
// tarballs as of v1alpha1) strips the subtree prefix — files appear
// relative to the requested subtree root. So whether the entry has
// `path: "./plugins/x"` or no path at all, the manifest, if present,
// lives at `.claude-plugin/plugin.json` in the tar. The verifier
// therefore takes no subtree argument; an earlier version did and was
// broken in production for every subtree-based fetch.

package ach

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// manifestRelPath is the path the verifier searches for inside the
// tarball (relative to the tar root, which is also the subtree root
// thanks to git.tarSubtree's prefix-stripping).
const manifestRelPath = ".claude-plugin/plugin.json"

// verifyPluginManifest walks the gzipped tar stream r and returns nil
// iff a regular-file entry named `.claude-plugin/plugin.json` exists.
// The walk is stream-only and returns early once the manifest entry
// is found.
func verifyPluginManifest(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("plugin.json check: gzip reader: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("plugin.json check: tar walk: %v: %w", err, sources.ErrUpstreamInvalid)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == manifestRelPath {
			return nil
		}
	}
	return fmt.Errorf("plugin.json check: %s not found in fetched tar: %w",
		manifestRelPath, sources.ErrUpstreamInvalid)
}

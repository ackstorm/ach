// SPDX-License-Identifier: Apache-2.0

// Post-fetch plugin.json existence check. The marketplace dispatcher
// returns a gzipped tar of the plugin's contents (whole worktree or a
// subtree slice). Before persisting the tar via rename(2), the
// materialize step calls verifyPluginManifest to ensure
// `<subtree>/.claude-plugin/plugin.json` is actually present in the
// stream — a fetched tar that lacks the manifest indicates the
// upstream entry doesn't point at a real plugin (e.g. wrong path,
// repo renamed, manifest moved). The contents of the manifest are NOT
// parsed; only presence is checked (issue #15 Pregunta 3).
//
// A missing manifest surfaces as a wrapped sources.ErrUpstreamInvalid,
// which classifyFetchErrorMarketplace already maps to
// ReasonUpstreamInvalid → per-entry pluginFailure.

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

// manifestRelPath is the path-suffix the verifier searches for inside
// the tarball (relative to the resolved subtree root).
const manifestRelPath = ".claude-plugin/plugin.json"

// verifyPluginManifest walks the gzipped tar stream r and returns nil
// iff a regular-file entry exists at <subtree>/.claude-plugin/plugin.json
// (subtree is normalized: leading "./" stripped, trailing "/" stripped).
// Empty subtree means whole-repo tar (manifest at top level).
//
// The walk is stream-only: no full-buffer materialization. Returns
// early once the manifest entry is found.
func verifyPluginManifest(r io.Reader, subtree string) error {
	want := path.Join(normalizeSubtree(subtree), manifestRelPath)
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
		if name == want {
			return nil
		}
	}
	return fmt.Errorf("plugin.json check: %s not found in fetched tar: %w",
		want, sources.ErrUpstreamInvalid)
}

// normalizeSubtree strips a leading "./" and any trailing "/" so the
// resulting path can be path.Join'd against manifestRelPath without
// producing ".//x/y/.claude-plugin/plugin.json".
func normalizeSubtree(s string) string {
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimSuffix(s, "/")
	return s
}

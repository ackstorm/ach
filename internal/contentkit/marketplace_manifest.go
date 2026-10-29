// SPDX-License-Identifier: Apache-2.0

// Post-fetch plugin-contents check. The marketplace dispatcher returns a
// gzipped tar of the plugin's contents (whole worktree or a subtree
// slice). Before persisting the tar via rename(2), the materialize step
// calls VerifyPluginContents to ensure the fetched tar actually looks
// like a Claude Code plugin.
//
// Per the Claude Code plugin schema
// (code.claude.com/docs/en/plugins-reference), `.claude-plugin/plugin.json`
// is OPTIONAL. When omitted, Claude Code auto-discovers components from
// convention directories (commands/, agents/, skills/, hooks/,
// output-styles/, themes/, monitors/) and root files (SKILL.md, .mcp.json,
// .lsp.json), deriving the plugin name from the directory basename — or,
// for a marketplace install, from the marketplace.json entry (which ACH
// already holds as entry.Name). A manifest-less plugin is therefore valid
// and MUST be accepted. (Originally gated as mandatory under issue #15
// Pregunta 3; that conflated "is a real plugin" with "has a manifest" and
// false-failed legitimate convention-only plugins such as
// anthropics/claude-code's plugin-dev.)
//
// The check accepts the tar iff it contains EITHER the manifest OR at
// least one recognized component. A tar with none of these (e.g. only a
// stray README.md) indicates the upstream entry does not point at a real
// plugin (wrong path, repo renamed, contents moved) and surfaces as a
// wrapped sourceserr.ErrUpstreamInvalid; callers map this to an
// upstream-invalid outcome.
//
// Tar layout assumption: git.tarSubtree (the only producer of plugin
// tarballs as of v1alpha1) strips the subtree prefix — files appear
// relative to the requested subtree root. So the manifest, if present,
// lives at `.claude-plugin/plugin.json` and convention dirs appear at the
// tar root. The verifier therefore takes no subtree argument; an earlier
// version did and was broken in production for every subtree-based fetch.

package contentkit

import (
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/sourceserr"
)

// manifestRelPath is the OPTIONAL plugin manifest location inside the tar
// (relative to the tar root, which is also the subtree root thanks to
// git.tarSubtree's prefix-stripping).
const manifestRelPath = ".claude-plugin/plugin.json"

// recognizedComponentDirs are the top-level convention directories a
// manifest-less plugin may carry; Claude Code auto-discovers these. Names
// match the on-disk directory layout (plugins-reference "File locations"
// table), NOT the camelCase plugin.json field names (e.g. dir is
// "output-styles", manifest field is "outputStyles").
var recognizedComponentDirs = map[string]struct{}{
	"commands":      {},
	"agents":        {},
	"skills":        {},
	"hooks":         {},
	"output-styles": {},
	"themes":        {},
	"monitors":      {},
}

// recognizedRootFiles are tar-root files that, alone, mark the directory
// as a plugin: a single-skill plugin (SKILL.md) or inline component
// config (.mcp.json / .lsp.json).
var recognizedRootFiles = map[string]struct{}{
	"SKILL.md":  {},
	".mcp.json": {},
	".lsp.json": {},
}

// VerifyPluginContents walks the gzipped tar stream r and returns nil iff
// the tar contains the optional `.claude-plugin/plugin.json` manifest OR
// at least one recognized plugin component (convention dir / root file) AND
// every entry passes tarEntrySafe (so the CLI hydrate extractor can actually
// unpack it — F3). The walk runs to EOF (no early return): a recognized signal
// seen early does NOT excuse a later unsafe entry, which would otherwise leave
// the plugin Synced=True yet unhydratable.
func VerifyPluginContents(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("plugin contents check: gzip reader: %v: %w", err, sourceserr.ErrUpstreamInvalid)
	}
	defer func() { _ = gz.Close() }()
	tr, cr := cappedTarReader(gz)
	recognized := false
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if cr.n > maxVerifyDecompressedBytes {
				return fmt.Errorf("plugin contents check: archive decompresses past %d-byte cap: %w", maxVerifyDecompressedBytes, sourceserr.ErrUpstreamInvalid)
			}
			return fmt.Errorf("plugin contents check: tar walk: %v: %w", err, sourceserr.ErrUpstreamInvalid)
		}
		entries++
		if entries > maxVerifyEntries {
			return fmt.Errorf("plugin contents check: more than %d entries: %w", maxVerifyEntries, sourceserr.ErrUpstreamInvalid)
		}
		if cr.n > maxVerifyDecompressedBytes {
			return fmt.Errorf("plugin contents check: archive decompresses past %d-byte cap: %w", maxVerifyDecompressedBytes, sourceserr.ErrUpstreamInvalid)
		}
		// Full-tar safety gate: any entry the CLI extractor rejects under every
		// policy fails the whole tar (F3).
		if err := tarEntrySafe(hdr); err != nil {
			return fmt.Errorf("plugin contents check: %w", err)
		}
		// Normalize the "./" some tar writers prefix, then Clean. We do
		// NOT filter by Typeflag: a bare convention directory entry is a
		// valid signal, and the manifest/root-file names only ever match
		// regular files anyway.
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		switch {
		case name == manifestRelPath, isRecognizedRootFile(name), isRecognizedComponentDir(name):
			recognized = true
		}
	}
	if !recognized {
		return fmt.Errorf("plugin contents check: no plugin manifest or recognized component "+
			"(commands/agents/skills/hooks/...) found in fetched tar: %w", sourceserr.ErrUpstreamInvalid)
	}
	return nil
}

// isRecognizedRootFile reports whether the cleaned tar-root name is a
// component-marking root file.
func isRecognizedRootFile(name string) bool {
	_, ok := recognizedRootFiles[name]
	return ok
}

// isRecognizedComponentDir reports whether the cleaned name's first path
// segment is a recognized convention directory.
func isRecognizedComponentDir(name string) bool {
	first := name
	if i := strings.IndexByte(name, '/'); i >= 0 {
		first = name[:i]
	}
	_, ok := recognizedComponentDirs[first]
	return ok
}

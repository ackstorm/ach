// SPDX-License-Identifier: Apache-2.0

package pluginpack

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// pluginRootRefRegex matches every `${CLAUDE_PLUGIN_ROOT}/<path>`
// occurrence inside a string value. The captured group is the relative
// path that follows the placeholder. Stop characters
// (whitespace, double-quote, single-quote, and `$`) bound the match so
// shell-style command strings like
// `node "${CLAUDE_PLUGIN_ROOT}/src/hooks/x.js"` extract just the
// `src/hooks/x.js` portion.
var pluginRootRefRegex = regexp.MustCompile(`\$\{CLAUDE_PLUGIN_ROOT\}/([^\s"'$]+)`)

// conventionDirBasenames is the set of plugin-root convention
// directory names. A bare-relative-path JSON value that equals one of
// these is treated as a directory reference (per the schema's
// dedicated path fields — `commands`, `agents`, `skills`, ...).
var conventionDirBasenames = map[string]struct{}{
	"commands":   {},
	"agents":     {},
	"skills":     {},
	"hooks":      {},
	"mcpServers": {},
	// Additional path-field basenames the manifest schema declares.
	// Their inclusion as bare-relative references is harmless for
	// caveman-shaped plugins (those fields are typically absent) but
	// gives forward-compat coverage for richer manifests.
	"outputStyles": {},
	"themes":       {},
	"lspServers":   {},
	"monitors":     {},
}

// parsePluginJSON unmarshals the manifest body into a generic
// map[string]any tree. Any unmarshal failure wraps
// sources.ErrUpstreamInvalid so the reconciler's classifyFetchError
// maps to ReasonUpstreamInvalid.
func parsePluginJSON(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("pluginpack: parse plugin.json: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	return m, nil
}

// extractReferences walks the manifest tree and returns the set of
// parent-directory paths that must be transitively included in the
// filtered tarball.
//
// The walk handles three JSON value shapes only:
//
//   - string  — scanned for `${CLAUDE_PLUGIN_ROOT}/<path>` matches AND
//     tested as a bare-relative-path reference per the schema's
//     dedicated path fields.
//   - map     — values walked recursively.
//   - []any   — elements walked recursively.
//
// `null`, numbers, booleans are ignored (the type-switch falls
// through). No panics.
//
// Any candidate path that, after path.Clean, starts with `..` or
// `/` is rejected as a malformed manifest reference (path-traversal
// gate) by returning a wrapped sources.ErrUpstreamInvalid.
func extractReferences(m map[string]any) (map[string]struct{}, error) {
	parents := map[string]struct{}{}
	if err := walkRefs(m, parents); err != nil {
		return nil, err
	}
	return parents, nil
}

// walkRefs is the recursive worker for extractReferences.
func walkRefs(v any, parents map[string]struct{}) error {
	switch val := v.(type) {
	case string:
		// (a) ${CLAUDE_PLUGIN_ROOT}/<path> matches inside the string.
		for _, match := range pluginRootRefRegex.FindAllStringSubmatch(val, -1) {
			if len(match) >= 2 {
				if err := addReference(match[1], parents); err != nil {
					return err
				}
			}
		}
		// (b) Bare-relative-path heuristic: per the schema, the
		// dedicated path fields (commands[].source, hooks, agents,
		// skills, ...) carry literal path strings. Treat a bare
		// candidate as a reference only when it looks like a path
		// (contains `/`, or has a file extension, or is a known
		// convention dir basename) — otherwise we'd pull `name`,
		// `description`, etc. into the include set.
		if looksLikeBareRef(val) {
			if err := addReference(val, parents); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, child := range val {
			if err := walkRefs(child, parents); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range val {
			if err := walkRefs(child, parents); err != nil {
				return err
			}
		}
	}
	// Other JSON types (null, numbers, booleans) intentionally ignored.
	return nil
}

// looksLikeBareRef tests whether a bare string value looks like a path
// reference suitable for the manifest's dedicated path fields. The
// heuristic intentionally rejects "name", "description", URLs, and
// other non-path strings.
func looksLikeBareRef(s string) bool {
	if s == "" {
		return false
	}
	// URLs / template placeholders / shell command strings don't
	// resolve to a static in-tarball path. Bail before the heuristic
	// promotes them into the include set.
	if strings.Contains(s, "://") {
		return false
	}
	if strings.ContainsAny(s, " \t\n\"'") {
		// Whitespace / quotes indicate a command line or sentence,
		// not a path-field value.
		return false
	}
	if strings.Contains(s, "$") {
		// Placeholder strings are handled by the pluginRootRefRegex
		// path above; the bare-ref pass should not double-count them.
		return false
	}
	if strings.Contains(s, "/") {
		return true
	}
	if _, ok := conventionDirBasenames[s]; ok {
		return true
	}
	if ext := path.Ext(s); ext != "" && len(ext) <= 8 {
		return true
	}
	return false
}

// addReference resolves a single candidate path and adds its immediate
// parent directory to the include set. Path-traversal candidates are
// rejected.
func addReference(ref string, parents map[string]struct{}) error {
	clean := path.Clean(ref)
	if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("pluginpack: manifest reference %q escapes plugin root: %w", ref, sources.ErrUpstreamInvalid)
	}
	if clean == "." {
		return nil
	}
	parent := path.Dir(clean)
	if parent == "." {
		// Reference is a top-level basename (e.g. "commands");
		// include the directory itself.
		parents[clean] = struct{}{}
		return nil
	}
	parents[parent] = struct{}{}
	return nil
}

// SPDX-License-Identifier: Apache-2.0

// Package namespace provides the leaf-prefixing scheme used to de-collide two
// resources that project to the SAME workspace-relative path. Both the governed
// hydrate engine (internal/cli/hydrate) and the local package manager
// (internal/cli/localpkg/manager) apply this identical `<plugin>-<name>` rule,
// so a command/skill name clash resolves the same way regardless of which
// install path produced it.
package namespace

import (
	"path"
	"strings"
)

// Leaf returns p with a `<plugin>-` prefix so two plugins' same-named resources
// no longer collide. For a skill resource (any file under a `skills/<name>/`
// dir) the prefix goes on the <name> segment; for a plain file it goes on the
// leaf filename. When the to-be-prefixed segment already equals plugin, p is
// returned unchanged.
//
// p is "/"-separated workspace-relative; uses the `path` package (never
// `filepath`) so behavior is OS-independent.
func Leaf(p, plugin string) string {
	segments := strings.Split(p, "/")

	// Find a "skills" segment; the segment immediately after it is the skill
	// <name>. Prefix that <name> segment (unless it already equals plugin).
	for i, seg := range segments {
		if seg == "skills" && i+1 < len(segments) {
			name := segments[i+1]
			if shouldSkipPrefix(name, plugin) {
				return p
			}
			segments[i+1] = plugin + "-" + name
			return strings.Join(segments, "/")
		}
	}

	// No skills segment — prefix the leaf filename.
	leaf := segments[len(segments)-1]
	if shouldSkipPrefix(leaf, plugin) {
		return p
	}
	segments[len(segments)-1] = plugin + "-" + leaf
	return strings.Join(segments, "/")
}

// shouldSkipPrefix reports whether seg (minus any file extension) already
// equals plugin, so prefixing would produce a redundant `<plugin>-<plugin>`.
func shouldSkipPrefix(seg, plugin string) bool {
	base := seg
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base == plugin
}

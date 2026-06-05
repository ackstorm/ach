// SPDX-License-Identifier: Apache-2.0

// Package skillref parses Environment context.skills references of the form
// "name" (standalone Skill CR) or "name@marketplace" (a skill discovered
// inside a SkillMarketplace). Mirrors internal/pluginref: the marketplace
// qualifier is the segment after the FINAL '@'.
package skillref

import "strings"

// Parse splits a reference into (name, marketplace, scoped). No '@' → bare.
func Parse(ref string) (name, marketplace string, scoped bool) {
	i := strings.LastIndexByte(ref, '@')
	if i < 0 {
		return ref, "", false
	}
	return ref[:i], ref[i+1:], true
}

// Valid reports whether ref is well-formed: name non-empty, and when scoped
// the marketplace part non-empty too.
func Valid(ref string) bool {
	name, mkt, scoped := Parse(ref)
	if name == "" {
		return false
	}
	if scoped && mkt == "" {
		return false
	}
	return true
}

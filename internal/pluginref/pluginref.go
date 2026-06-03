// SPDX-License-Identifier: Apache-2.0

// Package pluginref parses Environment context.plugins references of the
// form "name" (bare → internal Plugin CRD) or "name@marketplace" (→ a
// marketplace plugin, resolved by exact (marketplace_name, name) PK).
//
// The marketplace qualifier is the segment after the FINAL '@', so a
// plugin name may itself contain '@' (the CRD deny-pattern permits it).
// Marketplace metadata.name is DNS-1123 (no '@'), so the final '@' is an
// unambiguous separator.
package pluginref

import "strings"

// Parse splits a reference into (name, marketplace, scoped). When the ref
// carries no '@', marketplace is "" and scoped is false.
func Parse(ref string) (name, marketplace string, scoped bool) {
	i := strings.LastIndexByte(ref, '@')
	if i < 0 {
		return ref, "", false
	}
	return ref[:i], ref[i+1:], true
}

// Valid reports whether ref is a well-formed bare or scoped reference:
// the name part is non-empty, and when scoped the marketplace part is
// non-empty too.
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

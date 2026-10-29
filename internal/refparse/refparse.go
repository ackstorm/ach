// SPDX-License-Identifier: Apache-2.0

// Package refparse parses Environment context object references of the
// form "name" (standalone CR) or "name@marketplace" (an object discovered
// inside a marketplace) — used by both context.plugins and context.skills.
//
// The marketplace qualifier is the segment after the FINAL '@', so a
// name may itself contain '@' (the CRD deny-pattern permits it).
// Marketplace metadata.name is DNS-1123 (no '@'), so the final '@' is an
// unambiguous separator.
package refparse

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
	return name != "" && (!scoped || mkt != "")
}

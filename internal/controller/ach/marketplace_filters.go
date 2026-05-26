// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 1: RE2 include/exclude filters for PluginMarketplace
// catalogs (OP-07). Patterns are operator-prepended ^ anchored,
// case-sensitive. Compile failure surfaces ErrInvalidConfig so the
// caller maps to ReasonInvalidConfig (NOT ReasonUpstreamInvalid — the
// pattern is OPERATOR-supplied configuration, not upstream content).
//
// Go's regexp is RE2 (linear-time by design) so catastrophic backtracking
// is not a threat — T-02-06-04 is `accept`.

package ach

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidConfig is the package-level sentinel returned by
// compileAnchored when a user-supplied filter pattern fails to compile.
// The caller (pluginmarketplace_controller.go Stage-1) checks
// errors.Is(err, ErrInvalidConfig) and maps to ReasonInvalidConfig.
//
// Declared as exported (capital E) for parity with the sources.Err*
// sentinels, even though it is consumed only inside this package today —
// future Stage-1.5 extensions (CRD-driven include/exclude on Plugin or
// Prompt or Artifact CRDs in v1beta1) would consume this sentinel from
// other packages without churn.
var ErrInvalidConfig = errors.New("marketplace: invalid configuration")

// compileAnchored compiles each user-supplied pattern with a prepended
// '^' anchor per OP-07. Returns (nil, nil) on empty input so the caller
// can treat nil as "no filter".
//
// Compile failure wraps the offending pattern + the RE2 compile error +
// ErrInvalidConfig.
func compileAnchored(patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		compiled, err := regexp.Compile("^" + p)
		if err != nil {
			return nil, fmt.Errorf("regex %q: %v: %w", p, err, ErrInvalidConfig)
		}
		out = append(out, compiled)
	}
	return out, nil
}

// applyFilters narrows a parsed plugin list by anchored RE2 include/
// exclude patterns per OP-07.
//
//   - include nil → all plugins pass the include stage (vacuous match).
//     This is the "no include filter set" case.
//   - include non-nil → keep only plugins where AT LEAST ONE include
//     pattern matches plugin.Name.
//   - exclude nil → no exclusion.
//   - exclude non-nil → drop plugins matched by ANY exclude pattern from
//     the kept set. Exclude is applied AFTER include.
//
// Returns (kept, includeMatchedAny).
//
// includeMatchedAny is true when EITHER include was nil (vacuous) OR at
// least one input plugin matched at least one include pattern. The
// caller (Stage-1) uses this flag to decide whether "include matched
// zero" → ReasonUpstreamInvalid; the flag MUST be vacuously true when
// include is unset so the "no filter" case doesn't trip the
// zero-include-match guard.
//
// Zero-exclude-match is silent (no flag returned) — OP-07 explicitly
// treats this case as a no-op.
func applyFilters(plugins []ClaudeCodeMarketplacePlugin, include, exclude []*regexp.Regexp) (kept []ClaudeCodeMarketplacePlugin, includeMatchedAny bool) {
	// WR-05: short-circuit when neither filter is set — the no-filter
	// case is the hot path (most marketplaces don't declare filters)
	// and previously copied the full plugin slice + iterated twice.
	// Returning the input slice directly aliases the caller's storage;
	// applyFilters' callers do not mutate the returned slice in place
	// (they only append into a separate `decisions` aggregator), so
	// the alias is safe.
	if include == nil && exclude == nil {
		return plugins, true
	}
	// Include stage.
	var stage1 []ClaudeCodeMarketplacePlugin
	if include == nil {
		stage1 = append(stage1, plugins...)
		includeMatchedAny = true // vacuous
	} else {
		stage1 = make([]ClaudeCodeMarketplacePlugin, 0, len(plugins))
		for _, p := range plugins {
			if matchAny(include, p.Name) {
				stage1 = append(stage1, p)
				includeMatchedAny = true
			}
		}
	}
	// Exclude stage.
	if exclude == nil {
		return stage1, includeMatchedAny
	}
	kept = make([]ClaudeCodeMarketplacePlugin, 0, len(stage1))
	for _, p := range stage1 {
		if matchAny(exclude, p.Name) {
			continue
		}
		kept = append(kept, p)
	}
	return kept, includeMatchedAny
}

// matchAny returns true when any pattern in res matches s.
func matchAny(res []*regexp.Regexp, s string) bool {
	for _, r := range res {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}

// SPDX-License-Identifier: Apache-2.0

package route

// KnownComponentKinds is the canonical Claude-source plugin component
// vocabulary — the set of top-level source-tree kinds that SOME adapter in
// this build knows how to route, plus "hooks" (a real source kind no adapter
// currently supports). It gates the Project drop-warning surface: when a
// source tree carries one of these kinds but the active adapter's rule table
// has no destination for it, the kind is reported as dropped so the user
// learns "platform X does not support <kind>".
//
// Entries NOT in this set (plugin manifests like .claude-plugin /
// .codex-plugin, docs like README.md / LICENSE, and any unrecognized
// directory) are non-content by design and are skipped SILENTLY.
//
// INVARIANT: every FromGlob anchor first-segment across all adapter
// ProjectionRules() tables MUST appear here (kinds_test.go enforces it).
// When a new adapter rule introduces a new source kind, add it here in the
// SAME commit.
var KnownComponentKinds = map[string]bool{
	"rules":     true,
	"commands":  true,
	"agents":    true,
	"skills":    true,
	"mcp":       true,
	"prompts":   true,
	"AGENTS.md": true,
	"hooks":     true,
}

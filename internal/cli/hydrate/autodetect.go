// SPDX-License-Identifier: Apache-2.0

// Platform autodetection — ADAPT-02 / spec §7.5 / D-06.
//
// When the cobra layer (07-W3-05 cmd/ach-cli/cmd/hydrate.go) sees no
// `--platform` flag, it calls Autodetect against the workspace cwd (or
// $HOME under `--global`) and uses the returned canonical id to wire
// `Opts.Platform`. The three outcomes are exhaustive:
//
//   - Zero matches → *exit.CodedError{Code: General} with a "pass
//     --platform" prompt naming the closed set.
//   - One match    → returns the canonical id AND emits a single
//     "Detected platform: <id>" line to stderr (ADAPT-02).
//   - Multi-match  → *exit.CodedError{Code: General} listing every
//     matched id in deterministic (sort.Strings) order, plus a prompt
//     to pass `--platform`. NO silent priority ordering — the user
//     confronts the ambiguity per D-06.
//
// Iteration order for the decision: adapter.Iter() returns the
// registered Adapters; for each, call Detect(root). A Detect that
// returns Confidence >= ConfidenceLow counts as a match. Confidence
// itself is NOT used for ranking — D-06 explicitly forbids silent
// priority ordering. Two adapters both returning ConfidenceHigh would
// be a multi-match exit, not a silent pick.
//
// ResolvePlatform wraps adapter.Lookup with a typed CodedError on a
// miss so the cobra layer's --platform handling routes through one
// helper instead of duplicating the "unknown platform" message.

package hydrate

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// closedSetIDs is the v1alpha1 closed-set adapter id list. Used in the
// zero-match error message so the user sees the canonical names even
// when no adapter registered (defensive — production callers always
// blank-import all four, but a misconfigured test binary could surface
// "no platform detected" without any registered ids in adapter.Iter()).
const closedSetIDs = "claude-code, codex, gemini-cli, opencode"

// Autodetect scans `root` against every registered adapter's Detect and
// returns the canonical id of the single match. Behavior per outcome:
//
//   - Zero matches → ("", *exit.CodedError{Code: General}). The error
//     message names the closed set so the user can re-invoke with the
//     right `--platform` value.
//   - One match    → (id, nil). On success, emits "Detected platform:
//     <id>" to stderr.
//   - Multi-match  → ("", *exit.CodedError{Code: General}). The message
//     lists every matched id in sort.Strings order (deterministic for
//     tests) and prompts the user to pass `--platform`.
//
// Detect errors from individual adapters are treated as "no match for
// this adapter" — a single adapter's I/O fault should not block
// autodetection of an unrelated platform. (In practice every adapter's
// Detect is stat-only and rarely errors; this discipline prefers
// graceful degradation over an opaque autodetect abort.)
func Autodetect(root string, stderr io.Writer) (string, error) {
	matched := make([]string, 0, 4)
	for _, a := range adapter.Iter() {
		m, err := a.Detect(root)
		if err != nil {
			// A single adapter Detect failure is non-fatal for the
			// autodetect decision — skip it and continue iterating.
			continue
		}
		if m.ID == "" || m.Confidence == 0 {
			continue
		}
		matched = append(matched, m.ID)
	}

	sort.Strings(matched)

	switch len(matched) {
	case 0:
		return "", &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"no platform detected at %s; pass --platform <id> (one of: %s)",
				root, closedSetIDs,
			),
		}
	case 1:
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "Detected platform: %s\n", matched[0])
		}
		return matched[0], nil
	default:
		return "", &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"multiple platforms detected at %s: %s; pass --platform <id> to disambiguate",
				root, strings.Join(matched, ", "),
			),
		}
	}
}

// ResolvePlatform resolves a user-supplied platform id (canonical OR
// alias, case-insensitive) to the canonical id via the adapter
// registry. Returns the canonical id on hit; a typed *exit.CodedError
// (Code: General) on miss so the cobra layer's error envelope is
// consistent with the autodetect path.
//
// The unknown-platform message names every registered canonical id (in
// sort.Strings order) so a typo'd `--platform clade-code` surfaces the
// closed set rather than just rejecting silently.
func ResolvePlatform(id string) (string, error) {
	if id == "" {
		return "", &exit.CodedError{
			Code: exit.General,
			Msg:  "empty --platform value; pass one of: " + registeredIDs(),
		}
	}
	a, ok := adapter.Lookup(id)
	if !ok {
		return "", &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("unknown platform: %s; one of: %s", id, registeredIDs()),
		}
	}
	return a.ID(), nil
}

// registeredIDs returns the closed set of canonical ids in
// sort.Strings order for use in error messages. Sourced from
// adapter.Iter() so it reflects what is actually registered at the
// time of the call (defensive — production callers always have all
// four registered, but a misconfigured test could see fewer).
func registeredIDs() string {
	ids := make([]string, 0, 4)
	for _, a := range adapter.Iter() {
		ids = append(ids, a.ID())
	}
	if len(ids) == 0 {
		return closedSetIDs
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

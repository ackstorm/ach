// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"slices"
	"strings"

	"github.com/ackstorm/ach/internal/litellm"
)

// filterShellTeams normalizes a precheck-granted team set into the JWT
// "groups" claim: ACH's internal shell teams are dropped, the remainder is
// deduplicated and sorted.
//
// ach-env-<name> and ach-user-<email> are ACH's own permission plumbing
// (references/litellm-permission-model.md) and carry no meaning to a
// backend, so they never leave the forwarder. On the pk_ path the
// precheck intersection already excludes them — a human belongs to their
// own ach-user-* shell but never to an ach-env-* one, and authorizedTeams
// is spec-authored so it holds neither. The filter is therefore defense in
// depth, catching only an administrator who writes a shell alias into
// Environment.spec.authorizedTeams by hand.
//
// Returns nil rather than an empty slice when nothing survives, so the
// signer's len(Groups) > 0 test omits the claim entirely.
func filterShellTeams(in []string) []string {
	var out []string //nolint:prealloc // must stay nil (not empty) when nothing survives
	for _, t := range in {
		if strings.HasPrefix(t, litellm.ShellTeamPrefix) ||
			strings.HasPrefix(t, litellm.UserShellPrefix) {
			continue
		}
		if slices.Contains(out, t) {
			continue
		}
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

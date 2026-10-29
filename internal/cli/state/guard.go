// SPDX-License-Identifier: Apache-2.0

package state

import (
	"errors"
	"fmt"
)

// ErrEnvironmentGuard is the sentinel returned by GuardEnvironment
// when the same <ach-dir>/state.json was last hydrated for a
// different Environment than the current invocation. Maps to §9.3
// exit code 4 at the caller layer (cmd/ach-cli/cmd/hydrate.go via
// *exit.CodedError).
//
// The guard is purely workspace-scope: in global scope, the
// <ach-dir> already namespaces by Environment (~/.ach/<env>/), so a
// different Environment uses a different <ach-dir> by construction
// and the guard never triggers (spec §8.3).
var ErrEnvironmentGuard = errors.New(
	"state: same <ach-dir> bound to a different Environment (per STATE-03 / spec §8.3)",
)

// GuardEnvironment checks the §8.3 same-<ach-dir>-different-
// Environment invariant. Returns:
//
//   - nil when `existing` is nil (no prior state — first hydrate),
//     when `existing.Environment` is empty (state present but never
//     bound — should not occur in v2 but tolerated defensively), or
//     when `requested` equals `existing.Environment` (normal re-
//     hydrate of the same Environment in the same workspace);
//   - nil when `force` is true regardless of mismatch (spec §8.3
//     escape hatch — caller has explicitly asked to overwrite);
//   - an ErrEnvironmentGuard-wrapped error with `have=<existing>
//     want=<requested>` detail when the mismatch is real and
//     `force` is false.
//
// This function does NOT log the override or print the warning when
// force=true — that is the caller layer's responsibility (it has the
// stderr seam and the user-facing wording). Pure data check.
func GuardEnvironment(existing *File, requested string, force bool) error {
	if existing == nil || existing.Environment == "" {
		// Fresh state — no prior binding. Always OK.
		return nil
	}
	if existing.Environment == requested {
		// Same Environment as last hydrate — normal re-run. OK.
		return nil
	}
	if force {
		// Mismatch present but caller has accepted the override.
		// Caller layer prints the warning; this function returns
		// nil with no side effects.
		return nil
	}
	return fmt.Errorf(
		"%w: have=%q want=%q",
		ErrEnvironmentGuard, existing.Environment, requested,
	)
}

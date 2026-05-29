// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"fmt"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/state"
)

// DriftOutcome (defined in result.go alongside the Differ interface)
// values per the §8.4 four-outcome truth table.
//
// The truth table is a function of four xxh3 digest values:
//
//	stateEntry.Hash         — what the engine last wrote to disk
//	stateEntry.SourceHash   — the upstream bytes pre-transformation
//	onDiskHash              — fresh xxh3 of the actual on-disk file
//	freshSourceHash         — fresh xxh3 of the upstream bytes
//
// Outcome mapping (CLI spec §8.4 verbatim):
//
//	on-disk matches state.Hash    AND source matches state.SourceHash  → NoOp
//	on-disk matches state.Hash    AND source differs                   → UpstreamOnlyOverwrite
//	on-disk differs               AND source matches state.SourceHash  → LocalEditPreserve   (exit 2)
//	on-disk differs               AND source differs                   → ConflictPreserve    (exit 2)
//
// LocalEditPreserve + ConflictPreserve both surface as exit.Drift (2)
// per D-14/D-16 — drift wins; user edits sacred — unless opts.Force.
const (
	NoOp                  DriftOutcome = iota + 1
	UpstreamOnlyOverwrite              // upstream moved; no local edit; safe to overwrite.
	LocalEditPreserve                  // local edit present; upstream unchanged.
	ConflictPreserve                   // both moved; double conflict.
)

// Differ_ is the concrete Differ implementation per Task 3 of plan
// 07-W1-06. It holds no state — Compare is a pure function of its
// arguments — but the type exists so the commit struct can carry it
// as a typed field (the Differ interface in result.go).
//
// The trailing underscore disambiguates the type name from the
// Differ interface declared in result.go. Public callers reach the
// concrete impl via NewDiffer() which returns the interface type.
type Differ_ struct{}

// NewDiffer returns a Differ implementation suitable for the commit
// orchestrator's step 9 drift classification. Phase 7 ships a single
// stateless impl; future deviations (e.g. a per-file override path)
// would add a constructor variant.
func NewDiffer() Differ {
	return Differ_{}
}

// Compare classifies a single state entry against fresh hash values
// per §8.4. When stateEntry == nil (fresh-extract path — no prior
// state to compare against) the result is NoOp — there's nothing to
// preserve, the caller should overwrite freely.
//
// All four arms are pure string equality; the xxh3 digests carry their
// own "xxh3:" prefix from internal/cli/hash so a misuse (passing raw
// bytes) would be immediately visible at the comparison site.
func (Differ_) Compare(stateEntry *state.FileEntry, onDiskHash, freshSourceHash string) DriftOutcome {
	if stateEntry == nil {
		return NoOp
	}
	onDiskMatches := onDiskHash == stateEntry.Hash
	sourceMatches := freshSourceHash == stateEntry.SourceHash

	switch {
	case onDiskMatches && sourceMatches:
		return NoOp
	case onDiskMatches && !sourceMatches:
		return UpstreamOnlyOverwrite
	case !onDiskMatches && sourceMatches:
		return LocalEditPreserve
	default:
		// !onDiskMatches && !sourceMatches — last branch is the
		// ConflictPreserve arm.
		return ConflictPreserve
	}
}

// ShouldExit2 reports whether the outcome must abort the hydrate with
// exit code 2 (exit.Drift). LocalEditPreserve and ConflictPreserve are
// the two outcomes that PRESERVE the on-disk file vs the engine's
// would-be-written bytes; both surface as exit.Drift per D-14/D-16
// unless the caller passes --force.
func ShouldExit2(outcome DriftOutcome) bool {
	return outcome == LocalEditPreserve || outcome == ConflictPreserve
}

// WrapDriftError converts a drift outcome into the *exit.CodedError
// the caller layer (cmd/ach-cli/cmd/hydrate.go via Run's return path)
// dispatches into exit.Drift. Returns nil for NoOp and
// UpstreamOnlyOverwrite (no error path — those outcomes proceed
// without raising). target names the affected workspace-relative
// path for the user-facing message.
func WrapDriftError(outcome DriftOutcome, target string) error {
	if !ShouldExit2(outcome) {
		return nil
	}
	return &exit.CodedError{
		Code: exit.Drift,
		Msg:  fmt.Sprintf("drift detected on %q (outcome=%s) — use --force to override", target, outcomeString(outcome)),
	}
}

// outcomeString returns the human-readable name of a DriftOutcome for
// stderr / error messages. The zero value (and any out-of-range int)
// renders as "unknown" so a future-added outcome doesn't silently
// degrade to an empty string.
func outcomeString(outcome DriftOutcome) string {
	switch outcome {
	case NoOp:
		return "NoOp"
	case UpstreamOnlyOverwrite:
		return "UpstreamOnlyOverwrite"
	case LocalEditPreserve:
		return "LocalEditPreserve"
	case ConflictPreserve:
		return "ConflictPreserve"
	default:
		return "unknown"
	}
}

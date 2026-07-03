// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/state"
)

// digest literals — the actual byte values don't matter; string equality
// is what drives the truth table.
const (
	hA = "xxh3:aaaa00000000000000000000000000000001"
	hB = "xxh3:bbbb00000000000000000000000000000002"
	hC = "xxh3:cccc00000000000000000000000000000003"
	hD = "xxh3:dddd00000000000000000000000000000004"
)

// TestDrift_TruthTable iterates the §8.4 four-outcome truth table.
// Each case names the (stateHash, stateSourceHash, onDiskHash,
// freshSourceHash) quadruple and the expected DriftOutcome. The
// matrix covers all four arms PLUS the nil-entry edge case (fresh-
// extract path where there's nothing to compare against).
func TestDrift_TruthTable(t *testing.T) {
	cases := []struct {
		name            string
		stateHash       string
		stateSourceHash string
		onDiskHash      string
		freshSourceHash string
		want            DriftOutcome
	}{
		{
			name:            "NoOp_OnDiskMatches_AndSourceMatches",
			stateHash:       hA,
			stateSourceHash: hB,
			onDiskHash:      hA,
			freshSourceHash: hB,
			want:            NoOp,
		},
		{
			name:            "UpstreamOnlyOverwrite_OnDiskMatches_SourceDiffers",
			stateHash:       hA,
			stateSourceHash: hB,
			onDiskHash:      hA,
			freshSourceHash: hC, // source moved
			want:            UpstreamOnlyOverwrite,
		},
		{
			name:            "LocalEditPreserve_OnDiskDiffers_SourceMatches",
			stateHash:       hA,
			stateSourceHash: hB,
			onDiskHash:      hC, // local edit
			freshSourceHash: hB,
			want:            LocalEditPreserve,
		},
		{
			name:            "ConflictPreserve_BothDiffer",
			stateHash:       hA,
			stateSourceHash: hB,
			onDiskHash:      hC, // local edit
			freshSourceHash: hD, // upstream moved
			want:            ConflictPreserve,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &state.FileEntry{
				Target:     "foo.md",
				Hash:       tc.stateHash,
				SourceHash: tc.stateSourceHash,
			}
			got := compareDrift(entry, tc.onDiskHash, tc.freshSourceHash)
			if got != tc.want {
				t.Errorf("Compare(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestDrift_NilEntry_ReturnsNoOp asserts the fresh-extract edge
// case: stateEntry == nil means there's no prior state to compare
// against, so the engine should treat the situation as NoOp and
// overwrite freely.
func TestDrift_NilEntry_ReturnsNoOp(t *testing.T) {
	got := compareDrift(nil, hA, hB)
	if got != NoOp {
		t.Errorf("Compare(nil, ...) = %d, want NoOp (%d)", got, NoOp)
	}
}

// TestShouldExit2_LocalEdit_True asserts the LocalEditPreserve outcome
// is exit-code 2 worthy.
func TestShouldExit2_LocalEdit_True(t *testing.T) {
	if !ShouldExit2(LocalEditPreserve) {
		t.Error("ShouldExit2(LocalEditPreserve) = false, want true")
	}
}

// TestShouldExit2_Conflict_True asserts the ConflictPreserve outcome
// is also exit-code 2 worthy.
func TestShouldExit2_Conflict_True(t *testing.T) {
	if !ShouldExit2(ConflictPreserve) {
		t.Error("ShouldExit2(ConflictPreserve) = false, want true")
	}
}

// TestShouldExit2_NoOp_False asserts the no-drift outcomes do NOT
// trip exit 2.
func TestShouldExit2_NoOp_False(t *testing.T) {
	if ShouldExit2(NoOp) {
		t.Error("ShouldExit2(NoOp) = true, want false")
	}
}

// TestShouldExit2_UpstreamOnly_False asserts the upstream-overwrite
// outcome (no local edit, safe to clobber) does NOT trip exit 2.
func TestShouldExit2_UpstreamOnly_False(t *testing.T) {
	if ShouldExit2(UpstreamOnlyOverwrite) {
		t.Error("ShouldExit2(UpstreamOnlyOverwrite) = true, want false")
	}
}

// TestWrapDriftError_LocalEdit_HasExitCode2 asserts the wrap helper
// returns a *exit.CodedError with Code == exit.Drift for the
// LocalEditPreserve outcome, and the message names the target.
func TestWrapDriftError_LocalEdit_HasExitCode2(t *testing.T) {
	err := WrapDriftError(LocalEditPreserve, ".claude/CLAUDE.md")
	if err == nil {
		t.Fatal("WrapDriftError(LocalEditPreserve, ...) = nil, want CodedError")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.Drift {
		t.Errorf("ce.Code = %d, want exit.Drift (%d)", ce.Code, exit.Drift)
	}
	if !strings.Contains(ce.Msg, ".claude/CLAUDE.md") {
		t.Errorf("ce.Msg = %q, want substring %q", ce.Msg, ".claude/CLAUDE.md")
	}
	if !strings.Contains(ce.Msg, "LocalEditPreserve") {
		t.Errorf("ce.Msg = %q, want substring %q", ce.Msg, "LocalEditPreserve")
	}
}

// TestWrapDriftError_Conflict_HasExitCode2 asserts the conflict path
// produces the same exit code (2).
func TestWrapDriftError_Conflict_HasExitCode2(t *testing.T) {
	err := WrapDriftError(ConflictPreserve, "foo.md")
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.Drift {
		t.Errorf("ce.Code = %d, want exit.Drift (%d)", ce.Code, exit.Drift)
	}
}

// TestWrapDriftError_NoOp_ReturnsNil asserts no-drift outcomes do not
// raise — the caller proceeds.
func TestWrapDriftError_NoOp_ReturnsNil(t *testing.T) {
	if err := WrapDriftError(NoOp, "foo.md"); err != nil {
		t.Errorf("WrapDriftError(NoOp, ...) = %v, want nil", err)
	}
}

// TestWrapDriftError_UpstreamOnly_ReturnsNil asserts upstream-only
// outcomes do not raise — caller proceeds with overwrite.
func TestWrapDriftError_UpstreamOnly_ReturnsNil(t *testing.T) {
	if err := WrapDriftError(UpstreamOnlyOverwrite, "foo.md"); err != nil {
		t.Errorf("WrapDriftError(UpstreamOnlyOverwrite, ...) = %v, want nil", err)
	}
}

// TestOutcomeString_RendersAllFour asserts the human-readable name
// helper covers every named outcome. The default arm renders
// "unknown" so a future-added outcome can't silently degrade to "".
func TestOutcomeString_RendersAllFour(t *testing.T) {
	cases := []struct {
		outcome DriftOutcome
		want    string
	}{
		{NoOp, "NoOp"},
		{UpstreamOnlyOverwrite, "UpstreamOnlyOverwrite"},
		{LocalEditPreserve, "LocalEditPreserve"},
		{ConflictPreserve, "ConflictPreserve"},
		{DriftOutcome(99), "unknown"}, // out-of-range default arm.
	}
	for _, tc := range cases {
		got := outcomeString(tc.outcome)
		if got != tc.want {
			t.Errorf("outcomeString(%d) = %q, want %q", tc.outcome, got, tc.want)
		}
	}
}

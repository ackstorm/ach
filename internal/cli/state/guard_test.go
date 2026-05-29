// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestGuard_FreshState_ReturnsNil asserts the nil-existing branch:
// no prior state.json on disk → no Environment binding to honor →
// guard is a no-op. This is the first-hydrate case.
func TestGuard_FreshState_ReturnsNil(t *testing.T) {
	if err := state.GuardEnvironment(nil, "engineering-prod", false); err != nil {
		t.Fatalf("GuardEnvironment(nil, ...) = %v, want nil", err)
	}
}

// TestGuard_EmptyEnvironment_ReturnsNil asserts the defensive
// branch: an existing File whose Environment field is empty (should
// not occur in v2 but tolerated) is treated as fresh state.
func TestGuard_EmptyEnvironment_ReturnsNil(t *testing.T) {
	existing := &state.File{SchemaVersion: "2", Environment: ""}
	if err := state.GuardEnvironment(existing, "engineering-prod", false); err != nil {
		t.Fatalf("GuardEnvironment(empty env, ...) = %v, want nil", err)
	}
}

// TestGuard_SameEnvironment_ReturnsNil asserts the normal re-
// hydrate path: re-running with the same Environment as the prior
// state is the canonical hydrate flow and MUST NOT trip the guard.
func TestGuard_SameEnvironment_ReturnsNil(t *testing.T) {
	existing := &state.File{
		SchemaVersion: "2",
		Environment:   "engineering-prod",
	}
	if err := state.GuardEnvironment(existing, "engineering-prod", false); err != nil {
		t.Fatalf("GuardEnvironment(same env, ...) = %v, want nil", err)
	}
}

// TestGuard_DifferentEnvironment_ReturnsErrEnvironmentGuard asserts
// the §8.3 trip path: different Environment + no --force → sentinel
// error wrapping ErrEnvironmentGuard. Caller maps to exit 4.
//
// Also asserts the message contains `have=<existing> want=<requested>`
// so a `errors.Is` check and a `errors.Error()` grep both work in
// downstream callers.
func TestGuard_DifferentEnvironment_ReturnsErrEnvironmentGuard(t *testing.T) {
	existing := &state.File{
		SchemaVersion: "2",
		Environment:   "engineering-prod",
	}
	err := state.GuardEnvironment(existing, "marketing-prod", false)
	if err == nil {
		t.Fatalf("GuardEnvironment(different env, force=false) = nil; want non-nil")
	}
	if !errors.Is(err, state.ErrEnvironmentGuard) {
		t.Fatalf("err = %v, want errors.Is(..., ErrEnvironmentGuard)", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `have="engineering-prod"`) || !strings.Contains(msg, `want="marketing-prod"`) {
		t.Errorf("err.Error() = %q, want substrings have=\"engineering-prod\" + want=\"marketing-prod\"", msg)
	}
}

// TestGuard_DifferentEnvironment_WithForce_ReturnsNil asserts the
// §8.3 escape hatch: --force overrides the guard with no error and
// no side effects (the caller layer is responsible for the warning
// print, this function stays pure).
func TestGuard_DifferentEnvironment_WithForce_ReturnsNil(t *testing.T) {
	existing := &state.File{
		SchemaVersion: "2",
		Environment:   "engineering-prod",
	}
	if err := state.GuardEnvironment(existing, "marketing-prod", true); err != nil {
		t.Fatalf("GuardEnvironment(different env, force=true) = %v, want nil", err)
	}
}

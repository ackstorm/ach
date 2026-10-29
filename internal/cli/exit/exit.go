// SPDX-License-Identifier: Apache-2.0

// Package exit defines the closed exit-code set the CLI emits per CLI
// spec §9.3. Codes 2 (Drift), 4 (EnvironmentMismatch), 5 (SchemaMismatch),
// and 7 (CollisionRefuse) are Phase 7 additions per
// STATE-02/STATE-03/STATE-04/STATE-09/SAFE-04. The Phase 6 set
// (0/1/3/6/8) is unchanged — every renumber would be a wire-format break.
//
// MapServerError is the single chokepoint that translates an HTTP
// response into an exit code. cmd/ach/main.go calls it once at the
// top of the error-handling chain via errors.As; subcommands return
// raw errors and let the entry point own the os.Exit syscall.
package exit

import (
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// Code wraps the exit status as a typed integer so the constants
// below cannot be silently re-typed.
type Code int

// §9.3 exit-code matrix. Phase 6 ships codes 0/1/3/6/8; Phase 7 adds
// 2/4/5/7 (additive, no renumber).
const (
	// OK is success (0).
	OK Code = 0

	// General is the catch-all (1) — synth-incompatible flags,
	// mutex-credential conflicts, missing --environment on pk_,
	// generic transport-side oddities that don't fall into one of
	// the other arms.
	General Code = 1

	// Drift (2) — STATE-04 four-outcome truth table: both the
	// local-edit-preserve and conflict-preserve outcomes surface as
	// Drift (per D-14/D-16). Hydrate refuses to overwrite on-disk
	// state that has diverged from `state.json`'s recorded hash,
	// unless --force is passed. Emitted from the hydrate engine via
	// *exit.CodedError, never via MapServerError.
	Drift Code = 2

	// AuthN (3) covers HTTP 401, 403 not_admin, and 403
	// unauthorized_team. The asymmetric whoami --verify (D-13) also
	// emits this when a key fails its prefix-specific probe.
	AuthN Code = 3

	// EnvironmentMismatch (4) — STATE-03 guard. When `state.json`'s
	// recorded `environment` differs from the current `--environment`
	// flag in the same <ach-dir>, hydrate aborts with this code (per
	// CLI spec §8.3) unless --force is passed.
	EnvironmentMismatch Code = 4

	// SchemaMismatch (5) — two trigger paths, same code:
	// (a) STATE-09 — the POST /platform/hydrate manifest's
	//     `schemaVersion` is not "v1alpha1" (CLI spec §6.2);
	// (b) STATE-02 — the on-disk `state.json` `schemaVersion` is
	//     not "2" (CLI spec §8.2).
	// Hydrate aborts before writing any files unless --force is passed.
	SchemaMismatch Code = 5

	// Network (6) covers transport errors (server unreachable) and
	// HTTP 503/504 (server admitted unavailability).
	Network Code = 6

	// CollisionRefuse (7) — SAFE-04. Auto-claim final-rename detects
	// an existing-unowned target whose bytes differ from the engine's
	// expected output, and --force is not passed (CLI spec §6.4).
	// Hydrate refuses to clobber the foreign content.
	CollisionRefuse Code = 7

	// ConfigFile (8) covers ~/.config/ach/config.yaml parse or
	// write failures (CLI-02 / D-04). Distinct from General so an
	// operator can grep CI logs for "exit 8" → "fix your yaml".
	ConfigFile Code = 8
)

// CodedError is the CLI-side typed error every cobra RunE can return
// when the failure is not a *httpclient.ServerError. cmd/ach/main.go
// pulls Code out via errors.As and passes it to os.Exit.
type CodedError struct {
	Code    Code
	Msg     string
	Wrapped error
}

// Error implements the error interface — returns Msg verbatim so the
// caller controls the rendered text.
func (e *CodedError) Error() string { return e.Msg }

// Unwrap exposes Wrapped so errors.Is composes through CodedError.
func (e *CodedError) Unwrap() error { return e.Wrapped }

// MapServerError converts an *httpclient.ServerError to its §9.3 exit
// code. nil → OK. The closed switch is the threat-model guarantee
// against exit-code spoofing (T-06-01-07): a hostile server returning
// "ok" on a 500 still produces General via the catch-all arm.
func MapServerError(e *httpclient.ServerError) Code {
	if e == nil {
		return OK
	}
	switch e.Status {
	case 401:
		return AuthN
	case 403:
		if e.Code == "not_admin" || e.Code == "unauthorized_team" {
			return AuthN
		}
		return General
	case 503, 504:
		return Network
	}
	return General
}

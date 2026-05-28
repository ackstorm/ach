// SPDX-License-Identifier: Apache-2.0

// Package exit defines the closed exit-code set the CLI emits per CLI
// spec §9.3. Phase 6 ships codes 0/1/3/6/8 — codes 2 (drift), 4
// (state mismatch), 5 (schema mismatch), and 7 (local I/O) are
// hydrate-engine territory (Phase 7) and stay absent here so a future
// extension is purely additive (no rename, no renumber).
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

// §9.3 exit-code matrix (Phase 6 subset).
const (
	// OK is success (0).
	OK Code = 0

	// General is the catch-all (1) — synth-incompatible flags,
	// mutex-credential conflicts, missing --environment on pk_,
	// generic transport-side oddities that don't fall into one of
	// the other arms.
	General Code = 1

	// AuthN (3) covers HTTP 401, 403 not_admin, and 403
	// unauthorized_team. The asymmetric whoami --verify (D-13) also
	// emits this when a key fails its prefix-specific probe.
	AuthN Code = 3

	// Network (6) covers transport errors (server unreachable) and
	// HTTP 503/504 (server admitted unavailability).
	Network Code = 6

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

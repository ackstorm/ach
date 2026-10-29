//go:build !e2e

// SPDX-License-Identifier: Apache-2.0

package hydrate

// killFn is the function-typed test seam for the TEST-ONLY SIGKILL
// injection seam. The release build declares the same type as the
// e2e build because commit.go's *commit struct references it
// unconditionally; without this declaration the release build would
// not compile.
//
// In release builds the field is populated by defaultKillFn below
// (a no-op), AND readSigkillSeamFromEnv always returns 0 so
// injectSigkillAfterStep stays at 0 — maybeKill's existing guard
// (`if c.injectSigkillAfterStep != step { return }`) short-circuits
// before the killFn is ever invoked.
type killFn func(step int)

// defaultKillFn is the production stub under -tags=!e2e. It is a
// no-op: even if some future bug accidentally invoked it
// (c.killFn(N) without the guard), no SIGKILL would fire. This is
// the defense-in-depth second layer for WR-01 — the env-var read
// is gated by readSigkillSeamFromEnv below, and the syscall itself
// is gated here.
func defaultKillFn(_ int) {}

// newDefaultKillFn returns the no-op killFn for assignment to
// *commit.killFn at construction time. Indirecting via this
// constructor keeps the literal `defaultKillFn` symbol out of
// commit.go (per WR-01 acceptance criterion: commit.go contains
// neither the env-var literal nor the defaultKillFn symbol).
func newDefaultKillFn() killFn {
	return defaultKillFn
}

// readSigkillSeamFromEnv is the production stub under -tags=!e2e.
// It NEVER reads the env var — release binaries' strings do not
// contain ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP at all
// (WR-01: removes the code-injection vector for a misconfigured
// parent process or hostile env that would otherwise crash
// ach-cli mid-hydrate).
func readSigkillSeamFromEnv() int {
	return 0
}

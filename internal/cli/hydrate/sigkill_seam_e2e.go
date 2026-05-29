//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"strconv"
	"syscall"
)

// envSigkillStep is the literal env-var the SIGKILL injection seam
// reads at commit construction. Exported as a constant only so test
// files in the same package (commit_sigkill_seam_test.go) can
// reference it without re-typing the literal string.
//
// Compiled in ONLY under -tags=e2e; release builds receive a no-op
// stub via sigkill_seam_prod.go (WR-01 production safety fix —
// the env-var literal is not present in release binary strings).
const envSigkillStep = "ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP"

// killFn is the function-typed test seam for the TEST-ONLY
// ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP env-var seam. Production
// defaults to a closure calling syscall.Kill(os.Getpid(),
// syscall.SIGKILL); unit tests inject a recorder that captures the
// step number into a *int so the seam can be verified reachable
// without crashing the test runner.
//
// The type MUST be declared in both seam files (e2e + prod) because
// commit.go's *commit struct references it unconditionally; without
// both declarations, the release build would not compile.
type killFn func(step int)

// defaultKillFn is the production killFn under -tags=e2e — it
// terminates the process immediately via SIGKILL so the next step
// never runs. Defers do not fire on SIGKILL; any cleanup the
// orchestrator relied on (e.g. lease.Release) is the kernel's
// responsibility (POSIX flock is released on fd-close, which the
// kernel does on process exit).
func defaultKillFn(_ int) {
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
}

// newDefaultKillFn returns the production killFn for assignment to
// *commit.killFn at construction time. Indirecting via this
// constructor keeps the literal `defaultKillFn` symbol out of
// commit.go (per WR-01 acceptance criterion: commit.go contains
// neither the env-var literal nor the defaultKillFn symbol).
func newDefaultKillFn() killFn {
	return defaultKillFn
}

// readSigkillSeamFromEnv reads ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP
// from the environment, parses it as an int, and returns the parsed
// value. Empty/unset/unparsable values return 0 (seam disabled) — the
// fail-soft posture is intentional because the seam is for test
// infrastructure, not the production exit-code contract.
//
// This function is compiled in ONLY under -tags=e2e. Release builds
// receive the no-op stub from sigkill_seam_prod.go (always returns 0).
func readSigkillSeamFromEnv() int {
	raw := os.Getenv(envSigkillStep)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

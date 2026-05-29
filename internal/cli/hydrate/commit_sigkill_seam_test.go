//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"testing"
)

// TestCommit_NewCommit_ReadsSigkillEnvVar asserts that newCommit
// reads ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP at construction
// time. Setting the env var to "7" produces a *commit with
// injectSigkillAfterStep == 7.
//
// Relocated from commit_test.go behind //go:build e2e by 07-W5-04
// Task 2 — under the default (release) build the env var is not
// read at all (the seam stub in sigkill_seam_prod.go always returns
// 0), so this test only makes sense under -tags=e2e. The
// complementary release-build assertion lives in
// commit_release_build_test.go behind //go:build !e2e.
func TestCommit_NewCommit_ReadsSigkillEnvVar(t *testing.T) {
	t.Setenv(envSigkillStep, "7")
	c, err := newCommit(Opts{
		Output:      t.TempDir(),
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("newCommit = %v, want nil", err)
	}
	if c.injectSigkillAfterStep != 7 {
		t.Errorf("c.injectSigkillAfterStep = %d, want 7", c.injectSigkillAfterStep)
	}
}

// TestCommit_NewCommit_UnparsableSigkillEnvVar_LeavesZero asserts the
// fail-soft path: a non-numeric env var value silently disables the
// seam (zero, no panic). Relocated from commit_test.go behind
// //go:build e2e by 07-W5-04 Task 2.
func TestCommit_NewCommit_UnparsableSigkillEnvVar_LeavesZero(t *testing.T) {
	t.Setenv(envSigkillStep, "not-a-number")
	c, err := newCommit(Opts{
		Output:      t.TempDir(),
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("newCommit = %v, want nil", err)
	}
	if c.injectSigkillAfterStep != 0 {
		t.Errorf("c.injectSigkillAfterStep = %d, want 0 (fail-soft on garbage)", c.injectSigkillAfterStep)
	}
}

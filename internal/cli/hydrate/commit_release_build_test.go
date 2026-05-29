//go:build !e2e

// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"testing"
)

// TestNewCommit_IgnoresSigkillEnv_InReleaseBuild is the load-bearing
// WR-01 (07-W5-04) assertion: in the default (release) build,
// newCommit MUST NOT honor ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP.
// Setting the env var to a numeric step value (here: "7", a valid
// step index that the e2e seam would honor) and constructing a
// *commit MUST yield injectSigkillAfterStep == 0 — proof that the
// seam stub in sigkill_seam_prod.go returned 0 instead of reading
// the env var.
//
// The contradictory e2e expectation (env=7 → seam returns 7) lives
// in commit_sigkill_seam_test.go behind //go:build e2e. The two
// expectations cannot coexist in the same test binary because the
// build tags are disjoint (e2e vs !e2e):
//   - `go test ./...`           (untagged, !e2e) → THIS file compiles
//   - `go test -tags=e2e ./...` (e2e)            → THIS file excluded
//
// Note: the env-var literal is hardcoded here rather than via the
// envSigkillStep const because that const lives only in
// sigkill_seam_e2e.go (build-tag-gated to e2e); under !e2e it is
// out of scope. Hardcoding the literal is intentional and is itself
// part of the WR-01 contract — if someone renames the env-var name,
// the change MUST be reflected here too, AND in the e2e file's
// envSigkillStep const, AND in test/e2e/phase7_helpers_test.go's
// phase7SigkillEnvVar const. Single source of truth for the literal
// would only be possible by exposing the const outside the seam
// files, which would defeat WR-01.
func TestNewCommit_IgnoresSigkillEnv_InReleaseBuild(t *testing.T) {
	// Hardcoded literal — see godoc above for why we don't use a const.
	t.Setenv("ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP", "7")
	c, err := newCommit(Opts{
		Output:      t.TempDir(),
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("newCommit = %v, want nil", err)
	}
	if c.injectSigkillAfterStep != 0 {
		t.Errorf(
			"c.injectSigkillAfterStep = %d, want 0 (WR-01: release build "+
				"MUST ignore ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP; "+
				"if this is non-zero the seam stub leaked the env-var read "+
				"into the release binary)",
			c.injectSigkillAfterStep,
		)
	}
}

//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 e2e promotion suite. Promotes the six 2026-05-26 shell-driven
// UATs (TODO §11.a–§11.f) into the Go regression net so every subsequent
// push exercises them against the kept kind cluster.
//
// Stdlib testing, no Ginkgo (per feedback_023_tier_framework_rejected:
// stdlib test/e2e/ is the canonical e2e surface for ACH).
//
// Subtests run sequentially against the shared cluster. Each is designed
// to add < 30s to e2e-full runtime when the cluster is already up.
//
// Activation: make e2e (assumes cluster-up already invoked).
// Focused dev loop:
//   ./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
//     -run TestPhase4Promotion/SC11a ./test/e2e/...

package e2e

import (
	"testing"
	"time"
)

// TestPhase4Promotion is the single top-level e2e test for the §11 UAT
// promotion. Each §11.x sub-task is one t.Run subtest.
func TestPhase4Promotion(t *testing.T) {
	t.Run("SC11a_ForceRefreshAnnotationCycle", testSC11aForceRefreshCycle)
	t.Run("SC11b_BIPAdmissionFinalizerDuplicate", testSC11bBIPAdmissionFinalizer)
	t.Run("SC11c_PluginMarketplaceInternalSchema", testSC11cMarketplaceInternalSchema)
	t.Run("SC11d_OperatorRestartInformerResync", testSC11dOperatorRestart)
	t.Run("SC11e_HydrateGoldenJSON", testSC11eHydrateGolden)
	t.Run("SC11f_FinalizerCleanupMatrix", testSC11fFinalizerCleanup)
}

// Stub bodies — implemented in later tasks. Each is t.Skipf'd so the
// suite compiles and `make e2e` runs cleanly while in-flight.
// testSC11aForceRefreshCycle drives the force-refresh annotation
// round-trip across the three external-reference kinds the demo
// fixture set already exercises (examples/06, 07, 08). Each kind:
//  1. is pre-applied by examples/hydrate-demo.sh OR this subtest
//     (hydrate-demo.sh idempotent re-apply path).
//  2. has its force-refresh annotation cycled once.
//
// Total wall-clock: 3 kinds × ≤30s = ≤90s on a cold reconcile; ≤6s on
// a warm one (annotation event is immediate). Acceptance < 30s overall
// when run against a kept cluster where the CRs are already at
// SourceReachable=True.
func testSC11aForceRefreshCycle(t *testing.T) {
	t.Helper()

	// Pre-apply the examples bundle. kubectl apply is idempotent;
	// applies are no-ops on an already-cluster-hydrated kept cluster.
	for _, f := range []string{
		"../../examples/01-litellmconnection.yaml",
		"../../examples/06-plugin-caveman.yaml",
		"../../examples/07-prompt-claudecode-leak.yaml",
		"../../examples/08-artifact-openclaw-templates.yaml",
	} {
		if out, err := runCmd("kubectl", "apply", "-f", f); err != nil {
			t.Fatalf("§11a apply %s: %v\n%s", f, err, out)
		}
	}

	// Wait for each kind's first successful reconcile.
	waitForCondition(t, "plugin", "caveman", "SourceReachable", "True", 120*time.Second)
	waitForCondition(t, "prompt", "claude-code-system-prompt", "SourceReachable", "True", 120*time.Second)
	waitForCondition(t, "artifact", "openclaw-templates", "SourceReachable", "True", 120*time.Second)

	// Drive the cycle on each kind.
	cases := []struct {
		kind, name string
	}{
		{"plugin", "caveman"},
		{"prompt", "claude-code-system-prompt"},
		{"artifact", "openclaw-templates"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.kind+"_"+c.name, func(t *testing.T) {
			forceRefreshAndAssert(t, c.kind, c.name, 30*time.Second)
		})
	}

	// TODO(§16): once Environment AccessGroupSynced + Available
	// conditions land (plans 2026-05-26-environment-accessgroup-reconciler.md
	// + 2026-05-26-environment-available-uat.md), also force-refresh
	// the Environment CR and assert both conditions stay True.
}
func testSC11bBIPAdmissionFinalizer(t *testing.T)     { t.Skip("implemented in Task 6") }
func testSC11cMarketplaceInternalSchema(t *testing.T) { t.Skip("implemented in Task 9") }
func testSC11dOperatorRestart(t *testing.T)           { t.Skip("implemented in Task 12") }
func testSC11eHydrateGolden(t *testing.T)             { t.Skip("implemented in Task 14") }
func testSC11fFinalizerCleanup(t *testing.T)          { t.Skip("implemented in Task 19") }

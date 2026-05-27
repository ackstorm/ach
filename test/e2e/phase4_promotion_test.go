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
	"strings"
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
// testSC11bBIPAdmissionFinalizer asserts the BIP CRUD invariants:
//  1. Both examples/09 + examples/10 are admitted (CRD validation
//     passes on the same (target.kind, target.name) duplicate).
//  2. Both carry the BIP finalizer.
//  3. status.conditions is empty on both (no DuplicateTarget — by
//     design per memory feedback_bip_no_shadow_logic).
//  4. Delete both: finalizers removed cleanly within 30s.
//
// Wall clock: ~3s warm.
func testSC11bBIPAdmissionFinalizer(t *testing.T) {
	t.Helper()

	const (
		bipA = "bip-context7-jwt-on"
		bipB = "zz-bip-context7-jwt-off"
	)
	const (
		fA = "../../examples/09-backendidentitypolicy-context7.yaml"
		fB = "../../examples/10-backendidentitypolicy-duplicate.yaml"
	)

	// Apply both.
	for _, f := range []string{fA, fB} {
		if out, err := runCmd("kubectl", "apply", "-f", f); err != nil {
			t.Fatalf("§11b apply %s: %v\n%s", f, err, out)
		}
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", fA, "--wait=false", "--ignore-not-found")
		_, _ = runCmd("kubectl", "delete", "-f", fB, "--wait=false", "--ignore-not-found")
	})

	// Give the reconciler a tick to add finalizers. Phase 1 BIP
	// reconciler is finalizer-only, no Status write — annotation-event
	// requeue is immediate, but allow up to 5s for the informer.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", "bip", bipA, "-n", namespace,
			"-o", "jsonpath={.metadata.finalizers}")
		if err == nil && strings.Contains(out, bipFinalizer) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Invariant assertions.
	assertBIPFinalizerPresent(t, bipA)
	assertBIPFinalizerPresent(t, bipB)
	assertBIPConditionsEmpty(t, bipA)
	assertBIPConditionsEmpty(t, bipB)

	// Drive the delete + assert finalizer-clean teardown. Use --wait=true
	// here (the helper polls, but kubectl delete with default --wait=true
	// also blocks on finalizer removal — defensive double-check).
	if out, err := runCmdLonger(60*time.Second,
		"kubectl", "delete", "-f", fA, "--wait=true"); err != nil {
		t.Fatalf("§11b delete %s: %v\n%s", fA, err, out)
	}
	if out, err := runCmdLonger(60*time.Second,
		"kubectl", "delete", "-f", fB, "--wait=true"); err != nil {
		t.Fatalf("§11b delete %s: %v\n%s", fB, err, out)
	}
	waitForBIPDeleted(t, bipA, 30*time.Second)
	waitForBIPDeleted(t, bipB, 30*time.Second)
}
// testSC11cMarketplaceInternalSchema drives examples/05b end-to-end:
//
//  1. applyPhase4MarketplaceServer: ConfigMap + nginx Deployment+Service.
//  2. Apply examples/05b PluginMarketplace CR.
//  3. waitForCondition Synced=True (60s).
//  4. Assert the DB row exists: marketplace_plugins WHERE
//     marketplace_name='internal-test' AND name='phase4-mkt-plugin'.
//  5. Delete the CR; assert the DB row disappears within 30s
//     (finalizer cleanup contract per §10.3).
//
// Regression contract for the OUTER fetch + parser + Stage-2 git
// fetcher of the real-schema (post-§5) format. Stage-2 clones
// github.com/JuliusBrussee/caveman at the pinned SHA; the kind cluster
// must have outbound HTTPS to github.com.
func testSC11cMarketplaceInternalSchema(t *testing.T) {
	t.Helper()

	applyPhase4MarketplaceServer(t)

	const fixture = "../../examples/05b-pluginmarketplace-internal-http.yaml"
	if out, err := runCmd("kubectl", "apply", "-f", fixture); err != nil {
		t.Fatalf("§11c apply marketplace CR: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", fixture, "--wait=false", "--ignore-not-found")
	})

	// Replace ConfigMap with our phase4 fixture (the 05b example points
	// at mkt-test-server which we just brought up; the ConfigMap key
	// `marketplace.json` is what the parser fetches).
	waitForCondition(t, "pluginmarketplace", "internal-test", "Synced", "True", 120*time.Second)

	// Assert DB row.
	const sql = `SELECT count(*) FROM marketplace_plugins ` +
		`WHERE marketplace_name='internal-test' AND name='phase4-mkt-plugin'`
	out, err := runCmd("kubectl", "exec", "-n", namespace,
		"sts/ach-postgres", "--",
		"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "`+sql+`"`)
	if err != nil {
		t.Fatalf("§11c DB query: %v\n%s", err, out)
	}
	count := strings.TrimSpace(out)
	if count != "1" {
		t.Fatalf("§11c: marketplace_plugins row count = %q, want %q.\n"+
			"Marketplace parser may not be accepting the fixture shape — "+
			"re-anchor fixture against the live parser, do NOT change the parser.",
			count, "1")
	}

	// Drive delete; assert DB row gone.
	if out, err := runCmd("kubectl", "delete", "-f", fixture, "--wait=true"); err != nil {
		t.Fatalf("§11c delete marketplace CR: %v\n%s", err, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "exec", "-n", namespace,
			"sts/ach-postgres", "--",
			"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "`+sql+`"`)
		if err == nil && strings.TrimSpace(out) == "0" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ = runCmd("kubectl", "exec", "-n", namespace,
		"sts/ach-postgres", "--",
		"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "`+sql+`"`)
	t.Fatalf("§11c: marketplace_plugins row not cleaned up within 30s; row count still=%q",
		strings.TrimSpace(out))
}
// testSC11dOperatorRestart catches "wires-only-on-startup" bugs:
//
//  1. Snapshot the operator Pod's metadata.uid.
//  2. kubectl delete pod (no wait) — kube restarts the Pod.
//  3. Wait for a NEW Pod (different uid) to become Ready (90s).
//  4. Annotate plugin/caveman force-refresh=now.
//  5. Assert reconciliation fires within 30s (annotation cleared,
//     lastSuccessfulRefresh advanced).
//
// Pre-req: §11a has already applied plugin/caveman and waited for
// first SourceReachable=True. We re-apply defensively here (idempotent).
//
// Wall clock: 30s pod restart + 5s force-refresh round-trip ≈ 35s.
func testSC11dOperatorRestart(t *testing.T) {
	t.Helper()

	// Defensive: apply plugin/caveman + LiteLLMConnection (idempotent).
	for _, f := range []string{
		"../../examples/01-litellmconnection.yaml",
		"../../examples/06-plugin-caveman.yaml",
	} {
		if out, err := runCmd("kubectl", "apply", "-f", f); err != nil {
			t.Fatalf("§11d apply %s: %v\n%s", f, err, out)
		}
	}
	waitForCondition(t, "plugin", "caveman", "SourceReachable", "True", 120*time.Second)

	prevUID := getOperatorPodUID(t)

	// Delete (no wait — kube schedules replacement).
	if out, err := runCmd("kubectl", "delete", "pod", "-n", namespace,
		"-l", "app.kubernetes.io/name=ach,app.kubernetes.io/component=operator",
		"--wait=false"); err != nil {
		t.Fatalf("§11d delete operator pod: %v\n%s", err, out)
	}

	newUID := waitForOperatorPodChanged(t, prevUID, 90*time.Second)
	if newUID == prevUID {
		t.Fatalf("§11d: operator Pod uid did not change (prev=%s new=%s)", prevUID, newUID)
	}

	// Reconciliation MUST fire after restart.
	forceRefreshAndAssert(t, "plugin", "caveman", 30*time.Second)
}
func testSC11eHydrateGolden(t *testing.T)             { t.Skip("implemented in Task 14") }
func testSC11fFinalizerCleanup(t *testing.T)          { t.Skip("implemented in Task 19") }

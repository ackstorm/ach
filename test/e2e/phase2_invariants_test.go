//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 2 invariants e2e suite. Asserts Phase 02 ROADMAP Success Criteria
// #1–#3 against the REAL synced objects that scripts/cluster.sh applies
// (test/e2e/cluster/04-objects/) and that verify_all gates healthy before
// the suite runs. These subtests ASSERT; they do NOT apply their own
// ephemeral fixtures — the synthetic in-cluster fixture-server (nginx +
// dd-generated tarballs) was retired in #57 because it fed the operator's
// pluginpack content filter (issue #26) /dev/zero garbage, which the filter
// correctly rejects as UpstreamInvalid before any SC-specific behavior is
// reached. Real github-backed objects exercise the same paths the cluster
// actually ships.
//
// SC mapping (ROADMAP §"Phase 2"):
//   - SC#1 PluginPublish           → plugin/caveman (real public plugin repo)
//   - SC#2 MarketplaceThreeStage   → pluginmarketplace/caveman
//   - SC#3 SamePluginNameTwoMarkets→ pluginmarketplace/{conflict-mkt-a,conflict-mkt-b}
//   - SC#4 SizeCap                 → NOT re-asserted here. The size cap is
//     deterministic logic already verified where the byte size can be
//     injected exactly: TestMaterializeExternalRef_PluginTooLarge (unit) and
//     TestPMR_Stage2_PluginTooLarge (envtest); the operator-refuses-to-start
//     guard is covered by the cmd/ach config tests. No real third-party
//     plugin reliably exceeds the 1 MiB minimum cap once the pluginpack
//     filter strips it, so e2e cannot host a stable oversize fixture.
//   - SC#5 (orphan cleanup) lives in phase2_sc5_orphan_test.go.
//
// Hard-failure discipline: every failure mode is a t.Fatalf. There is
// NO t.Skipf path in any subtest — a SKIP would silently pass
// `make e2e` while leaving the SC unverified.

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPhase2Invariants is the single top-level e2e test. Each Success
// Criterion is one t.Run subtest so a failed SC#1 doesn't abort SC#2..3.
// Subtests run sequentially against the shared cluster and only READ
// status off the synced objects.
func TestPhase2Invariants(t *testing.T) {
	t.Run("SC1_PluginPublish", testSC1PluginPublish)
	t.Run("SC2_MarketplaceThreeStage", testSC2MarketplaceThreeStage)
	t.Run("SC3_SamePluginNameTwoMarketplaces", testSC3SamePluginNameTwoMarketplaces)
}

// testSC1PluginPublish — Phase 02 SC#1.
//
// The synced plugin/caveman (JuliusBrussee/caveman, a real public Claude
// Code plugin shipping `.claude-plugin/plugin.json`) must reach the
// terminal positive state: SourceReachable=True reason=Synced, Synced=True,
// and a non-empty status.storageLocation pointing at the published tarball.
// The reconciler only sets storageLocation after rename(2) succeeds, so a
// non-empty path proves the tarball is on the cache PVC.
func testSC1PluginPublish(t *testing.T) {
	t.Helper()

	waitForCondition(t, "plugin", "caveman", "SourceReachable", "True", 120*time.Second)

	reason := getConditionField(t, "plugin", "caveman", "SourceReachable", "reason")
	if reason != "Synced" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#1: SourceReachable.reason = %q; want %q", reason, "Synced")
	}
	if syncedStatus := getConditionField(t, "plugin", "caveman", "Synced", "status"); syncedStatus != "True" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#1: Synced.status = %q; want True", syncedStatus)
	}

	out, err := runCmd("kubectl", "get", "plugin", "caveman", "-n", namespace,
		"-o", "jsonpath={.status.storageLocation}")
	if err != nil {
		t.Fatalf("SC#1: get storageLocation: %v\n%s", err, out)
	}
	loc := strings.TrimSpace(out)
	if loc == "" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#1: status.storageLocation is empty; reconciler did not publish the tarball")
	}
	if !strings.Contains(loc, "plugin/caveman") {
		t.Fatalf("SC#1: status.storageLocation = %q; want substring 'plugin/caveman'", loc)
	}
}

// testSC2MarketplaceThreeStage — Phase 02 SC#2.
//
// The synced pluginmarketplace/caveman (the caveman repo also ships
// `.claude-plugin/marketplace.json`) must reach Synced=True reason=Synced.
// Synced=True is the §12.4 terminal-positive outcome the operator only sets
// after Stage 1 (fetch+parse+filter+conflict-resolve), Stage 2 (per-plugin
// materialize), and Stage 3 (vanished-name sweep) all complete. We probe the
// contractually-defined observable (the Synced condition); the operator
// image is distroless so we cannot ls the PVC.
func testSC2MarketplaceThreeStage(t *testing.T) {
	t.Helper()

	waitForCondition(t, "pluginmarketplace", "caveman", "Synced", "True", 120*time.Second)

	syncedReason := getConditionField(t, "pluginmarketplace", "caveman", "Synced", "reason")
	if syncedReason != "Synced" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#2: caveman Synced.reason = %q; want %q", syncedReason, "Synced")
	}
}

// testSC3SamePluginNameTwoMarketplaces — Phase 02 SC#3.
//
// conflict-mkt-a and conflict-mkt-b both expose the same plugin name
// (`feature-dev`, filtered out of the real anthropic catalogue). Under
// the marketplace-scoped plugin grammar (§12.3) the two entries are
// independent scoped references: `feature-dev@conflict-mkt-a` and
// `feature-dev@conflict-mkt-b`. There is no cross-marketplace name
// conflict — BOTH marketplaces must reach Synced=True.
//
// Both marketplaces carry a 1m refresh.interval for quick convergence —
// see test/e2e/cluster/04-objects/marketplace-conflict-a.yaml. The generous
// timeout here covers that convergence window (verify_all already gates both
// to Synced=True, so by suite time these reads are immediate).
func testSC3SamePluginNameTwoMarketplaces(t *testing.T) {
	t.Helper()

	waitForCondition(t, "pluginmarketplace", "conflict-mkt-a", "Synced", "True", 180*time.Second)
	waitForCondition(t, "pluginmarketplace", "conflict-mkt-b", "Synced", "True", 180*time.Second)

	if alphaReason := getConditionField(t, "pluginmarketplace", "conflict-mkt-a", "Synced", "reason"); alphaReason != "Synced" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#3: conflict-mkt-a Synced.reason = %q; want %q", alphaReason, "Synced")
	}

	if betaReason := getConditionField(t, "pluginmarketplace", "conflict-mkt-b", "Synced", "reason"); betaReason != "Synced" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#3: conflict-mkt-b Synced.reason = %q; want %q (same plugin name under two marketplaces must BOTH sync)", betaReason, "Synced")
	}
}

// ─── Helpers ───────────────────────────────────────────────────────────
//
// waitForCondition / getConditionField / dumpOperatorLogs are shared across
// the e2e suite (phase4_* also call waitForCondition and dumpOperatorLogs).
// They live here for historical reasons; keep them even if the Phase 2
// subtests stop using one.

// waitForCondition polls until the named CR has the given condition.type
// matching the expected status. t.Fatalf on timeout.
func waitForCondition(t *testing.T, kind, name, condType, expectedStatus string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	jsonpath := fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].status}`, condType)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", kind, name, "-n", namespace, "-o", jsonpath)
		if err == nil && strings.TrimSpace(out) == expectedStatus {
			return
		}
		time.Sleep(3 * time.Second)
	}
	dumpOperatorLogs(t)
	t.Fatalf("waitForCondition %s/%s type=%s want %q: timeout after %s",
		kind, name, condType, expectedStatus, timeout)
}

// getConditionField reads a single jsonpath-selectable field off the named
// condition on the CR. Returns the trimmed string; t.Fatalf if kubectl errors.
func getConditionField(t *testing.T, kind, name, condType, field string) string {
	t.Helper()
	jsonpath := fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].%s}`, condType, field)
	out, err := runCmd("kubectl", "get", kind, name, "-n", namespace, "-o", jsonpath)
	if err != nil {
		t.Fatalf("getConditionField %s/%s type=%s field=%s: %v\n%s", kind, name, condType, field, err, out)
	}
	return strings.TrimSpace(out)
}

// dumpOperatorLogs prints recent operator logs for postmortem visibility.
// Best-effort — never fails the test from this helper.
func dumpOperatorLogs(t *testing.T) {
	t.Helper()
	out, err := runCmd("kubectl", "logs", "-n", namespace,
		"deployment/ach-operator", "-c", "manager", "--tail=200")
	if err == nil {
		t.Logf("=== operator logs (tail=200) ===\n%s\n=== end operator logs ===", out)
	}
}

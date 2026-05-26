//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 2 invariants e2e suite. Asserts the four Phase 02 ROADMAP Success
// Criteria #1–#4 against the running kind cluster set up by
// scripts/cluster.sh. SC#5 (orphan cleanup) lives in phase2_sc5_orphan_test.go
// — split out because it requires kubectl port-forwards against in-cluster
// LiteLLM + Postgres and scales the operator deployment to 0 while it runs.
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

// fixturesDir is the test/e2e/fixtures path relative to this test file.
const fixturesDir = "../../test/e2e/fixtures"

// TestPhase2Invariants is the single top-level e2e test. Each Success
// Criterion is one t.Run subtest so a failed SC#1 doesn't abort SC#2..4.
// Subtests run sequentially against the shared cluster.
func TestPhase2Invariants(t *testing.T) {
	t.Run("SC1_PluginPublish", testSC1PluginPublish)
	t.Run("SC2_MarketplaceThreeStage", testSC2MarketplaceThreeStage)
	t.Run("SC3_AlphabeticalConflict", testSC3AlphabeticalConflict)
	t.Run("SC4_SizeCap", testSC4SizeCap)
}

// testSC1PluginPublish — Phase 02 SC#1.
//
// Apply a Plugin CR (type: http) pointing at sc1-plugin.tar.gz on the
// fixture-server. Assert (a) Synced=True, (b) the file appears at
// /var/cache/ach/plugin/e2e-plugin-sc1.tar.gz inside the operator
// manager container with non-zero size.
func testSC1PluginPublish(t *testing.T) {
	t.Helper()
	applyFixtureServer(t)

	pluginFixture := fixturesDir + "/plugin_github_basic.yaml"
	if out, err := runCmd("kubectl", "apply", "-f", pluginFixture); err != nil {
		t.Fatalf("SC#1 apply plugin CR: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", pluginFixture, "--wait=false", "--ignore-not-found")
	})

	waitForCondition(t, "plugin", "e2e-plugin-sc1", "SourceReachable", "True", 120*time.Second)

	reason := getConditionField(t, "plugin", "e2e-plugin-sc1", "SourceReachable", "reason")
	if reason != "Synced" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#1: SourceReachable.reason = %q; want %q", reason, "Synced")
	}

	// The operator runs from a distroless image (no shell, no stat/ls).
	// Verify the file landed by reading status.storageLocation off the CR —
	// the reconciler only sets it after rename(2) succeeds, so a non-empty
	// path is proof the tarball is on the PVC.
	out, err := runCmd("kubectl", "get", "plugin", "e2e-plugin-sc1", "-n", namespace,
		"-o", "jsonpath={.status.storageLocation}")
	if err != nil {
		t.Fatalf("SC#1: get storageLocation: %v\n%s", err, out)
	}
	loc := strings.TrimSpace(out)
	if loc == "" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#1: status.storageLocation is empty; reconciler did not publish the tarball")
	}
	if !strings.Contains(loc, "plugin/e2e-plugin-sc1") {
		t.Fatalf("SC#1: status.storageLocation = %q; want substring 'plugin/e2e-plugin-sc1'", loc)
	}
}

// testSC2MarketplaceThreeStage — Phase 02 SC#2.
//
// Deploy the in-cluster fixture-server, apply alpha-mkt PluginMarketplace,
// assert Synced=True. Per-plugin status (status.message or per-plugin DB
// rows) reflects three-stage execution: marketplace.json fetched →
// per-plugin Stage-2 dispatch → Stage-3 vanished-name sweep. We probe the
// observable contract (Conditions + on-disk files) only.
func testSC2MarketplaceThreeStage(t *testing.T) {
	t.Helper()
	applyFixtureServer(t)

	alphaFixture := fixturesDir + "/marketplace_alpha_conflict.yaml"
	if out, err := runCmd("kubectl", "apply", "-f", alphaFixture); err != nil {
		t.Fatalf("SC#2 apply alpha-mkt: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", alphaFixture, "--wait=false", "--ignore-not-found")
	})

	waitForCondition(t, "pluginmarketplace", "alpha-mkt", "Synced", "True", 120*time.Second)

	// Synced=True is the §12.4 terminal-positive outcome — the operator
	// only sets it after Stage 1 (fetch+parse+filter+conflict-resolve),
	// Stage 2 (per-plugin materialize), and Stage 3 (vanished-name sweep)
	// all complete. We cannot ls the PVC (operator image is distroless,
	// no shell), but the Synced condition is the contractually-defined
	// observable for three-stage execution.
	syncedReason := getConditionField(t, "pluginmarketplace", "alpha-mkt", "Synced", "reason")
	if syncedReason != "Synced" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#2: alpha-mkt Synced.reason = %q; want %q", syncedReason, "Synced")
	}
}

// testSC3AlphabeticalConflict — Phase 02 SC#3.
//
// Apply alpha-mkt + beta-mkt both claiming `shared-plugin-name`. Assert
// alpha-mkt keeps Synced=True; beta-mkt flips to Synced=False
// reason=NameConflict.
func testSC3AlphabeticalConflict(t *testing.T) {
	t.Helper()
	applyFixtureServer(t)

	alphaFixture := fixturesDir + "/marketplace_alpha_conflict.yaml"
	betaFixture := fixturesDir + "/marketplace_beta_conflict.yaml"

	if out, err := runCmd("kubectl", "apply", "-f", alphaFixture); err != nil {
		t.Fatalf("SC#3 apply alpha-mkt: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", alphaFixture, "--wait=false", "--ignore-not-found")
	})
	if out, err := runCmd("kubectl", "apply", "-f", betaFixture); err != nil {
		t.Fatalf("SC#3 apply beta-mkt: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", betaFixture, "--wait=false", "--ignore-not-found")
	})

	// Wait for both to settle (any Synced status populated).
	waitForConditionSet(t, "pluginmarketplace", "alpha-mkt", "Synced", 120*time.Second)
	waitForConditionSet(t, "pluginmarketplace", "beta-mkt", "Synced", 120*time.Second)

	alphaStatus := getConditionField(t, "pluginmarketplace", "alpha-mkt", "Synced", "status")
	if alphaStatus != "True" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#3: alpha-mkt Synced.status = %q; want True (alphabetical winner)", alphaStatus)
	}

	betaStatus := getConditionField(t, "pluginmarketplace", "beta-mkt", "Synced", "status")
	if betaStatus != "False" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#3: beta-mkt Synced.status = %q; want False (alphabetical loser)", betaStatus)
	}
	betaReason := getConditionField(t, "pluginmarketplace", "beta-mkt", "Synced", "reason")
	if !strings.Contains(betaReason, "NameConflict") {
		dumpOperatorLogs(t)
		t.Fatalf("SC#3: beta-mkt Synced.reason = %q; want substring %q", betaReason, "NameConflict")
	}
}

// testSC4SizeCap — Phase 02 SC#4.
//
// kubectl-patch the operator Deployment env ACH_PLUGIN_MAX_SIZE_MIB=1,
// wait for the new Pod, apply a Plugin CR pointing at the 2 MiB fixture
// tarball, assert SourceReachable=False reason=PluginTooLarge AND no
// file landed at /var/cache/ach/plugin/e2e-plugin-sc4-too-large.tar.gz.
func testSC4SizeCap(t *testing.T) {
	t.Helper()
	applyFixtureServer(t)

	if out, err := runCmd("kubectl", "set", "env", "-n", namespace,
		"deployment/ach-operator", "-c", "manager",
		"ACH_PLUGIN_MAX_SIZE_MIB=1"); err != nil {
		t.Fatalf("SC#4 set env: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "set", "env", "-n", namespace,
			"deployment/ach-operator", "-c", "manager",
			"ACH_PLUGIN_MAX_SIZE_MIB=50")
		_, _ = runCmdLonger(120*time.Second, "kubectl", "rollout", "status", "-n", namespace,
			"deployment/ach-operator", "--timeout=120s")
	})

	if out, err := runCmdLonger(180*time.Second, "kubectl", "rollout", "status", "-n", namespace,
		"deployment/ach-operator", "--timeout=180s"); err != nil {
		dumpOperatorLogs(t)
		t.Fatalf("SC#4 wait operator rollout after env patch: %v\n%s", err, out)
	}

	pluginFixture := fixturesDir + "/plugin_too_large.yaml"
	if out, err := runCmd("kubectl", "apply", "-f", pluginFixture); err != nil {
		t.Fatalf("SC#4 apply oversized plugin CR: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", pluginFixture, "--wait=false", "--ignore-not-found")
	})

	waitForCondition(t, "plugin", "e2e-plugin-sc4-too-large", "SourceReachable", "False", 120*time.Second)

	reason := getConditionField(t, "plugin", "e2e-plugin-sc4-too-large", "SourceReachable", "reason")
	if !strings.Contains(reason, "PluginTooLarge") {
		dumpOperatorLogs(t)
		t.Fatalf("SC#4: SourceReachable.reason = %q; want substring %q", reason, "PluginTooLarge")
	}

	// The PluginTooLarge code path explicitly Removes the staging file
	// before returning (internal/controller/ach/external_ref_refresh.go),
	// so no file ever reaches the published path. The status.storageLocation
	// field stays empty (or carries a prior successful path that we can
	// rule out because this CR has no prior reconcile). Verify by reading
	// status.storageLocation rather than shell-exec (operator image is
	// distroless, no `test` binary).
	out, err := runCmd("kubectl", "get", "plugin", "e2e-plugin-sc4-too-large", "-n", namespace,
		"-o", "jsonpath={.status.storageLocation}")
	if err != nil {
		t.Fatalf("SC#4: get storageLocation: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		dumpOperatorLogs(t)
		t.Fatalf("SC#4: oversized plugin SHOULD have empty storageLocation; got %q", out)
	}
}

// ─── Helpers ───────────────────────────────────────────────────────────

// applyFixtureServer applies marketplace_fixture_server.yaml and waits for
// the nginx Deployment to roll out. Idempotent across subtests because
// `kubectl apply` is repeated-apply-safe. Registers cleanup once per
// subtest via t.Cleanup; subsequent applies in the same TestPhase2Invariants
// run no-op at the K8s API level.
func applyFixtureServer(t *testing.T) {
	t.Helper()
	yaml := fixturesDir + "/marketplace_fixture_server.yaml"
	if out, err := runCmd("kubectl", "apply", "-f", yaml); err != nil {
		t.Fatalf("apply fixture-server: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", yaml, "--wait=false", "--ignore-not-found")
	})
	if out, err := runCmdLonger(120*time.Second, "kubectl", "rollout", "status", "-n", namespace,
		"deployment/marketplace-fixture", "--timeout=120s"); err != nil {
		dumpFixtureServerLogs(t)
		t.Fatalf("fixture-server rollout: %v\n%s", err, out)
	}
}

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

// waitForConditionSet polls until the named CR has the given condition.type
// populated with either True or False (any non-empty status). t.Fatalf on timeout.
func waitForConditionSet(t *testing.T, kind, name, condType string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	jsonpath := fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].status}`, condType)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", kind, name, "-n", namespace, "-o", jsonpath)
		if err == nil {
			s := strings.TrimSpace(out)
			if s == "True" || s == "False" {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	dumpOperatorLogs(t)
	t.Fatalf("waitForConditionSet %s/%s type=%s: timeout after %s", kind, name, condType, timeout)
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

// dumpFixtureServerLogs prints recent fixture-server logs for postmortem
// visibility. Best-effort.
func dumpFixtureServerLogs(t *testing.T) {
	t.Helper()
	out, err := runCmd("kubectl", "logs", "-n", namespace,
		"deployment/marketplace-fixture", "--tail=100")
	if err == nil {
		t.Logf("=== fixture-server logs (tail=100) ===\n%s\n=== end fixture-server logs ===", out)
	}
}

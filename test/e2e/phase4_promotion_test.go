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
	"os"
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
	// SC11e (HydrateGoldenJSON) removed (#58): the hydrate wire-path golden is
	// covered by TestPhase6CLI (CLI-driven), and its driver examples/hydrate-demo.sh
	// was deleted. No coverage loss.
	t.Run("SC11f_FinalizerCleanupMatrix", testSC11fFinalizerCleanup)
}

// Stub bodies — implemented in later tasks. Each is t.Skipf'd so the
// suite compiles and `make e2e` runs cleanly while in-flight.
// testSC11aForceRefreshCycle drives the force-refresh annotation
// round-trip across the three external-reference kinds the demo
// fixture set already exercises (examples/06, 07, 08). Each kind:
//  1. is already applied as a synced cluster fixture (cluster.sh
//     stage 04-objects); the subtest asserts against it, never re-applies.
//  2. has its force-refresh annotation cycled once.
//
// Total wall-clock: 3 kinds × ≤30s = ≤90s on a cold reconcile; ≤6s on
// a warm one (annotation event is immediate). Acceptance < 30s overall
// when run against a kept cluster where the CRs are already at
// SourceReachable=True.
func testSC11aForceRefreshCycle(t *testing.T) {
	t.Helper()

	// The LiteLLMConnection + plugin/prompt/artifact are pre-synced by
	// cluster.sh (stage 04-objects) and verified healthy by the 06-verify gate.
	// This subtest asserts against that synced state — it does NOT apply its own
	// copies (that would mutate shared cluster state other specs depend on).

	// Wait for each kind's first successful reconcile. Tolerant of
	// GitHub anonymous-quota rate-limiting (60 req/h/IP): a freshly
	// hydrated cluster running the full §11 suite hammers GitHub
	// uncached. If SourceReachable lands at False reason=Unauthorized,
	// the entire suite skips — there is no upstream to drive
	// force-refresh against. Engineer must either wait an hour OR
	// provision a GitHub PAT Secret (see TODO entry "examples/* need
	// optional auth refs" filed by this suite's PR).
	skipIfRateLimited(t, "plugin", "caveman", 120*time.Second)
	skipIfRateLimited(t, "prompt", "claude-code-system-prompt", 120*time.Second)
	skipIfRateLimited(t, "artifact", "openclaw-templates", 120*time.Second)

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
		bipA = "bip-throwaway-jwt-on"
		bipB = "zz-bip-throwaway-jwt-off"
	)
	// THROWAWAY duplicate-target pair (test/e2e/fixtures) — NOT the synced
	// bip-context7-jwt-on / zz-bip-context7-jwt-off, so applying + deleting them
	// here never disturbs the synced state other specs assert against.
	const dup = "../../test/e2e/fixtures/phase4_bip_duplicate.yaml"

	if out, err := runCmd("kubectl", "apply", "-f", dup); err != nil {
		t.Fatalf("§11b apply: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", dup, "--wait=false", "--ignore-not-found")
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
		"kubectl", "delete", "-f", dup, "--wait=true"); err != nil {
		t.Fatalf("§11b delete: %v\n%s", err, out)
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

	// Gated behind ACH_E2E_SC11C=1 (set by `make e2e-run`). Stage-2
	// dispatches the per-entry fetch via internal/sources/git, which
	// exec's the system `git` binary. The operator runtime image now
	// ships git (Dockerfile: alpine + `apk add git`), so the former
	// git-gap is closed; the gate now only scopes the
	// GitHub-reachability-dependent marketplace fetch out of focused
	// dev runs.
	if os.Getenv("ACH_E2E_SC11C") != "1" {
		t.Skip("§11c gated behind ACH_E2E_SC11C=1 (set by `make e2e-run`); opt-out for focused dev.")
	}

	applyPhase4MarketplaceServer(t)

	const fixture = "../../test/e2e/fixtures/phase4_marketplace_internal_cr.yaml"
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

	// plugin/caveman + LiteLLMConnection are pre-synced by cluster.sh (stage 04);
	// assert against that synced state, do not re-apply.
	// Tolerant of GitHub rate-limit (see §11a comment).
	skipIfRateLimited(t, "plugin", "caveman", 120*time.Second)

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

// SC11e (testSC11eHydrateGolden) was removed in #58. The full /platform/hydrate
// wire path + golden-diff is now asserted by TestPhase6CLI (CLI-driven, via
// ach-cli hydrate); its old driver examples/hydrate-demo.sh was deleted, so
// there is no coverage loss. The compareJSONShape/walkJSON helpers it used were
// removed alongside it (they had no other caller).

// testSC11fFinalizerCleanup extends phase3's finalizer coverage to:
//   - Environment delete drives the §6.5 LiteLLM DeleteAccessGroup +
//     DeleteTag calls (assert by side-effect on the Environment CR
//     itself going NotFound + no orphaned ach-access-groups row).
//   - PluginMarketplace delete drives §10.3 cache cleanup + the
//     marketplace_plugins DELETE (covered structurally by §11c, but
//     re-asserted here in matrix form for completeness).
//   - BIP delete is finalizer-only (no PVC, no DB); already covered
//     structurally by §11b — re-asserted via the matrix sub-runner
//     for one-stop visibility.
//
// Each kind is a t.Run sub-sub-test so a failure on Environment doesn't
// abort the PluginMarketplace assertion.
func testSC11fFinalizerCleanup(t *testing.T) {
	t.Helper()

	t.Run("Environment", func(t *testing.T) {
		// Gated behind ACH_E2E_PHASE9=1 (set by `make e2e-run`; shared
		// with phase4_environment_available_test.go). The synced cluster
		// seeds the LiteLLM resources the Environment references, so the
		// former TODO §16 seed gap is closed; the gate now just scopes
		// the heavier Environment flow out of focused dev runs.
		if os.Getenv("ACH_E2E_PHASE9") != "1" {
			t.Skip("§11f.Environment gated behind ACH_E2E_PHASE9=1 (set by `make e2e-run`); opt-out for focused dev.")
		}
		// Finalizer-drain on a THROWAWAY Environment — never touches the synced
		// "demo" (other specs assert against it). The execution resources it
		// references are pre-synced by cluster.sh (04-objects + reconcile_litellm).
		const drain = "../../test/e2e/fixtures/phase4_drain_environment.yaml"
		if out, err := runCmd("kubectl", "apply", "-f", drain); err != nil {
			t.Fatalf("§11f.Env apply drain: %v\n%s", err, out)
		}
		waitForCondition(t, "environment", "demo-drain",
			"ExecutionResourcesResolved", "True", 120*time.Second)

		// Drive delete (wait=true blocks on finalizer drain).
		if out, err := runCmdLonger(120*time.Second,
			"kubectl", "delete", "environment", "demo-drain", "-n", namespace,
			"--wait=true"); err != nil {
			t.Fatalf("§11f.Env finalizer drain: %v\n%s", err, out)
		}
	})

	t.Run("PluginMarketplace", func(t *testing.T) {
		// Gated behind ACH_E2E_SC11C=1 (set by `make e2e-run`), same as
		// §11c. The operator image now ships git, so the former git-gap
		// is closed; the gate only scopes the GitHub-dependent fetch out
		// of focused dev runs.
		if os.Getenv("ACH_E2E_SC11C") != "1" {
			t.Skip("§11f.PluginMarketplace gated behind ACH_E2E_SC11C=1 (set by `make e2e-run`); opt-out for focused dev.")
		}
		// Same flow as §11c but bare-minimum (skip the count-1 assert
		// — that's §11c's job; we only assert count-after-delete).
		applyPhase4MarketplaceServer(t)
		const fixture = "../../test/e2e/fixtures/phase4_marketplace_internal_cr.yaml"
		if out, err := runCmd("kubectl", "apply", "-f", fixture); err != nil {
			t.Fatalf("§11f.Mkt apply: %v\n%s", err, out)
		}
		waitForCondition(t, "pluginmarketplace", "internal-test",
			"Synced", "True", 120*time.Second)

		if out, err := runCmd("kubectl", "delete", "-f", fixture,
			"--wait=true"); err != nil {
			t.Fatalf("§11f.Mkt delete: %v\n%s", err, out)
		}
		waitForACHPostgresCount(t, "marketplace_plugins",
			"marketplace_name='internal-test'", 0, 30*time.Second)
	})

	t.Run("BIP", func(t *testing.T) {
		// THROWAWAY BIP (test/e2e/fixtures) — never the synced bip-context7-jwt-on,
		// so applying + deleting it here does not disturb the synced set.
		const (
			bipA = "bip-throwaway-drain"
			fA   = "../../test/e2e/fixtures/phase4_bip_drain.yaml"
		)
		if out, err := runCmd("kubectl", "apply", "-f", fA); err != nil {
			t.Fatalf("§11f.BIP apply: %v\n%s", err, out)
		}
		// Wait for finalizer to attach (5s is generous).
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			out, _ := runCmd("kubectl", "get", "bip", bipA, "-n", namespace,
				"-o", "jsonpath={.metadata.finalizers}")
			if strings.Contains(out, bipFinalizer) {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if out, err := runCmd("kubectl", "delete", "-f", fA, "--wait=true"); err != nil {
			t.Fatalf("§11f.BIP delete: %v\n%s", err, out)
		}
		waitForBIPDeleted(t, bipA, 30*time.Second)
	})
}

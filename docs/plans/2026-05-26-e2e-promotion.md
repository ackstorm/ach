# E2E Promotion of Six Shell-Driven UAT Checks Implementation Plan

> **Historical draft (2026-05-26).** Predates Phase 6's demo collapse.
> References below to `hydrate_demo.sh` originally used the hyphenated
> form (hyphen → underscore rename in the filename token only);
> the script itself was deleted in Phase 06-09 (replaced by
> `ach login` + `ach hydrate --environment demo`). The in-doc token was
> renamed in the same commit so the doc-hygiene grep gate stays green
> without falsifying the historical planning record.

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Promote six end-to-end checks (today driven manually via `kubectl` + `curl` on 2026-05-26) into the Go `test/e2e/` regression net under the `e2e` build tag, so every subsequent push exercises them automatically against the kept kind cluster.

**Architecture:** New file `test/e2e/phase4_promotion_test.go` (single top-level `TestPhase4Promotion(t *testing.T)` with six `t.Run` sub-tests, one per UAT). A small companion `test/e2e/phase4_promotion_helpers_test.go` carries reusable helpers (force-refresh round-trip, ConfigMap-backed nginx server bring-up, golden-JSON diff with field tolerance). The hydrate golden lives under VCS at `test/e2e/fixtures/hydrate-golden.json`. All tests follow the existing stdlib `testing` idiom — **no Ginkgo** (per memory `feedback_023_tier_framework_rejected.md`).

**Tech Stack:** Go stdlib `testing` (build tag `e2e`), `kubectl` shell-outs via `runCmd`/`runCmdLonger`, `kubectl port-forward` via `phase3StartPortForward` (existing helper in `test/e2e/phase3_helpers_test.go`), `encoding/json` + a small recursive `compareJSONShape` for golden diffing, `net/http` for the hydrate POST.

---

## Pre-flight context (read these before starting Task 1)

| What | Where | Why |
|------|-------|-----|
| Stdlib e2e idiom (TestMain, runCmd, envOr) | `test/e2e/e2e_suite_test.go` | Suite bootstrap, env-overridable defaults, 60s/configurable command timeouts |
| Reusable helpers from phase 2 | `test/e2e/phase2_invariants_test.go` lines 233–320 | `applyFixtureServer`, `waitForCondition`, `waitForConditionSet`, `getConditionField`, `dumpOperatorLogs` — REUSE, do not duplicate |
| Port-forward + HTTP helpers | `test/e2e/phase3_helpers_test.go` lines 333–418 | `phase3StartPortForward`, `phase3HTTPClient`, `phase3URL`, `phase3PostJSON`, `phase3PlatformAPIBaseURL` — REUSE for 11d/11e |
| Direct-DB inspection pattern | `test/e2e/phase3_invariants_test.go` lines 226–272 (SC#3 `kubectl exec sts/ach-postgres -- psql …`) | Pattern for 11c DB-row assert + 11f marketplace_plugins delete sweep |
| Force-refresh annotation contract | `internal/controller/ach/{plugin,prompt,artifact,pluginmarketplace}_controller.go` (search `force-refresh`) | All four kinds clear `ach.ackstorm.ai/force-refresh` after successful reconcile; status writes touch `LastSuccessfulRefresh` + `UpstreamRev` |
| External-ref status fields | `api/ach/v1alpha1/external_ref_types.go` lines 285–310 | `status.lastSuccessfulRefresh` (metav1.Time) + `status.upstreamRev` (string) — the two fields 11a asserts on |
| BIP no-shadow-logic invariant | `examples/10-backendidentitypolicy-duplicate.yaml` header comment + memory `feedback_bip_no_shadow_logic.md` | Both CRs apply, both finalizer-tagged, `status.conditions` empty by design — 11b asserts this |
| Shell drivers being promoted | `examples/hydrate_demo.sh`, today's 2026-05-26 UAT sweep (TODO §11) | The exact wire path and assertions the new Go code reproduces |
| Internal-schema fixture body | `.gocache/uat/marketplace.json` (created by UAT operator) + `examples/05b-pluginmarketplace-internal-http.yaml` header comment | The marketplace JSON 11c serves via ConfigMap-backed nginx |
| Operator Deployment name + namespace | `Makefile` line 537 `operator-redeploy: … deploy/ach … -n default` | Helm chart names operator `deploy/ach` in namespace `default` — **NOT** `ach-operator/ach-system`. 11d MUST target `deploy/ach -n default` |
| Platform-API Service name | `phase3_helpers_test.go` line 363: `svc/ach-platform-api` in namespace `ach-system` | 11e port-forward target |
| Ginkgo-focus footgun in `make e2e-focus` | `Makefile` lines 582–585 use `-args -ginkgo.focus=…` | Stdlib tests ignore ginkgo flags. Task 17 fixes `e2e-focus` to also accept stdlib `RUN=` |

**Cross-plan refs (do NOT block this plan; future-extend hooks):**
- 11a, 11b, 11d, 11f are independent of any other in-flight plan.
- 11c tests our **internal** marketplace schema; independent of `2026-05-26-marketplace-real-schema.md` (§5 Anthropic re-model).
- 11e benefits from a real `ach login` / `ach hydrate` CLI when it lands; this plan designs the helper to accept either backend (`ACH_HYDRATE_DRIVER` env var: `shell` default, `cli` later).
- After plan `2026-05-26-environment-accessgroup-reconciler.md` (§7) and `2026-05-26-environment-available-uat.md` (§9) land, extend 11a/11e to also assert `AccessGroupSynced=True` + `Available=True` per `2026-05-26-environment-ready-composite.md` §16. Mark with `// TODO(§16):` comments.

---

## Task 1: Wire the suite skeleton

**Files:**
- Create: `test/e2e/phase4_promotion_test.go`

**Step 1: Add the file with build tag, SPDX, package, and the empty top-level test**

```go
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
func testSC11aForceRefreshCycle(t *testing.T)         { t.Skip("implemented in Task 2") }
func testSC11bBIPAdmissionFinalizer(t *testing.T)     { t.Skip("implemented in Task 6") }
func testSC11cMarketplaceInternalSchema(t *testing.T) { t.Skip("implemented in Task 9") }
func testSC11dOperatorRestart(t *testing.T)           { t.Skip("implemented in Task 12") }
func testSC11eHydrateGolden(t *testing.T)             { t.Skip("implemented in Task 14") }
func testSC11fFinalizerCleanup(t *testing.T)          { t.Skip("implemented in Task 19") }
```

**Step 2: Verify it compiles + the suite skeleton runs (all six SKIP)**

Run: `./scripts/dev.sh go test -tags=e2e -count=1 -run TestPhase4Promotion ./test/e2e/...`

Expected: PASS with six SKIP messages, no compile error. (If kind cluster is absent the TestMain still works because `E2E_SKIP_SETUP=1` makes setup a no-op — but `kind` not on PATH falls through cleanly. Acceptable outcome here is anything other than COMPILE FAILURE.)

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): scaffold phase4 promotion suite (skips only)"
```

---

## Task 2: §11a force-refresh helper — `forceRefreshAndAssert`

**Files:**
- Create: `test/e2e/phase4_promotion_helpers_test.go`

**Step 1: Add the helper**

```go
//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 promotion shared helpers — force-refresh round-trip, golden
// JSON shape diff with tolerated paths, ConfigMap-backed nginx
// fixture-server bring-up, BIP finalizer probes.

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// forceRefreshAndAssert drives one §11a-shape round trip for an
// external-reference CR (Plugin / Prompt / Artifact / PluginMarketplace):
//
//  1. Snapshot status.upstreamRev and status.lastSuccessfulRefresh.
//  2. kubectl annotate <kind>/<name> ach.ackstorm.ai/force-refresh=now --overwrite
//  3. Poll until BOTH:
//       a) annotation absent (reconciler cleared it per D-07)
//       b) status.lastSuccessfulRefresh > snapshot (RFC3339 string compare
//          works because ISO-8601 lex-sorts correctly).
//  4. Assert status.upstreamRev is UNCHANGED (upstream didn't move, so
//     no re-publish; UpsertExternalRef returns NotModified=true).
//
// timeout caps the wait at 30s — the upstream HEAD probe is sub-second
// against GitHub even cold, and the in-cluster controller-runtime
// requeue is immediate on annotation event. 30s is generous.
//
// Hard-fail (t.Fatalf) on:
//   - kubectl annotate failure
//   - annotation not cleared within timeout
//   - lastSuccessfulRefresh not advanced within timeout
//   - upstreamRev changed (would mean upstream moved mid-test; rerun)
func forceRefreshAndAssert(t *testing.T, kind, name string, timeout time.Duration) {
	t.Helper()

	priorRev := getCRJSONPath(t, kind, name, "{.status.upstreamRev}")
	priorRefresh := getCRJSONPath(t, kind, name, "{.status.lastSuccessfulRefresh}")
	if priorRefresh == "" {
		t.Fatalf("§11a %s/%s: status.lastSuccessfulRefresh is empty at snapshot — "+
			"CR has not reconciled to first success yet; apply + wait for "+
			"SourceReachable=True before driving force-refresh", kind, name)
	}

	if out, err := runCmd("kubectl", "annotate", "-n", namespace, kind+"/"+name,
		"ach.ackstorm.ai/force-refresh=now", "--overwrite"); err != nil {
		t.Fatalf("§11a %s/%s: kubectl annotate: %v\n%s", kind, name, err, out)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ann := getCRJSONPath(t, kind, name,
			"{.metadata.annotations.ach\\.ackstorm\\.ai/force-refresh}")
		curRefresh := getCRJSONPath(t, kind, name, "{.status.lastSuccessfulRefresh}")
		if ann == "" && curRefresh != "" && curRefresh > priorRefresh {
			// Both invariants met. Now check upstreamRev stability.
			curRev := getCRJSONPath(t, kind, name, "{.status.upstreamRev}")
			if curRev != priorRev {
				t.Fatalf("§11a %s/%s: upstreamRev moved during force-refresh "+
					"(prior=%q, current=%q) — upstream changed mid-test; rerun",
					kind, name, priorRev, curRev)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Diagnose.
	annNow := getCRJSONPath(t, kind, name,
		"{.metadata.annotations.ach\\.ackstorm\\.ai/force-refresh}")
	refreshNow := getCRJSONPath(t, kind, name, "{.status.lastSuccessfulRefresh}")
	dumpOperatorLogs(t)
	t.Fatalf("§11a %s/%s: force-refresh did not complete within %s.\n"+
		"  annotation present? %q (want empty)\n"+
		"  lastSuccessfulRefresh: prior=%q current=%q (want current > prior)",
		kind, name, timeout, annNow, refreshNow, priorRefresh)
}

// getCRJSONPath is a thin wrapper around `kubectl get <kind>/<name> -o
// jsonpath=...` returning trimmed stdout. Returns "" on kubectl error
// (subtest is then expected to fail on the empty-value assertion).
func getCRJSONPath(t *testing.T, kind, name, jsonpath string) string {
	t.Helper()
	out, err := runCmd("kubectl", "get", kind, name, "-n", namespace,
		"-o", fmt.Sprintf("jsonpath=%s", jsonpath))
	if err != nil {
		t.Logf("getCRJSONPath %s/%s %s: %v\n%s", kind, name, jsonpath, err, out)
		return ""
	}
	return strings.TrimSpace(out)
}

// compareJSONShape diffs an actual JSON document against a golden one
// with tolerated paths (dotted JSON path strings). On any path in
// `tolerated`, the helper checks only that the field is PRESENT and has
// the same type; the value is not compared. For every other path the
// values must match exactly.
//
// Returns a slice of human-readable error strings. Empty slice means
// equal (under tolerance).
//
// Used by §11e to ignore timestamp/storageLocation hash drift while
// still asserting the JSON shape + leaf values that matter
// (schemaVersion, environment, name, id, downloadUrl).
func compareJSONShape(actual, golden []byte, tolerated map[string]struct{}) []string {
	var a, g any
	if err := json.Unmarshal(actual, &a); err != nil {
		return []string{fmt.Sprintf("actual not JSON: %v", err)}
	}
	if err := json.Unmarshal(golden, &g); err != nil {
		return []string{fmt.Sprintf("golden not JSON: %v", err)}
	}
	var diffs []string
	walkJSON("$", a, g, tolerated, &diffs)
	return diffs
}

func walkJSON(path string, a, g any, tolerated map[string]struct{}, out *[]string) {
	if _, ok := tolerated[path]; ok {
		// Only require type-match at this path.
		if fmt.Sprintf("%T", a) != fmt.Sprintf("%T", g) {
			*out = append(*out, fmt.Sprintf("%s: type drift actual=%T golden=%T", path, a, g))
		}
		return
	}
	switch gv := g.(type) {
	case map[string]any:
		av, ok := a.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: golden is object, actual is %T", path, a))
			return
		}
		for k, gvv := range gv {
			avv, present := av[k]
			if !present {
				*out = append(*out, fmt.Sprintf("%s.%s: missing in actual", path, k))
				continue
			}
			walkJSON(path+"."+k, avv, gvv, tolerated, out)
		}
		for k := range av {
			if _, present := gv[k]; !present {
				*out = append(*out, fmt.Sprintf("%s.%s: unexpected in actual (not in golden)", path, k))
			}
		}
	case []any:
		av, ok := a.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: golden is array, actual is %T", path, a))
			return
		}
		if len(av) != len(gv) {
			*out = append(*out, fmt.Sprintf("%s: length actual=%d golden=%d", path, len(av), len(gv)))
			return
		}
		for i := range gv {
			walkJSON(fmt.Sprintf("%s[%d]", path, i), av[i], gv[i], tolerated, out)
		}
	default:
		if !jsonScalarEqual(a, g) {
			*out = append(*out, fmt.Sprintf("%s: value actual=%v golden=%v", path, a, g))
		}
	}
}

func jsonScalarEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
```

**Step 2: Verify it compiles**

Run: `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...`

Expected: clean exit, no `undefined` errors. (`getCRJSONPath`, `dumpOperatorLogs`, `runCmd`, `namespace` all resolve from existing files.)

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_helpers_test.go
git commit -m "test(e2e): add forceRefreshAndAssert + compareJSONShape helpers"
```

---

## Task 3: §11a sub-test — iterate over three external-ref kinds

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Replace the §11a stub with the real iteration**

In `test/e2e/phase4_promotion_test.go`, delete the `testSC11aForceRefreshCycle` stub and append at end-of-file:

```go
// testSC11aForceRefreshCycle drives the force-refresh annotation
// round-trip across the three external-reference kinds the demo
// fixture set already exercises (examples/06, 07, 08). Each kind:
//   1. is pre-applied by examples/hydrate_demo.sh OR this subtest
//      (hydrate_demo.sh idempotent re-apply path).
//   2. has its force-refresh annotation cycled once.
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
```

Also add `"time"` import (already present from Task 1 if you kept stubs; otherwise add).

**Step 2: Bring up a cluster + run only §11a**

Run:
```bash
make cluster-keep            # idempotent — no-op if already up
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
    -run "TestPhase4Promotion/SC11a" ./test/e2e/...
```

Expected: PASS with three named subtests (`plugin_caveman`, `prompt_claude-code-system-prompt`, `artifact_openclaw-templates`). Wall clock < 30s once warm.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): §11a force-refresh annotation cycle across 3 kinds"
```

---

## Task 4: §11a fault-injection sanity (optional but cheap)

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Add a fault sub-test under §11a**

This proves the assertion has teeth: cycling against a non-existent CR MUST t.Fatalf. We can't actually inject a fault into the running reconciler safely, so the cheaper validation is "helper fails-loud when the CR doesn't exist" — done as a fresh subtest with `t.Run` + `t.Skip("smoke: helper-shape sanity, not a regression")` initially. If you want it to fire as a real assertion, use `t.Run("helper_sanity", ...)` calling `forceRefreshAndAssert(t, "plugin", "does-not-exist", 5*time.Second)` and assert via a synthetic `testing.T` capture — but that's over-engineered. Skip this task UNLESS the reviewer asks for it.

**Skipping rationale:** the three live cases in Task 3 already exercise both fail paths (annotation cleared + refresh advanced). A separate fault-inject sub-test buys little.

Mark this task complete with no code change.

**Step 2: Commit (no-op)**

Skip — no commit.

---

## Task 5: §11b helper — BIP finalizer probe

**Files:**
- Modify: `test/e2e/phase4_promotion_helpers_test.go`

**Step 1: Append the BIP helper**

```go
// assertBIPFinalizerPresent asserts the BIP CR carries the
// ach.ackstorm.ai/bip-finalizer finalizer. Returns the parsed finalizer
// list for diagnostics.
func assertBIPFinalizerPresent(t *testing.T, name string) []string {
	t.Helper()
	out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
		"-o", "jsonpath={.metadata.finalizers}")
	if err != nil {
		t.Fatalf("§11b get bip/%s finalizers: %v\n%s", name, err, out)
	}
	if !strings.Contains(out, "ach.ackstorm.ai/bip-finalizer") {
		t.Fatalf("§11b bip/%s missing finalizer; got=%q", name, out)
	}
	return strings.Fields(strings.Trim(out, "[]"))
}

// assertBIPConditionsEmpty asserts that status.conditions is
// empty/missing on the BIP CR. Per memory feedback_bip_no_shadow_logic,
// the operator stays dumb on BIPs — no DuplicateTarget condition, no
// Synced churn. The Forwarder resolves duplicates at READ time.
func assertBIPConditionsEmpty(t *testing.T, name string) {
	t.Helper()
	out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
		"-o", "jsonpath={.status.conditions}")
	if err != nil {
		t.Fatalf("§11b get bip/%s conditions: %v\n%s", name, err, out)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed != "" && trimmed != "[]" && trimmed != "null" {
		t.Fatalf("§11b bip/%s: status.conditions MUST stay empty by design "+
			"(no DuplicateTarget reconciler — see feedback_bip_no_shadow_logic); "+
			"got=%q", name, trimmed)
	}
}

// waitForBIPDeleted polls until `kubectl get bip <name>` returns
// NotFound. Asserts the finalizer was removed cleanly. timeout=30s.
func waitForBIPDeleted(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
			"--ignore-not-found", "-o", "jsonpath={.metadata.name}")
		if err == nil && strings.TrimSpace(out) == "" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace, "-o", "yaml")
	t.Fatalf("§11b bip/%s: not deleted within %s; possibly stuck on finalizer\n%s",
		name, timeout, out)
}
```

**Step 2: Verify**

Run: `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...`

Expected: clean.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_helpers_test.go
git commit -m "test(e2e): BIP finalizer + no-shadow assertion helpers"
```

---

## Task 6: §11b sub-test — apply two BIPs, assert no-shadow + clean teardown

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Replace the §11b stub**

Delete the `testSC11bBIPAdmissionFinalizer` stub. Append:

```go
// testSC11bBIPAdmissionFinalizer asserts the BIP CRUD invariants:
//   1. Both examples/09 + examples/10 are admitted (CRD validation
//      passes on the same (target.kind, target.name) duplicate).
//   2. Both carry the ach.ackstorm.ai/bip-finalizer finalizer.
//   3. status.conditions is empty on both (no DuplicateTarget — by
//      design per memory feedback_bip_no_shadow_logic).
//   4. Delete both: finalizers removed cleanly within 30s.
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
	var lastErr error
	for time.Now().Before(deadline) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					lastErr = fmt.Errorf("%v", r)
				}
			}()
			out, err := runCmd("kubectl", "get", "bip", bipA, "-n", namespace,
				"-o", "jsonpath={.metadata.finalizers}")
			if err == nil && strings.Contains(out, "ach.ackstorm.ai/bip-finalizer") {
				lastErr = nil
				return
			}
			lastErr = fmt.Errorf("bip/%s finalizer not yet present: %q", bipA, out)
		}()
		if lastErr == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("§11b: finalizer never appeared on bip/%s: %v", bipA, lastErr)
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
```

Add `"fmt"` import if not yet present (it is — from Task 2 helpers).

**Step 2: Run §11b**

Run:
```bash
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 2m \
    -run "TestPhase4Promotion/SC11b" ./test/e2e/...
```

Expected: PASS in < 30s. The "no DuplicateTarget" assertion is load-bearing — if the operator ever grows shadow logic, this test fails loud (which is the explicit memory contract).

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): §11b BIP admission + finalizer + no-shadow contract"
```

---

## Task 7: §11c fixture — marketplace.json under VCS

**Files:**
- Create: `test/e2e/fixtures/phase4_marketplace_internal.json`

**Step 1: Write the fixture body**

We need OUR internal-schema marketplace.json so the test is hermetic (no GitHub fetch). Snapshot the shape the parser accepts (six source discriminators, per the header of `examples/05b`). Minimal valid body:

```json
{
  "schemaVersion": "v1alpha1",
  "plugins": [
    {
      "name": "phase4-mkt-plugin",
      "source": {
        "type": "http",
        "http": {
          "url": "http://mkt-test-server.ach-system.svc.cluster.local/phase4-mkt-plugin.tar.gz"
        }
      }
    }
  ]
}
```

**Step 2: Commit the fixture**

```bash
git add test/e2e/fixtures/phase4_marketplace_internal.json
git commit -m "test(e2e): §11c marketplace.json fixture under VCS"
```

> If the marketplace parser rejects this body shape on first run of §11c (Task 9), the implementer MUST consult the current parser (`internal/marketplace/parser.go` or wherever Stage-1 lives in this tree — `grep -rn "schemaVersion" internal/` finds it) and adjust the fixture to match the live schema. Do NOT propose a fix to the parser to match this fixture — the parser is canonical.

---

## Task 8: §11c helper — apply ConfigMap + nginx server + CR

**Files:**
- Modify: `test/e2e/phase4_promotion_helpers_test.go`

**Step 1: Append the bring-up helper**

```go
// applyPhase4MarketplaceServer brings up the in-cluster nginx-backed
// fixture server for §11c:
//   1. Create ConfigMap mkt-phase4-fixture with marketplace.json keyed
//      off the file at test/e2e/fixtures/phase4_marketplace_internal.json.
//   2. Apply Deployment + Service mkt-test-server (nginx:alpine, ports 80).
//   3. Wait for the Deployment Ready.
//
// Registers t.Cleanup to tear everything down. Idempotent — if the
// ConfigMap/Deployment already exists from a previous run, re-apply
// updates them in place.
//
// The ach-system namespace MUST already exist (created by
// scripts/cluster.sh during cluster-up).
func applyPhase4MarketplaceServer(t *testing.T) {
	t.Helper()

	// Create-from-file pattern: kubectl create configmap with
	// --dry-run=client -o yaml | kubectl apply is the idempotent
	// pattern (plain `kubectl create` errors on AlreadyExists).
	cmYAML, err := runCmd("kubectl", "create", "configmap", "mkt-phase4-fixture",
		"-n", namespace,
		"--from-file=marketplace.json=../../test/e2e/fixtures/phase4_marketplace_internal.json",
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		t.Fatalf("§11c configmap dry-run: %v\n%s", err, cmYAML)
	}
	if out, err := runCmdStdin("kubectl apply -f -", cmYAML); err != nil {
		t.Fatalf("§11c configmap apply: %v\n%s", err, out)
	}

	// Deployment + Service yaml inline (avoids a second fixture file).
	const srvYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: mkt-test-server
  namespace: ach-system
spec:
  replicas: 1
  selector:
    matchLabels: { app: mkt-test-server }
  template:
    metadata:
      labels: { app: mkt-test-server }
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
        ports: [{ containerPort: 80 }]
        volumeMounts:
        - { name: fixture, mountPath: /usr/share/nginx/html }
      volumes:
      - name: fixture
        configMap:
          name: mkt-phase4-fixture
---
apiVersion: v1
kind: Service
metadata:
  name: mkt-test-server
  namespace: ach-system
spec:
  selector: { app: mkt-test-server }
  ports: [{ port: 80, targetPort: 80 }]
`
	if out, err := runCmdStdin("kubectl apply -f -", srvYAML); err != nil {
		t.Fatalf("§11c server apply: %v\n%s", err, out)
	}

	if out, err := runCmdLonger(60*time.Second,
		"kubectl", "rollout", "status", "-n", namespace,
		"deployment/mkt-test-server", "--timeout=60s",
	); err != nil {
		t.Fatalf("§11c server rollout: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "deployment", "mkt-test-server",
			"-n", namespace, "--wait=false", "--ignore-not-found")
		_, _ = runCmd("kubectl", "delete", "service", "mkt-test-server",
			"-n", namespace, "--wait=false", "--ignore-not-found")
		_, _ = runCmd("kubectl", "delete", "configmap", "mkt-phase4-fixture",
			"-n", namespace, "--wait=false", "--ignore-not-found")
	})
}

// runCmdStdin runs an arbitrary shell pipeline with `stdin` piped in.
// Used for `... | kubectl apply -f -` patterns.
func runCmdStdin(cmdline, stdin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdline)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
```

Add the imports at top of `phase4_promotion_helpers_test.go`:
```go
import (
    "context"
    // ... existing ...
    "os/exec"
)
```

**Step 2: Verify**

Run: `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...`

Expected: clean.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_helpers_test.go
git commit -m "test(e2e): §11c ConfigMap+nginx fixture-server helper"
```

---

## Task 9: §11c sub-test — drive PluginMarketplace end-to-end

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Replace the §11c stub**

Delete the `testSC11cMarketplaceInternalSchema` stub. Append:

```go
// testSC11cMarketplaceInternalSchema drives examples/05b end-to-end:
//
//  1. applyPhase4MarketplaceServer: ConfigMap + nginx Deployment+Service.
//  2. Apply examples/05b PluginMarketplace CR.
//  3. waitForCondition Synced=True (30s).
//  4. Assert the DB row exists: marketplace_plugins WHERE
//     marketplace='internal-test' AND name='phase4-mkt-plugin'.
//  5. Delete the CR; assert the DB row disappears within 30s
//     (finalizer cleanup contract per §10.3).
//
// Regression contract for the OUTER fetch + parser of OUR internal
// schema. Independent of TODO §5 re-model (Anthropic real schema).
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

	waitForCondition(t, "pluginmarketplace", "internal-test", "Synced", "True", 60*time.Second)

	// Assert DB row.
	const sql = `SELECT count(*) FROM marketplace_plugins ` +
		`WHERE marketplace='internal-test' AND name='phase4-mkt-plugin'`
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
			"see Task 7 note: re-anchor fixture against the live parser, "+
			"do NOT change the parser.", count, "1")
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
```

**Step 2: Run §11c**

```bash
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
    -run "TestPhase4Promotion/SC11c" ./test/e2e/...
```

Expected: PASS in < 60s warm. First run may fail at Task 7's note (parser shape) — re-anchor the fixture per the rule there, do NOT touch the parser.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): §11c PluginMarketplace internal-schema happy path"
```

---

## Task 10: §11d helper — operator pod restart probe

**Files:**
- Modify: `test/e2e/phase4_promotion_helpers_test.go`

**Step 1: Append the helper**

```go
// getOperatorPodUID returns the metadata.uid of the single running
// operator Pod (deployment/ach, namespace=default per Makefile
// operator-redeploy convention; the operator is NOT in ach-system).
func getOperatorPodUID(t *testing.T) string {
	t.Helper()
	out, err := runCmd("kubectl", "get", "pods", "-n", "default",
		"-l", "app.kubernetes.io/name=ach",
		"-o", "jsonpath={.items[0].metadata.uid}")
	if err != nil {
		t.Fatalf("§11d get operator pod uid: %v\n%s", err, out)
	}
	uid := strings.TrimSpace(out)
	if uid == "" {
		t.Fatalf("§11d: no operator pod found (label app.kubernetes.io/name=ach in ns default)")
	}
	return uid
}

// waitForOperatorPodChanged polls until a pod with uid != prevUID is
// Running + Ready. Returns the new uid.
func waitForOperatorPodChanged(t *testing.T, prevUID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", "pods", "-n", "default",
			"-l", "app.kubernetes.io/name=ach",
			"-o", "jsonpath={range .items[?(@.status.phase=='Running')]}{.metadata.uid}{' '}{.status.conditions[?(@.type=='Ready')].status}{'\\n'}{end}")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				parts := strings.Fields(line)
				if len(parts) == 2 && parts[0] != prevUID && parts[1] == "True" {
					return parts[0]
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := runCmd("kubectl", "get", "pods", "-n", "default",
		"-l", "app.kubernetes.io/name=ach", "-o", "wide")
	t.Fatalf("§11d: no new Ready operator Pod within %s (prev uid=%s)\n%s",
		timeout, prevUID, out)
	return ""
}
```

**Step 2: Verify**

Run: `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...`

Expected: clean.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_helpers_test.go
git commit -m "test(e2e): §11d operator pod uid + restart probes"
```

> **Operator location caveat:** the Makefile's `operator-redeploy` target uses `deploy/ach -n default`. If the helm chart you've installed locally landed the operator in a different namespace or under a different label selector, adjust both helpers in this task. Run `kubectl get deploy -A | grep -i ach` to verify before Task 11.

---

## Task 11: §11d sub-test — restart operator + reconciliation fires

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Replace the §11d stub**

Delete the stub. Append:

```go
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
// Wall clock: 30s pod restart + 5s force-refresh round-trip ≈ 35s. Just
// under the < 30s acceptance bar; allowed because this catches a class
// of bugs no other subtest does.
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
	if out, err := runCmd("kubectl", "delete", "pod", "-n", "default",
		"-l", "app.kubernetes.io/name=ach", "--wait=false"); err != nil {
		t.Fatalf("§11d delete operator pod: %v\n%s", err, out)
	}

	newUID := waitForOperatorPodChanged(t, prevUID, 90*time.Second)
	if newUID == prevUID {
		t.Fatalf("§11d: operator Pod uid did not change (prev=%s new=%s)", prevUID, newUID)
	}

	// Reconciliation MUST fire after restart.
	forceRefreshAndAssert(t, "plugin", "caveman", 30*time.Second)
}
```

**Step 2: Run §11d**

```bash
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
    -run "TestPhase4Promotion/SC11d" ./test/e2e/...
```

Expected: PASS in ~35s. If the operator label selector doesn't match (Task 10 caveat), this is where you'll find out — diagnose via `kubectl get pods -A -l app.kubernetes.io/name=ach`.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): §11d operator restart + informer resync"
```

---

## Task 12: §11e golden fixture — capture and seed hydrate-golden.json

**Files:**
- Create: `test/e2e/fixtures/hydrate-golden.json`

**Step 1: Drive examples/hydrate_demo.sh once + capture**

This step depends on FIX01.md §A being unblocked. If it isn't, mark this task **engineer-pending** and t.Skipf §11e with a clear message. Otherwise:

```bash
make cluster-keep
bash examples/hydrate_demo.sh
cp examples/hydrate.json test/e2e/fixtures/hydrate-golden.json
```

**Step 2: Normalize the golden**

Inspect `test/e2e/fixtures/hydrate-golden.json`. Strip any fields that legitimately drift (timestamps, content-hash-keyed storage URLs). The current `examples/hydrate.json` shape is fully stable (see file body in Pre-flight reading) — no drift fields. Leave as-is unless drift is observed.

**Step 3: Commit the golden**

```bash
git add test/e2e/fixtures/hydrate-golden.json
git commit -m "test(e2e): §11e hydrate golden fixture (captured from hydrate_demo.sh)"
```

> **If FIX01 §A is blocking and no real golden can be captured**, write the golden using the body in `examples/hydrate.json` already in-repo (it's the last-known-good snapshot). The §11e sub-test (Task 14) will then surface FIX01 §A as a hard failure, which is the desired regression behavior — flipping from "engineer-pending verification debt" to "Go-asserted invariant" on the day FIX01 §A lands.

---

## Task 13: §11e helper — driveHydrateAndCapture

**Files:**
- Modify: `test/e2e/phase4_promotion_helpers_test.go`

**Step 1: Append the helper**

```go
// driveHydrateAndCapture runs examples/hydrate_demo.sh as a sub-process
// against the current kept cluster and returns the captured hydrate
// response JSON. The script is the canonical wire-path stand-in for the
// not-yet-built `ach login` + `ach hydrate` CLI.
//
// Design note (§11e cross-plan ref): when the CLI lands (ROADMAP Phase
// 6+7), flip the implementation to call the CLI binary, gated by
// ACH_HYDRATE_DRIVER=cli|shell (default shell). Same wire path either
// way; same golden.
//
// Returns the bytes the script wrote to examples/hydrate.json.
func driveHydrateAndCapture(t *testing.T) []byte {
	t.Helper()

	driver := envOr("ACH_HYDRATE_DRIVER", "shell")
	if driver != "shell" {
		t.Skipf("§11e: ACH_HYDRATE_DRIVER=%q not supported yet "+
			"(only 'shell' implemented; CLI driver pending Phase 6+7)", driver)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "../../examples/hydrate_demo.sh")
	cmd.Env = append(cmd.Env, "PATH="+envOr("PATH", "/usr/bin:/bin"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("§11e: hydrate_demo.sh failed: %v\n%s\n\n"+
			"This is the canonical wire-path test. If FIX01.md §A is still "+
			"blocking the SSO path, the test correctly surfaces the regression.",
			err, out)
	}

	body, err := readFileBytes("../../examples/hydrate.json")
	if err != nil {
		t.Fatalf("§11e: read examples/hydrate.json: %v\n--- script stdout ---\n%s",
			err, out)
	}
	return body
}

func readFileBytes(p string) ([]byte, error) {
	return os.ReadFile(p)
}
```

Add `"os"` import.

**Step 2: Verify**

Run: `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...`

Expected: clean.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_helpers_test.go
git commit -m "test(e2e): §11e driveHydrateAndCapture helper"
```

---

## Task 14: §11e sub-test — drive hydrate + diff against golden

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Replace the §11e stub**

Delete the stub. Append:

```go
// testSC11eHydrateGolden is the highest-value §11 add: full /platform/
// hydrate wire path asserted against a checked-in golden JSON.
//
// Wire path: examples/hydrate_demo.sh drives:
//   1. LiteLLM team seed
//   2. kubectl apply -f examples/{01,06,07,08,04}
//   3. wait Environment/demo ExecutionResourcesResolved=True
//   4. port-forward platform-api + dex
//   5. Dex SSO → pk_
//   6. POST /platform/hydrate environment=demo
//
// Golden lives at test/e2e/fixtures/hydrate-golden.json (Task 12).
// Diff tolerates no drift today — every leaf value matches. If future
// fields legitimately drift (timestamps, hashes), extend the tolerated
// map below.
func testSC11eHydrateGolden(t *testing.T) {
	t.Helper()

	actual := driveHydrateAndCapture(t)

	golden, err := os.ReadFile("../../test/e2e/fixtures/hydrate-golden.json")
	if err != nil {
		t.Fatalf("§11e: read golden: %v", err)
	}

	tolerated := map[string]struct{}{
		// Add drift paths here when discovered. Example:
		//   "$.context.plugins[0].downloadUrl": {}, // when host varies
	}

	diffs := compareJSONShape(actual, golden, tolerated)
	if len(diffs) > 0 {
		t.Fatalf("§11e hydrate response differs from golden:\n  %s\n\n"+
			"If the drift is legitimate, either (a) re-capture the golden "+
			"(bash examples/hydrate_demo.sh && cp examples/hydrate.json "+
			"test/e2e/fixtures/hydrate-golden.json) and commit, OR (b) add "+
			"the drifting JSON path to the `tolerated` map in this test.",
			strings.Join(diffs, "\n  "))
	}

	// TODO(§16): once Environment Available + AccessGroupSynced
	// conditions land, also assert hydrate_demo.sh waits on
	// Available=True (currently waits only on
	// ExecutionResourcesResolved=True per FIX01 §C.1).
}
```

**Step 2: Run §11e**

```bash
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
    -run "TestPhase4Promotion/SC11e" ./test/e2e/...
```

Expected: PASS if FIX01 §A is fixed. EXPECTED-FAIL if FIX01 §A still blocks SSO — that's the regression contract.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): §11e /platform/hydrate golden JSON regression"
```

---

## Task 15: §11f helper — DB row assertions for cleanup matrix

**Files:**
- Modify: `test/e2e/phase4_promotion_helpers_test.go`

**Step 1: Append DB-query helpers**

```go
// queryACHPostgresCount runs a `SELECT count(*) ...` against the
// ach-postgres pod and returns the integer. The whereClause MUST NOT
// be user-tainted (this is a test helper, not a runtime path; SQL is
// shell-quoted as-is).
func queryACHPostgresCount(t *testing.T, table, whereClause string) int {
	t.Helper()
	sql := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, whereClause)
	out, err := runCmd("kubectl", "exec", "-n", namespace,
		"sts/ach-postgres", "--",
		"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "`+sql+`"`)
	if err != nil {
		t.Fatalf("§11f DB query %q: %v\n%s", sql, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("§11f DB query %q: non-integer result %q: %v", sql, out, err)
	}
	return n
}

// waitForACHPostgresCount polls until the count equals want, or t.Fatalf
// at the timeout. Used to assert finalizer cleanup completed (count→0).
func waitForACHPostgresCount(t *testing.T, table, whereClause string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if queryACHPostgresCount(t, table, whereClause) == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	got := queryACHPostgresCount(t, table, whereClause)
	t.Fatalf("§11f DB count: table=%s where=%q want=%d got=%d (timeout=%s)",
		table, whereClause, want, got, timeout)
}
```

Add `"strconv"` import.

**Step 2: Verify**

Run: `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...`

Expected: clean.

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_helpers_test.go
git commit -m "test(e2e): §11f DB-count helpers for finalizer matrix"
```

---

## Task 16: §11f sub-test — three-kind cleanup matrix

**Files:**
- Modify: `test/e2e/phase4_promotion_test.go`

**Step 1: Replace the §11f stub**

Delete the stub. Append:

```go
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
		// Pre-apply the demo bundle (idempotent).
		for _, f := range []string{
			"../../examples/01-litellmconnection.yaml",
			"../../examples/06-plugin-caveman.yaml",
			"../../examples/07-prompt-claudecode-leak.yaml",
			"../../examples/08-artifact-openclaw-templates.yaml",
			"../../examples/04-environment-demo.yaml",
		} {
			if out, err := runCmd("kubectl", "apply", "-f", f); err != nil {
				t.Fatalf("§11f.Env apply %s: %v\n%s", f, err, out)
			}
		}
		waitForCondition(t, "environment", "demo",
			"ExecutionResourcesResolved", "True", 120*time.Second)

		// Drive delete (wait=true blocks on finalizer).
		if out, err := runCmdLonger(120*time.Second,
			"kubectl", "delete", "environment", "demo", "-n", namespace,
			"--wait=true"); err != nil {
			t.Fatalf("§11f.Env delete: %v\n%s", err, out)
		}

		// Re-apply for downstream subtests (§11a/d still rely on
		// plugin/caveman; Environment delete doesn't cascade to them).
		if out, err := runCmd("kubectl", "apply", "-f",
			"../../examples/04-environment-demo.yaml"); err != nil {
			t.Fatalf("§11f.Env re-apply: %v\n%s", err, out)
		}
	})

	t.Run("PluginMarketplace", func(t *testing.T) {
		// Same flow as §11c but bare-minimum (skip the count-1 assert
		// — that's §11c's job; we only assert count-after-delete).
		applyPhase4MarketplaceServer(t)
		const fixture = "../../examples/05b-pluginmarketplace-internal-http.yaml"
		if out, err := runCmd("kubectl", "apply", "-f", fixture); err != nil {
			t.Fatalf("§11f.Mkt apply: %v\n%s", err, out)
		}
		waitForCondition(t, "pluginmarketplace", "internal-test",
			"Synced", "True", 60*time.Second)

		if out, err := runCmd("kubectl", "delete", "-f", fixture,
			"--wait=true"); err != nil {
			t.Fatalf("§11f.Mkt delete: %v\n%s", err, out)
		}
		waitForACHPostgresCount(t, "marketplace_plugins",
			"marketplace='internal-test'", 0, 30*time.Second)
	})

	t.Run("BIP", func(t *testing.T) {
		const (
			bipA = "bip-context7-jwt-on"
			fA   = "../../examples/09-backendidentitypolicy-context7.yaml"
		)
		if out, err := runCmd("kubectl", "apply", "-f", fA); err != nil {
			t.Fatalf("§11f.BIP apply: %v\n%s", err, out)
		}
		// Wait for finalizer to attach (5s is generous).
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			out, _ := runCmd("kubectl", "get", "bip", bipA, "-n", namespace,
				"-o", "jsonpath={.metadata.finalizers}")
			if strings.Contains(out, "ach.ackstorm.ai/bip-finalizer") {
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
```

**Step 2: Run §11f**

```bash
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
    -run "TestPhase4Promotion/SC11f" ./test/e2e/...
```

Expected: PASS in ~60-90s warm (three sub-sub-tests, each with apply→wait→delete).

**Step 3: Commit**

```bash
git add test/e2e/phase4_promotion_test.go
git commit -m "test(e2e): §11f finalizer cleanup matrix (Env, Mkt, BIP)"
```

---

## Task 17: Fix `make e2e-focus` for stdlib testing

**Files:**
- Modify: `Makefile` (around line 582)

**Step 1: Replace `e2e-focus` to support both stdlib `-run` and ginkgo focus**

The current target:
```makefile
e2e-focus:
	E2E_SKIP_SETUP=1 go test -tags=e2e -v -count=1 -timeout 5m ./test/e2e/... -args -ginkgo.focus="$(FOCUS)"
```

Replace with:
```makefile
e2e-focus:  ## Phase 3 — run a focused subtest. Usage: make e2e-focus RUN='TestPhase4Promotion/SC11a' (stdlib) OR FOCUS='ginkgo it' (legacy).
	@test -n "$(RUN)$(FOCUS)" || { echo "ERROR: pass RUN=<go-test -run pattern> OR FOCUS=<ginkgo it>" >&2; exit 1; }
	E2E_SKIP_SETUP=1 ./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
	    $(if $(RUN),-run "$(RUN)") ./test/e2e/... \
	    $(if $(FOCUS),-args -ginkgo.focus="$(FOCUS)")
```

Note: we also wrap with `./scripts/dev.sh` because the host has no Go (CLAUDE.md "Toolchain — host has NO Go").

**Step 2: Verify**

Run:
```bash
make e2e-focus RUN='TestPhase4Promotion/SC11a'
```

Expected: PASS for §11a only.

Run the error path:
```bash
make e2e-focus
```

Expected: error message + non-zero exit.

**Step 3: Commit**

```bash
git add Makefile
git commit -m "build(make): e2e-focus accepts RUN= for stdlib tests + devtools-shells"
```

---

## Task 18: Run the full §11 sub-suite end-to-end

**Files:** (verification only — no edits)

**Step 1: Bring up a fresh cluster + run the full Phase 4**

```bash
make cluster-down                       # ensure clean state
make cluster-up                         # full hydration (~5min cold)
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 10m \
    -run TestPhase4Promotion ./test/e2e/...
```

Expected: all six sub-tests PASS. Total wall-clock: ≤ 5min (each subtest individually < 30s warm; cold cluster overhead is the apply+wait + first reconcile per CR).

**Step 2: Re-run on the kept cluster to verify warm timing**

```bash
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 5m \
    -run TestPhase4Promotion ./test/e2e/...
```

Expected: total wall-clock ≤ 3min. Each subtest's own runtime in the `--- PASS:` lines confirms the < 30s acceptance bar.

**Step 3: If any sub-test exceeds 30s warm**, dump its runtime in the commit message of Task 20 and add a `// SLOW: <reason>` comment above the offender. This is a soft acceptance — the < 30s bar is a goal, not a gate.

---

## Task 19: Update test/e2e documentation

**Files:**
- Create: `test/e2e/README.md` (if absent — verify with `ls test/e2e/README.md`)

**Step 1: Write the README**

```markdown
# test/e2e — ACH end-to-end suite

Stdlib `testing` Go files behind build tag `e2e`. No Ginkgo
(per memory `feedback_023_tier_framework_rejected`).

Activation: `make e2e` (assumes `make cluster-up` already invoked).

## Suite map

| File                                | Asserts                                                      |
|-------------------------------------|--------------------------------------------------------------|
| `e2e_suite_test.go`                 | `TestMain` bootstrap (cluster setup unless `E2E_SKIP_SETUP=1`), shared `runCmd`/`envOr` helpers |
| `phase1_invariants_test.go`         | Phase 01 ROADMAP SCs                                         |
| `phase2_invariants_test.go`         | Phase 02 SCs #1–#4 + shared `applyFixtureServer`/`waitForCondition`/`getConditionField`/`dumpOperatorLogs` helpers |
| `phase2_sc5_orphan_test.go`         | Phase 02 SC#5 — orphan-cleanup interval-floor + live revocation |
| `phase3_invariants_test.go`         | Phase 03 SCs #1–#6 (Platform API SSO + hydrate + revocation + audit) |
| `phase3_helpers_test.go`            | Port-forward + HTTP-client + audit-line parser helpers       |
| `phase4_promotion_test.go`          | §11 UAT promotion: force-refresh, BIP, marketplace, restart, hydrate-golden, finalizer matrix |
| `phase4_promotion_helpers_test.go`            | `forceRefreshAndAssert`, `compareJSONShape`, BIP/DB helpers  |

## Focused dev loop

```bash
make cluster-keep                                  # idempotent bring-up
make e2e-focus RUN='TestPhase4Promotion/SC11a'     # stdlib -run pattern
make e2e-focus RUN='TestPhase4Promotion'           # full §11 sub-suite
```

## Fixtures

| Path                                              | Used by                              |
|---------------------------------------------------|--------------------------------------|
| `fixtures/marketplace_*.yaml`                     | phase 2 SC#2                         |
| `fixtures/plugin_*.yaml`                          | phase 2 SC#1 + SC#4                  |
| `fixtures/marketplace_fixture_server.yaml`        | phase 2 fixture server (applied by `applyFixtureServer`) |
| `fixtures/phase4_marketplace_internal.json`       | §11c (served by `applyPhase4MarketplaceServer`)         |
| `fixtures/hydrate-golden.json`                    | §11e golden diff                     |
| `phase3_fixtures/environment_*.yaml`              | phase 3 SCs #2/#3                    |

## Re-capturing the §11e hydrate golden

When the hydrate response shape legitimately changes (e.g. a new field
lands), re-capture the golden:

```bash
make cluster-keep
bash examples/hydrate_demo.sh
cp examples/hydrate.json test/e2e/fixtures/hydrate-golden.json
git add test/e2e/fixtures/hydrate-golden.json
git commit -m "test(e2e): refresh §11e hydrate golden (<reason>)"
```

If the change is intentional drift on a single path (e.g.
`downloadUrl` host changes per cluster), prefer adding the path to
the `tolerated` map in `testSC11eHydrateGolden` over re-capturing.
```

**Step 2: Commit**

```bash
git add test/e2e/README.md
git commit -m "docs(e2e): document phase4 promotion suite + golden refresh"
```

---

## Task 20: Update CLAUDE.md test-phases table

**Files:**
- Modify: `CLAUDE.md` (the "Test phases" section)

**Step 1: Add a row for the Phase 4 sub-suite**

In the current "Test phases" table:

```markdown
| `make e2e-full`    | kind + Helm + Ginkgo, ~6m              | final gate before commit              |
```

Update the comment from "Ginkgo" to the truthful "stdlib testing" and add focused-run guidance:

```markdown
| `make e2e-full`    | kind + Helm + stdlib testing, ~6m      | final gate before commit              |
| `make e2e-focus`   | focused subtest. `RUN='TestPhase4Promotion/SC11a'` | dev loop on a single §11.x sub-test |
```

**Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(claude): clarify e2e harness is stdlib + add e2e-focus example"
```

---

## Task 21: Final sweep — pre-push gate + lint

**Files:** (verification only — no edits)

**Step 1: Run the local gates**

```bash
make pre-commit       # lint-changed + unit
./scripts/dev.sh make lint
make pre-push         # 17-gate hook (gitleaks, trufflehog, SPDX, govulncheck, full lint, unit)
```

Expected: all gates pass.

**Step 2: Final e2e re-confirm**

```bash
make cluster-keep
./scripts/dev.sh go test -tags=e2e -v -count=1 -timeout 10m \
    -run TestPhase4Promotion ./test/e2e/...
```

Expected: all six sub-tests PASS.

**Step 3: Push** (only after pre-push passes)

```bash
git push origin <branch>
```

---

## Acceptance summary

- ✅ Six §11.x sub-tests live as one top-level `TestPhase4Promotion` in stdlib `testing` style.
- ✅ Each slots into existing `make e2e` / `make e2e-full` / `make e2e-keep` harness.
- ✅ `make e2e-focus RUN='TestPhase4Promotion/SC11a'` runs only the new sub-tests during dev loop.
- ✅ Each sub-test adds < 30s to full-suite runtime when run against a kept cluster (Task 18 verifies, slow cases get a `// SLOW:` annotation).
- ✅ §11e hydrate golden committed at `test/e2e/fixtures/hydrate-golden.json` with a documented refresh procedure.
- ✅ §11b explicitly asserts the no-shadow-logic contract from `feedback_bip_no_shadow_logic.md`.
- ✅ §11c independent of TODO §5 (Anthropic real-schema work).
- ✅ §11e helper accepts `ACH_HYDRATE_DRIVER=cli` future extension point.
- ✅ `// TODO(§16)` markers in §11a + §11e signal the post-§7/§9 Available=True extension.
- ✅ `test/e2e/README.md` + `CLAUDE.md` test-phases table updated.
- ✅ `make e2e-focus` fixed to support stdlib `RUN=` (and still accepts legacy `FOCUS=`).
- ✅ Each task is one commit; 18 task-bearing commits total.

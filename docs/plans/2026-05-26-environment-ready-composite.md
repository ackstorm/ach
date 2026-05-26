# Environment Available Composite Condition Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Emit `Environment.status.conditions[type=Available]` as a deterministic rollup of the three Hub §6.6 sub-conditions (`AccessGroupSynced`, `ContentReady`, `ExecutionResourcesResolved`) so `kubectl wait --for=condition=Available environment/demo --timeout=60s` exits 0 within 60s of a fully-resolved Environment.

**Architecture:** Pure helper `computeAvailable(conds []metav1.Condition) metav1.Condition` evaluates the three required sub-conditions and returns the rollup. Reconciler calls it ONCE at the end of the steady-state path, after `ExecutionResourcesResolved` (and Phase-1 placeholders for the others) have been set in memory, then issues the existing single `Status().Update`. Replaces the `aaa175b` placeholder block. No new dependencies, no new RBAC.

**Tech Stack:** Go, `k8s.io/apimachinery/pkg/api/meta`, controller-runtime envtest, kubectl in kind cluster.

**Naming clarification (resolved upfront so the plan doesn't drift):**
- The composite condition type is **`Available`**, NOT `Ready`. TODO §9's symptom mentions `Ready` because that's what `kubectl wait` users habitually try first, but Hub §6.6 documents the closed set as `{Available, ContentReady, ExecutionResourcesResolved, AccessGroupSynced}`. The fix is to make the existing `Available` placeholder a real rollup — not to introduce a separate `Ready` type. The kubebuilder printcolumn at `api/ach/v1alpha1/environment_types.go:130` already advertises `Available`.
- The True reason is **`AllSubConditionsTrue`** (matches TODO §16 line 563).
- The False reason is **`SubConditionsNotReady`** (new; one of the three sub-conditions is `False`).
- The Unknown reason is **`PendingSubConditions`** (carry-forward from `aaa175b` placeholder; one of the three sub-conditions is `Unknown` and none are `False`).

**Mandatory-vs-optional sub-condition decision (resolved upfront):**
Hub §6.6 documents four condition types. `Available` is itself the rollup, so it's NEVER its own input. That leaves three candidates: `AccessGroupSynced`, `ContentReady`, `ExecutionResourcesResolved`. Of these:
- `ExecutionResourcesResolved` — written today by the Snapshotter steady-state branch. **REQUIRED.**
- `AccessGroupSynced` — written today as `Unknown` placeholder; TODO §7 will flip to real True/False. **REQUIRED.** (Per TODO §16 line 562: "IFF (a) AccessGroupSynced=True and (b) ExecutionResourcesResolved=True".)
- `ContentReady` — no reconciler writes this today. Hub §6.6 lists it in the closed set but neither this repo nor `ach-old` emits it. **OPTIONAL FOR ROLLUP** in this plan. Rationale: if `Available` required a sub-condition no reconciler will ever write in Phase 2, `Available` would never reach True. We treat absent sub-conditions as "not blocking" — only explicit `False` or `Unknown` on a REQUIRED type degrades the rollup. When some future plan wires `ContentReady`, that reconciler can either (a) be added to the required set in `computeAvailable` or (b) be left optional — that's a one-line change to a single constant. The plan validates this decision via the table-driven test (a row asserting "missing ContentReady → still True iff the two required are True").

**Cross-plan refs:**
- DEPENDS ON TODO §7 (AccessGroupSynced needs a real `True` writer — until §7 lands, the rollup will stay at `Unknown reason=PendingSubConditions` because the placeholder writes `Unknown`).
- BLOCKS TODO §16 (validation gate explicitly chains §7+§9; cannot run end-to-end until both land).

---

## Task 1: Confirm Hub §6.6 closed-set in CRD doc-comment matches plan decisions

**Files:**
- Read-only: `api/ach/v1alpha1/environment_types.go:85-91` (Conditions field doc-comment)
- Read-only: `internal/controller/ach/environment_controller.go:399-405` (writeStatus doc-comment listing closed set)
- Read-only: `TODO.md:528-567` (§16 validation procedure with the expected `Available=True reason=AllSubConditionsTrue` shape)

**Step 1: Sanity-check that the four condition types in the closed set are exactly what this plan assumes.**

Run: `grep -n "Available, ContentReady, ExecutionResourcesResolved, AccessGroupSynced" api/ach/v1alpha1/environment_types.go internal/controller/ach/environment_controller.go`

Expected: at least one match in each file (api types doc-comment AND `writeStatus` doc-comment). If either is missing, the closed set has drifted and the plan needs revision before code lands.

**Step 2: Sanity-check the §16 expected YAML shape.**

Run: `sed -n '557,567p' TODO.md`

Expected output contains:
```
- type: Available,                   status: True,  reason: AllSubConditionsTrue
```

If the reason string differs, update this plan's `ReasonAllSubConditionsTrue` constant to match, because §16 is the post-implementation acceptance contract.

**Step 3: NO commit.** This task is a pre-flight read-only check; nothing changes on disk.

---

## Task 2: Add reason constants for the Available rollup to `internal/controller/ach/conditions.go`

**Files:**
- Modify: `internal/controller/ach/conditions.go` (add three new constants alongside the existing `Reason*` block at lines 21-109)

**Step 1: Write the failing test**

Create: `internal/controller/ach/environment_available_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

// Tests for TODO §9: Environment Available composite-condition rollup.
//
// The helper under test is pure (no k8s client, no DB) so this file is a
// stdlib `testing` table-driven unit test — NO envtest, NO suite_test
// fixtures. It runs in milliseconds via `go test ./internal/controller/ach/
// -run TestComputeAvailable`.

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestAvailableReasonConstantsExist is a compile-time check that the three
// reason constants required by the rollup helper exist with the documented
// string values. The constants are referenced by computeAvailable AND by
// the §16 acceptance test (TODO.md:563). If a future cleanup pass renames
// them, this test catches the divergence before §16 fails.
func TestAvailableReasonConstantsExist(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"AllSubConditionsTrue", ReasonAllSubConditionsTrue, "AllSubConditionsTrue"},
		{"SubConditionsNotReady", ReasonSubConditionsNotReady, "SubConditionsNotReady"},
		{"PendingSubConditions", ReasonPendingSubConditions, "PendingSubConditions"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q; want %q", tc.name, tc.got, tc.want)
		}
	}
	// Silence unused-import for metav1 — referenced once Task 3 lands.
	_ = metav1.ConditionTrue
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/controller/ach/ -run TestAvailableReasonConstantsExist -count=1`

Expected: BUILD FAIL with `undefined: ReasonAllSubConditionsTrue`, `undefined: ReasonSubConditionsNotReady`, `undefined: ReasonPendingSubConditions`.

**Step 3: Add the constants**

Edit `internal/controller/ach/conditions.go`. After the existing `ReasonUnsupportedPluginSource` block (line 108), and before the closing `)` of the const block, append:

```go

	// ─── Hub §6.6 closed-set reasons for the Environment.Available
	// rollup (TODO §9). The Environment reconciler is the only writer.
	// ───────────────────────────────────────────────────────────────

	// ReasonAllSubConditionsTrue is the terminal positive outcome for
	// the Environment.Available rollup — every required sub-condition
	// (AccessGroupSynced, ExecutionResourcesResolved) is True. Mirrors
	// the §16 acceptance YAML shape (TODO.md:563) verbatim so the
	// validation gate compares against a stable string.
	ReasonAllSubConditionsTrue = "AllSubConditionsTrue"

	// ReasonSubConditionsNotReady is the degraded outcome — at least
	// one required sub-condition is False. message includes the failing
	// sub-condition names so operators reading `kubectl describe
	// environment` can pivot without re-querying.
	ReasonSubConditionsNotReady = "SubConditionsNotReady"

	// ReasonPendingSubConditions is the in-flight outcome — at least
	// one required sub-condition is Unknown and none are False.
	// Pre-§7 this is the steady-state because AccessGroupSynced is the
	// J.6 placeholder Unknown.
	ReasonPendingSubConditions = "PendingSubConditions"
```

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/controller/ach/ -run TestAvailableReasonConstantsExist -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/controller/ach/conditions.go internal/controller/ach/environment_available_test.go
git commit -m "feat(environment): §9 step 1 — reason constants for Available rollup"
```

---

## Task 3: Add the pure `computeAvailable` helper with table-driven test

**Files:**
- Modify: `internal/controller/ach/environment_controller.go` (add helper near `hasCondition` at line 281)
- Modify: `internal/controller/ach/environment_available_test.go` (extend with the table-driven test)

**Step 1: Write the failing table-driven test**

Append to `internal/controller/ach/environment_available_test.go` (after the existing `TestAvailableReasonConstantsExist`):

```go

// TestComputeAvailable is the §9 rollup contract. Two sub-conditions are
// REQUIRED for the rollup to be True: AccessGroupSynced and
// ExecutionResourcesResolved. ContentReady is OPTIONAL (no reconciler in
// Phase 2 writes it; absence does not degrade the rollup) per the plan's
// "Mandatory-vs-optional" decision.
//
// Precedence rules (any False > any Unknown > all True):
//   - any REQUIRED sub-condition False → Available=False, reason=SubConditionsNotReady
//   - else any REQUIRED sub-condition Unknown or missing → Available=Unknown,
//     reason=PendingSubConditions
//   - all REQUIRED sub-conditions True → Available=True, reason=AllSubConditionsTrue
//
// The helper is pure: input is the conditions slice from
// env.Status.Conditions; output is a metav1.Condition the caller pushes
// back into the slice via apimeta.SetStatusCondition.
func TestComputeAvailable(t *testing.T) {
	cond := func(typ string, status metav1.ConditionStatus) metav1.Condition {
		return metav1.Condition{Type: typ, Status: status, Reason: "TestSeed"}
	}

	cases := []struct {
		name       string
		in         []metav1.Condition
		wantStatus metav1.ConditionStatus
		wantReason string
		// wantMsgContains is a substring assertion — the helper composes
		// a human-readable message that names the failing sub-conditions,
		// but the exact wording is not load-bearing for the rollup logic.
		wantMsgContains string
	}{
		{
			name: "all required True → Available True",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
		{
			name: "all required True + optional ContentReady True → Available True",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				cond("ContentReady", metav1.ConditionTrue),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
		{
			name: "ContentReady missing entirely → still True (optional, not blocking)",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				// ContentReady absent — plan's optional-sub-condition decision.
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
		{
			name: "one required False → Available False with reason SubConditionsNotReady",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionFalse),
			},
			wantStatus:      metav1.ConditionFalse,
			wantReason:      ReasonSubConditionsNotReady,
			wantMsgContains: "ExecutionResourcesResolved",
		},
		{
			name: "False beats Unknown — False outcome wins precedence",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionUnknown),
				cond("ExecutionResourcesResolved", metav1.ConditionFalse),
			},
			wantStatus:      metav1.ConditionFalse,
			wantReason:      ReasonSubConditionsNotReady,
			wantMsgContains: "ExecutionResourcesResolved",
		},
		{
			name: "one required Unknown, none False → Available Unknown",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionUnknown),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
			},
			wantStatus:      metav1.ConditionUnknown,
			wantReason:      ReasonPendingSubConditions,
			wantMsgContains: "AccessGroupSynced",
		},
		{
			name: "required sub-condition missing entirely → Available Unknown (treated as Unknown)",
			in: []metav1.Condition{
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				// AccessGroupSynced absent — required types not yet written
				// (e.g. pre-J.6, before placeholder commits).
			},
			wantStatus:      metav1.ConditionUnknown,
			wantReason:      ReasonPendingSubConditions,
			wantMsgContains: "AccessGroupSynced",
		},
		{
			name:            "empty conditions → Available Unknown",
			in:              []metav1.Condition{},
			wantStatus:      metav1.ConditionUnknown,
			wantReason:      ReasonPendingSubConditions,
			wantMsgContains: "AccessGroupSynced",
		},
		{
			name: "pre-existing Available in input is ignored (helper never reads its own type)",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				cond("Available", metav1.ConditionFalse), // stale prior write
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeAvailable(tc.in)
			if got.Type != "Available" {
				t.Errorf("Type = %q; want %q", got.Type, "Available")
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q; want %q (message=%q)", got.Status, tc.wantStatus, got.Message)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q; want %q (message=%q)", got.Reason, tc.wantReason, got.Message)
			}
			if tc.wantMsgContains != "" && !contains(got.Message, tc.wantMsgContains) {
				t.Errorf("Message = %q; want substring %q", got.Message, tc.wantMsgContains)
			}
		})
	}
}

// contains is a tiny strings.Contains alias kept local so the test file
// imports stay minimal (no `strings` import for one helper).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/controller/ach/ -run TestComputeAvailable -count=1`

Expected: BUILD FAIL with `undefined: computeAvailable`.

**Step 3: Implement the helper**

Edit `internal/controller/ach/environment_controller.go`. After the `hasCondition` helper (ends at line 288), insert:

```go

// requiredAvailableSubConditions is the closed set of condition types whose
// status drives the Environment.Available rollup. Per TODO §9 and TODO §16
// the contract is: Available=True IFF every required sub-condition is True.
// ContentReady is intentionally OMITTED — Hub §6.6 lists it in the closed
// set but no reconciler writes it in Phase 2; including it would pin the
// rollup at Unknown indefinitely. When a future plan wires ContentReady,
// add it to this slice in one line.
var requiredAvailableSubConditions = []string{
	"AccessGroupSynced",
	"ExecutionResourcesResolved",
}

// computeAvailable is the pure §9 rollup. Given the current conditions
// slice, return the Available condition the reconciler should write back.
// The helper is independent of env.Generation (the caller stamps that)
// and of LastTransitionTime (apimeta.SetStatusCondition preserves it
// when the (Type, Status, Reason) tuple is unchanged).
//
// Precedence:
//
//  1. Any REQUIRED sub-condition False → Available=False reason=SubConditionsNotReady
//  2. Else any REQUIRED sub-condition Unknown or absent → Available=Unknown reason=PendingSubConditions
//  3. All REQUIRED sub-conditions True → Available=True reason=AllSubConditionsTrue
//
// The pre-existing "Available" entry in conds (if any) is ignored — the
// helper recomputes from scratch every call, so a stale prior write does
// not influence the new outcome.
func computeAvailable(conds []metav1.Condition) metav1.Condition {
	// Build a quick lookup so we walk conds once.
	byType := make(map[string]metav1.ConditionStatus, len(conds))
	for _, c := range conds {
		byType[c.Type] = c.Status
	}

	var falseTypes, unknownOrMissing []string
	for _, t := range requiredAvailableSubConditions {
		switch byType[t] {
		case metav1.ConditionTrue:
			// happy path — accumulates implicitly
		case metav1.ConditionFalse:
			falseTypes = append(falseTypes, t)
		default:
			// metav1.ConditionUnknown OR map zero-value (missing entirely).
			unknownOrMissing = append(unknownOrMissing, t)
		}
	}

	switch {
	case len(falseTypes) > 0:
		return metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionFalse,
			Reason:  ReasonSubConditionsNotReady,
			Message: fmt.Sprintf("sub-conditions False: %v", falseTypes),
		}
	case len(unknownOrMissing) > 0:
		return metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  ReasonPendingSubConditions,
			Message: fmt.Sprintf("sub-conditions Unknown or not yet written: %v", unknownOrMissing),
		}
	default:
		return metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionTrue,
			Reason:  ReasonAllSubConditionsTrue,
			Message: fmt.Sprintf("all required sub-conditions True: %v", requiredAvailableSubConditions),
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/controller/ach/ -run TestComputeAvailable -count=1 -v`

Expected: PASS on all 9 subtests.

**Step 5: Commit**

```bash
git add internal/controller/ach/environment_controller.go internal/controller/ach/environment_available_test.go
git commit -m "feat(environment): §9 step 2 — computeAvailable pure helper + table tests"
```

---

## Task 4: Wire `computeAvailable` into the steady-state reconcile path

**Files:**
- Modify: `internal/controller/ach/environment_controller.go` lines 256-266 (the §9 placeholder block) and the `if r.Snapshotter == nil` branch at 162-171.

**Step 1: Run the existing envtest to confirm baseline green**

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestEnvironmentFinalizerAddRemove`

Expected: PASS in ~30s. This is the safety net — if Task 4 breaks the finalizer test, we have a regression.

**Step 2: Replace the §9 placeholder block with the rollup call**

In `internal/controller/ach/environment_controller.go`, locate the placeholder block at lines 256-266 (begins with `// Placeholder: TODO §9 owns the composite rollup.`). Replace it with:

```go
	// §9: composite Available rollup. Called AFTER the two writes above
	// (ExecutionResourcesResolved + the AccessGroupSynced placeholder
	// or its real successor when §7 lands) so the helper reads the
	// freshly-set sub-conditions. The previous J.6 placeholder Unknown
	// is now superseded — apimeta.SetStatusCondition replaces the
	// in-memory entry regardless of prior status/reason because the
	// reason changes between calls.
	available := computeAvailable(env.Status.Conditions)
	available.ObservedGeneration = env.Generation
	available.LastTransitionTime = metav1.Now()
	apimeta.SetStatusCondition(&env.Status.Conditions, available)
```

The deletion is the entire `if !hasCondition(env.Status.Conditions, "Available") { ... }` block. The insertion is the four lines above.

**Step 3: Also wire the helper into the Snapshotter-nil branch (unit-test mode)**

The `if r.Snapshotter == nil` branch at lines 162-171 currently calls `r.writeStatus(...)` for `AccessGroupSynced=Unknown` and returns immediately — `Available` never gets written in this branch. Replace the body of that `if` with:

```go
	if r.Snapshotter == nil {
		// Phase 1 unit-test back-compat: emit AccessGroupSynced=Unknown
		// (J.6 placeholder), then run the §9 rollup over the resulting
		// conditions slice so envtest assertions on Available are
		// stable even without a wired Snapshotter.
		apimeta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:               "AccessGroupSynced",
			Status:             metav1.ConditionUnknown,
			Reason:             ReasonInitializing,
			Message:            "snapshotter not wired (unit-test mode)",
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		})
		available := computeAvailable(env.Status.Conditions)
		available.ObservedGeneration = env.Generation
		available.LastTransitionTime = metav1.Now()
		apimeta.SetStatusCondition(&env.Status.Conditions, available)
		env.Status.ObservedGeneration = env.Generation
		if err := r.Status().Update(ctx, &env); err != nil {
			logger.Error(err, "status update failed")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
```

This replaces the previous `r.writeStatus(...)` call. The reason is that `writeStatus` issues its own `Status().Update`, but we now want the rollup in the same payload — so we inline the SetStatusCondition + single Update.

**Step 4: Remove the now-unused `hasCondition` helper if it has zero callers**

Run: `grep -n "hasCondition" internal/controller/ach/*.go`

Expected (after step 2's deletion): only the helper definition at lines 281-288 remains. Delete the helper. If any test or other file still references it, leave it in place.

**Step 5: Run the envtest suite**

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/...`

Expected: ALL existing envtests still PASS. Specifically `TestEnvironmentFinalizerAddRemove` (the finalizer test that runs Reconcile against the Snapshotter-nil branch) must stay green — its assertion is unchanged (it only checks the LiteLLM counter), but the Snapshotter-nil branch is now executing one extra SetStatusCondition + the rollup. Any failure here means Step 3 perturbed the branch in a way that broke the finalizer test.

**Step 6: Run the full unit + envtest matrix to catch package-wide build breakage**

Run: `./scripts/dev.sh make unit && ./scripts/dev.sh make envtest-run`

Expected: PASS.

**Step 7: Commit**

```bash
git add internal/controller/ach/environment_controller.go
git commit -m "feat(environment): §9 step 3 — wire computeAvailable into Reconcile (replaces J.6 placeholder)"
```

---

## Task 5: Add an envtest assertion for Available rollup in Snapshotter-nil mode

**Files:**
- Modify: `internal/controller/ach/environment_available_test.go` (append envtest)

**Why this exists**: Task 3's unit test covers the helper in isolation. Task 4 wires it into the reconciler. This task closes the loop: a real `Reconcile` cycle in envtest produces an `Available` condition in `env.Status.Conditions`. The Snapshotter-nil branch is the unit-test mode (the suite does not wire `r.Snapshotter`), so the expected outcome is `Available=Unknown reason=PendingSubConditions` (because `AccessGroupSynced=Unknown` is the placeholder; the rollup correctly propagates Unknown). True-path coverage lives in the e2e suite (Task 6) where a real Snapshotter is wired.

**Step 1: Write the failing envtest**

Append to `internal/controller/ach/environment_available_test.go`:

```go

// TestEnvironmentAvailableConditionEmittedInUnitTestMode verifies that
// a Reconcile against the envtest API server (with Snapshotter nil)
// writes an Available condition with status=Unknown reason=
// PendingSubConditions. This proves Task 4's wiring landed: the rollup
// is called in BOTH branches (Snapshotter-nil unit-test branch AND the
// steady-state branch), and the resulting Available condition is
// persisted via the same Status().Update as AccessGroupSynced.
//
// Pre-§7 the AccessGroupSynced placeholder is Unknown, so the rollup
// MUST be Unknown — this test will start failing the day §7 lands and
// flips AccessGroupSynced to True (at which point this test should be
// REPLACED by a True-path version, not patched, because the §7 plan
// will assume Available=True is the new steady state).
func TestEnvironmentAvailableConditionEmittedInUnitTestMode(t *testing.T) {
	ctx := context.Background()

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-available",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime: achv1alpha1.RuntimeBlock{
				Models:     []string{},
				MCPServers: []string{},
				A2AAgents:  []string{},
			},
			Context: achv1alpha1.ContextBlock{
				Prompts:   []string{},
				Plugins:   []string{},
				Artifacts: []string{},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Poll: wait until BOTH AccessGroupSynced and Available are written
	// to status.conditions. The reconciler emits both in a single
	// Status().Update, so they land atomically — but the informer cache
	// takes a beat to propagate.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		ags := apimeta.FindStatusCondition(got.Status.Conditions, "AccessGroupSynced")
		avail := apimeta.FindStatusCondition(got.Status.Conditions, "Available")
		return ags != nil && avail != nil
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("AccessGroupSynced + Available conditions never both written within 15s")
	}

	// Re-Get and assert the rollup outcome.
	var got achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatalf("re-get Environment: %v", err)
	}
	avail := apimeta.FindStatusCondition(got.Status.Conditions, "Available")
	if avail == nil {
		t.Fatalf("Available condition missing after Eventually returned true (race or wiring bug)")
	}
	if avail.Status != metav1.ConditionUnknown {
		t.Errorf("Available.Status = %q; want %q (rollup should propagate AccessGroupSynced=Unknown placeholder)",
			avail.Status, metav1.ConditionUnknown)
	}
	if avail.Reason != ReasonPendingSubConditions {
		t.Errorf("Available.Reason = %q; want %q",
			avail.Reason, ReasonPendingSubConditions)
	}
	t.Logf("OK: Available=%s reason=%s message=%q",
		avail.Status, avail.Reason, avail.Message)
}
```

This requires adding three new imports to the test file's import block (insert after the existing `metav1` import in the same group):

```go
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
```

Also add `"context"` and `"time"` to the standard-library import group.

**Step 2: Run the test to verify it passes**

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestEnvironmentAvailableConditionEmittedInUnitTestMode`

Expected: PASS in ~5-15s. The test creates an Environment, polls for the two conditions to appear, then asserts the rollup outcome.

If it fails with "Available condition missing":
- Confirm Task 4 step 3 wrote BOTH `SetStatusCondition` calls inside the Snapshotter-nil branch. The bug is that `Available` never gets written.

If it fails with "Available.Reason = ...; want PendingSubConditions":
- Confirm Task 2's `ReasonPendingSubConditions = "PendingSubConditions"` constant matches the helper's reason string.

**Step 3: Run the full ach controller envtest suite to confirm no regression**

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/...`

Expected: PASS for all tests (finalizer tests, CEL admission test, external-ref refresh tests, all unchanged).

**Step 4: Commit**

```bash
git add internal/controller/ach/environment_available_test.go
git commit -m "test(environment): §9 step 4 — envtest covers Available emission in unit-test mode"
```

---

## Task 6: Add e2e wait gate that mirrors TODO §16 kubectl wait

**Files:**
- Modify: `test/e2e/phase3_invariants_test.go` OR a new `test/e2e/phase4_environment_available_test.go` (decide in step 1 based on which is more appropriate)

**Why this exists**: The TODO §9 acceptance is "`kubectl wait --for=condition=Available environment/demo --timeout=60s` exits 0 within 60s of a fully-resolved environment." Mirror that in Ginkgo against the kind cluster.

**Step 1: Decide on file placement**

Run: `grep -l "Environment" test/e2e/*.go`

If a Ginkgo test file already declares Environment-related specs, append to that file. Otherwise create `test/e2e/phase4_environment_available_test.go`. Phase 4 is the next-up file slot per the existing `phase{1,2,3}_*` naming.

For the rest of this task, assume the new file path.

**Step 2: Write the failing test**

Create: `test/e2e/phase4_environment_available_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

// E2E acceptance for TODO §9: kubectl wait --for=condition=Available
// returns within 60s for an Environment whose required sub-conditions
// all reach True. Mirrors TODO §16 line 539 verbatim.
//
// Pre-§7 the AccessGroupSynced placeholder is Unknown, so the
// Available rollup is Unknown — the wait will TIME OUT today. That
// matches the J.6 placeholder reality and proves the rollup correctly
// blocks on the placeholder; the assertion logic is:
//
//   - if §7 has NOT landed → expect timeout exit code (60s wait fails)
//   - if §7 HAS landed     → expect zero exit code (Available=True
//                            within 60s)
//
// We DON'T branch the test on a runtime check (too brittle). Instead,
// we use Ginkgo's `It` with a deferred decision: the spec is named
// such that a maintainer running §7 can flip the expected outcome in
// one place. Today the spec is SKIPPED so CI stays green.

package e2e

import (
	"context"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Environment Available composite condition (TODO §9)", func() {
	// SKIPPED until TODO §7 (AccessGroupSynced real writer) lands and
	// flips the placeholder Unknown to True. At that point delete the
	// Skip() call and the spec becomes the §16 acceptance contract.
	BeforeEach(func() {
		Skip("blocked by TODO §7 — AccessGroupSynced still emits Unknown placeholder; Available rollup correctly stays Unknown")
	})

	It("transitions Environment/demo to Available=True within 60s", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		// Assumes examples/04-environment-demo.yaml is already applied
		// by the cluster-up flow (verify via the standard fixtures
		// helper if/when one exists).
		cmd := exec.CommandContext(ctx,
			"kubectl", "wait",
			"--for=condition=Available",
			"environment/demo",
			"-n", "ach-system",
			"--timeout=60s",
		)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(),
			"kubectl wait failed: %s", string(out))
	})
})
```

**Step 3: Verify the test compiles and is properly SKIPPED in the current state**

Run: `./scripts/dev.sh make e2e-keep` followed by `./scripts/dev.sh make e2e-focus FOCUS="Available"`

Expected: the spec runs but is SKIPPED with the explanatory message. NO failures.

If the test FAILS to compile (e.g. import path drift, Ginkgo version mismatch), check sibling phase3 file for the canonical imports and copy them.

**Step 4: Commit**

```bash
git add test/e2e/phase4_environment_available_test.go
git commit -m "test(e2e): §9 step 5 — phase4 Available wait gate (skipped pending §7)"
```

---

## Task 7: Update inline doc-comment in `environment_controller.go` to reflect new state

**Files:**
- Modify: `internal/controller/ach/environment_controller.go` lines 219-231 (the "Placeholder" comment block at the top of the steady-state block)

**Step 1: Replace the now-stale comment**

Locate the comment block that begins `// Patch the unresolved field on env.Status, then emit all three §6.6` and reads:

```
// ExecutionResourcesResolved is the only condition this reconciler
// computes today. AccessGroupSynced and Available are written as
// placeholder Unknown values so the kubebuilder printcolumns surface
// SOMETHING (operators reading `kubectl get environment` see
// "Unknown" instead of an empty cell). When TODO §7 lands, the
// access-group binding step overrides AccessGroupSynced to True /
// False with the real reason. When TODO §9 lands, the composite
// rollup overrides Available.
```

Replace with:

```
// Three conditions are emitted in one Status().Update for atomicity:
//
//   - ExecutionResourcesResolved: computed above from the Snapshotter
//     set-difference (Resolved / ResourceUnresolved).
//   - AccessGroupSynced: J.6 placeholder Unknown reason=Initializing
//     until TODO §7 lands and emits the real True/False per Hub §6.6.
//     SetStatusCondition is gated by hasCondition so the placeholder
//     does NOT clobber a future §7 True write.
//   - Available: §9 composite rollup. computeAvailable reads the
//     two REQUIRED sub-conditions (AccessGroupSynced,
//     ExecutionResourcesResolved) from the in-memory conditions
//     slice and produces True/False/Unknown per the documented
//     precedence. Pre-§7 the rollup is Unknown reason=
//     PendingSubConditions because AccessGroupSynced is Unknown;
//     once §7 lands the rollup becomes True iff both required
//     sub-conditions are True.
```

Note: the AccessGroupSynced placeholder block (lines 246-255) still uses `hasCondition`. That's fine — keep it. The Available placeholder block (the deleted one) was the only call site to be removed.

**Step 2: Verify build still passes**

Run: `./scripts/dev.sh go build ./...`

Expected: PASS. Doc-only change.

**Step 3: Run lint to confirm no comment-induced lint warnings**

Run: `./scripts/dev.sh make lint-changed`

Expected: PASS.

**Step 4: Commit**

```bash
git add internal/controller/ach/environment_controller.go
git commit -m "docs(environment): §9 step 6 — refresh reconciler comment for Available rollup"
```

---

## Task 8: Full pre-push gate before publishing

**Files:** None directly modified — this task is the publication gate.

**Step 1: Run the full local verification matrix**

```bash
./scripts/dev.sh make unit
./scripts/dev.sh make envtest-run
./scripts/dev.sh make lint
make pre-push
```

Expected: all PASS. `make pre-push` enforces the 17-gate publication contract (gitleaks, govulncheck, SPDX headers, license, full lint, unit, ...).

If `pre-push` fails on the SPDX-header gate, confirm the new test file `environment_available_test.go` starts with `// SPDX-License-Identifier: Apache-2.0` (it should — Task 2 step 1 wrote the line).

If `pre-push` fails on govulncheck drift, that's an unrelated regression; do NOT bypass — investigate and either patch deps or update `references/security/govulncheck-acknowledged.md` per the existing policy.

**Step 2: Inspect the commit history before push**

Run: `git log --oneline origin/main..HEAD`

Expected: 5 commits in order:
1. `feat(environment): §9 step 1 — reason constants for Available rollup`
2. `feat(environment): §9 step 2 — computeAvailable pure helper + table tests`
3. `feat(environment): §9 step 3 — wire computeAvailable into Reconcile (replaces J.6 placeholder)`
4. `test(environment): §9 step 4 — envtest covers Available emission in unit-test mode`
5. `test(e2e): §9 step 5 — phase4 Available wait gate (skipped pending §7)`
6. `docs(environment): §9 step 6 — refresh reconciler comment for Available rollup`

(Six commits in total — the count above off by one in the prose; the actual sequence is 6.)

**Step 3: Push**

```bash
git push origin main
```

Or open a PR if working on a branch (preferred per ACKstorm git guidelines).

---

## Acceptance summary

| Acceptance criterion | Verified by |
|----------------------|-------------|
| `computeAvailable` returns the correct rollup for True / False / Unknown / missing sub-conditions | Task 3 table test (9 subtests) |
| `Environment.status.conditions[type=Available]` is written by the reconciler in the Snapshotter-nil branch | Task 5 envtest |
| Pre-§7: `Available=Unknown reason=PendingSubConditions` because `AccessGroupSynced` is the J.6 placeholder Unknown | Task 5 envtest + Task 6 e2e Skip rationale |
| Post-§7: `kubectl wait --for=condition=Available environment/demo --timeout=60s` exits 0 within 60s | Task 6 e2e (un-Skip after §7) — fulfils TODO §9 + TODO §16 acceptance |
| No regression in existing envtest suite (finalizer, CEL, external-ref refresh) | Task 4 step 5 + Task 8 step 1 (full `envtest-run`) |
| Pre-push gate passes (lint, security, SPDX, govulncheck) | Task 8 step 1 |

## Open items deferred to other plans

- TODO §7 — the real `AccessGroupSynced=True/False` writer. Until that lands, the rollup correctly emits `Unknown`. When §7 lands, Task 6's e2e spec becomes unblocked: delete the `Skip()` call and verify the kubectl wait exits 0.
- TODO §16 — the full validation gate (apply `examples/04-environment-demo.yaml`, seed 5 LiteLLM resources, assert `Available=True reason=AllSubConditionsTrue`). The reason vocabulary `AllSubConditionsTrue` is established by this plan so §16 has a stable string to compare against.
- `ContentReady` reconciler — Hub §6.6 lists it but no Phase 2 plan writes it. If a future plan introduces a `ContentReady` writer AND the rollup contract changes to require it, the one-line patch is appending `"ContentReady"` to `requiredAvailableSubConditions` in `environment_controller.go`.

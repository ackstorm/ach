# PluginMarketplace Status: Synced Plugin Count + Per-Plugin Detail (issue #53) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `kubectl get pluginmarketplace <name> -o yaml` actually show the synced plugin count + per-plugin detail by fixing a persistence regression, surfacing the count in the `Synced=True` message, and regenerating the api-reference docs.

**Architecture:** The `status.plugins[]` / `status.pluginsCount` field surface, the print column, the CRD schema, and the reconciler code that *computes* the materialized set already exist (commit `2035908`). A later conflict-retry refactor (`c28eeff`, "closes #18") rewrote `markSynced{True,False}` to write a freshly-`Get`-ed CR copy that carries only `Conditions`/`ObservedGeneration`/`LastSuccessfulRefresh` — silently dropping the caller-computed `Plugins`/`PluginsCount` on every write. The fix is to carry those two fields onto the fresh copy. We then append `plugins=<N>` to the success message and regenerate the api-reference markdown (stale since `2035908` never ran `gen-crd-ref-docs`).

**Tech Stack:** Go, controller-runtime (envtest), kubebuilder/controller-gen, crd-ref-docs, Make (devtools container auto-routing).

---

## Background — root cause (read before starting)

`internal/controller/ach/pluginmarketplace_controller.go`:
- The success path (Stage-final, ~line 446-460) computes `successful` (the materialized `[]MarketplacePluginRef`), assigns it to `cr.Status.Plugins` / `cr.Status.PluginsCount`, then calls `r.markSyncedTrue(ctx, &cr, finalMsg, requeue)`.
- `markSyncedTrue` (and `markSyncedFalse`) do `r.Get(ctx, key, &fresh)` into a *new* `fresh` struct, apply only `Conditions`/`ObservedGeneration`/`LastSuccessfulRefresh` onto `fresh`, `r.Status().Update(ctx, &fresh)`, then `cr.Status = fresh.Status`.
- Because `fresh.Status.Plugins`/`PluginsCount` are never set from `cr`, the computed set is discarded on write, and `cr.Status = fresh.Status` clobbers the caller's local copy too. Net effect: the fields are persisted as empty/0 on every reconcile.

This was **proven empirically** with a focused envtest (reproduced below as Task 1's test) that fails on current `main` with `status.pluginsCount = 0, want 2` and passes once the carry is added.

**Scope decisions (kept OUT of this PR, documented in the PR body):**
- Per-plugin `reachable` and `lastSynced` fields on `MarketplacePluginRef` — current model encodes reachability by presence (successful entries are listed; failures land in the `Synced` message's `stage-2:` summary), and `lastSynced` is tracked per-plugin in the `marketplace_plugins` DB row but not surfaced on the CR. Deferred — not required by the acceptance criteria.
- The stale "Phase 1 ships the field surface; Phase 2 fills the reconciler logic" prose in the `PluginMarketplaceStatus` Go doc comment — cosmetic; editing it forces a `gen-manifests` + `helm-sync` cascade. Deferred to keep this PR minimal.

## File Structure

| File | Responsibility | Touched in |
|------|----------------|-----------|
| `internal/controller/ach/pluginmarketplace_envtest_test.go` | New regression test `TestPMR_Stage2_StatusPluginsPopulated` (asserts status fields + message) | Task 1 (add), Task 2 (extend) |
| `internal/controller/ach/pluginmarketplace_controller.go` | Fix `markSynced{True,False}` to carry `Plugins`/`PluginsCount`; append `plugins=<N>` to success message | Task 1, Task 2 |
| `docs/api-reference/ach.ackstorm.ai.md` | Regenerated api-reference (adds `plugins`/`pluginsCount` rows) | Task 3 (generated, do not hand-edit) |

No new files; no new types (the type surface already exists in `api/ach/v1alpha1/pluginmarketplace_types.go`).

## Conventions

- All `make test-*` / `gen-*` targets auto-route into the devtools container — run them bare (no `./scripts/dev.sh` prefix).
- `make test-envtest-pkg` maps `FOCUS=` to `go test -run`. **Do not** add a trailing `$` anchor — it is mangled through the make/container layers and matches zero tests. The bare name `TestPMR_Stage2_StatusPluginsPopulated` is a unique prefix.
- Commit messages: Conventional Commits, imperative subject < 72 chars, end with the `Co-Authored-By` trailer.
- Both target `.go` files already carry the `// SPDX-License-Identifier: Apache-2.0` header — preserve it (do not duplicate).

---

## Task 1: Fix `status.plugins[]` / `pluginsCount` persistence regression (TDD)

**Files:**
- Test: `internal/controller/ach/pluginmarketplace_envtest_test.go` (add one test function)
- Modify: `internal/controller/ach/pluginmarketplace_controller.go` (`markSyncedTrue` and `markSyncedFalse`)

- [ ] **Step 1: Write the failing regression test**

In `internal/controller/ach/pluginmarketplace_envtest_test.go`, insert the following function immediately **after** the closing brace of `TestPMR_Stage2_PartialFailure_StatusMessage` and **before** `func TestPMR_Stage2_UnsupportedNpm(t *testing.T) {`. It reuses the existing helpers (`pmrCR`, `applyMarketplaceCR`, `waitForFinalizer`, `newMarketplaceFakeFactory`, `withFakeGitFetcher`, `mkGitSubdirPlugin`, `mustMarketplaceJSON`, `mustPluginTarGz`, `shaForName`, `drainReconcileUntil`, `syncedCondition`):

```go
// TestPMR_Stage2_StatusPluginsPopulated proves issue #53's acceptance
// criterion #1: a successful multi-plugin reconcile must surface the
// materialized set on status.plugins[] + status.pluginsCount so an
// operator running `kubectl get pluginmarketplace -o yaml` can see what
// the catalog actually resolved to.
//
// REGRESSION GUARD: commit 2035908 ("surface materialized plugins on
// status.plugins[]") wired these fields and persisted them via
// `r.Status().Update(ctx, cr)`. The issue #18 conflict-retry refactor
// (c28eeff) then rewrote markSynced{True,False} to Get a FRESH CR and
// copy ONLY Conditions/ObservedGeneration/LastSuccessfulRefresh onto it
// before Update — silently dropping the caller-set Plugins/PluginsCount
// on every write. On that regressed code this test fails with
// pluginsCount=0 and an empty plugins list.
func TestPMR_Stage2_StatusPluginsPopulated(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s2-status-plugins", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	// Two plugins, BOTH succeed → status.plugins must list both.
	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name:    "m",
		Plugins: []ClaudeCodeMarketplacePlugin{mkGitSubdirPlugin("alpha"), mkGitSubdirPlugin("beta")},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	gitReg := withFakeGitFetcher(t)
	gitReg.register(shaForName("alpha"), &fakeGitFetcher{body: mustPluginTarGz(t, "plugins/alpha"), rev: shaForName("alpha")})
	gitReg.register(shaForName("beta"), &fakeGitFetcher{body: mustPluginTarGz(t, "plugins/beta"), rev: shaForName("beta")})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}

	// Drain until Synced=True lands. In correct code the plugins list is
	// written in the SAME Status().Update as the Synced condition, so once
	// Synced=True is observed the fields are at their final value (the
	// within-interval gate then skips subsequent fetches, freezing status).
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced
	})
	if !ok {
		t.Fatalf("never observed Synced=True")
	}

	// THE ASSERTION issue #53 is about.
	var got achv1alpha1.PluginMarketplace
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatalf("get CR: %v", err)
	}
	if got.Status.PluginsCount != 2 {
		t.Errorf("status.pluginsCount = %d, want 2", got.Status.PluginsCount)
	}
	if len(got.Status.Plugins) != 2 {
		t.Fatalf("len(status.plugins) = %d, want 2 (entries: %+v)", len(got.Status.Plugins), got.Status.Plugins)
	}
	// Entries are sorted by Name → [alpha, beta], each carrying its
	// resolved UpstreamRev.
	wantRev := map[string]string{"alpha": shaForName("alpha"), "beta": shaForName("beta")}
	for _, p := range got.Status.Plugins {
		if wantRev[p.Name] != p.UpstreamRev {
			t.Errorf("status.plugins[%q].upstreamRev = %q, want %q", p.Name, p.UpstreamRev, wantRev[p.Name])
		}
		delete(wantRev, p.Name)
	}
	if len(wantRev) != 0 {
		t.Errorf("status.plugins missing expected entries: %v", wantRev)
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS (proves the regression)**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_Stage2_StatusPluginsPopulated' TIMEOUT=8m
```
Expected: FAIL, with these lines in the output:
```
    pluginmarketplace_envtest_test.go:NNN: status.pluginsCount = 0, want 2
    pluginmarketplace_envtest_test.go:NNN: len(status.plugins) = 0, want 2 (entries: [])
--- FAIL: TestPMR_Stage2_StatusPluginsPopulated
```
(If it does not match `-run`, confirm you used the bare name with no trailing `$`.)

- [ ] **Step 3: Fix `markSyncedTrue` — carry the discovery set onto `fresh`**

In `internal/controller/ach/pluginmarketplace_controller.go`, inside `markSyncedTrue`'s `retry.RetryOnConflict` closure, change:

```go
		applyReconcileConditions(&fresh.Status.Conditions, ReasonSynced, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		fresh.Status.LastSuccessfulRefresh = &now
		if u := r.Status().Update(ctx, &fresh); u != nil {
```

to:

```go
		applyReconcileConditions(&fresh.Status.Conditions, ReasonSynced, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		fresh.Status.LastSuccessfulRefresh = &now
		// Carry the caller-computed discovery set onto the fresh copy —
		// the Reconcile body set these on `cr` and the fresh Get would
		// otherwise drop them (issue #53 regression introduced by c28eeff).
		fresh.Status.Plugins = cr.Status.Plugins
		fresh.Status.PluginsCount = cr.Status.PluginsCount
		if u := r.Status().Update(ctx, &fresh); u != nil {
```

- [ ] **Step 4: Fix `markSyncedFalse` — same carry (loser-branch correctness)**

In the same file, inside `markSyncedFalse`'s `retry.RetryOnConflict` closure, change:

```go
		applyReconcileConditions(&fresh.Status.Conditions, reason, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		if u := r.Status().Update(ctx, &fresh); u != nil {
```

to:

```go
		applyReconcileConditions(&fresh.Status.Conditions, reason, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		// Carry the caller's discovery set (issue #53 regression: c28eeff).
		// The marketplace-loser branch zeroes cr.Status.Plugins before
		// calling this so the stale set is cleared; other failure paths
		// pass through the prior value loaded at the top of Reconcile.
		fresh.Status.Plugins = cr.Status.Plugins
		fresh.Status.PluginsCount = cr.Status.PluginsCount
		if u := r.Status().Update(ctx, &fresh); u != nil {
```

> Note: the `markSyncedFalse` carry is exercised end-to-end only by the DB-backed integration test `TestPMR_NameConflict_AlphabeticalPriority` (the loser branch needs `listOtherMarketplaceCatalogs`, which requires `r.DB`). It is `t.Skip`-ed in the Docker-free envtest run; that is expected. The success-path carry (`markSyncedTrue`) is what Step 1's test proves.

- [ ] **Step 5: Run the test to verify it PASSES**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_Stage2_StatusPluginsPopulated' TIMEOUT=8m
```
Expected:
```
--- PASS: TestPMR_Stage2_StatusPluginsPopulated
PASS
ok  	github.com/ackstorm/ach/internal/controller/ach
```

- [ ] **Step 6: Run the full PMR suite to confirm no collateral breakage**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_' TIMEOUT=8m
```
Expected: all `TestPMR_*` PASS except `TestPMR_Stage3_DeleteSweep` and `TestPMR_NameConflict_AlphabeticalPriority` which SKIP (DB-only). No FAIL.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/ach/pluginmarketplace_controller.go internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "fix(pmr): persist status.plugins[]/pluginsCount through markSynced (#53)

c28eeff (closes #18) rewrote markSynced{True,False} to write a freshly
Get-ed CR copy carrying only Conditions/ObservedGeneration/
LastSuccessfulRefresh, silently dropping the caller-computed
Plugins/PluginsCount set by the Stage-final success path. The fields
were therefore persisted as empty/0 on every reconcile, defeating the
2035908 feature and the Plugins print column. Carry both fields onto the
fresh copy in both helpers; add an envtest regression guard.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Surface the synced plugin count in the `Synced=True` message

The issue asks the count to appear in the condition message (which already carries `transport=<git|rest>`). We append `plugins=<N>` where N is the number of successfully materialized plugins, between the transport label and any partial-failure summary.

**Files:**
- Test: `internal/controller/ach/pluginmarketplace_envtest_test.go` (extend `TestPMR_Stage2_StatusPluginsPopulated`)
- Modify: `internal/controller/ach/pluginmarketplace_controller.go` (Stage-final message composition)

- [ ] **Step 1: Add the failing message assertion to the existing test**

In `internal/controller/ach/pluginmarketplace_envtest_test.go`, inside `TestPMR_Stage2_StatusPluginsPopulated`, append this block at the **end** of the function (after the `wantRev` loop, before the closing `}`):

```go
	// Issue #53 acceptance #2: the synced count is surfaced in the
	// Synced=True message alongside transport=.
	if c := syncedCondition(&got); c == nil || !strings.Contains(c.Message, "plugins=2") {
		var m string
		if c != nil {
			m = c.Message
		}
		t.Errorf("Synced message = %q, want it to contain %q", m, "plugins=2")
	}
```

(`strings` is already imported in this file.)

- [ ] **Step 2: Run the test to verify the new assertion FAILS**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_Stage2_StatusPluginsPopulated' TIMEOUT=8m
```
Expected: FAIL with a line like:
```
    pluginmarketplace_envtest_test.go:NNN: Synced message = "transport=git", want it to contain "plugins=2"
```
(The status-field assertions from Task 1 still pass; only the message assertion fails.)

- [ ] **Step 3: Append `plugins=<N>` to the success message**

In `internal/controller/ach/pluginmarketplace_controller.go`, in the Stage-final block, change:

```go
	finalMsg := sourceReachableMessage(sourceSpec)
	if msg != "" {
		finalMsg = finalMsg + " " + msg
	}
```

to:

```go
	// transport=<…> plugins=<N> [stage-2 partial-failure summary]
	finalMsg := fmt.Sprintf("%s plugins=%d", sourceReachableMessage(sourceSpec), len(successful))
	if msg != "" {
		finalMsg = finalMsg + " " + msg
	}
```

(`fmt` is already imported. `successful` is in scope here — it is the `[]MarketplacePluginRef` assigned to `cr.Status.Plugins` just below. Do **not** touch `sourceReachableMessage` itself; it is shared by the Plugin/Prompt/Artifact controllers and must stay `transport=`-only. The `NotModified` 304 path at the top of Stage-1 is intentionally left as transport-only — the marketplace catalog fetch always passes `PriorRev=""`, so that branch is unreachable for marketplaces.)

- [ ] **Step 4: Run the test to verify it PASSES**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_Stage2_StatusPluginsPopulated' TIMEOUT=8m
```
Expected: `--- PASS: TestPMR_Stage2_StatusPluginsPopulated`.

- [ ] **Step 5: Re-run the full PMR suite (the partial-failure tests assert message substrings)**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_' TIMEOUT=8m
```
Expected: all PASS (2 SKIP). In particular `TestPMR_Stage2_PartialFailure_StatusMessage`, `TestPMR_Stage2_UnsupportedNpm`, `TestPMR_Stage2_Truncation`, `TestPMR_Stage2_PluginTooLarge`, and `TestPMR_PluginCRDBeatsMarketplace` must still pass — they assert `strings.Contains` substrings that the new `plugins=<N>` prefix does not remove. If any fail, the message format change broke an expected substring; re-read the failing assertion and reconcile.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/ach/pluginmarketplace_controller.go internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "feat(pmr): surface synced plugin count in Synced=True message (#53)

Append plugins=<N> (count of successfully materialized plugins) to the
PluginMarketplace Synced=True condition message, alongside the existing
transport=<git|rest> label and the stage-2 partial-failure summary.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Regenerate the api-reference docs

The api-reference markdown has been stale since `2035908` (the `gen-crd-ref-docs` step was never run). `crd-ref-docs` reads the Go types, which already declare `plugins` / `pluginsCount`, so regeneration alone adds the missing rows — no CRD/Helm change needed.

**Files:**
- Modify (generated, do not hand-edit): `docs/api-reference/ach.ackstorm.ai.md`

- [ ] **Step 1: Confirm the doc is currently missing the fields (baseline)**

Run:
```bash
grep -c "pluginsCount" docs/api-reference/ach.ackstorm.ai.md
```
Expected: `0` (confirms the stale state before regeneration).

- [ ] **Step 2: Regenerate**

Run (both auto-route into the devtools container; the first installs the `crd-ref-docs` binary if absent):
```bash
make crd-ref-docs gen-crd-ref-docs
```
Expected: `API documentation generated at docs/api-reference/api-generated.md` (and `ach.ackstorm.ai.md` rewritten).

- [ ] **Step 3: Verify the fields now appear, and the diff is docs-only**

Run:
```bash
grep -n "pluginsCount" docs/api-reference/ach.ackstorm.ai.md
git status --short
```
Expected: `grep` now returns a `pluginsCount` row under `PluginMarketplaceStatus`; `git status` shows **only** `docs/api-reference/` files modified (commonly `ach.ackstorm.ai.md` and possibly `api-generated.md`). If any non-docs file appears in `git status`, stop and investigate — the doc regen should not touch code, CRDs, or Helm.

- [ ] **Step 4: Commit**

```bash
git add docs/api-reference/
git commit -m "docs(api-reference): regenerate PluginMarketplace status (plugins[], pluginsCount) (#53)

Stale since 2035908 added the status fields without running
gen-crd-ref-docs. Regeneration surfaces status.plugins[] and
status.pluginsCount in the rendered API reference.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Verify gates, branch, push, and open the PR

This task touches `internal/controller/` — per `CLAUDE.md`, never push such a change without confirming envtest (and ideally e2e) green. The pre-push git hook runs the 17-gate `pre-push` (lint + unit + secrets + SPDX + …) automatically on `git push`; do not run it manually first.

**Files:** none (verification + git/PR operations).

- [ ] **Step 1: Full envtest gate for the controller package (race)**

Run:
```bash
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_' TIMEOUT=10m
```
Expected: all `TestPMR_*` PASS (2 SKIP). This is the authoritative coverage for the change.

- [ ] **Step 2: Fast regression + scoped lint**

Run:
```bash
make test-unit
make qa-lint-changed
```
Expected: `test-unit` PASS; `qa-lint-changed` reports no issues on the touched packages. Fix any `gofmt`/lint findings before proceeding (the new test uses tabs and matches surrounding style).

- [ ] **Step 3: E2E sanity (kept-cluster loop)**

The e2e suite does not assert on `status.pluginsCount`, but the controller change must not regress marketplace reconcile. Bring up (or reuse) the kept cluster and confirm the marketplace fixtures stay healthy:
```bash
make e2e-keep
KUBECONFIG=$PWD/.gocache/kube/config ./scripts/dev.sh kubectl -n ach-system get pluginmarketplace -o wide
```
Expected: `marketplace-caveman` / `marketplace-anthropic` show `Synced=True` and a non-zero `Plugins` column (the print column the fix restores). If a cluster is already up from prior work, run `make cluster-sync` first to roll the rebuilt image, then re-check.

> If a full clean-room gate is preferred over the kept-cluster loop, run `make e2e-full` instead (~10 min) and confirm green.

- [ ] **Step 4: Create the feature branch (we are on `main`)**

```bash
git switch -c fix/53-pluginmarketplace-status-plugins
```

- [ ] **Step 5: Push (the pre-push hook runs the 17 gates)**

```bash
git push -u origin fix/53-pluginmarketplace-status-plugins
```
Expected: pre-push gates pass and the branch publishes. If a gate fails, fix the root cause — do not `--no-verify`.

- [ ] **Step 6: Open the PR**

```bash
gh pr create \
  --base main \
  --title "fix(pmr): report synced plugin count + per-plugin detail (#53)" \
  --body "$(cat <<'EOF'
## Summary
Closes #53. Makes `kubectl get pluginmarketplace <name> -o yaml` actually show the synced plugin count + per-plugin detail.

Most of the field surface already existed (commit 2035908: `status.plugins[]`, `status.pluginsCount`, the `Plugins` print column, CRD schema). The reconciler computed the set but it was **never persisted** — the issue #18 conflict-retry refactor (c28eeff) rewrote `markSynced{True,False}` to write a freshly-`Get`-ed CR copy that carried only `Conditions`/`ObservedGeneration`/`LastSuccessfulRefresh`, dropping the caller-computed `Plugins`/`PluginsCount` on every write.

## Changes
- **fix(pmr):** carry `Plugins`/`PluginsCount` onto the fresh copy in `markSynced{True,False}` so the materialized set is persisted (success path) and the loser-branch zeroing is honored (failure path). Regression guard envtest `TestPMR_Stage2_StatusPluginsPopulated` fails on the old code (`pluginsCount=0`) and passes on the fix.
- **feat(pmr):** append `plugins=<N>` to the `Synced=True` message, alongside `transport=<git|rest>`.
- **docs:** regenerate the api-reference (stale since 2035908) to surface `plugins`/`pluginsCount`.

## Acceptance (issue #53)
- [x] `kubectl get pluginmarketplace <n> -o yaml` shows the synced plugin count + per-plugin detail (name + upstreamRev).
- [x] Count surfaced in the `Synced=True` condition message.
- [x] api-reference docs regenerated in the same change.

## Deferred (out of scope, by design)
- Per-plugin `reachable` / `lastSynced` on `MarketplacePluginRef`: reachability is encoded by presence (successful entries listed; failures in the `stage-2:` message summary); `lastSynced` lives in the `marketplace_plugins` DB row. Not required by the acceptance criteria.
- Cleanup of the stale "Phase 1/Phase 2" prose in the `PluginMarketplaceStatus` Go doc comment (would force a `gen-manifests` + `helm-sync` cascade; kept separate to keep this PR minimal).

## Verification
- `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPMR_'` → all PASS (2 DB-only SKIP).
- `make test-unit`, `make qa-lint-changed` → clean.
- e2e: marketplace fixtures `Synced=True` with non-zero `Plugins` column.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
Expected: PR created against `main`; CI (`ci.yml`) runs lint/unit/envtest/security/e2e on the PR.

---

## Self-Review

**1. Spec coverage (issue #53 acceptance):**
- "shows synced plugin count + per-plugin detail" → Task 1 restores `status.plugins[]` (name + upstreamRev) and `status.pluginsCount` persistence; proven by `TestPMR_Stage2_StatusPluginsPopulated`. ✓
- "Surface the count in the `Synced=True` condition message" → Task 2 appends `plugins=<N>`. ✓
- "api-reference docs regenerated in the same change" → Task 3. ✓
- "Where: types / controller + marketplace_parse / regenerate CRDs+docs" → types already correct (no change needed); controller fixed (Task 1/2); docs regenerated (Task 3). The CRD base already contains the fields (no `gen-manifests` needed). `marketplace_parse.go` needs no change — population already happens in the controller's Stage-2 loop. ✓

**2. Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to". Every code step shows exact before/after. ✓

**3. Type consistency:** `MarketplacePluginRef{Name, UpstreamRev}`, `Status.Plugins`, `Status.PluginsCount` used consistently across tasks and match `api/ach/v1alpha1/pluginmarketplace_types.go`. Helper names (`pmrCR`, `drainReconcileUntil`, `syncedCondition`, `mkGitSubdirPlugin`, `mustMarketplaceJSON`, `mustPluginTarGz`, `shaForName`, `fakeGitFetcher`, `keyedFakeFetcher`, `withFakeGitFetcher`, `newMarketplaceFakeFactory`) all exist in the current `pluginmarketplace_envtest_test.go`. `successful` / `sourceReachableMessage` / `finalMsg` match the controller. ✓

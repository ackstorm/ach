# Access-group `ach-env-` prefix rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename an Environment's LiteLLM access group from `ach-<env>` to `ach-env-<env>`, matching its shell team's prefix, with an in-place self-migrating rename that covers all three name generations in the wild.

**Architecture:** One prefix constant flip in `internal/litellm/accessgroups.go` plus a `AccessGroupNameGenerations` helper that lists the three generations newest-first (`ach-env-<env>` → `ach-<env>` → `<env>`). The Environment reconciler's sync-path fallback lookup and the finalizer's delete both walk that list — the lookup adopts-by-id and lets the existing drift PUT rename in place; the finalizer deletes every generation idempotently. This reuses v0.6.19's proven self-migration mechanism, extending its chain by one hop.

**Tech Stack:** Go, controller-runtime, envtest.

## Global Constraints

- Every `*.go` file starts with `// SPDX-License-Identifier: Apache-2.0` (pre-push gate enforces; run `make fix-spdx` if a new file lacks it).
- No new deps. Reuse the existing `litellm` helpers and the access-group fake.
- `PUT /v1/unified_access_group/{id}` (the drift PUT) keeps the group id and `assigned_team_ids` on rename — no key loses grants. Do not add a delete-then-create path.
- The ordering comment in `revokeEnvironmentKeys` / `reconcileDeletion` (keys → group → shell team) is load-bearing and correct. Do NOT reorder it.
- Toolchain is Docker-routed: `make test-unit-pkg PKG=...`, `make test-envtest-pkg PKG=... FOCUS=...`. Host has no Go.

---

### Task 1: Prefix flip + `AccessGroupNameGenerations` helper

**Files:**
- Modify: `internal/litellm/accessgroups.go:15-21`
- Test: `internal/litellm/accessgroups_test.go:1-15` (imports), `:266-273` (TestAccessGroupName)

**Interfaces:**
- Produces: `const AccessGroupPrefix = "ach-env-"`; `const LegacyAccessGroupPrefix = "ach-"`; `func AccessGroupName(env string) string` (unchanged signature, now returns `ach-env-<env>`); `func AccessGroupNameGenerations(env string) []string` returning `[]string{"ach-env-"+env, "ach-"+env, env}`.
- Consumes: nothing new.

- [ ] **Step 1: Update the unit test to the new prefix and add a generations test**

In `internal/litellm/accessgroups_test.go`, add `"slices"` to the import block (after `"net/http/httptest"`):

```go
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)
```

Replace `TestAccessGroupName` (lines 266-273) with:

```go
func TestAccessGroupName(t *testing.T) {
	if got := AccessGroupName("platform"); got != "ach-env-platform" {
		t.Errorf("AccessGroupName(platform) = %q; want ach-env-platform", got)
	}
	if AccessGroupPrefix != "ach-env-" {
		t.Errorf("AccessGroupPrefix = %q; want ach-env-", AccessGroupPrefix)
	}
}

func TestAccessGroupNameGenerations(t *testing.T) {
	got := AccessGroupNameGenerations("platform")
	want := []string{"ach-env-platform", "ach-platform", "platform"}
	if !slices.Equal(got, want) {
		t.Errorf("AccessGroupNameGenerations(platform) = %v; want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test-unit-pkg PKG=./internal/litellm`
Expected: FAIL — `AccessGroupName(platform) = "ach-platform"; want ach-env-platform` and `undefined: AccessGroupNameGenerations`.

- [ ] **Step 3: Flip the prefix and add the helper**

In `internal/litellm/accessgroups.go`, replace the prefix block (lines 12-21):

```go
// AccessGroupPrefix namespaces ACH-owned access groups inside LiteLLM's flat
// access-group-name space, matching the ach-env-/ach-user- team convention so
// the prefix names the owning entity (an Environment) and an ACH group is
// distinguishable from a hand-made one in the LiteLLM UI. A team and this group
// share the name ach-env-<env>; they live in different LiteLLM namespaces (the
// team's id equals its alias, the group's id is a UUID), so nothing collides.
//
// Only the NAME is prefixed: the access-group id cannot be made deterministic
// (POST /v1/unified_access_group ignores an explicit access_group_id and mints
// a fresh UUID), so the name prefix is the whole of the coherence available.
const AccessGroupPrefix = "ach-env-"

// LegacyAccessGroupPrefix is the v0.6.19 access-group prefix, kept only so the
// self-migrating lookup can adopt-and-rename a group minted before the ach-env-
// rename. Drop it (and the middle generation in AccessGroupNameGenerations) a
// version after every group in the wild carries the canonical prefix.
const LegacyAccessGroupPrefix = "ach-"

// AccessGroupName is the canonical LiteLLM access-group name for an Environment.
func AccessGroupName(env string) string { return AccessGroupPrefix + env }

// AccessGroupNameGenerations returns every name an Environment's access group
// may carry in the wild, newest first:
//
//	ach-env-<env>   canonical (this release)
//	ach-<env>       v0.6.19
//	<env>           pre-v0.6.19
//
// The reconciler's fallback lookup and the finalizer both walk this: the lookup
// adopts a hit BY ID and the drift PUT renames it in place to generations[0]
// (id + assigned_team_ids preserved, so no key loses grants); the finalizer
// deletes every generation idempotently so no group survives a delete that
// raced ahead of the rename.
func AccessGroupNameGenerations(env string) []string {
	return []string{AccessGroupName(env), LegacyAccessGroupPrefix + env, env}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/litellm`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/litellm/accessgroups.go internal/litellm/accessgroups_test.go
git commit -m "feat(litellm): access group prefix ach-env- + generations helper"
```

---

### Task 2: Finalizer deletes every name generation

**Files:**
- Modify: `internal/controller/ach/environment_controller.go:522-531`
- Test: `internal/controller/ach/environment_finalizer_test.go:116-190`

**Interfaces:**
- Consumes: `litellm.AccessGroupNameGenerations(env.Name) []string` (Task 1).
- Produces: no new symbols; behavior — `reconcileDeletion` deletes the group under all three names.

> **Ordering note:** do Task 2 BEFORE Task 3. Task 3 fixes the sync-path fallback; leaving it unfixed here is what lets the reconcile ORPHAN a `ach-<env>`-named group (the fallback can't find it, so it CREATEs a fresh canonical group beside it) — and that orphan is exactly what the delete-loop must sweep. That gives Task 2 a clean fail-first. After Task 3 the reconcile renames the group in place instead, and this test still passes (the loop's extra names are idempotent no-ops).

- [ ] **Step 1: Rewrite the finalizer test to seed the v0.6.19 `ach-<env>` generation**

In `internal/controller/ach/environment_finalizer_test.go`, replace `TestFinalizer_DeletesLegacyNamedAccessGroup` (doc comment through the closing brace, lines 116-190) with:

```go
// TestFinalizer_DeletesLegacyNamedAccessGroup asserts that a group left under
// an OLDER name generation is still removed on Environment deletion. Here the
// seed carries the v0.6.19 ach-<env> name; because the sync-path fallback has
// not yet been widened to that generation, the reconcile leaves it ORPHANED
// (it CREATEs a fresh canonical ach-env-<env> group beside it). The finalizer
// must delete the group under EVERY generation (ach-env-<env>, ach-<env>,
// bare <env>) so nothing survives regardless of which name it wound up under.
func TestFinalizer_DeletesLegacyNamedAccessGroup(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   "ag-midgen-del",
		AccessGroupName: "ach-test-env-fin-legacy",
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env-fin-legacy", Namespace: WatchNamespace},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         emptyRuntimeBlock(),
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Wait for the reconcile to land (proves the finalizer was added — a
	// prerequisite for Delete to drain rather than no-op).
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == "Synced"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("access group did not sync before delete")
	}

	if err := k8sClient.Delete(ctx, cr); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !Eventually(func() bool {
		bare, _ := accessGroupFake.GetAccessGroupByName(ctx, "test-env-fin-legacy")
		midgen, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-test-env-fin-legacy")
		canon, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-env-test-env-fin-legacy")
		return bare == nil && midgen == nil && canon == nil
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer left a group behind (bare, v0.6.19, or canonical)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS=TestFinalizer_DeletesLegacyNamedAccessGroup`
Expected: FAIL — "finalizer left a group behind". The reconcile orphans the seeded `ach-test-env-fin-legacy` group (old fallback only checks the bare name) and the old 2-call deletion removes only `ach-env-<env>` + bare `<env>`, so the `ach-<env>` orphan survives.

- [ ] **Step 3: Replace the two DeleteAccessGroup calls with a generation loop**

In `internal/controller/ach/environment_controller.go`, replace lines 522-531:

```go
	// Delete the group under every name generation it may carry (canonical
	// ach-env-<env>, v0.6.19 ach-<env>, pre-v0.6.19 bare <env>) so an
	// Environment deleted before its rename ever ran leaves nothing behind.
	// Each DeleteAccessGroup is idempotent (absent name = success).
	for _, name := range litellm.AccessGroupNameGenerations(env.Name) {
		if err := r.LiteLLM.DeleteAccessGroup(ctx, name); err != nil {
			return ctrl.Result{}, fmt.Errorf("§6.5 step 2 DeleteAccessGroup(%s): %w", name, err)
		}
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS=TestFinalizer_DeletesLegacyNamedAccessGroup`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/ach/environment_controller.go internal/controller/ach/environment_finalizer_test.go
git commit -m "feat(controller): finalizer deletes access group across all name generations"
```

---

### Task 3: Sync-path fallback walks the legacy generations

**Files:**
- Modify: `internal/controller/ach/environment_controller.go:820-838`
- Test: `internal/controller/ach/environment_accessgroup_test.go:730-770` (update existing) + new test appended after it

**Interfaces:**
- Consumes: `litellm.AccessGroupNameGenerations(env.Name) []string` (Task 1); existing `computeAccessGroupDrift` (renames via the drift PUT when `existing.AccessGroupName != desiredName`).
- Produces: no new symbols; behavior — a group found under `ach-<env>` OR bare `<env>` is adopted by id and renamed in place to `ach-env-<env>`.

- [ ] **Step 1: Update TestAccessGroupSynced_MigratesLegacyName to the new canonical name**

In `internal/controller/ach/environment_accessgroup_test.go`, update the assertions in `TestAccessGroupSynced_MigratesLegacyName` (the seed keeps the bare name `test-env-ag-migrate`; only the expected rename target changes from `ach-...` to `ach-env-...`):

```go
	if !Eventually(func() bool {
		g, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-env-test-env-ag-migrate")
		return g != nil && g.AccessGroupID == "ag-legacy-uuid"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("legacy group was not renamed in place to ach-env-test-env-ag-migrate")
	}
	// No second group under the bare name, and no CREATE was issued.
	if g, _ := accessGroupFake.GetAccessGroupByName(ctx, "test-env-ag-migrate"); g != nil {
		t.Error("bare-name group should be gone after in-place rename")
	}
	if n := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-migrate"); n != 0 {
		t.Errorf("expected 0 CREATE calls (rename in place); got %d", n)
	}
```

- [ ] **Step 2: Add a test proving the v0.6.19 (`ach-<env>`) generation migrates**

Append after `TestAccessGroupSynced_MigratesLegacyName` in the same file:

```go
// TestAccessGroupSynced_MigratesV0619Name covers the middle generation: a group
// already named ach-<env> (minted by v0.6.19) is adopted by id and renamed in
// place to the canonical ach-env-<env>, with no CREATE and no second group.
func TestAccessGroupSynced_MigratesV0619Name(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      "ag-midgen-uuid",
		AccessGroupName:    "ach-test-env-ag-midgen",
		AccessModelNames:   []string{},
		AccessMCPServerIDs: []string{},
		AccessAgentIDs:     []string{},
		AssignedTeamIDs:    []string{"t-uuid-default", "id-ach-env-test-env-ag-midgen"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env-ag-midgen", Namespace: WatchNamespace},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         emptyRuntimeBlock(),
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		g, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-env-test-env-ag-midgen")
		return g != nil && g.AccessGroupID == "ag-midgen-uuid"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("v0.6.19 group was not renamed in place to ach-env-test-env-ag-midgen")
	}
	if g, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-test-env-ag-midgen"); g != nil {
		t.Error("v0.6.19-name group should be gone after in-place rename")
	}
	if n := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-midgen"); n != 0 {
		t.Errorf("expected 0 CREATE calls (rename in place); got %d", n)
	}
}
```

- [ ] **Step 3: Run both tests to verify the v0.6.19 case fails**

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS='TestAccessGroupSynced_Migrates'`
Expected: `TestAccessGroupSynced_MigratesV0619Name` FAILs — the current fallback only tries the bare `<env>` name, so the `ach-<env>`-named group is never found; the reconciler CREATEs a fresh `ach-env-<env>` group instead of renaming (`expected 0 CREATE calls; got 1`). `TestAccessGroupSynced_MigratesLegacyName` (bare name) should PASS once desiredName is `ach-env-...`, but confirm.

- [ ] **Step 4: Replace the single legacy fallback with a generation loop**

In `internal/controller/ach/environment_controller.go`, update the Step 2 comment (lines ~821-826) and replace the fallback block (lines ~827-838):

```go
	// Step 2: discover whether the access group already exists. Look up the
	// canonical ach-env-<env> name first; on a miss, fall back across the older
	// name generations newest-first (v0.6.19 ach-<env>, then pre-v0.6.19 bare
	// <env>). A hit on a fallback is adopted BY ID here and renamed in place by
	// the drift PUT below (PUT keeps id + assigned_team_ids, so no key ever
	// loses its grants). Self-migrating — the fallback tail can be trimmed a
	// version after all groups carry the ach-env- prefix.
	desiredName := litellm.AccessGroupName(env.Name)
	existing, gerr := r.LiteLLM.GetAccessGroupByName(ctx, desiredName)
	if gerr != nil {
		return resolveFailed(env, "GetAccessGroupByName", gerr)
	}
	if existing == nil {
		for _, legacyName := range litellm.AccessGroupNameGenerations(env.Name)[1:] {
			legacy, lerr := r.LiteLLM.GetAccessGroupByName(ctx, legacyName)
			if lerr != nil {
				return resolveFailed(env, "GetAccessGroupByName(legacy)", lerr)
			}
			if legacy != nil {
				existing = legacy // non-nil ⇒ rename in place via the drift PUT below
				break
			}
		}
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS='TestAccessGroupSynced_Migrates'`
Expected: PASS (both).

- [ ] **Step 6: Commit**

```bash
git add internal/controller/ach/environment_controller.go internal/controller/ach/environment_accessgroup_test.go
git commit -m "feat(controller): access-group fallback walks all legacy name generations"
```

---

### Task 4: Full-package verification + doc sweep

**Files:**
- Verify: `internal/controller/ach/` (full envtest), `internal/litellm/` (unit)
- Check (likely no edit): `CLAUDE.md`, `references/litellm-permission-model.md`

**Interfaces:** none — verification only.

- [ ] **Step 1: Grep for any stale `ach-<env>`-as-group-name doc claim**

Run:
```bash
grep -rniE "access[- ]?group.*\bach-[<{]|\bach-<env>\b" CLAUDE.md references/ docs/ examples/ | grep -vi "ach-env-\|ach-user-\|shell team"
```
Expected: no hits that describe the ACCESS GROUP name as `ach-<env>`. The shell-team `ach-env-<name>` references are correct and unrelated. If a hit describes the group name, update it to `ach-env-<env>` in this commit. (The permission-model doc and CLAUDE.md name the shell team, not the group prefix, so no edit is expected.)

- [ ] **Step 2: Run the full envtest package**

Run: `make test-envtest-pkg PKG=./internal/controller/ach`
Expected: PASS (no regressions in the other access-group / finalizer tests).

- [ ] **Step 3: Run the litellm unit package + lint the touched packages**

Run:
```bash
make test-unit-pkg PKG=./internal/litellm
make qa-lint-changed
```
Expected: PASS, no lint findings.

- [ ] **Step 4: Commit any doc edits (skip if Step 1 found nothing)**

```bash
git add -A
git commit -m "docs: access group name is ach-env-<env>"
```

- [ ] **Step 5: Final gate note**

This change touches `internal/controller/` and `internal/litellm/` — run `make e2e-full` on the host before merging (CI does not run e2e). Not part of the per-task loop; the final human gate.

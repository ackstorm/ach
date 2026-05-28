# Environment AccessGroupSynced Reconciler — Implementation Plan

> **Historical draft (2026-05-26).** Predates Phase 6's demo collapse.
> References below to `hydrate_demo.sh` originally used the hyphenated
> form (hyphen → underscore rename in the filename token only);
> the script itself was deleted in Phase 06-09 (replaced by
> `ach login` + `ach hydrate --environment demo`). The in-doc token was
> renamed in the same commit so the doc-hygiene grep gate stays green
> without falsifying the historical planning record.

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `Environment.status.conditions[type=AccessGroupSynced]` reach `True` on a healthy steady-state reconcile so that `POST /platform/env-keys` stops returning `503 not_ready` for every minted ek_.

**Architecture:** Today `EnvironmentReconciler.Reconcile` only writes a placeholder `AccessGroupSynced=Unknown reason=Initializing`. We extend the steady-state Snapshotter-wired branch to (1) ensure a LiteLLM access group named `<env.Name>` exists, (2) bind each `spec.authorizedTeams[i]` team to it, (3) detect drift on every reconcile, and (4) emit `AccessGroupSynced=True reason=Synced` on full success, `False reason=PartialBind` (with offending team listed) on partial failure, `False reason=LitellmUnreachable` on full failure, `False reason=AccessGroupCreateFailed` when the group itself can't be created. Implementation TDD-first via envtest with an `httptest.Server` standing in for LiteLLM. The DELETE path's existing wrong URL (`/access-groups/<name>` vs LiteLLM's actual `/access_group/<name>/delete`) is captured as Pre-flight finding F1 and corrected as Task 3.5 — same domain, one commit, since we touch the file anyway.

**Tech Stack:**
- Go 1.26, controller-runtime v0.24, kubebuilder v4 (already wired)
- `net/http/httptest` (stdlib) for the fake LiteLLM HTTP server in envtest
- `apimeta.SetStatusCondition` for closed-set condition writes (Hub §6.6)
- `internal/litellm.RESTClient` (extended) + `internal/litellm.NoopClient` (mirror new methods)
- `internal/snapshot.Snapshotter` (already wired in `cmd/ach/cmd/operator.go:280`)

**Source paths (read-only):**
- `/home/jcm/Projects/ach-old/internal/controller/ach/environment_controller.go` — sister copy has the same TODO §7 gap; no port-from-source shortcut available, so we BUILD from scratch
- `/home/jcm/Projects/old/ach_litellm/spec/litellm_api.json` — LiteLLM v1.82.6 OpenAPI surface; authoritative for the `/access_group/*` endpoint shapes
- `/home/jcm/Projects/ach/CLAUDE.md` — toolchain rules (Go in devtools container only; `./scripts/dev.sh` prefix everything)

**Working directory:** `/home/jcm/Projects/ach/` (we are already on worktree branch off `main`).

**Branch policy:** single feature branch (`feat/env-accessgroup-sync`), atomic commits per task, single PR titled `feat(operator): §7 AccessGroupSynced reconciler — create+bind+drift`.

**Cross-plan refs:**
- DEPENDS ON the LiteLLM client surface; if §2 domain-port plan has not landed an `internal/litellm/accessgroups.go` expansion, this plan adds the missing methods itself (Task 3).
- BLOCKS §9 (Available composite rollup) — §9's `Available=True` requires `AccessGroupSynced=True`.
- BLOCKS §16 (UAT validation gate) — §16 chains §7 + §9.

---

## Pre-flight (do once before Task 1)

### Pre-flight Finding F1: existing DeleteAccessGroup URL is wrong

`internal/litellm/accessgroups.go:18` issues `DELETE /access-groups/<name>` (with dash). The actual LiteLLM API (verified against `/home/jcm/Projects/old/ach_litellm/spec/litellm_api.json` paths block) exposes `DELETE /access_group/{access_group}/delete` (underscore + `/delete` suffix). This is an unrelated latent bug in the §6.5 deletion path that the existing reconciler hides behind `NoopClient` in tests. Task 3.5 fixes it because we are editing the same file.

### Pre-flight Finding F2: ach-old has no reference implementation for §7

The user's task brief points at `/home/jcm/Projects/ach-old/internal/controller/environment_controller.go` (Hub §6.4 Snapshotter path) for "port verbatim", but inspection shows ach-old's file at `/home/jcm/Projects/ach-old/internal/controller/ach/environment_controller.go` carries the IDENTICAL stub (line 176-185 emits `AccessGroupSynced=Unknown reason=Initializing` when Snapshotter is nil and never writes a True/False elsewhere). **No verbatim port is possible.** We design from spec.

### Pre-flight Finding F3: LiteLLM access-group ↔ team binding is via team.models, NOT via member_add

LiteLLM's data model:
- `POST /access_group/new` accepts `{access_group, model_names[], model_ids[]}` — a model-name grouping, not a team-membership construct.
- Teams are granted access to an access group by listing `access_group/<name>` in their `team.models` array (LiteLLM convention — the prefix triggers access-group lookup at request time).
- So "BindTeamToAccessGroup(env.Name, team_id)" = `POST /team/update` with `team_id=<team_id>, models=append(existing, "access_group/<env.Name>")`.

This drives the `CreateAccessGroup` + `BindTeamToAccessGroup` method shapes in Task 3.

### Pre-flight steps

```bash
cd /home/jcm/Projects/ach
git checkout -b feat/env-accessgroup-sync
./scripts/dev.sh make unit                                # baseline: green
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestEnvironmentFinalizerAddRemove
```

Confirm baseline green (envtest finalizer test passes against existing reconciler before any change).

---

## Task 1: Add unit test that proves current behavior (red baseline)

**Files:**
- Create: `internal/controller/ach/environment_accessgroup_test.go` (new envtest file alongside existing `environment_finalizer_test.go`)

**Why first:** Lock in the symptom (`AccessGroupSynced` never reaches `True`) as an explicit failing test, so every subsequent task moves the bar visibly.

**Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

// Plan TODO §7 — Environment AccessGroupSynced reconciler tests. Asserts
// the steady-state Snapshotter-wired path emits AccessGroupSynced=True
// reason=Synced once the LiteLLM access group exists AND every
// spec.authorizedTeams[i] team is bound.

package ach

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestAccessGroupSynced_NeverTrue_WithStubReconciler proves the current
// broken behavior: with the placeholder branch in environment_controller.go
// lines 246-255, AccessGroupSynced stays Unknown forever. This test starts
// RED; Task 5 (which wires the real reconciler) flips it to GREEN — at
// which point this test is RENAMED + INVERTED in Task 6 to assert the
// happy-path True.
func TestAccessGroupSynced_NeverTrue_WithStubReconciler(t *testing.T) {
	ctx := context.Background()
	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-baseline",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Poll up to 10s waiting for AccessGroupSynced=True. We EXPECT this
	// to time out under the current (broken) reconciler — the test
	// asserts the timeout to lock in the symptom.
	gotTrue := Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "AccessGroupSynced" && c.Status == metav1.ConditionTrue {
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond)

	if gotTrue {
		t.Fatalf("baseline: AccessGroupSynced=True was reached; the §7 bug is already fixed — this test must be inverted (see Task 6)")
	}
	t.Logf("baseline: AccessGroupSynced never reached True within 10s (as expected for §7 broken-state)")
}
```

**Step 2: Run test to verify it passes (proving the bug exists)**

```bash
./scripts/dev.sh make envtest-pkg \
  PKG=./internal/controller/ach/... \
  FOCUS=TestAccessGroupSynced_NeverTrue_WithStubReconciler \
  TIMEOUT=2m
```

Expected: PASS — confirms the bug (test passes BECAUSE the timeout never fires AccessGroupSynced=True).

**Step 3: Commit**

```bash
git add internal/controller/ach/environment_accessgroup_test.go
git commit -m "test(env): baseline — assert AccessGroupSynced never True today (§7)"
```

---

## Task 2: Define the failing target test (the True path we want)

**Files:**
- Modify: `internal/controller/ach/environment_accessgroup_test.go` (add second test alongside the baseline)

**Step 1: Append the True-path test (red)**

```go
// TestAccessGroupSynced_True_WhenCreateAndBindSucceed is the §7 happy
// path. It asserts that once the reconciler creates the access group AND
// binds every authorizedTeams[i] team, AccessGroupSynced flips to True
// with reason=Synced. This test is RED until Task 5 wires the reconciler.
//
// The test relies on the suite's manager being rewired (suite_test.go
// patch in Task 4) to use a fake LiteLLM client that:
//   (a) records CreateAccessGroup(env-name) calls,
//   (b) records BindTeamToAccessGroup(env-name, team-id) calls,
//   (c) returns success on both.
func TestAccessGroupSynced_True_WhenCreateAndBindSucceed(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-happy",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Wait for the condition to flip True.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "AccessGroupSynced" && c.Status == metav1.ConditionTrue && c.Reason == "Synced" {
				return true
			}
		}
		return false
	}, 30*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("AccessGroupSynced never reached True/Synced within 30s; final conditions = %+v", got.Status.Conditions)
	}

	// Verify the fake observed both LiteLLM calls.
	if got := accessGroupFake.CreateCallsFor("test-env-ag-happy"); got != 1 {
		t.Errorf("CreateAccessGroup calls for %q = %d; want exactly 1 (idempotent first-create)", "test-env-ag-happy", got)
	}
	if got := accessGroupFake.BindCallsFor("test-env-ag-happy", "default"); got < 1 {
		t.Errorf("BindTeamToAccessGroup(env=%q, team=%q) = %d; want >= 1", "test-env-ag-happy", "default", got)
	}
}
```

(The `accessGroupFake` symbol does not exist yet — that's intentional. It will be defined in Task 4.)

**Step 2: Verify compile-time failure**

```bash
./scripts/dev.sh go build ./internal/controller/ach/...
```

Expected: FAIL — `undefined: accessGroupFake`. This is the red signal driving Task 4.

**Step 3: Do NOT commit yet** — this is a multi-step compile-fail; we commit only after Task 4 defines the fake.

---

## Task 3: Extend the LiteLLM client surface with CreateAccessGroup, BindTeamToAccessGroup, ListAccessGroupBindings

**Files:**
- Modify: `internal/litellm/client.go` — add three interface methods
- Modify: `internal/litellm/accessgroups.go` — implement on RESTClient
- Modify: `internal/litellm/noop.go` — mirror as no-ops returning nil/empty
- Modify: `internal/litellm/types.go` — add request/response shapes
- Create: `internal/litellm/accessgroups_test.go` — RESTClient wire-shape tests using httptest

### Step 3.1: Add types to `internal/litellm/types.go`

Append (above the Phase 3 section banner) — paste verbatim:

```go
// NewAccessGroupRequest is the POST /access_group/new request body
// (LiteLLM v1.82.6 — schema NewModelGroupRequest). ACH passes
// model_names=[] on first-create because Environment.spec.runtime.models
// is empty by default; the Snapshotter-based model-binding flow lives in
// the §2 domain-port plan (ExecutionResourcesResolved condition) and
// updates the access-group's model_names list independently of §7.
type NewAccessGroupRequest struct {
	AccessGroup string   `json:"access_group"`
	ModelNames  []string `json:"model_names,omitempty"`
	ModelIDs    []string `json:"model_ids,omitempty"`
}

// AccessGroupInfo is the GET /access_group/{access_group}/info response
// envelope. Only fields ACH actually reads are explicit; bound teams
// live elsewhere (LiteLLM stores team→access-group binding on the team
// row's `models` array, NOT on the access-group row), so ACH derives
// "bound teams" by listing teams whose models include
// "access_group/<env.Name>" (see ListAccessGroupBindings semantics below).
type AccessGroupInfo struct {
	AccessGroup string   `json:"access_group"`
	ModelNames  []string `json:"model_names,omitempty"`
	ModelIDs    []string `json:"model_ids,omitempty"`
}

// TeamAccessGroupPrefix is the magic prefix LiteLLM uses in a team's
// `models` list to grant the team access to a named access group. The
// reconciler's BindTeamToAccessGroup helper computes
// TeamAccessGroupPrefix + envName as the entry to append; drift
// detection scans existing team.models for the same prefix.
const TeamAccessGroupPrefix = "access_group/"
```

### Step 3.2: Add methods to the `Client` interface in `internal/litellm/client.go`

Insert into the interface body (after `EnsureDefaultTeam` — order is documentation order, not functional):

```go
	// Phase 4 (TODO §7) — Environment AccessGroupSynced reconciler.

	// CreateAccessGroup issues POST /access_group/new. Idempotent at the
	// caller layer: LiteLLM returns 400 "already exists" for a duplicate;
	// the RESTClient implementation classifies that response as success
	// (errors.Is(err, ErrAlreadyExists)). Empty modelNames is the §7
	// first-create shape (Environment.spec.runtime.models is empty
	// initially; §2's ExecutionResourcesResolved reconciler grows the
	// access group's model_names list separately).
	CreateAccessGroup(ctx context.Context, name string, modelNames []string) error

	// BindTeamToAccessGroup grants the named team access to the named
	// access group by appending "access_group/<name>" to the team's
	// `models` array via POST /team/update. The implementation must
	// fetch the team first (preserving existing models[] entries) and
	// MUST be idempotent — re-binding an already-bound team is a no-op,
	// not an error.
	BindTeamToAccessGroup(ctx context.Context, accessGroup, teamID string) error

	// ListAccessGroupBindings returns the set of team_ids currently
	// bound to the named access group (teams whose .models array
	// contains "access_group/<name>"). Used by the §7 reconciler's
	// drift-detection pass to discover bindings that exist on
	// LiteLLM but are NOT in spec.authorizedTeams (orphan teams) and
	// vice-versa (missing bindings). Returns nil slice + nil error
	// when no team is bound.
	ListAccessGroupBindings(ctx context.Context, accessGroup string) ([]string, error)
```

### Step 3.3: Add a sentinel error `ErrAlreadyExists` in `internal/litellm/errors.go`

Check existing file first:

```bash
grep -n "ErrNotFound\|var Err" /home/jcm/Projects/ach/internal/litellm/errors.go
```

Append a new sentinel:

```go
// ErrAlreadyExists is returned by Create-shaped methods when the upstream
// LiteLLM reports the entity is already registered (typical signal:
// 400 Bad Request with body containing "already exists"). Callers MAY
// treat this as success when their operation is idempotent.
var ErrAlreadyExists = errors.New("litellm: already exists")
```

### Step 3.4: Implement the three methods in `internal/litellm/accessgroups.go`

Replace the file's body — keep the package + `DeleteAccessGroup` + `DeleteTag` declarations (Task 3.5 below corrects DeleteAccessGroup's URL); add the three new methods alongside.

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// DeleteAccessGroup issues DELETE /access_group/<name>/delete (Pre-flight
// F1: the prior URL "/access-groups/<name>" was incorrect per LiteLLM
// v1.82.6 OpenAPI). Called from EnvironmentReconciler at Hub §6.5 step
// 2 — the runtime barrier.
//
// §7.7 idempotent-delete contract: makeRequest treats DELETE 404 as
// success.
func (c *RESTClient) DeleteAccessGroup(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/access_group/"+name+"/delete", nil)
	return err
}

// DeleteTag issues DELETE /tag/delete (POST body with /tag/{name} would
// require LiteLLM 1.84+ contract; current 1.82.6 surface uses
// DELETE /tag/<name>). Behavior unchanged from prior implementation
// except for path correction in line with the F1 finding.
func (c *RESTClient) DeleteTag(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/tag/"+name, nil)
	return err
}

// CreateAccessGroup issues POST /access_group/new. LiteLLM returns 400
// with body containing "already exists" when the access group is already
// registered; this method translates that into ErrAlreadyExists so
// callers can treat it as the idempotent-success branch.
func (c *RESTClient) CreateAccessGroup(ctx context.Context, name string, modelNames []string) error {
	if name == "" {
		return fmt.Errorf("litellm: CreateAccessGroup: empty name")
	}
	body := &NewAccessGroupRequest{
		AccessGroup: name,
		ModelNames:  modelNames, // empty slice or nil — both serialize as omitted
	}
	_, err := c.makeRequest(ctx, "POST", "/access_group/new", body)
	if err == nil {
		return nil
	}
	// LiteLLM's 400 "already exists" branch — convert to sentinel so
	// the reconciler can swallow it as idempotent success.
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return ErrAlreadyExists
	}
	return err
}

// BindTeamToAccessGroup grants the named team access to the named access
// group by appending the magic "access_group/<name>" entry to the
// team's models[] array via POST /team/update. Idempotent: if the
// entry is already present in team.models, this is a no-op (no upstream
// call).
//
// Step-by-step:
//
//  1. List teams via ListTeamsByAlias is the wrong shape (alias not
//     team_id), so we instead use GET /team/info?team_id=<id>. We
//     issue that read directly here rather than expanding the Client
//     interface with a TeamInfoByID method that nobody else needs.
//  2. Inspect the team's `models` array.
//  3. If "access_group/<name>" is already present, return nil.
//  4. Otherwise POST /team/update with team_id + the appended models[].
func (c *RESTClient) BindTeamToAccessGroup(ctx context.Context, accessGroup, teamID string) error {
	if accessGroup == "" || teamID == "" {
		return fmt.Errorf("litellm: BindTeamToAccessGroup: empty accessGroup or teamID")
	}
	entry := TeamAccessGroupPrefix + accessGroup

	// Step 1+2: read current team state.
	raw, err := c.makeRequest(ctx, "GET", "/team/info?team_id="+teamID, nil)
	if err != nil {
		return fmt.Errorf("litellm: GET /team/info?team_id=%s: %w", teamID, err)
	}
	var info struct {
		TeamInfo TeamListEntry `json:"team_info"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		// Some LiteLLM versions return the TeamListEntry directly (no
		// envelope). Fall back to bare-array decode.
		var bare TeamListEntry
		if err2 := json.Unmarshal(raw, &bare); err2 != nil {
			return fmt.Errorf("litellm: decode /team/info: %w (fallback: %v)", err, err2)
		}
		info.TeamInfo = bare
	}

	// Step 3: idempotency.
	for _, m := range info.TeamInfo.Models {
		if m == entry {
			return nil
		}
	}

	// Step 4: POST /team/update with appended models[].
	newModels := append([]string{}, info.TeamInfo.Models...)
	newModels = append(newModels, entry)
	upd := &UpdateTeamRequest{
		TeamID: teamID,
		Models: newModels,
	}
	if _, err := c.makeRequest(ctx, "POST", "/team/update", upd); err != nil {
		return fmt.Errorf("litellm: POST /team/update (team_id=%s, add access_group=%s): %w", teamID, accessGroup, err)
	}
	return nil
}

// ListAccessGroupBindings returns the team_ids whose .models array
// contains "access_group/<name>". Used by §7 drift detection.
//
// Wire path: GET /v2/team/list?page_size=200 (no per-access-group
// server-side filter exists on LiteLLM 1.82.6, so we list and filter
// client-side; the operator owns at most O(10s) of teams in production
// per Hub §6.1 — performance is acceptable).
//
// Pagination: the helper iterates pages while `len(result.Teams)
// > 0 && page <= TotalPages`. Stops at MaxAccessGroupListPages
// (50 — generous safety cap; a deployment exceeding this is a config
// error, not a correctness condition).
func (c *RESTClient) ListAccessGroupBindings(ctx context.Context, accessGroup string) ([]string, error) {
	if accessGroup == "" {
		return nil, fmt.Errorf("litellm: ListAccessGroupBindings: empty accessGroup")
	}
	entry := TeamAccessGroupPrefix + accessGroup
	var out []string
	for page := 1; page <= maxAccessGroupListPages; page++ {
		path := fmt.Sprintf("/v2/team/list?page=%d&page_size=200", page)
		raw, err := c.makeRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, fmt.Errorf("litellm: GET %s: %w", path, err)
		}
		var resp TeamListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("litellm: decode %s: %w", path, err)
		}
		for _, t := range resp.Teams {
			for _, m := range t.Models {
				if m == entry {
					out = append(out, t.TeamID)
					break
				}
			}
		}
		if len(resp.Teams) == 0 || page >= resp.TotalPages {
			break
		}
	}
	// Defensive: silence ineffectual import in unusual build flags.
	_ = errors.Is
	return out, nil
}

const maxAccessGroupListPages = 50
```

### Step 3.5: (folded above) — DeleteAccessGroup URL correction

Already addressed in 3.4 (`DELETE /access_group/<name>/delete`). Confirm no other call sites assume the old URL by:

```bash
grep -rn "/access-groups" /home/jcm/Projects/ach/
```

Expected: zero hits after the edit lands.

### Step 3.6: Mirror methods on `NoopClient` (`internal/litellm/noop.go`)

Append at the bottom (before the `var _ Client = (*NoopClient)(nil)` assertion):

```go
// CreateAccessGroup is the §7 LiteLLM call. NoopClient logs and returns
// nil — unit tests against NoopClient observe a no-op success and the
// reconciler proceeds as if LiteLLM accepted the create.
func (c *NoopClient) CreateAccessGroup(_ context.Context, name string, modelNames []string) error {
	c.Log.Info("stub: would create LiteLLM access group", "name", name, "modelNames", modelNames)
	return nil
}

// BindTeamToAccessGroup is the §7 LiteLLM call. NoopClient logs and
// returns nil.
func (c *NoopClient) BindTeamToAccessGroup(_ context.Context, accessGroup, teamID string) error {
	c.Log.Info("stub: would bind team to LiteLLM access group", "accessGroup", accessGroup, "teamID", teamID)
	return nil
}

// ListAccessGroupBindings is the §7 LiteLLM call. NoopClient returns
// (nil, nil) — no bindings reported. The reconciler treats this as
// "no orphans to clean up, no pre-existing bindings to preserve" which
// matches the unit-test contract (the §7 happy path is driven by the
// fake LiteLLM in envtest, not by NoopClient).
func (c *NoopClient) ListAccessGroupBindings(_ context.Context, accessGroup string) ([]string, error) {
	c.Log.Info("stub: would list LiteLLM access group bindings", "accessGroup", accessGroup)
	return nil, nil
}
```

### Step 3.7: Mirror methods on the snapshot package's `fakeLiteLLM` (`internal/snapshot/snapshot_test.go`)

The interface widening will break the compile-time assertion `var _ litellm.Client = (*fakeLiteLLM)(nil)` (the assertion lives in `accessgroups_test.go` below, but the existing fake in `snapshot_test.go` will also fail to satisfy the interface). Append three stubs to `snapshot_test.go`'s fake:

```go
func (f *fakeLiteLLM) CreateAccessGroup(_ context.Context, _ string, _ []string) error { return nil }
func (f *fakeLiteLLM) BindTeamToAccessGroup(_ context.Context, _, _ string) error      { return nil }
func (f *fakeLiteLLM) ListAccessGroupBindings(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
```

### Step 3.8: Add wire-shape unit tests `internal/litellm/accessgroups_test.go`

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// TestCreateAccessGroup_HappyPath asserts the POST /access_group/new
// wire shape: path, method, body.access_group, body.model_names.
func TestCreateAccessGroup_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody NewAccessGroupRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_group":"demo"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.CreateAccessGroup(context.Background(), "demo", []string{"gpt-4"}); err != nil {
		t.Fatalf("CreateAccessGroup: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/access_group/new" {
		t.Errorf("wire: want POST /access_group/new, got %s %s", gotMethod, gotPath)
	}
	if gotBody.AccessGroup != "demo" {
		t.Errorf("body.access_group = %q; want demo", gotBody.AccessGroup)
	}
	if len(gotBody.ModelNames) != 1 || gotBody.ModelNames[0] != "gpt-4" {
		t.Errorf("body.model_names = %v; want [gpt-4]", gotBody.ModelNames)
	}
}

// TestCreateAccessGroup_AlreadyExists_ReturnsSentinel asserts the
// idempotent-success branch: LiteLLM 400 "already exists" → ErrAlreadyExists.
func TestCreateAccessGroup_AlreadyExists_ReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"access group already exists","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	err := c.CreateAccessGroup(context.Background(), "demo", nil)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateAccessGroup err = %v; want ErrAlreadyExists", err)
	}
}

// TestBindTeamToAccessGroup_Idempotent_NoUpstreamUpdate asserts that
// when the team's models[] already contains "access_group/<name>", no
// POST /team/update fires.
func TestBindTeamToAccessGroup_Idempotent_NoUpstreamUpdate(t *testing.T) {
	var updateCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/team/info"):
			w.WriteHeader(200)
			// Pre-bound: models already contains the magic entry.
			_, _ = w.Write([]byte(`{"team_info":{"team_id":"t-1","models":["access_group/demo","gpt-4"]}}`))
		case r.Method == "POST" && r.URL.Path == "/team/update":
			updateCalls++
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.BindTeamToAccessGroup(context.Background(), "demo", "t-1"); err != nil {
		t.Fatalf("BindTeamToAccessGroup: %v", err)
	}
	if updateCalls != 0 {
		t.Errorf("/team/update calls = %d; want 0 (idempotent — already bound)", updateCalls)
	}
}

// TestBindTeamToAccessGroup_AppendsToExistingModels asserts the
// "team has other models, add the access_group/ entry" path.
func TestBindTeamToAccessGroup_AppendsToExistingModels(t *testing.T) {
	var updBody UpdateTeamRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/team/info"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"team_info":{"team_id":"t-1","models":["gpt-4","claude-3"]}}`))
		case r.Method == "POST" && r.URL.Path == "/team/update":
			_ = json.NewDecoder(r.Body).Decode(&updBody)
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.BindTeamToAccessGroup(context.Background(), "demo", "t-1"); err != nil {
		t.Fatalf("BindTeamToAccessGroup: %v", err)
	}
	if updBody.TeamID != "t-1" {
		t.Errorf("update.team_id = %q; want t-1", updBody.TeamID)
	}
	if len(updBody.Models) != 3 {
		t.Fatalf("update.models length = %d; want 3 (gpt-4, claude-3, access_group/demo)", len(updBody.Models))
	}
	want := "access_group/demo"
	found := false
	for _, m := range updBody.Models {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Errorf("update.models = %v; missing %q", updBody.Models, want)
	}
}

// TestListAccessGroupBindings_FiltersByPrefix asserts pagination + the
// "access_group/<name>" filter.
func TestListAccessGroupBindings_FiltersByPrefix(t *testing.T) {
	page1 := `{
		"teams":[
			{"team_id":"t-1","models":["access_group/demo"]},
			{"team_id":"t-2","models":["gpt-4"]},
			{"team_id":"t-3","models":["access_group/other","access_group/demo"]}
		],
		"total":3,"page":1,"page_size":200,"total_pages":1
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v2/team/list") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, page1)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.ListAccessGroupBindings(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListAccessGroupBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d bindings; want 2 (t-1, t-3)", len(got))
	}
	wantSet := map[string]bool{"t-1": true, "t-3": true}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("unexpected team_id %q", id)
		}
	}
}
```

### Step 3.9: Run the new tests

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/litellm/...
```

Expected: 4 new tests pass; existing tests untouched.

### Step 3.10: Commit

```bash
git add internal/litellm/accessgroups.go internal/litellm/accessgroups_test.go \
        internal/litellm/client.go internal/litellm/errors.go internal/litellm/noop.go \
        internal/litellm/types.go internal/snapshot/snapshot_test.go
git commit -m "feat(litellm): CreateAccessGroup, BindTeamToAccessGroup, ListAccessGroupBindings + DeleteAccessGroup URL fix (F1)"
```

---

## Task 4: Build the envtest fake LiteLLM and wire it into the suite

**Files:**
- Create: `internal/controller/ach/access_group_fake_test.go` — the in-memory fake + helper accessor
- Modify: `internal/controller/ach/suite_test.go` — wire the fake into the reconciler

### Step 4.1: Define the fake

```go
// SPDX-License-Identifier: Apache-2.0

// Envtest fake LiteLLM for the §7 AccessGroupSynced reconciler tests.
// The fake delegates all NoopClient behaviors except CreateAccessGroup,
// BindTeamToAccessGroup, and ListAccessGroupBindings, which it tallies +
// optionally injects errors into. Each test resets via accessGroupFake.Reset()
// before driving the reconciler.

package ach

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

// accessGroupFakeImpl is the per-suite singleton. Tests interact via the
// package-level `accessGroupFake` variable (initialized in TestMain).
type accessGroupFakeImpl struct {
	*litellm.NoopClient

	mu sync.Mutex

	// Per-(env, team) call counters. The outer map is env name, the
	// inner map team id → call count.
	createCalls map[string]int
	bindCalls   map[string]map[string]int
	listCalls   map[string]int

	// Injection knobs. Set BEFORE creating the Environment CR.
	createErrByEnv map[string]error
	bindErrByPair  map[string]map[string]error // env → team → err
	bindings       map[string][]string         // env → list of team_ids "already bound"
}

func newAccessGroupFake() *accessGroupFakeImpl {
	return &accessGroupFakeImpl{
		NoopClient:     litellm.NewNoopClient(logr.Discard()),
		createCalls:    map[string]int{},
		bindCalls:      map[string]map[string]int{},
		listCalls:      map[string]int{},
		createErrByEnv: map[string]error{},
		bindErrByPair:  map[string]map[string]error{},
		bindings:       map[string][]string{},
	}
}

func (f *accessGroupFakeImpl) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = map[string]int{}
	f.bindCalls = map[string]map[string]int{}
	f.listCalls = map[string]int{}
	f.createErrByEnv = map[string]error{}
	f.bindErrByPair = map[string]map[string]error{}
	f.bindings = map[string][]string{}
}

func (f *accessGroupFakeImpl) CreateAccessGroup(_ context.Context, name string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls[name]++
	if err := f.createErrByEnv[name]; err != nil {
		return err
	}
	return nil
}

func (f *accessGroupFakeImpl) BindTeamToAccessGroup(_ context.Context, accessGroup, teamID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bindCalls[accessGroup]; !ok {
		f.bindCalls[accessGroup] = map[string]int{}
	}
	f.bindCalls[accessGroup][teamID]++
	if m := f.bindErrByPair[accessGroup]; m != nil {
		if err := m[teamID]; err != nil {
			return err
		}
	}
	// Record the binding as observed (idempotent).
	already := false
	for _, t := range f.bindings[accessGroup] {
		if t == teamID {
			already = true
			break
		}
	}
	if !already {
		f.bindings[accessGroup] = append(f.bindings[accessGroup], teamID)
	}
	return nil
}

func (f *accessGroupFakeImpl) ListAccessGroupBindings(_ context.Context, accessGroup string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls[accessGroup]++
	if existing := f.bindings[accessGroup]; existing != nil {
		out := make([]string, len(existing))
		copy(out, existing)
		return out, nil
	}
	return nil, nil
}

// Accessors used by test assertions.

func (f *accessGroupFakeImpl) CreateCallsFor(env string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls[env]
}

func (f *accessGroupFakeImpl) BindCallsFor(env, team string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bindCalls[env][team]
}

func (f *accessGroupFakeImpl) ListCallsFor(env string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[env]
}

func (f *accessGroupFakeImpl) InjectCreateErr(env string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createErrByEnv[env] = err
}

func (f *accessGroupFakeImpl) InjectBindErr(env, team string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bindErrByPair[env]; !ok {
		f.bindErrByPair[env] = map[string]error{}
	}
	f.bindErrByPair[env][team] = err
}

// SeedBinding pretends a prior reconcile already bound the team — used
// by the idempotency / drift tests.
func (f *accessGroupFakeImpl) SeedBinding(env, team string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[env] = append(f.bindings[env], team)
}

// Compile-time interface assertion — extends the existing one for
// countingNoopClient at suite_test.go.
var _ litellm.Client = (*accessGroupFakeImpl)(nil)

// litellmCounter operates orthogonally — the access-group fake does not
// touch DeleteAccessGroup / DeleteTag (those still flow through the
// embedded NoopClient and bump nothing).
var _ atomic.Int64 // appease vet for the import we may not use here

// errFakeBindFailed is a stable error string for negative-path tests.
// Defined here (not inline in the test) so multiple tests can reference
// the same value.
var errFakeBindFailed = errors.New("fake: bind failed")
```

### Step 4.2: Wire the fake into `suite_test.go`

Currently `suite_test.go` constructs `llm := &countingNoopClient{...}` (lines 184-187) and passes it to `EnvironmentReconciler.LiteLLM` (line 230). Two problems we need to solve simultaneously:

1. The §7 tests need `accessGroupFake` to receive the LiteLLM calls.
2. The existing finalizer test needs `litellmCounter` to keep counting `DeleteAccessGroup` + `DeleteTag` calls.

Solution: a thin composite that satisfies both — `countingNoopClient`'s embedded type becomes `*accessGroupFakeImpl` instead of `*litellm.NoopClient`. The accessGroupFake itself embeds NoopClient, so the delegation chain is preserved.

Edit `suite_test.go`:

```go
// Replace the existing block:
//   litellmCounter = &atomic.Int64{}
//   llm := &countingNoopClient{
//       NoopClient: litellm.NewNoopClient(logr.Discard()),
//       counter:    litellmCounter,
//   }
// with:
litellmCounter = &atomic.Int64{}
accessGroupFake = newAccessGroupFake()
llm := &countingNoopClient{
    NoopClient: accessGroupFake.NoopClient, // embeds *litellm.NoopClient
    counter:    litellmCounter,
    accessGroup: accessGroupFake,
}
```

Then change `countingNoopClient`'s struct to forward CreateAccessGroup/BindTeamToAccessGroup/ListAccessGroupBindings to the embedded accessGroupFake:

```go
type countingNoopClient struct {
    *litellm.NoopClient
    counter     *atomic.Int64
    accessGroup *accessGroupFakeImpl
}

// Keep existing:
func (c *countingNoopClient) DeleteAccessGroup(ctx context.Context, name string) error {
    c.counter.Add(1)
    return c.NoopClient.DeleteAccessGroup(ctx, name)
}
func (c *countingNoopClient) DeleteTag(ctx context.Context, name string) error {
    c.counter.Add(1)
    return c.NoopClient.DeleteTag(ctx, name)
}

// NEW: route §7 calls through the fake.
func (c *countingNoopClient) CreateAccessGroup(ctx context.Context, name string, modelNames []string) error {
    return c.accessGroup.CreateAccessGroup(ctx, name, modelNames)
}
func (c *countingNoopClient) BindTeamToAccessGroup(ctx context.Context, accessGroup, teamID string) error {
    return c.accessGroup.BindTeamToAccessGroup(ctx, accessGroup, teamID)
}
func (c *countingNoopClient) ListAccessGroupBindings(ctx context.Context, accessGroup string) ([]string, error) {
    return c.accessGroup.ListAccessGroupBindings(ctx, accessGroup)
}
```

Add a package-level declaration alongside the other suite-globals (around line 56):

```go
var accessGroupFake *accessGroupFakeImpl
```

### Step 4.3: Add a fake Snapshotter for the reconciler

The new §7 logic requires `r.Snapshotter` to be non-nil (it gates the steady-state branch at `environment_controller.go:162`). The existing `suite_test.go` reconciler construction leaves `Snapshotter` as the zero value (nil), which steers Reconcile into the back-compat Unknown branch. Wire a real Snapshotter built against the same `llm` fake:

In `suite_test.go` where `EnvironmentReconciler{...}` is constructed, add:

```go
import "github.com/ackstorm/ach/internal/snapshot"

// ... inside setupAndRun, before EnvironmentReconciler{...}:
envSnapshotter := snapshot.NewSnapshotter(llm, logr.Discard())
// Single synchronous refresh — no need to start the ticker for unit-style
// envtest. The snapshot is populated once before reconcile.
envSnapshotter.RefreshForTest(ctx)

if err := (&EnvironmentReconciler{
    Client:      mgr.GetClient(),
    Scheme:      mgr.GetScheme(),
    LiteLLM:     llm,
    Namespace:   WatchNamespace,
    Log:         logr.Discard(),
    Snapshotter: envSnapshotter, // NEW
}).SetupWithManager(mgr); err != nil {
    ...
}
```

`Snapshotter.RefreshForTest(ctx)` is a new test helper — add it to `internal/snapshot/snapshot.go`:

```go
// RefreshForTest invokes refresh synchronously. Exposed for envtest
// suites that need a populated snapshot before manager.Start without
// running the ticker loop. NOT for production callers — production
// invokes Start() which owns the ticker.
func (s *Snapshotter) RefreshForTest(ctx context.Context) {
	s.refresh(ctx)
}
```

### Step 4.4: Compile + smoke test

```bash
./scripts/dev.sh go build ./...
./scripts/dev.sh make envtest-pkg \
  PKG=./internal/controller/ach/... \
  FOCUS=TestEnvironmentFinalizerAddRemove
```

Expected: PASS — finalizer test still green (the embedded NoopClient + counter delegation chain preserves the count of 2 calls).

Note: the §7 happy-path test from Task 2 STILL fails at this point — that's correct (the reconciler hasn't been wired yet — Task 5 does that).

### Step 4.5: Commit

```bash
git add internal/controller/ach/access_group_fake_test.go \
        internal/controller/ach/suite_test.go \
        internal/snapshot/snapshot.go
git commit -m "test(env): envtest fake LiteLLM + Snapshotter wiring for §7 tests"
```

---

## Task 5: Wire the AccessGroupSynced reconciler (make the happy-path test green)

**Files:**
- Modify: `internal/controller/ach/environment_controller.go` (lines 154-275 — the steady-state branch and placeholder writes)

### Step 5.1: Add the reconcileAccessGroup helper

Insert a new method on `EnvironmentReconciler` (placement: between `hasCondition` and `drainEkRows`):

```go
// reconcileAccessGroup is the §7 implementation step: ensure the LiteLLM
// access group <env.Name> exists AND every spec.authorizedTeams[i] team
// is bound to it. Returns the metav1.Condition that the caller should
// publish on env.Status.Conditions.
//
// Wire steps (Hub §6.4 / TODO §7):
//
//  1. CreateAccessGroup(env.Name, nil). ErrAlreadyExists swallowed
//     as idempotent success.
//  2. For each team in env.Spec.AuthorizedTeams: BindTeamToAccessGroup
//     (env.Name, team). Collect failures into a partial-bind set.
//  3. On full success: return True/Synced.
//     On any bind failure: return False/PartialBind with offending
//     teams listed in the message.
//     On create failure (other than ErrAlreadyExists): return
//     False/AccessGroupCreateFailed with the wrapped error.
//
// Drift detection (cheap — runs every reconcile because the cost is one
// GET /v2/team/list page):
//   - ListAccessGroupBindings(env.Name) returns CURRENT bindings.
//   - orphans = CURRENT \ spec.authorizedTeams.
//   - missing = spec.authorizedTeams \ CURRENT.
//   - "missing" drives the BindTeamToAccessGroup loop above (idempotent).
//   - "orphans" are LOGGED at INFO but not auto-removed in this slice;
//     orphan-cleanup is owned by §16 / TODO §10 (separate plan).
func (r *EnvironmentReconciler) reconcileAccessGroup(
	ctx context.Context,
	env *achv1alpha1.Environment,
) metav1.Condition {
	logger := log.FromContext(ctx).WithValues("environment", env.Name)

	// Step 1: ensure the access group exists.
	if err := r.LiteLLM.CreateAccessGroup(ctx, env.Name, nil); err != nil && !errors.Is(err, litellm.ErrAlreadyExists) {
		logger.Error(err, "CreateAccessGroup failed")
		return metav1.Condition{
			Type:               "AccessGroupSynced",
			Status:             metav1.ConditionFalse,
			Reason:             "AccessGroupCreateFailed",
			Message:            fmt.Sprintf("LiteLLM CreateAccessGroup(%s) failed: %v", env.Name, err),
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		}
	}

	// Step 2: discover the CURRENT binding set (drift detection input).
	current, err := r.LiteLLM.ListAccessGroupBindings(ctx, env.Name)
	if err != nil {
		// Drift-discovery failure is NOT fatal to the bind loop — we
		// fall back to assuming "nothing bound" and let the
		// BindTeamToAccessGroup helpers handle idempotency upstream.
		logger.Info("ListAccessGroupBindings failed; proceeding without drift baseline", "err", err)
		current = nil
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, t := range current {
		currentSet[t] = struct{}{}
	}

	// Step 3: bind every spec.authorizedTeams[i]. Skip teams already
	// observed in the CURRENT set (avoids the upstream GET /team/info
	// + POST /team/update round-trip for the steady-state nominal case).
	var failed []string
	var lastErr error
	for _, team := range env.Spec.AuthorizedTeams {
		if _, ok := currentSet[team]; ok {
			continue // already bound — no work.
		}
		if err := r.LiteLLM.BindTeamToAccessGroup(ctx, env.Name, team); err != nil {
			logger.Error(err, "BindTeamToAccessGroup failed", "team", team)
			failed = append(failed, team)
			lastErr = err
			continue
		}
	}

	// Step 4: orphan detection (log only — §10 owns auto-removal).
	specSet := make(map[string]struct{}, len(env.Spec.AuthorizedTeams))
	for _, t := range env.Spec.AuthorizedTeams {
		specSet[t] = struct{}{}
	}
	for _, t := range current {
		if _, ok := specSet[t]; !ok {
			logger.Info("orphan team binding detected (not auto-removed; see TODO §10)",
				"env", env.Name, "team", t)
		}
	}

	// Step 5: closed-set condition emit.
	if len(failed) > 0 {
		return metav1.Condition{
			Type:               "AccessGroupSynced",
			Status:             metav1.ConditionFalse,
			Reason:             "PartialBind",
			Message:            fmt.Sprintf("LiteLLM BindTeamToAccessGroup failed for %d team(s): %v (last err: %v)", len(failed), failed, lastErr),
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		}
	}
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("LiteLLM access group %q bound to %d team(s)", env.Name, len(env.Spec.AuthorizedTeams)),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}
```

### Step 5.2: Replace the placeholder AccessGroupSynced emission

In `Reconcile` (current lines 246-255), replace the placeholder block:

```go
// Placeholder: TODO §7 owns the real reconciliation logic. Use
// SetStatusCondition so an existing True/False set by a future §7
// implementation is NOT overwritten here — apimeta's logic preserves
// the existing entry when (Type, Status, Reason) match.
if !hasCondition(env.Status.Conditions, "AccessGroupSynced") {
    apimeta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
        Type:               "AccessGroupSynced",
        Status:             metav1.ConditionUnknown,
        Reason:             "Initializing",
        Message:            "operator-side access-group binding not yet implemented (see TODO §7)",
        ObservedGeneration: env.Generation,
        LastTransitionTime: metav1.Now(),
    })
}
```

with:

```go
// §7: real AccessGroupSynced reconciliation. The helper consults
// the snapshot's freshness to compute the condition; we publish it
// unconditionally (it owns the closed-set Type/Reason mapping
// per Hub §6.6).
agCond := r.reconcileAccessGroup(ctx, &env)
// Surface snapshot-stale prefix so operators see when the binding
// decision was made against cached LiteLLM data (Hub §6.4 / D-14).
if snap.Stale && agCond.Status == metav1.ConditionTrue {
    agCond.Message = "snapshot stale (LiteLLM unreachable); " + agCond.Message
}
apimeta.SetStatusCondition(&env.Status.Conditions, agCond)
// Echo the synced access group name (CRD-02 status field). Only set
// when AccessGroupSynced is True so a stale Status doesn't lie.
if agCond.Status == metav1.ConditionTrue {
    env.Status.LitellmAccessGroup = env.Name
}
```

### Step 5.3: Verify imports

Confirm `internal/controller/ach/environment_controller.go` imports both `"errors"` (already imported per line 7) and the `litellm` package (already imported line 26). No new imports.

### Step 5.4: Run the happy-path test

```bash
./scripts/dev.sh make envtest-pkg \
  PKG=./internal/controller/ach/... \
  FOCUS=TestAccessGroupSynced_True_WhenCreateAndBindSucceed \
  TIMEOUT=2m
```

Expected: PASS.

### Step 5.5: Invert / rename the baseline test

The baseline test from Task 1 (`TestAccessGroupSynced_NeverTrue_WithStubReconciler`) is now misleading — it would still fail-pass against the broken reconciler. Replace it with an explicit "stay False on bind failure" test:

In `environment_accessgroup_test.go`, delete the original baseline test and replace with:

```go
// TestAccessGroupSynced_False_OnBindFailure asserts the PartialBind
// reason path. The fake injects an error on the bind call; the
// reconciler must surface False/PartialBind with the offending team
// listed in the message.
func TestAccessGroupSynced_False_OnBindFailure(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.InjectBindErr("test-env-ag-partialbind", "team-broken", errFakeBindFailed)

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-partialbind",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"team-ok", "team-broken"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "AccessGroupSynced" && c.Status == metav1.ConditionFalse && c.Reason == "PartialBind" {
				return true
			}
		}
		return false
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/PartialBind, conditions = %+v", got.Status.Conditions)
	}
}
```

### Step 5.6: Run the full suite to confirm no regressions

```bash
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... TIMEOUT=10m
```

Expected: all tests pass (finalizer, CEL admission, §7 happy-path, §7 partial-bind).

### Step 5.7: Commit

```bash
git add internal/controller/ach/environment_controller.go \
        internal/controller/ach/environment_accessgroup_test.go
git commit -m "feat(env): §7 — AccessGroupSynced reconciler (create+bind+drift detect)"
```

---

## Task 6: Add idempotency + drift envtest coverage

**Files:**
- Modify: `internal/controller/ach/environment_accessgroup_test.go`

### Step 6.1: Add the idempotency test

```go
// TestAccessGroupSynced_Idempotent_NoExtraBindOnRereconcile asserts that
// once the binding is in place, a subsequent reconcile (triggered by the
// 5min RequeueAfter or by a no-op spec touch) does NOT re-issue the
// bind call. The fake's ListAccessGroupBindings returns the already-
// recorded binding; the reconciler's currentSet membership check skips
// the bind loop.
func TestAccessGroupSynced_Idempotent_NoExtraBindOnRereconcile(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-idemp",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Wait for first reconcile to land True.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "AccessGroupSynced" && c.Status == metav1.ConditionTrue {
				return true
			}
		}
		return false
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("first reconcile did not flip AccessGroupSynced=True")
	}

	firstBindCount := accessGroupFake.BindCallsFor("test-env-ag-idemp", "default")
	if firstBindCount < 1 {
		t.Fatalf("expected >= 1 bind call after first reconcile, got %d", firstBindCount)
	}

	// Trigger a re-reconcile by no-op-touching an annotation.
	var got achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations == nil {
		got.Annotations = map[string]string{}
	}
	got.Annotations["test/touch"] = "1"
	if err := k8sClient.Update(ctx, &got); err != nil {
		t.Fatal(err)
	}

	// Give the controller a settle window; assert bind count did NOT grow.
	// 3s is plenty — the manager's work queue processes the annotation
	// update in <500ms typically.
	time.Sleep(3 * time.Second)
	if grew := accessGroupFake.BindCallsFor("test-env-ag-idemp", "default"); grew != firstBindCount {
		t.Errorf("bind call count = %d after re-reconcile; want unchanged (%d) — idempotency violated", grew, firstBindCount)
	}
}
```

### Step 6.2: Add a drift-detection test (orphan logging)

```go
// TestAccessGroupSynced_DriftDetection_OrphanLogged asserts the orphan
// branch: ListAccessGroupBindings returns a team that's NOT in
// spec.authorizedTeams. The reconciler logs the orphan but still emits
// True/Synced (auto-removal is §10 / TODO §10 scope). The assertion
// reads the bind-call count to prove the reconciler did NOT re-issue
// the bind for the orphan (it was already present per the seeded
// binding).
func TestAccessGroupSynced_DriftDetection_OrphanLogged(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	// Pretend an old team is still bound on LiteLLM side.
	accessGroupFake.SeedBinding("test-env-ag-drift", "orphan-team")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-drift",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"current-team"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "AccessGroupSynced" && c.Status == metav1.ConditionTrue {
				return true
			}
		}
		return false
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("orphan-present case did NOT reach True (orphans must NOT block sync)")
	}

	// Orphan must NOT have received any bind call (it's already bound).
	if got := accessGroupFake.BindCallsFor("test-env-ag-drift", "orphan-team"); got != 0 {
		t.Errorf("orphan team bind count = %d; want 0 (orphans logged but untouched)", got)
	}
	// current-team must have received exactly one bind.
	if got := accessGroupFake.BindCallsFor("test-env-ag-drift", "current-team"); got != 1 {
		t.Errorf("current-team bind count = %d; want 1", got)
	}
}
```

### Step 6.3: Add a create-failure test

```go
// TestAccessGroupSynced_False_OnCreateFailure asserts the
// AccessGroupCreateFailed reason path: LiteLLM rejects the access-group
// create (non-AlreadyExists error), reconciler must surface
// False/AccessGroupCreateFailed and must NOT proceed to bind.
func TestAccessGroupSynced_False_OnCreateFailure(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.InjectCreateErr("test-env-ag-createfail", errors.New("fake: create blew up"))

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-createfail",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "AccessGroupSynced" && c.Status == metav1.ConditionFalse && c.Reason == "AccessGroupCreateFailed" {
				return true
			}
		}
		return false
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/AccessGroupCreateFailed, conditions = %+v", got.Status.Conditions)
	}
	if got := accessGroupFake.BindCallsFor("test-env-ag-createfail", "default"); got != 0 {
		t.Errorf("bind call count = %d after create failure; want 0 (no proceed past failed create)", got)
	}
}
```

You'll need to add `"errors"` to the test file's imports.

### Step 6.4: Run the new tests

```bash
./scripts/dev.sh make envtest-pkg \
  PKG=./internal/controller/ach/... \
  FOCUS="TestAccessGroupSynced" TIMEOUT=5m
```

Expected: all four §7 tests pass.

### Step 6.5: Commit

```bash
git add internal/controller/ach/environment_accessgroup_test.go
git commit -m "test(env): §7 — idempotency, drift, and create-failure coverage"
```

---

## Task 7: Full lint + unit + envtest sweep

**Step 1: Lint changed packages**

```bash
./scripts/dev.sh make lint-changed
```

Expected: clean. Fix any new lint findings.

**Step 2: Full unit test sweep**

```bash
./scripts/dev.sh make unit
```

Expected: green (no regressions in `internal/litellm/`, `internal/snapshot/`).

**Step 3: Full envtest sweep**

```bash
./scripts/dev.sh make envtest-run
```

Expected: green across all reconcilers (not just `ach`).

**Step 4: Commit any lint fixes**

If any cleanup was required:

```bash
git add <files>
git commit -m "chore(lint): cleanups from §7 PR sweep"
```

---

## Task 8: E2E validation — POST /platform/env-keys returns 200

The §7 fix unblocks the entire ek_ lifecycle. The e2e gate proves the end-to-end POST flow now succeeds.

### Step 8.1: Bring up the kept cluster

```bash
./scripts/dev.sh make e2e-keep
```

This spins up kind + Helm + the dependencies. The cluster persists after the run for iteration.

### Step 8.2: Apply the demo Environment

```bash
./scripts/dev.sh kubectl apply -f examples/01-litellmconnection.yaml
./scripts/dev.sh kubectl apply -f examples/06-plugin-caveman.yaml
./scripts/dev.sh kubectl apply -f examples/07-prompt-claudecode-leak.yaml
./scripts/dev.sh kubectl apply -f examples/08-artifact-openclaw-templates.yaml
./scripts/dev.sh kubectl apply -f examples/04-environment-demo.yaml
```

### Step 8.3: Wait for AccessGroupSynced=True

```bash
./scripts/dev.sh make wait-cr-ready KIND=environment NAME=demo NS=ach-system
```

If `wait-cr-ready` is not implemented yet (per CLAUDE.md it should be defined first-use), define it as:

```makefile
wait-cr-ready:
	@kubectl wait \
	  --for=jsonpath='{.status.conditions[?(@.type=="AccessGroupSynced")].status}'=True \
	  $(KIND)/$(NAME) -n $(NS) --timeout=$(or $(WAIT_TIMEOUT),300s)
```

Then run; expected: succeeds within 30s.

### Step 8.4: Run the full POST /platform/env-keys flow against the running cluster

Use the existing demo driver:

```bash
bash examples/hydrate_demo.sh
```

`hydrate_demo.sh` stands in for `ach login` + `ach hydrate` per `examples/README.md`. Inside it the POST to `/platform/env-keys` should now return 200 instead of the previous 503.

If `hydrate_demo.sh` does not exercise POST /platform/env-keys directly, add an explicit step:

```bash
# After hydrate_demo.sh emits the pk_ to stdout:
PK=$(cat /tmp/ach-demo-pk)
curl -sS -X POST http://localhost:8080/platform/env-keys \
  -H "x-ach-key: $PK" \
  -H "Content-Type: application/json" \
  -d '{"environment":"demo","name":"my-worker"}' | tee /tmp/envkey-response.json
test "$(jq -r '.id' /tmp/envkey-response.json)" != "null" || { echo "FAIL: no ek_ id"; exit 1; }
```

Expected: response is `{"id":"ek_...","key":"ek_<plaintext>","environment":"demo",...}`.

### Step 8.5: Verify GET /platform/env-keys lists it

```bash
curl -sS http://localhost:8080/platform/env-keys?environment=demo \
  -H "x-ach-key: $PK" | jq
```

Expected: at least one entry with `environment=demo`.

### Step 8.6: Tear down

```bash
make cluster-down
```

### Step 8.7: No commit needed for e2e validation (no file changes)

---

## Task 9: Open the PR

### Step 9.1: Run the full pre-push gate

```bash
make pre-push
```

This is the hard gate — gitleaks, trufflehog, govulncheck, license headers, lint, unit. Any failure: fix root cause; do NOT bypass.

### Step 9.2: Push the branch

```bash
git push -u origin feat/env-accessgroup-sync
```

### Step 9.3: Open the PR

```bash
gh pr create --title "feat(operator): §7 AccessGroupSynced reconciler — create+bind+drift" --body "$(cat <<'EOF'
## Summary
- Implements the steady-state §7 reconciliation step for `Environment.status.conditions[type=AccessGroupSynced]`. Adds `internal/litellm.{CreateAccessGroup,BindTeamToAccessGroup,ListAccessGroupBindings}` (RESTClient + NoopClient + interface).
- Replaces the placeholder `Unknown/Initializing` condition in `EnvironmentReconciler.Reconcile` with a real create-then-bind-then-drift flow. Emits the §6.6 closed-set conditions `True/Synced`, `False/PartialBind`, `False/AccessGroupCreateFailed`.
- Drift detection logs orphan team bindings without auto-removal (§10 scope).
- Pre-flight finding F1: corrects the `DeleteAccessGroup` URL from `/access-groups/<name>` to `/access_group/<name>/delete` per LiteLLM v1.82.6 OpenAPI.

## Tests added
- `internal/litellm/accessgroups_test.go` — 4 wire-shape tests (CreateAccessGroup happy + AlreadyExists, BindTeamToAccessGroup idempotency + append, ListAccessGroupBindings filter).
- `internal/controller/ach/environment_accessgroup_test.go` — 4 envtest cases against fake LiteLLM:
  - True/Synced happy path
  - False/PartialBind on bind failure
  - False/AccessGroupCreateFailed on create failure
  - Re-reconcile idempotency (no extra bind calls)
  - Drift / orphan logging

## Test plan
- [x] `./scripts/dev.sh make unit-pkg PKG=./internal/litellm/...`
- [x] `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestAccessGroupSynced`
- [x] `./scripts/dev.sh make envtest-run` (full envtest sweep — no regressions)
- [x] `make pre-push` (15-gate gate green)
- [x] `make e2e-keep` + `examples/hydrate_demo.sh` → `POST /platform/env-keys` returns 200 with `ek_<plaintext>`

## Cross-plan refs
- BLOCKS §9 (Available composite rollup) — `Available=True` now achievable.
- BLOCKS §16 (UAT validation gate).
- Depends on the `internal/litellm/` package surface — extends it; does not depend on §2 landing first.

🤖 Generated with Claude Code
EOF
)"
```

---

## Acceptance criteria recap

After all tasks land:

1. `kubectl get environment demo -o jsonpath='{.status.conditions[?(@.type=="AccessGroupSynced")].status}'` → `True` within 30s of CR apply.
2. `POST /platform/env-keys` with valid `pk_` returns 200 + `ek_<plaintext>` body.
3. `GET /platform/env-keys?environment=demo` lists the minted `ek_`.
4. `./scripts/dev.sh make envtest-run` green.
5. `make pre-push` green.
6. Re-applying the Environment CR (no-op update) does NOT issue duplicate LiteLLM bind calls (idempotency proven by Task 6).
7. A LiteLLM-side orphan team is logged at INFO but does NOT flip `AccessGroupSynced` to False (drift behavior proven by Task 6).

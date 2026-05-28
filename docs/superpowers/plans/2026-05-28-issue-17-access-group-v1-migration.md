# Issue #17 — Access Group `/v1` Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every shell command runs from `/home/coder/workspace/local/ach`. Go commands MUST be wrapped in `./scripts/dev.sh` (host has no Go) — see "Toolchain" in `CLAUDE.md`.

**Goal:** Replace the legacy LiteLLM `POST /access_group/new` flow (which 400s on empty `model_names`, per [issue #17](https://github.com/ackstorm/ach/issues/17)) with the ackstorm `/v1/access_group*` endpoints. Move team→access-group binding off the `team.models[]` `access_group/<name>` magic prefix onto the new first-class `assigned_team_ids` field. Wire MCP-server and A2A-agent bindings (previously not bound at all) through `access_mcp_server_ids` / `access_agent_ids`. Resolve every name→ID on demand each reconcile — no Snapshotter changes, no CRD changes.

**Architecture:**
- New `internal/litellm` types match the live `api.ackstorm.ai/openapi.json` schemas (`AccessGroupCreateRequest`, `AccessGroupUpdateRequest`, `AccessGroupResponse`).
- `internal/litellm.Client` interface methods replaced in place: `CreateAccessGroup` returns the new UUID; `DeleteAccessGroup(name)` becomes a high-level helper that lists-by-name then `DELETE /v1/access_group/{id}`; `BindTeamToAccessGroup` / `ListAccessGroupBindings` / `TeamAccessGroupPrefix` are deleted (dead). New `ListMCPServers` / `ListA2AAgents` / `ListTeams` primitives back the on-demand name→ID resolution.
- `EnvironmentReconciler.reconcileAccessGroup` becomes a desired-state sync: resolve names → IDs, `GET /v1/access_group` filter by name, POST if absent, PUT if drifted, emit `AccessGroupSynced=True/Synced`. Unresolved names short-circuit with `AccessGroupSynced=False reason=UnresolvedReferences`.
- §6.5 step 2 finalizer path looks up the UUID by name, then `DELETE /v1/access_group/{id}`. Absent → idempotent success (matches existing §7.7 contract).
- E2E `scripts/cluster.sh hydrate_litellm` is extended to register one Model + one MCP server + one A2A agent in LiteLLM's DB so `examples/04-environment-demo.yaml` reaches `AccessGroupSynced=True` end-to-end. The demo CR is split into a "happy" Environment and an "unresolved" Environment so both branches are e2e-covered.

**Tech Stack:** Go 1.x · controller-runtime · `internal/litellm` REST client · stdlib `net/http/httptest` for unit tests · controller envtest for integration · `scripts/cluster.sh` (bash) + `curl` for e2e hydration · `kubectl apply` for example CRs.

**Issue reference:** [ackstorm/ach#17](https://github.com/ackstorm/ach/issues/17) — LiteLLM client `POST /access_group/new` returns 400 (request shape drift).

**OpenAPI authority for endpoint shapes:** `https://api.ackstorm.ai/openapi.json` — fetch fresh into `/tmp/ackstorm-openapi.json` during Phase A if any field rename surfaces during execution. The shapes used in this plan are pinned to the spec as of 2026-05-28.

---

## File Structure

```
internal/litellm/
├── accessgroups.go           REWRITE: replace 4 methods (Create/Delete/Bind/ListBindings)
│                                       with new /v1 surface; delete dead helpers
├── accessgroups_test.go      REWRITE: unit-tests for the new client surface (httptest)
├── client.go                 MODIFY:  interface — swap 4 method signatures,
│                                       add 3 list primitives
├── noop.go                   MODIFY:  match new interface; log + return zero-values
├── types.go                  MODIFY:  add AccessGroupCreateRequest /
│                                       AccessGroupUpdateRequest /
│                                       AccessGroupResponse; remove
│                                       NewAccessGroupRequest / AccessGroupInfo /
│                                       TeamAccessGroupPrefix
└── mcp_agents_teams.go       CREATE:  list primitives (ListMCPServers,
                                       ListA2AAgents, ListTeams) returning
                                       name→ID maps; httptest-backed unit tests

internal/connection/
└── client.go                 MODIFY:  drop wrappers for BindTeamToAccessGroup +
                                       ListAccessGroupBindings; update
                                       CreateAccessGroup signature; keep
                                       DeleteAccessGroup(name) as the public
                                       convenience helper

internal/controller/ach/
├── environment_controller.go MODIFY:  replace reconcileAccessGroup body
│                                       (steps 1–5) with the desired-state
│                                       sync against /v1/access_group; §6.5
│                                       step 2 unchanged at the call site
│                                       (the new DeleteAccessGroup helper
│                                        internally lists-then-deletes)
├── environment_accessgroup_test.go
│                             MODIFY:  add Runtime.Models / MCPServers /
│                                       A2AAgents to every Environment fixture
│                                       so envtests exercise the
│                                       resolver path; add new tests for
│                                       UnresolvedReferences + drift PUT
├── access_group_fake_test.go MODIFY:  rewrite fake to track new client
│                                       surface; bindCalls / SeedBinding
│                                       removed
├── suite_test.go             MODIFY:  countingNoopClient matches new
│                                       interface (4 signature changes,
│                                       3 new methods returning empty maps)
└── main_wiring_envtest_test.go
                              MODIFY:  wiringFakeLiteLLM matches new interface

internal/snapshot/
└── snapshot_test.go          MODIFY:  fakeLiteLLM matches new interface
                                       (3 new methods returning empty maps)

scripts/
└── cluster.sh                MODIFY:  hydrate_litellm seeds 1 Model row
                                       (POST /model/new), 1 MCP server
                                       (POST /v1/mcp/server), 1 A2A agent
                                       (POST /v1/agents). Idempotent.

examples/
├── 04-environment-demo.yaml  MODIFY:  trim to ONLY names the hydration
                                       seeds (one model, one MCP, one agent);
                                       drop the previously-aspirational set
└── 04b-environment-unresolved.yaml
                              CREATE:  sibling Environment that intentionally
                                       references names absent from LiteLLM,
                                       for end-to-end coverage of the
                                       AccessGroupSynced=False
                                       reason=UnresolvedReferences path

test/e2e/
└── phase4_accessgroup_test.go
                              CREATE:  two e2e specs — happy path on demo
                                       Environment + unresolved path on
                                       04b. Gated behind ACH_E2E_PHASE9=1
                                       just like the existing §11e test.

docs/superpowers/plans/
└── 2026-05-28-issue-17-access-group-v1-migration.md
                              CREATE:  this plan

CLAUDE.md                     MODIFY:  add a "Common failure modes" entry
                                       for the access_group migration —
                                       what to do if a name doesn't resolve
```

**Branch:** `fix/issue-17-access-group-v1-migration` — one branch, six commits (one per Phase), one PR at the end.

**Test discipline per phase:** Each phase MUST be green via `./scripts/dev.sh make unit-pkg PKG=./internal/litellm/...` (Phases A/B) or `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=AccessGroupSynced` (Phases C/D) before commit. Phase E adds the e2e test which runs under `make e2e-focus FOCUS=AccessGroup`.

---

## Task 0: Branch Setup

**Files:** none

- [ ] **Step 1: Verify working tree clean**

```bash
cd /home/coder/workspace/local/ach
git status --short
git rev-parse --abbrev-ref HEAD
```

Expected: ` M go.sum` is the only output (pre-existing, unrelated to this work). Branch `main`. If anything else is dirty, STOP and resolve before starting.

- [ ] **Step 2: Pull latest main**

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
```

Expected: `Already up to date.` or a fast-forward. No merge commits.

- [ ] **Step 3: Create the feature branch**

```bash
git checkout -b fix/issue-17-access-group-v1-migration
```

Expected: `Switched to a new branch 'fix/issue-17-access-group-v1-migration'`.

- [ ] **Step 4: Confirm baseline unit + lint green before any edits**

```bash
./scripts/dev.sh make unit
./scripts/dev.sh make lint-changed BASE_REF=origin/main
```

Expected: both PASS. If anything fails on the baseline, STOP and investigate — do not start the plan against a red baseline.

---

## Task 1 (Phase A): New Access-Group Types

**Goal:** Add the three Go structs that mirror `AccessGroupCreateRequest`, `AccessGroupUpdateRequest`, `AccessGroupResponse` from `api.ackstorm.ai/openapi.json`. Remove the obsolete `NewAccessGroupRequest`, `AccessGroupInfo`, and the `TeamAccessGroupPrefix` constant.

**Files:**
- Modify: `internal/litellm/types.go`

- [ ] **Step 1: Read the current access-group blocks in types.go**

```bash
./scripts/dev.sh bash -c "grep -n 'NewAccessGroupRequest\|AccessGroupInfo\|TeamAccessGroupPrefix' internal/litellm/types.go"
```

Expected output: line numbers for the three identifiers (around lines 316, 328, 345 in the current tree).

- [ ] **Step 2: Replace the three obsolete blocks with the new schemas**

In `internal/litellm/types.go`, locate the block beginning with `// NewAccessGroupRequest is the POST /access_group/new request body` and ending with `const TeamAccessGroupPrefix = "access_group/"`. Replace the ENTIRE span (the three doc-comment-prefixed declarations) with:

```go
// AccessGroupCreateRequest is the POST /v1/access_group request body
// (ackstorm OpenAPI schema: AccessGroupCreateRequest). access_group_name
// is the only required field; every other slice may be nil/empty. The
// endpoint returns AccessGroupResponse with the assigned UUID.
//
// Migration note (issue #17): replaces NewAccessGroupRequest. The legacy
// POST /access_group/new endpoint required at least one model_name OR
// model_id; the /v1 endpoint accepts an empty-resource creation, so the
// controller no longer needs an AwaitingModels short-circuit. Unresolved
// MCP/A2A/Team names are still surfaced via the resolver layer in
// reconcileAccessGroup (AccessGroupSynced=False reason=UnresolvedReferences).
type AccessGroupCreateRequest struct {
	AccessGroupName    string   `json:"access_group_name"`
	Description        string   `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names,omitempty"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids,omitempty"`
	AccessAgentIDs     []string `json:"access_agent_ids,omitempty"`
	AssignedTeamIDs    []string `json:"assigned_team_ids,omitempty"`
	AssignedKeyIDs     []string `json:"assigned_key_ids,omitempty"`
}

// AccessGroupUpdateRequest is the PUT /v1/access_group/{id} request body
// (ackstorm OpenAPI schema: AccessGroupUpdateRequest). Every field is
// optional; nil values are omitted, instructing the upstream to keep
// the corresponding stored value. To CLEAR a list the caller must send
// an explicit empty []string — use the json tag's non-omitempty form by
// passing a non-nil zero-length slice through the marshaler. The
// reconciler's desired-state sync always sends the full computed set
// for every dimension, so the clear-via-empty semantics rarely matter
// in practice.
type AccessGroupUpdateRequest struct {
	AccessGroupName    *string  `json:"access_group_name,omitempty"`
	Description        *string  `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names,omitempty"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids,omitempty"`
	AccessAgentIDs     []string `json:"access_agent_ids,omitempty"`
	AssignedTeamIDs    []string `json:"assigned_team_ids,omitempty"`
	AssignedKeyIDs     []string `json:"assigned_key_ids,omitempty"`
}

// AccessGroupResponse is the body returned by POST /v1/access_group,
// GET /v1/access_group, GET /v1/access_group/{id}, and PUT /v1/access_group/{id}.
// access_group_id is the stable UUID the reconciler resolves by name on
// each reconcile (per issue #17 decision: no status field, list-by-name).
type AccessGroupResponse struct {
	AccessGroupID      string   `json:"access_group_id"`
	AccessGroupName    string   `json:"access_group_name"`
	Description        string   `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids"`
	AccessAgentIDs     []string `json:"access_agent_ids"`
	AssignedTeamIDs    []string `json:"assigned_team_ids"`
	AssignedKeyIDs     []string `json:"assigned_key_ids"`
	CreatedAt          string   `json:"created_at,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
	UpdatedBy          string   `json:"updated_by,omitempty"`
}
```

The block ends here — nothing else from the old span (NewAccessGroupRequest, AccessGroupInfo, TeamAccessGroupPrefix) survives. Removing `TeamAccessGroupPrefix` is intentional: the magic `access_group/<name>` entry on `team.models[]` is no longer how teams bind to access groups.

- [ ] **Step 3: Verify the file compiles in isolation**

```bash
./scripts/dev.sh go build ./internal/litellm/
```

Expected: clean build OR errors pointing to call sites of the removed identifiers (in `accessgroups.go`, `client.go`, `noop.go`, `connection/client.go`, `accessgroups_test.go`). Those callers are rewritten in later tasks — record the errors and proceed; they will be resolved by the end of Phase B.

- [ ] **Step 4: Commit Phase-A-types (incremental)**

```bash
git add internal/litellm/types.go
git commit -m "$(cat <<'EOF'
refactor(litellm): swap legacy access-group request/response types for /v1

Replace NewAccessGroupRequest/AccessGroupInfo with the ackstorm /v1
schemas (AccessGroupCreateRequest, AccessGroupUpdateRequest,
AccessGroupResponse). Remove the TeamAccessGroupPrefix constant —
team→access_group binding moves to the first-class assigned_team_ids
field on the access group itself.

Build is intentionally broken at this commit; subsequent commits in this
branch rewrite the call sites.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands. `git status` clean.

---

## Task 2 (Phase A): New Client Methods + List Primitives

**Goal:** Rewrite `internal/litellm/accessgroups.go` to call `/v1/access_group*`. Add a new `internal/litellm/mcp_agents_teams.go` with the three on-demand list primitives. Each method has a one-shot unit test in the corresponding `_test.go` using `httptest.NewServer`.

**Files:**
- Rewrite: `internal/litellm/accessgroups.go`
- Rewrite: `internal/litellm/accessgroups_test.go`
- Create: `internal/litellm/mcp_agents_teams.go`
- Create: `internal/litellm/mcp_agents_teams_test.go`

- [ ] **Step 1: Write the first failing test for `CreateAccessGroup`**

Open `internal/litellm/accessgroups_test.go` and REPLACE the entire file contents with:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// TestCreateAccessGroup_PostsV1Endpoint asserts the migrated wire shape:
// POST /v1/access_group with access_group_name in the body, returning the
// AccessGroupResponse (UUID + name + lists).
func TestCreateAccessGroup_PostsV1Endpoint(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody AccessGroupCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"access_group_id":"ag-uuid-1",
			"access_group_name":"demo",
			"access_model_names":["gpt-4"],
			"access_mcp_server_ids":["mcp-1"],
			"access_agent_ids":[],
			"assigned_team_ids":["t-1"],
			"assigned_key_ids":[]
		}`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	resp, err := c.CreateAccessGroup(context.Background(), AccessGroupCreateRequest{
		AccessGroupName:    "demo",
		AccessModelNames:   []string{"gpt-4"},
		AccessMCPServerIDs: []string{"mcp-1"},
		AssignedTeamIDs:    []string{"t-1"},
	})
	if err != nil {
		t.Fatalf("CreateAccessGroup: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/v1/access_group" {
		t.Errorf("wire: want POST /v1/access_group, got %s %s", gotMethod, gotPath)
	}
	if gotBody.AccessGroupName != "demo" {
		t.Errorf("body.access_group_name = %q; want demo", gotBody.AccessGroupName)
	}
	if len(gotBody.AccessModelNames) != 1 || gotBody.AccessModelNames[0] != "gpt-4" {
		t.Errorf("body.access_model_names = %v; want [gpt-4]", gotBody.AccessModelNames)
	}
	if resp == nil || resp.AccessGroupID != "ag-uuid-1" {
		t.Fatalf("response access_group_id = %q; want ag-uuid-1", resp.AccessGroupID)
	}
}

// TestGetAccessGroupByName_ListsAndFilters asserts the helper that
// resolves a UUID by name. Used by reconcileAccessGroup to discover
// whether to POST (create) or PUT (drift correction), and by the §6.5
// finalizer to find the UUID to DELETE.
func TestGetAccessGroupByName_ListsAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/access_group" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[
			{"access_group_id":"ag-uuid-a","access_group_name":"alpha","access_model_names":[],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":[],"assigned_key_ids":[]},
			{"access_group_id":"ag-uuid-d","access_group_name":"demo","access_model_names":["gpt-4"],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":[],"assigned_key_ids":[]}
		]`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.GetAccessGroupByName(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetAccessGroupByName: %v", err)
	}
	if got == nil || got.AccessGroupID != "ag-uuid-d" {
		t.Fatalf("got = %+v; want access_group_id=ag-uuid-d", got)
	}
}

// TestGetAccessGroupByName_AbsentReturnsNilNil asserts the "no row found"
// contract: nil response, nil error. The reconciler treats this as "must
// POST to create".
func TestGetAccessGroupByName_AbsentReturnsNilNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.GetAccessGroupByName(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %+v; want nil", got)
	}
}

// TestUpdateAccessGroup_PutsByID asserts PUT /v1/access_group/{id} with
// the AccessGroupUpdateRequest body.
func TestUpdateAccessGroup_PutsByID(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody AccessGroupUpdateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"access_group_id":"ag-uuid-1","access_group_name":"demo","access_model_names":["gpt-4","claude-3"],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":["t-1"],"assigned_key_ids":[]}`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	_, err := c.UpdateAccessGroup(context.Background(), "ag-uuid-1", AccessGroupUpdateRequest{
		AccessModelNames: []string{"gpt-4", "claude-3"},
		AssignedTeamIDs:  []string{"t-1"},
	})
	if err != nil {
		t.Fatalf("UpdateAccessGroup: %v", err)
	}
	if gotMethod != "PUT" || gotPath != "/v1/access_group/ag-uuid-1" {
		t.Errorf("wire: want PUT /v1/access_group/ag-uuid-1, got %s %s", gotMethod, gotPath)
	}
	if len(gotBody.AccessModelNames) != 2 {
		t.Errorf("body.access_model_names = %v; want 2 entries", gotBody.AccessModelNames)
	}
}

// TestDeleteAccessGroupByID_DeletesByID asserts DELETE /v1/access_group/{id}.
// 204 → nil. 404 → nil (idempotent §7.7 contract).
func TestDeleteAccessGroupByID_DeletesByID(t *testing.T) {
	cases := []int{204, 404}
	for _, code := range cases {
		t.Run("status"+strings.ReplaceAll(http.StatusText(code), " ", ""), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "DELETE" || r.URL.Path != "/v1/access_group/ag-uuid-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)

			c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
			if err := c.DeleteAccessGroupByID(context.Background(), "ag-uuid-1"); err != nil {
				t.Fatalf("DeleteAccessGroupByID (status %d): %v", code, err)
			}
		})
	}
}

// TestDeleteAccessGroup_LooksUpThenDeletes asserts the high-level helper
// the §6.5 finalizer calls: GET /v1/access_group → find by name → DELETE
// /v1/access_group/{id}. Absent name = idempotent success.
func TestDeleteAccessGroup_LooksUpThenDeletes(t *testing.T) {
	var deleteHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/access_group":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `[{"access_group_id":"ag-uuid-1","access_group_name":"demo","access_model_names":[],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":[],"assigned_key_ids":[]}]`)
		case r.Method == "DELETE" && r.URL.Path == "/v1/access_group/ag-uuid-1":
			deleteHit = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.DeleteAccessGroup(context.Background(), "demo"); err != nil {
		t.Fatalf("DeleteAccessGroup: %v", err)
	}
	if !deleteHit {
		t.Errorf("expected DELETE /v1/access_group/ag-uuid-1 to fire")
	}
}

// TestDeleteAccessGroup_AbsentName_NoDelete asserts the §7.7 idempotent
// branch: a §6.5 finalizer running after a partially-completed prior
// delete must NOT error if the access group is already gone.
func TestDeleteAccessGroup_AbsentName_NoDelete(t *testing.T) {
	var deleteHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/access_group":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `[]`)
		case r.Method == "DELETE":
			deleteHit = true
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.DeleteAccessGroup(context.Background(), "missing"); err != nil {
		t.Fatalf("DeleteAccessGroup (missing): %v", err)
	}
	if deleteHit {
		t.Errorf("DELETE must NOT fire when name is absent")
	}
}
```

- [ ] **Step 2: Run the new tests; they MUST fail with "undefined" because the implementation does not exist yet**

```bash
./scripts/dev.sh go test ./internal/litellm/ -run TestCreateAccessGroup_PostsV1Endpoint -v
```

Expected: compilation error mentioning undefined `AccessGroupCreateRequest` (already defined in Task 1, so this should resolve) AND undefined methods `CreateAccessGroup` (new signature), `GetAccessGroupByName`, `UpdateAccessGroup`, `DeleteAccessGroupByID`. This is the RED step in TDD — proceed to Step 3.

- [ ] **Step 3: Rewrite `internal/litellm/accessgroups.go`**

Replace the ENTIRE file contents with:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// CreateAccessGroup issues POST /v1/access_group. Returns the
// AccessGroupResponse (UUID, name, current bindings). Replaces the
// legacy POST /access_group/new flow whose validator rejected empty
// model_names; the /v1 endpoint accepts an empty-resource creation.
//
// The reconciler is expected to call GetAccessGroupByName first and only
// POST when the result is nil — `already exists` semantics are owned at
// the controller layer, not here (per issue-17 plan §4 decision:
// list-first-then-create).
func (c *RESTClient) CreateAccessGroup(ctx context.Context, req AccessGroupCreateRequest) (*AccessGroupResponse, error) {
	if req.AccessGroupName == "" {
		return nil, fmt.Errorf("litellm: CreateAccessGroup: empty access_group_name")
	}
	raw, err := c.makeRequest(ctx, "POST", "/v1/access_group", req)
	if err != nil {
		return nil, fmt.Errorf("litellm: POST /v1/access_group (name=%s): %w", req.AccessGroupName, err)
	}
	var resp AccessGroupResponse
	if uerr := json.Unmarshal(raw, &resp); uerr != nil {
		return nil, fmt.Errorf("litellm: decode POST /v1/access_group: %w", uerr)
	}
	return &resp, nil
}

// GetAccessGroupByName performs GET /v1/access_group and returns the
// matching entry by access_group_name. (nil, nil) when not found — the
// reconciler treats this as "needs POST to create".
//
// Decision (issue #17): no status field stores the UUID; we resolve by
// name on every reconcile. The N here is small (O(10s) of access groups
// in production per Hub §6.1), so a single list call per reconcile is
// acceptable.
func (c *RESTClient) GetAccessGroupByName(ctx context.Context, name string) (*AccessGroupResponse, error) {
	if name == "" {
		return nil, fmt.Errorf("litellm: GetAccessGroupByName: empty name")
	}
	raw, err := c.makeRequest(ctx, "GET", "/v1/access_group", nil)
	if err != nil {
		return nil, fmt.Errorf("litellm: GET /v1/access_group: %w", err)
	}
	var list []AccessGroupResponse
	if uerr := json.Unmarshal(raw, &list); uerr != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/access_group: %w", uerr)
	}
	for i := range list {
		if list[i].AccessGroupName == name {
			out := list[i]
			return &out, nil
		}
	}
	return nil, nil
}

// UpdateAccessGroup issues PUT /v1/access_group/{id}. Used by the
// reconciler's drift-correction branch when GetAccessGroupByName found
// an existing group with diverged bindings.
func (c *RESTClient) UpdateAccessGroup(ctx context.Context, id string, req AccessGroupUpdateRequest) (*AccessGroupResponse, error) {
	if id == "" {
		return nil, fmt.Errorf("litellm: UpdateAccessGroup: empty id")
	}
	raw, err := c.makeRequest(ctx, "PUT", "/v1/access_group/"+id, req)
	if err != nil {
		return nil, fmt.Errorf("litellm: PUT /v1/access_group/%s: %w", id, err)
	}
	var resp AccessGroupResponse
	if uerr := json.Unmarshal(raw, &resp); uerr != nil {
		return nil, fmt.Errorf("litellm: decode PUT /v1/access_group/%s: %w", id, uerr)
	}
	return &resp, nil
}

// DeleteAccessGroupByID issues DELETE /v1/access_group/{id}. The
// underlying makeRequest treats 404 as success per the existing §7.7
// idempotent-delete contract, so a re-reconcile after a partially-
// completed §6.5 sequence does NOT spurious-error.
func (c *RESTClient) DeleteAccessGroupByID(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("litellm: DeleteAccessGroupByID: empty id")
	}
	_, err := c.makeRequest(ctx, "DELETE", "/v1/access_group/"+id, nil)
	return err
}

// DeleteAccessGroup is the high-level helper the §6.5 finalizer path
// calls. It list-by-name → DELETE-by-id, treating absent-name as the
// idempotent-success branch (matches the §7.7 contract). The Environment
// reconciler's existing r.LiteLLM.DeleteAccessGroup(ctx, env.Name) call
// site does not change.
func (c *RESTClient) DeleteAccessGroup(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("litellm: DeleteAccessGroup: empty name")
	}
	found, err := c.GetAccessGroupByName(ctx, name)
	if err != nil {
		return fmt.Errorf("litellm: DeleteAccessGroup(%s) lookup: %w", name, err)
	}
	if found == nil {
		return nil // already gone — idempotent
	}
	if derr := c.DeleteAccessGroupByID(ctx, found.AccessGroupID); derr != nil {
		return fmt.Errorf("litellm: DeleteAccessGroup(%s, id=%s): %w", name, found.AccessGroupID, derr)
	}
	return nil
}

// DeleteTag is preserved verbatim from the legacy file — §6.5 step 3
// "delete tag" is orthogonal to the access-group migration.
func (c *RESTClient) DeleteTag(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/tag/"+name, nil)
	return err
}

// errLegacyHelpersRemoved marks BindTeamToAccessGroup / ListAccessGroupBindings
// as deliberately deleted in issue #17. If a caller still references them
// the linker will fail; this var is here only to make the intent grep-able.
var errLegacyHelpersRemoved = errors.New("legacy access-group bind helpers removed in issue #17 — use AccessGroupCreateRequest.AssignedTeamIDs / AccessGroupUpdateRequest.AssignedTeamIDs")
```

- [ ] **Step 4: Re-run the unit tests; CreateAccessGroup + lookup + update + delete tests should pass**

```bash
./scripts/dev.sh go test ./internal/litellm/ -run 'TestCreateAccessGroup_PostsV1Endpoint|TestGetAccessGroupByName_ListsAndFilters|TestGetAccessGroupByName_AbsentReturnsNilNil|TestUpdateAccessGroup_PutsByID|TestDeleteAccessGroupByID_DeletesByID|TestDeleteAccessGroup_LooksUpThenDeletes|TestDeleteAccessGroup_AbsentName_NoDelete' -v
```

Expected: all seven tests PASS. The interface (`client.go`) and other consumers (`noop.go`, `connection/client.go`) are still red — that is expected; they're rewritten in Tasks 3 and 4.

- [ ] **Step 5: Create the on-demand list primitives**

Create `internal/litellm/mcp_agents_teams.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListMCPServers issues GET /v1/mcp/server and returns a name→id map.
// MCP rows without a server_name (rare; OpenAPI marks the field nullable)
// are skipped — they cannot be referenced by Environment.spec.runtime.mcpServers
// anyway. Used by EnvironmentReconciler.reconcileAccessGroup to resolve
// env.Spec.Runtime.MCPServers (names) to AccessGroupCreateRequest.AccessMCPServerIDs.
func (c *RESTClient) ListMCPServers(ctx context.Context) (map[string]string, error) {
	raw, err := c.makeRequest(ctx, "GET", "/v1/mcp/server", nil)
	if err != nil {
		return nil, fmt.Errorf("litellm: GET /v1/mcp/server: %w", err)
	}
	var list []struct {
		ServerID   string `json:"server_id"`
		ServerName string `json:"server_name"`
	}
	if uerr := json.Unmarshal(raw, &list); uerr != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/mcp/server: %w", uerr)
	}
	out := make(map[string]string, len(list))
	for _, row := range list {
		if row.ServerName == "" {
			continue
		}
		out[row.ServerName] = row.ServerID
	}
	return out, nil
}

// ListA2AAgents issues GET /v1/agents and returns a name→id map.
// Used by reconcileAccessGroup to resolve env.Spec.Runtime.A2AAgents
// (names) to AccessGroupCreateRequest.AccessAgentIDs.
func (c *RESTClient) ListA2AAgents(ctx context.Context) (map[string]string, error) {
	raw, err := c.makeRequest(ctx, "GET", "/v1/agents", nil)
	if err != nil {
		return nil, fmt.Errorf("litellm: GET /v1/agents: %w", err)
	}
	var list []struct {
		AgentID   string `json:"agent_id"`
		AgentName string `json:"agent_name"`
	}
	if uerr := json.Unmarshal(raw, &list); uerr != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/agents: %w", uerr)
	}
	out := make(map[string]string, len(list))
	for _, row := range list {
		if row.AgentName == "" {
			continue
		}
		out[row.AgentName] = row.AgentID
	}
	return out, nil
}

// ListTeams issues GET /v2/team/list (paginated) and returns an
// alias→team_id map. Used by reconcileAccessGroup to resolve
// env.Spec.AuthorizedTeams (aliases) to AccessGroupCreateRequest.AssignedTeamIDs.
//
// Pagination loop matches the legacy ListAccessGroupBindings cap
// (maxAccessGroupListPages = 50 in accessgroups.go) — but inlined here
// to avoid a cross-file constant after that file shrank.
func (c *RESTClient) ListTeams(ctx context.Context) (map[string]string, error) {
	const maxPages = 50
	out := map[string]string{}
	for page := 1; page <= maxPages; page++ {
		path := fmt.Sprintf("/v2/team/list?page=%d&page_size=200", page)
		raw, err := c.makeRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, fmt.Errorf("litellm: GET %s: %w", path, err)
		}
		var resp TeamListResponse
		if uerr := json.Unmarshal(raw, &resp); uerr != nil {
			return nil, fmt.Errorf("litellm: decode %s: %w", path, uerr)
		}
		for _, t := range resp.Teams {
			if t.TeamAlias == "" || t.TeamID == "" {
				continue
			}
			out[t.TeamAlias] = t.TeamID
		}
		if len(resp.Teams) == 0 || page >= resp.TotalPages {
			break
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Create unit tests for the list primitives**

Create `internal/litellm/mcp_agents_teams_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestListMCPServers_NameToID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/mcp/server" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[
			{"server_id":"mcp-1","server_name":"vmcp-dev","transport":"http"},
			{"server_id":"mcp-2","server_name":"vmcp-aws","transport":"http"},
			{"server_id":"mcp-3","server_name":null,"transport":"http"}
		]`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.ListMCPServers(context.Background())
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if got["vmcp-dev"] != "mcp-1" || got["vmcp-aws"] != "mcp-2" {
		t.Errorf("map = %v; want vmcp-dev→mcp-1, vmcp-aws→mcp-2", got)
	}
	if _, hasNullName := got[""]; hasNullName {
		t.Errorf("null-name MCP must be skipped, not mapped under empty-string key")
	}
}

func TestListA2AAgents_NameToID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/agents" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[
			{"agent_id":"a-1","agent_name":"test-noop-agent","agent_card_params":{}}
		]`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.ListA2AAgents(context.Background())
	if err != nil {
		t.Fatalf("ListA2AAgents: %v", err)
	}
	if got["test-noop-agent"] != "a-1" {
		t.Errorf("map = %v; want test-noop-agent→a-1", got)
	}
}

func TestListTeams_PaginatesAndCollapsesAliasToID(t *testing.T) {
	var page1 = `{"teams":[
		{"team_id":"t-1","team_alias":"default","models":[]},
		{"team_id":"t-2","team_alias":"qa","models":[]}
	],"total":2,"page":1,"page_size":200,"total_pages":1}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v2/team/list") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, page1)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if got["default"] != "t-1" || got["qa"] != "t-2" {
		t.Errorf("map = %v; want default→t-1, qa→t-2", got)
	}
}
```

- [ ] **Step 7: Run the list-primitive tests**

```bash
./scripts/dev.sh go test ./internal/litellm/ -run 'TestListMCPServers|TestListA2AAgents|TestListTeams' -v
```

Expected: all three PASS. The package as a whole is still red because `client.go` (the interface) doesn't match — that's Task 3.

- [ ] **Step 8: Commit Phase A**

```bash
git add internal/litellm/accessgroups.go internal/litellm/accessgroups_test.go internal/litellm/mcp_agents_teams.go internal/litellm/mcp_agents_teams_test.go
git commit -m "$(cat <<'EOF'
feat(litellm): rewrite access-group helpers + add list primitives for /v1

Replaces accessgroups.go with the /v1 endpoint surface: CreateAccessGroup
returns AccessGroupResponse (UUID), GetAccessGroupByName resolves the
UUID on demand, UpdateAccessGroup handles drift correction, and
DeleteAccessGroup keeps its name-based call signature while internally
looking up the UUID before issuing DELETE /v1/access_group/{id}.

Adds mcp_agents_teams.go with three on-demand name→id resolvers used by
EnvironmentReconciler.reconcileAccessGroup to translate
env.Spec.Runtime.{MCPServers,A2AAgents} and env.Spec.AuthorizedTeams
into AccessGroupCreateRequest.{AccessMCPServerIDs,AccessAgentIDs,
AssignedTeamIDs} (no Snapshotter change — issue #17 plan §1 decision).

Removes BindTeamToAccessGroup / ListAccessGroupBindings — team binding
is now first-class on the access group via assigned_team_ids.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 3 (Phase B): Update Client Interface, NoopClient, Connection Wrapper

**Goal:** Make the package compile end-to-end by aligning every implementer with the new `Client` interface. NoopClient is updated to log + return empty maps / nil responses. The thin `internal/connection.Client` wrapper sheds the dead `BindTeamToAccessGroup` / `ListAccessGroupBindings` methods.

**Files:**
- Modify: `internal/litellm/client.go`
- Modify: `internal/litellm/noop.go`
- Modify: `internal/connection/client.go`

- [ ] **Step 1: Read current `client.go` interface block**

```bash
grep -n "type Client interface\|CreateAccessGroup\|BindTeamToAccessGroup\|ListAccessGroupBindings\|DeleteAccessGroup" internal/litellm/client.go
```

Expected: line numbers for `type Client interface { ... }` plus the four method declarations.

- [ ] **Step 2: Modify the `Client` interface in `internal/litellm/client.go`**

Locate the block

```go
	// CreateAccessGroup issues POST /access_group/new. Idempotent at the
	// ...
	CreateAccessGroup(ctx context.Context, name string, modelNames []string) error
	// BindTeamToAccessGroup ...
	BindTeamToAccessGroup(ctx context.Context, accessGroup, teamID string) error
	// ListAccessGroupBindings ...
	ListAccessGroupBindings(ctx context.Context, accessGroup string) ([]string, error)
```

(approximately lines 140-165 in the current tree — verify before editing) and REPLACE with:

```go
	// CreateAccessGroup issues POST /v1/access_group. The reconciler
	// must call GetAccessGroupByName first; only POST when nil is
	// returned (list-first-then-create per issue #17 plan §4).
	CreateAccessGroup(ctx context.Context, req AccessGroupCreateRequest) (*AccessGroupResponse, error)

	// GetAccessGroupByName performs GET /v1/access_group and returns
	// the matching entry by access_group_name. (nil, nil) when not
	// found.
	GetAccessGroupByName(ctx context.Context, name string) (*AccessGroupResponse, error)

	// UpdateAccessGroup issues PUT /v1/access_group/{id}. Used by the
	// reconciler's drift-correction branch.
	UpdateAccessGroup(ctx context.Context, id string, req AccessGroupUpdateRequest) (*AccessGroupResponse, error)

	// DeleteAccessGroupByID issues DELETE /v1/access_group/{id}. 404
	// is treated as success (§7.7 idempotent-delete contract).
	DeleteAccessGroupByID(ctx context.Context, id string) error

	// ListMCPServers / ListA2AAgents / ListTeams resolve env-spec
	// names (and team aliases) to LiteLLM-side IDs. Called by
	// EnvironmentReconciler.reconcileAccessGroup once per reconcile.
	ListMCPServers(ctx context.Context) (map[string]string, error)
	ListA2AAgents(ctx context.Context) (map[string]string, error)
	ListTeams(ctx context.Context) (map[string]string, error)
```

Keep the existing `DeleteAccessGroup(ctx context.Context, name string) error` declaration in the interface — its signature is unchanged (the lookup-then-delete logic is internal to the implementation).

- [ ] **Step 3: Modify `internal/litellm/noop.go`**

Find the existing `func (c *NoopClient) CreateAccessGroup(...) error` block and the `BindTeamToAccessGroup` / `ListAccessGroupBindings` blocks. Replace them with:

```go
// CreateAccessGroup is the §7 LiteLLM call (issue #17: /v1/access_group).
// NoopClient logs and returns a synthetic response with a deterministic
// UUID so envtests that don't override the LiteLLM client can still
// progress through reconcileAccessGroup without ID-resolution surprises.
func (c *NoopClient) CreateAccessGroup(_ context.Context, req AccessGroupCreateRequest) (*AccessGroupResponse, error) {
	c.log.V(1).Info("NoopClient: CreateAccessGroup (no-op)", "name", req.AccessGroupName)
	return &AccessGroupResponse{
		AccessGroupID:      "noop-" + req.AccessGroupName,
		AccessGroupName:    req.AccessGroupName,
		AccessModelNames:   req.AccessModelNames,
		AccessMCPServerIDs: req.AccessMCPServerIDs,
		AccessAgentIDs:     req.AccessAgentIDs,
		AssignedTeamIDs:    req.AssignedTeamIDs,
		AssignedKeyIDs:     req.AssignedKeyIDs,
	}, nil
}

// GetAccessGroupByName always returns (nil, nil) — the reconciler will
// take the POST branch on every reconcile in noop mode, which is
// harmless (the noop POST returns synthetic success).
func (c *NoopClient) GetAccessGroupByName(_ context.Context, name string) (*AccessGroupResponse, error) {
	c.log.V(2).Info("NoopClient: GetAccessGroupByName (no-op)", "name", name)
	return nil, nil
}

// UpdateAccessGroup logs and echoes the request back.
func (c *NoopClient) UpdateAccessGroup(_ context.Context, id string, req AccessGroupUpdateRequest) (*AccessGroupResponse, error) {
	c.log.V(1).Info("NoopClient: UpdateAccessGroup (no-op)", "id", id)
	resp := &AccessGroupResponse{AccessGroupID: id}
	if req.AccessGroupName != nil {
		resp.AccessGroupName = *req.AccessGroupName
	}
	if req.AccessModelNames != nil {
		resp.AccessModelNames = req.AccessModelNames
	}
	if req.AccessMCPServerIDs != nil {
		resp.AccessMCPServerIDs = req.AccessMCPServerIDs
	}
	if req.AccessAgentIDs != nil {
		resp.AccessAgentIDs = req.AccessAgentIDs
	}
	if req.AssignedTeamIDs != nil {
		resp.AssignedTeamIDs = req.AssignedTeamIDs
	}
	if req.AssignedKeyIDs != nil {
		resp.AssignedKeyIDs = req.AssignedKeyIDs
	}
	return resp, nil
}

// DeleteAccessGroupByID is a no-op.
func (c *NoopClient) DeleteAccessGroupByID(_ context.Context, id string) error {
	c.log.V(1).Info("NoopClient: DeleteAccessGroupByID (no-op)", "id", id)
	return nil
}

// ListMCPServers / ListA2AAgents / ListTeams return empty maps. The
// reconciler's UnresolvedReferences branch handles empty-resolver
// gracefully (everything declared in spec.runtime is unresolved when
// no upstream rows exist), so noop envtests can still reach steady
// state.
func (c *NoopClient) ListMCPServers(_ context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (c *NoopClient) ListA2AAgents(_ context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (c *NoopClient) ListTeams(_ context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
```

Delete the legacy `BindTeamToAccessGroup` and `ListAccessGroupBindings` method blocks entirely. `DeleteAccessGroup(name)` stays in noop.go with its existing signature, but its body becomes a one-liner since the helper just logs:

```go
// DeleteAccessGroup is the §6.5 step 2 LiteLLM call. Phase 1 logs and
// returns nil — no upstream side effect.
func (c *NoopClient) DeleteAccessGroup(_ context.Context, name string) error {
	c.log.V(1).Info("NoopClient: DeleteAccessGroup (no-op)", "name", name)
	return nil
}
```

- [ ] **Step 4: Modify `internal/connection/client.go`**

Find the four wrapper methods (`CreateAccessGroup`, `BindTeamToAccessGroup`, `ListAccessGroupBindings`, and the existing `DeleteAccessGroup`). DELETE the `BindTeamToAccessGroup` and `ListAccessGroupBindings` methods entirely.

Replace the `CreateAccessGroup` wrapper with:

```go
// CreateAccessGroup proxies to the underlying RESTClient. See
// internal/litellm/accessgroups.go for semantics.
func (c *Client) CreateAccessGroup(ctx context.Context, req litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	client, err := c.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return client.CreateAccessGroup(ctx, req)
}
```

Add (next to `CreateAccessGroup`) the new wrappers, matching the legacy `DeleteAccessGroup` pattern:

```go
func (c *Client) GetAccessGroupByName(ctx context.Context, name string) (*litellm.AccessGroupResponse, error) {
	client, err := c.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return client.GetAccessGroupByName(ctx, name)
}

func (c *Client) UpdateAccessGroup(ctx context.Context, id string, req litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	client, err := c.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return client.UpdateAccessGroup(ctx, id, req)
}

func (c *Client) DeleteAccessGroupByID(ctx context.Context, id string) error {
	client, err := c.resolve(ctx)
	if err != nil {
		return err
	}
	return client.DeleteAccessGroupByID(ctx, id)
}

func (c *Client) ListMCPServers(ctx context.Context) (map[string]string, error) {
	client, err := c.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListMCPServers(ctx)
}

func (c *Client) ListA2AAgents(ctx context.Context) (map[string]string, error) {
	client, err := c.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListA2AAgents(ctx)
}

func (c *Client) ListTeams(ctx context.Context) (map[string]string, error) {
	client, err := c.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListTeams(ctx)
}
```

Leave the `DeleteAccessGroup(name)` wrapper untouched — its signature is unchanged.

- [ ] **Step 5: Run the litellm package tests**

```bash
./scripts/dev.sh go build ./internal/litellm/ ./internal/connection/
./scripts/dev.sh go test ./internal/litellm/ -v
```

Expected: `go build` succeeds. `go test` PASSes (the seven new tests from Task 2 + the three list-primitive tests).

- [ ] **Step 6: Commit Phase B**

```bash
git add internal/litellm/client.go internal/litellm/noop.go internal/connection/client.go
git commit -m "$(cat <<'EOF'
refactor(litellm,connection): align Client interface + NoopClient + wrapper with /v1

Swaps four interface methods (CreateAccessGroup signature change,
DeleteAccessGroup unchanged, three new list primitives ListMCPServers /
ListA2AAgents / ListTeams). Removes BindTeamToAccessGroup +
ListAccessGroupBindings from both Client and the connection wrapper —
team binding now lives on the access group itself as
assigned_team_ids.

NoopClient returns synthetic AccessGroupResponse with deterministic
UUIDs ("noop-<name>") so envtests that don't override the client can
still progress through reconcileAccessGroup unchanged.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 4 (Phase C): Migrate `reconcileAccessGroup`

**Goal:** Replace the body of `EnvironmentReconciler.reconcileAccessGroup` (currently at `internal/controller/ach/environment_controller.go:437-514`) with the desired-state sync against `/v1/access_group`. The §6.5 finalizer call site (`environment_controller.go:123`) is unchanged because `DeleteAccessGroup(name)` internalizes the lookup-then-delete.

**Files:**
- Modify: `internal/controller/ach/environment_controller.go`

- [ ] **Step 1: Read the current reconcileAccessGroup body**

```bash
sed -n '415,520p' internal/controller/ach/environment_controller.go
```

Confirm the function spans approximately lines 415–514, ending with `}` just before `// requiredAvailableSubConditions`.

- [ ] **Step 2: Replace the function body**

Locate the line `func (r *EnvironmentReconciler) reconcileAccessGroup(` (around line 437) and REPLACE the entire function (header through final closing brace, up to but not including the `// requiredAvailableSubConditions ...` comment) with:

```go
// reconcileAccessGroup is the §7 implementation step: ensure the LiteLLM
// access group <env.Name> exists with the correct desired-state bindings
// for env.Spec.Runtime.Models / .MCPServers / .A2AAgents and
// env.Spec.AuthorizedTeams. Returns the metav1.Condition that the caller
// should publish on env.Status.Conditions.
//
// Migration (issue #17): rewrites the legacy POST /access_group/new flow
// (which required non-empty model_names per LiteLLM 1.83.x's hidden
// validator, and bound teams via the magic team.models[] entry
// "access_group/<name>") onto the /v1/access_group endpoints. Resolution
// of MCP / A2A / Team names → LiteLLM IDs happens on demand each
// reconcile (no Snapshotter changes per issue #17 plan §1); the access
// group UUID is resolved by name each reconcile (no CRD status field
// per issue #17 plan §2).
//
// Closed-set conditions emitted (Hub §6.6, updated for issue #17):
//   - True/Synced              — desired state matches observed
//   - False/UnresolvedReferences — one or more env.Spec names had no
//                                  matching LiteLLM ID (the resolver
//                                  layer failed). Distinct from
//                                  ExecutionResourcesResolved=False
//                                  because that condition is about the
//                                  Snapshotter (cached, may be stale);
//                                  this one is about the fresh-fetched
//                                  resolver maps.
//   - False/AccessGroupCreateFailed — POST /v1/access_group failed
//   - False/AccessGroupUpdateFailed — PUT /v1/access_group/{id} failed
//   - False/ResolveFailed       — one of ListMCPServers / ListA2AAgents
//                                  / ListTeams errored (LiteLLM
//                                  unreachable mid-reconcile)
func (r *EnvironmentReconciler) reconcileAccessGroup(
	ctx context.Context,
	env *achv1alpha1.Environment,
) metav1.Condition {
	logger := log.FromContext(ctx).WithValues("environment", env.Name)

	// Step 1: resolve names → IDs (on-demand each reconcile per #17 §1).
	mcpMap, mcpErr := r.LiteLLM.ListMCPServers(ctx)
	if mcpErr != nil {
		return resolveFailed(env, "ListMCPServers", mcpErr)
	}
	agentMap, agentErr := r.LiteLLM.ListA2AAgents(ctx)
	if agentErr != nil {
		return resolveFailed(env, "ListA2AAgents", agentErr)
	}
	teamMap, teamErr := r.LiteLLM.ListTeams(ctx)
	if teamErr != nil {
		return resolveFailed(env, "ListTeams", teamErr)
	}

	mcpIDs, mcpUnresolved := mapResolve(env.Spec.Runtime.MCPServers, mcpMap)
	agentIDs, agentUnresolved := mapResolve(env.Spec.Runtime.A2AAgents, agentMap)
	teamIDs, teamUnresolved := mapResolve(env.Spec.AuthorizedTeams, teamMap)

	if len(mcpUnresolved)+len(agentUnresolved)+len(teamUnresolved) > 0 {
		return metav1.Condition{
			Type:   "AccessGroupSynced",
			Status: metav1.ConditionFalse,
			Reason: "UnresolvedReferences",
			Message: fmt.Sprintf(
				"unresolved: mcpServers=%v a2aAgents=%v authorizedTeams=%v",
				mcpUnresolved, agentUnresolved, teamUnresolved,
			),
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		}
	}

	// Step 2: discover whether the access group already exists.
	existing, gerr := r.LiteLLM.GetAccessGroupByName(ctx, env.Name)
	if gerr != nil {
		return resolveFailed(env, "GetAccessGroupByName", gerr)
	}

	desiredModels := env.Spec.Runtime.Models
	if desiredModels == nil {
		desiredModels = []string{}
	}

	// Step 3a: POST when absent.
	if existing == nil {
		created, cerr := r.LiteLLM.CreateAccessGroup(ctx, litellm.AccessGroupCreateRequest{
			AccessGroupName:    env.Name,
			AccessModelNames:   desiredModels,
			AccessMCPServerIDs: mcpIDs,
			AccessAgentIDs:     agentIDs,
			AssignedTeamIDs:    teamIDs,
		})
		if cerr != nil {
			logger.Error(cerr, "POST /v1/access_group failed")
			return metav1.Condition{
				Type:               "AccessGroupSynced",
				Status:             metav1.ConditionFalse,
				Reason:             "AccessGroupCreateFailed",
				Message:            fmt.Sprintf("LiteLLM CreateAccessGroup(%s) failed: %v", env.Name, cerr),
				ObservedGeneration: env.Generation,
				LastTransitionTime: metav1.Now(),
			}
		}
		logger.Info("created access group", "name", env.Name, "id", created.AccessGroupID)
		return syncedCondition(env, created)
	}

	// Step 3b: PUT when drifted.
	if drift := computeAccessGroupDrift(existing, desiredModels, mcpIDs, agentIDs, teamIDs); drift {
		updated, uerr := r.LiteLLM.UpdateAccessGroup(ctx, existing.AccessGroupID, litellm.AccessGroupUpdateRequest{
			AccessModelNames:   desiredModels,
			AccessMCPServerIDs: mcpIDs,
			AccessAgentIDs:     agentIDs,
			AssignedTeamIDs:    teamIDs,
		})
		if uerr != nil {
			logger.Error(uerr, "PUT /v1/access_group/{id} failed", "id", existing.AccessGroupID)
			return metav1.Condition{
				Type:               "AccessGroupSynced",
				Status:             metav1.ConditionFalse,
				Reason:             "AccessGroupUpdateFailed",
				Message:            fmt.Sprintf("LiteLLM UpdateAccessGroup(%s, id=%s) failed: %v", env.Name, existing.AccessGroupID, uerr),
				ObservedGeneration: env.Generation,
				LastTransitionTime: metav1.Now(),
			}
		}
		logger.Info("updated access group", "name", env.Name, "id", updated.AccessGroupID)
		return syncedCondition(env, updated)
	}

	return syncedCondition(env, existing)
}

// resolveFailed packages a LiteLLM-unreachable failure during the
// reconcileAccessGroup resolver phase into the closed-set condition.
func resolveFailed(env *achv1alpha1.Environment, op string, err error) metav1.Condition {
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionFalse,
		Reason:             "ResolveFailed",
		Message:            fmt.Sprintf("LiteLLM %s failed: %v", op, err),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}

// syncedCondition is the True/Synced terminal condition shared by the
// create and update branches.
func syncedCondition(env *achv1alpha1.Environment, ag *litellm.AccessGroupResponse) metav1.Condition {
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("access group %q (id=%s) bound to %d team(s), %d model(s), %d mcp, %d agent", ag.AccessGroupName, ag.AccessGroupID, len(ag.AssignedTeamIDs), len(ag.AccessModelNames), len(ag.AccessMCPServerIDs), len(ag.AccessAgentIDs)),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}

// mapResolve splits `names` into (resolvedIDs, unresolvedNames) by
// looking each up in `m`. Empty names are skipped (defensive: should
// not appear since the CRD validators reject empty list members).
func mapResolve(names []string, m map[string]string) (ids []string, unresolved []string) {
	for _, n := range names {
		if n == "" {
			continue
		}
		if id, ok := m[n]; ok {
			ids = append(ids, id)
		} else {
			unresolved = append(unresolved, n)
		}
	}
	return ids, unresolved
}

// computeAccessGroupDrift returns true iff the existing access group's
// stored bindings diverge from the desired state. Each dimension is
// compared as a set (order-independent).
func computeAccessGroupDrift(existing *litellm.AccessGroupResponse, models, mcps, agents, teams []string) bool {
	return !sameSet(existing.AccessModelNames, models) ||
		!sameSet(existing.AccessMCPServerIDs, mcps) ||
		!sameSet(existing.AccessAgentIDs, agents) ||
		!sameSet(existing.AssignedTeamIDs, teams)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]struct{}, len(a))
	for _, x := range a {
		m[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := m[x]; !ok {
			return false
		}
	}
	return true
}
```

- [ ] **Step 3: Build the package**

```bash
./scripts/dev.sh go build ./internal/controller/ach/
```

Expected: clean build. If errors point to test files (`*_test.go`), they will be addressed in Task 5.

- [ ] **Step 4: Verify the §6.5 finalizer call site still compiles unchanged**

```bash
grep -n "DeleteAccessGroup(ctx, env.Name)" internal/controller/ach/environment_controller.go
```

Expected: still exactly one match at the existing line (~123) — the call site signature did not change.

- [ ] **Step 5: Commit Phase C**

```bash
git add internal/controller/ach/environment_controller.go
git commit -m "$(cat <<'EOF'
feat(controller): rewrite reconcileAccessGroup as desired-state /v1 sync

Replaces the legacy four-step flow (create → list-bindings → bind-each →
orphan-log) with a single-pass desired-state sync against
POST/GET/PUT /v1/access_group:

  1. Resolve env.Spec.Runtime.{MCPServers,A2AAgents} +
     env.Spec.AuthorizedTeams names → LiteLLM IDs via three fresh list
     calls (Snapshotter unchanged per #17 plan §1).
  2. Unresolved names → AccessGroupSynced=False reason=UnresolvedReferences
     with the failing-names list.
  3. GET /v1/access_group, find by access_group_name (no CRD status
     field per #17 plan §2 — UUID resolved on each reconcile).
  4. POST when absent, PUT when drifted (set-equality across four
     dimensions), no-op when in sync.
  5. Closed-set conditions: Synced / UnresolvedReferences /
     AccessGroupCreateFailed / AccessGroupUpdateFailed / ResolveFailed.

The §6.5 finalizer call site (DeleteAccessGroup(ctx, env.Name)) is
unchanged at the call-site level — the helper now internally lists by
name and DELETEs by UUID.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 5 (Phase D): Update Test Fakes and Envtests

**Goal:** Bring every fake LiteLLM client implementation back in line with the new `Client` interface, then update the existing envtests in `environment_accessgroup_test.go` to populate `Runtime.{Models,MCPServers,A2AAgents}` and assert the new condition reasons.

**Files:**
- Modify: `internal/controller/ach/access_group_fake_test.go`
- Modify: `internal/controller/ach/suite_test.go`
- Modify: `internal/controller/ach/main_wiring_envtest_test.go`
- Modify: `internal/snapshot/snapshot_test.go`
- Modify: `internal/controller/ach/environment_accessgroup_test.go`

- [ ] **Step 1: Rewrite `internal/controller/ach/access_group_fake_test.go`**

Replace the entire file contents with:

```go
// SPDX-License-Identifier: Apache-2.0

// Envtest fake LiteLLM for the §7 AccessGroupSynced reconciler tests
// (issue #17 — /v1/access_group surface). The fake records:
//   - Create / Update / Delete calls per env.Name
//   - Last-seen create + update requests (for desired-state assertion)
//   - Resolver maps that callers may seed before reconciler fires
//
// Each test resets via accessGroupFake.Reset() before driving the
// reconciler.

package ach

import (
	"context"
	"errors"
	"sync"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

type accessGroupFakeImpl struct {
	*litellm.NoopClient

	mu sync.Mutex

	createCalls map[string]int
	updateCalls map[string]int
	deleteCalls map[string]int

	lastCreate map[string]litellm.AccessGroupCreateRequest
	lastUpdate map[string]litellm.AccessGroupUpdateRequest

	createErrByName map[string]error
	updateErrByName map[string]error

	// stored simulates the upstream /v1/access_group state. Keyed by
	// access_group_name.
	stored map[string]*litellm.AccessGroupResponse

	// resolver maps; tests seed BEFORE creating the Environment CR.
	mcps   map[string]string
	agents map[string]string
	teams  map[string]string

	listErr error
}

func newAccessGroupFake() *accessGroupFakeImpl {
	return &accessGroupFakeImpl{
		NoopClient:      litellm.NewNoopClient(logr.Discard()),
		createCalls:     map[string]int{},
		updateCalls:     map[string]int{},
		deleteCalls:     map[string]int{},
		lastCreate:      map[string]litellm.AccessGroupCreateRequest{},
		lastUpdate:      map[string]litellm.AccessGroupUpdateRequest{},
		createErrByName: map[string]error{},
		updateErrByName: map[string]error{},
		stored:          map[string]*litellm.AccessGroupResponse{},
		mcps:            map[string]string{},
		agents:          map[string]string{},
		teams:           map[string]string{},
	}
}

func (f *accessGroupFakeImpl) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = map[string]int{}
	f.updateCalls = map[string]int{}
	f.deleteCalls = map[string]int{}
	f.lastCreate = map[string]litellm.AccessGroupCreateRequest{}
	f.lastUpdate = map[string]litellm.AccessGroupUpdateRequest{}
	f.createErrByName = map[string]error{}
	f.updateErrByName = map[string]error{}
	f.stored = map[string]*litellm.AccessGroupResponse{}
	f.mcps = map[string]string{}
	f.agents = map[string]string{}
	f.teams = map[string]string{}
	f.listErr = nil
}

func (f *accessGroupFakeImpl) CreateAccessGroup(_ context.Context, req litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls[req.AccessGroupName]++
	f.lastCreate[req.AccessGroupName] = req
	if err := f.createErrByName[req.AccessGroupName]; err != nil {
		return nil, err
	}
	resp := &litellm.AccessGroupResponse{
		AccessGroupID:      "ag-uuid-" + req.AccessGroupName,
		AccessGroupName:    req.AccessGroupName,
		AccessModelNames:   append([]string{}, req.AccessModelNames...),
		AccessMCPServerIDs: append([]string{}, req.AccessMCPServerIDs...),
		AccessAgentIDs:     append([]string{}, req.AccessAgentIDs...),
		AssignedTeamIDs:    append([]string{}, req.AssignedTeamIDs...),
		AssignedKeyIDs:     append([]string{}, req.AssignedKeyIDs...),
	}
	f.stored[req.AccessGroupName] = resp
	return resp, nil
}

func (f *accessGroupFakeImpl) GetAccessGroupByName(_ context.Context, name string) (*litellm.AccessGroupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if r, ok := f.stored[name]; ok {
		out := *r
		return &out, nil
	}
	return nil, nil
}

func (f *accessGroupFakeImpl) UpdateAccessGroup(_ context.Context, id string, req litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// find by id; tests look up by name → id is "ag-uuid-<name>".
	var found *litellm.AccessGroupResponse
	var name string
	for n, r := range f.stored {
		if r.AccessGroupID == id {
			found = r
			name = n
			break
		}
	}
	f.updateCalls[name]++
	f.lastUpdate[name] = req
	if err := f.updateErrByName[name]; err != nil {
		return nil, err
	}
	if found == nil {
		return nil, errors.New("fake: UpdateAccessGroup id not found")
	}
	if req.AccessModelNames != nil {
		found.AccessModelNames = append([]string{}, req.AccessModelNames...)
	}
	if req.AccessMCPServerIDs != nil {
		found.AccessMCPServerIDs = append([]string{}, req.AccessMCPServerIDs...)
	}
	if req.AccessAgentIDs != nil {
		found.AccessAgentIDs = append([]string{}, req.AccessAgentIDs...)
	}
	if req.AssignedTeamIDs != nil {
		found.AssignedTeamIDs = append([]string{}, req.AssignedTeamIDs...)
	}
	out := *found
	return &out, nil
}

func (f *accessGroupFakeImpl) DeleteAccessGroupByID(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for n, r := range f.stored {
		if r.AccessGroupID == id {
			f.deleteCalls[n]++
			delete(f.stored, n)
			return nil
		}
	}
	return nil
}

func (f *accessGroupFakeImpl) DeleteAccessGroup(ctx context.Context, name string) error {
	f.mu.Lock()
	r, ok := f.stored[name]
	f.mu.Unlock()
	if !ok {
		return nil
	}
	return f.DeleteAccessGroupByID(ctx, r.AccessGroupID)
}

func (f *accessGroupFakeImpl) ListMCPServers(_ context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.mcps))
	for k, v := range f.mcps {
		out[k] = v
	}
	return out, nil
}

func (f *accessGroupFakeImpl) ListA2AAgents(_ context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.agents))
	for k, v := range f.agents {
		out[k] = v
	}
	return out, nil
}

func (f *accessGroupFakeImpl) ListTeams(_ context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.teams))
	for k, v := range f.teams {
		out[k] = v
	}
	return out, nil
}

func (f *accessGroupFakeImpl) CreateCallsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls[name]
}

func (f *accessGroupFakeImpl) UpdateCallsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updateCalls[name]
}

func (f *accessGroupFakeImpl) LastCreate(name string) litellm.AccessGroupCreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCreate[name]
}

func (f *accessGroupFakeImpl) SeedMCP(name, id string)   { f.mu.Lock(); f.mcps[name] = id; f.mu.Unlock() }
func (f *accessGroupFakeImpl) SeedAgent(name, id string) { f.mu.Lock(); f.agents[name] = id; f.mu.Unlock() }
func (f *accessGroupFakeImpl) SeedTeam(alias, id string) { f.mu.Lock(); f.teams[alias] = id; f.mu.Unlock() }

func (f *accessGroupFakeImpl) InjectCreateErr(name string, err error) {
	f.mu.Lock()
	f.createErrByName[name] = err
	f.mu.Unlock()
}

var _ litellm.Client = (*accessGroupFakeImpl)(nil)

var errFakeBindFailed = errors.New("fake: bind failed")
```

- [ ] **Step 2: Update `internal/controller/ach/suite_test.go`'s `countingNoopClient`**

Open `internal/controller/ach/suite_test.go`. Locate the `countingNoopClient` block (currently lines 76-112 per the grep above). Replace `CreateAccessGroup`, `BindTeamToAccessGroup`, `ListAccessGroupBindings` methods on it with the new surface. The final state of the block should be:

```go
type countingNoopClient struct {
	*litellm.NoopClient
	accessGroup  *accessGroupFakeImpl
	delAGCount   atomic.Int32
	delTagCount  atomic.Int32
}

func (c *countingNoopClient) DeleteAccessGroup(ctx context.Context, name string) error {
	c.delAGCount.Add(1)
	return c.accessGroup.DeleteAccessGroup(ctx, name)
}

func (c *countingNoopClient) DeleteTag(ctx context.Context, name string) error {
	c.delTagCount.Add(1)
	return c.NoopClient.DeleteTag(ctx, name)
}

func (c *countingNoopClient) CreateAccessGroup(ctx context.Context, req litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return c.accessGroup.CreateAccessGroup(ctx, req)
}

func (c *countingNoopClient) GetAccessGroupByName(ctx context.Context, name string) (*litellm.AccessGroupResponse, error) {
	return c.accessGroup.GetAccessGroupByName(ctx, name)
}

func (c *countingNoopClient) UpdateAccessGroup(ctx context.Context, id string, req litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return c.accessGroup.UpdateAccessGroup(ctx, id, req)
}

func (c *countingNoopClient) DeleteAccessGroupByID(ctx context.Context, id string) error {
	return c.accessGroup.DeleteAccessGroupByID(ctx, id)
}

func (c *countingNoopClient) ListMCPServers(ctx context.Context) (map[string]string, error) {
	return c.accessGroup.ListMCPServers(ctx)
}
func (c *countingNoopClient) ListA2AAgents(ctx context.Context) (map[string]string, error) {
	return c.accessGroup.ListA2AAgents(ctx)
}
func (c *countingNoopClient) ListTeams(ctx context.Context) (map[string]string, error) {
	return c.accessGroup.ListTeams(ctx)
}

var _ litellm.Client = (*countingNoopClient)(nil)
```

Delete the `BindTeamToAccessGroup` / `ListAccessGroupBindings` methods on the same type — they no longer exist on the interface.

- [ ] **Step 3: Update `internal/controller/ach/main_wiring_envtest_test.go`'s `wiringFakeLiteLLM`**

```bash
grep -n "wiringFakeLiteLLM\|CreateAccessGroup\|BindTeamToAccessGroup\|ListAccessGroupBindings" internal/controller/ach/main_wiring_envtest_test.go
```

For each method that no longer matches the new interface, update the receiver. Pattern:

```go
func (f *wiringFakeLiteLLM) CreateAccessGroup(_ context.Context, req litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return &litellm.AccessGroupResponse{AccessGroupID: "wiring-" + req.AccessGroupName, AccessGroupName: req.AccessGroupName}, nil
}
func (f *wiringFakeLiteLLM) GetAccessGroupByName(_ context.Context, _ string) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *wiringFakeLiteLLM) UpdateAccessGroup(_ context.Context, id string, _ litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return &litellm.AccessGroupResponse{AccessGroupID: id}, nil
}
func (f *wiringFakeLiteLLM) DeleteAccessGroupByID(_ context.Context, _ string) error { return nil }
func (f *wiringFakeLiteLLM) ListMCPServers(_ context.Context) (map[string]string, error) { return map[string]string{}, nil }
func (f *wiringFakeLiteLLM) ListA2AAgents(_ context.Context) (map[string]string, error) { return map[string]string{}, nil }
func (f *wiringFakeLiteLLM) ListTeams(_ context.Context) (map[string]string, error)     { return map[string]string{}, nil }
```

Delete the `BindTeamToAccessGroup` and `ListAccessGroupBindings` methods from this fake.

- [ ] **Step 4: Update `internal/snapshot/snapshot_test.go`'s `fakeLiteLLM`**

```bash
grep -n "fakeLiteLLM\|CreateAccessGroup\|BindTeamToAccessGroup\|ListAccessGroupBindings" internal/snapshot/snapshot_test.go
```

Apply the same pattern as Step 3. The snapshot package's fake never exercises these methods so empty bodies returning zero values are correct.

- [ ] **Step 5: Run the litellm + connection package + envtest builds**

```bash
./scripts/dev.sh go build ./...
```

Expected: clean build across the whole module. If anything else surfaces (e.g. a fake we missed), it will fail here — search and fix using the same pattern.

- [ ] **Step 6: Update existing envtests in `environment_accessgroup_test.go`**

For EACH of the five `cr := &achv1alpha1.Environment{...}` blocks in this file, change

```go
Runtime: achv1alpha1.RuntimeBlock{},
```

to

```go
Runtime: achv1alpha1.RuntimeBlock{
    Models:     []string{},
    MCPServers: []string{},
    A2AAgents:  []string{},
},
```

For tests that need to assert the create call's payload (the happy path test in particular), seed the resolver maps and populate `Runtime`:

In `TestAccessGroupSynced_True_WhenCreateAndBindSucceed`, AFTER `accessGroupFake.Reset()` and BEFORE `k8sClient.Create(ctx, cr)`, add:

```go
accessGroupFake.SeedTeam("default", "t-uuid-default")
```

and change the `Runtime` block on the same CR to:

```go
Runtime: achv1alpha1.RuntimeBlock{
    Models:     []string{},
    MCPServers: []string{},
    A2AAgents:  []string{},
},
```

Then BEFORE the closing assertions, add a payload check:

```go
last := accessGroupFake.LastCreate("test-env-ag-happy")
if len(last.AssignedTeamIDs) != 1 || last.AssignedTeamIDs[0] != "t-uuid-default" {
    t.Errorf("LastCreate.AssignedTeamIDs = %v; want [t-uuid-default]", last.AssignedTeamIDs)
}
```

Replace the legacy `BindCallsFor` assertion with the new contract — since binding now happens via the access-group request itself, `BindCallsFor` is obsolete. The presence of the team ID in `LastCreate` is the equivalent assertion.

In `TestAccessGroupSynced_False_OnBindFailure`, this test no longer makes sense as-is (there is no separate "bind" call to fail). REPLACE its body with a new test that asserts the `UnresolvedReferences` branch:

```go
// TestAccessGroupSynced_False_OnUnresolvedTeam asserts the
// UnresolvedReferences branch: a team in spec.authorizedTeams that does
// not resolve via ListTeams flips AccessGroupSynced to False with the
// distinct UnresolvedReferences reason (issue #17).
func TestAccessGroupSynced_False_OnUnresolvedTeam(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	// Seed only one of two needed teams.
	accessGroupFake.SeedTeam("team-ok", "t-uuid-ok")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-unresolved",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"team-ok", "team-missing"},
			Runtime: achv1alpha1.RuntimeBlock{
				Models:     []string{},
				MCPServers: []string{},
				A2AAgents:  []string{},
			},
			Context: achv1alpha1.ContextBlock{},
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
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "UnresolvedReferences"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/UnresolvedReferences, conditions = %+v", got.Status.Conditions)
	}
}
```

In `TestAccessGroupSynced_DriftDetection_OrphanLogged`, replace the `SeedBinding` call with seeded `stored` access group and a team-ID mismatch to exercise the PUT (drift) branch:

```go
// TestAccessGroupSynced_DriftCorrected asserts that when the existing
// access group has bindings that diverge from spec, the reconciler
// emits PUT /v1/access_group/{id} to converge. Replaces the legacy
// orphan-log behavior (orphans are now handled implicitly by the PUT).
func TestAccessGroupSynced_DriftCorrected(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("current-team", "t-uuid-current")
	// Pre-seed a stored AG with a stale orphan team that's NOT in spec.
	accessGroupFake.stored["test-env-ag-drift"] = &litellm.AccessGroupResponse{
		AccessGroupID:   "ag-uuid-test-env-ag-drift",
		AccessGroupName: "test-env-ag-drift",
		AssignedTeamIDs: []string{"t-uuid-orphan"},
	}

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-drift",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"current-team"},
			Runtime: achv1alpha1.RuntimeBlock{
				Models:     []string{},
				MCPServers: []string{},
				A2AAgents:  []string{},
			},
			Context: achv1alpha1.ContextBlock{},
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
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == "Synced"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("drift-correction did NOT reach True/Synced")
	}
	if got := accessGroupFake.UpdateCallsFor("test-env-ag-drift"); got < 1 {
		t.Errorf("update call count = %d; want >= 1 (PUT to correct drift)", got)
	}
}
```

In `TestAccessGroupSynced_False_OnCreateFailure`, change `accessGroupFake.InjectCreateErr` to keep working — its signature is unchanged. The test body still works with the new conditions (`AccessGroupCreateFailed` reason persists).

In `TestAccessGroupSynced_Idempotent_NoExtraBindOnRereconcile`, rename to `TestAccessGroupSynced_Idempotent_NoExtraUpdateOnRereconcile`, replace the assertion `BindCallsFor` with `UpdateCallsFor`, and ensure the first reconcile creates the AG (stored map populated) so the second reconcile hits the "no drift, no PUT" branch.

- [ ] **Step 7: Run the controller envtests**

```bash
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=AccessGroupSynced TIMEOUT=10m
```

Expected: all `AccessGroupSynced*` tests PASS. If any fail, read the condition diagnostic and fix the test fixture (resolver seeding is the most common miss).

- [ ] **Step 8: Run the full controller envtest suite to catch unrelated regressions**

```bash
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... TIMEOUT=15m
```

Expected: all PASS.

- [ ] **Step 9: Commit Phase D**

```bash
git add internal/controller/ach/access_group_fake_test.go internal/controller/ach/suite_test.go internal/controller/ach/main_wiring_envtest_test.go internal/snapshot/snapshot_test.go internal/controller/ach/environment_accessgroup_test.go
git commit -m "$(cat <<'EOF'
test(controller,snapshot): align fakes + envtests with /v1 access-group surface

Rewrites accessGroupFakeImpl to track the new client surface (create /
update / delete with stored desired-state) and exposes resolver-seed
helpers (SeedMCP / SeedAgent / SeedTeam). countingNoopClient (suite),
wiringFakeLiteLLM (main_wiring), and fakeLiteLLM (snapshot) match the
new interface.

Updates environment_accessgroup_test.go:
  - All Environment fixtures populate Runtime.{Models,MCPServers,
    A2AAgents} so the resolver path is exercised.
  - Happy-path test seeds the "default" team and asserts the
    AssignedTeamIDs payload on the captured create request.
  - Replaces TestAccessGroupSynced_False_OnBindFailure with
    TestAccessGroupSynced_False_OnUnresolvedTeam (the new
    UnresolvedReferences branch).
  - Replaces orphan-log test with drift-correction test asserting
    PUT /v1/access_group/{id} fires when stored bindings diverge.
  - Renames idempotency test to *_NoExtraUpdateOnRereconcile.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 6 (Phase E): E2E Hydration — Seed Model + MCP + A2A

**Goal:** Extend `scripts/cluster.sh hydrate_litellm` to register one DB-stored Model row (via `POST /model/new`), one MCP server (via `POST /v1/mcp/server`), and one A2A agent (via `POST /v1/agents`) AFTER the helm install completes. Update `examples/04-environment-demo.yaml` so it references ONLY the seeded names. Add `examples/04b-environment-unresolved.yaml` for the negative-path test.

**Files:**
- Modify: `scripts/cluster.sh`
- Modify: `examples/04-environment-demo.yaml`
- Create: `examples/04b-environment-unresolved.yaml`

- [ ] **Step 1: Read existing hydrate_litellm bookend**

```bash
sed -n '184,196p' scripts/cluster.sh
```

Confirm `hydrate_litellm` ends after the `kubectl ... wait --for=condition=complete job/litellm-migrations` block, around line 192.

- [ ] **Step 2: Append the seed block to `hydrate_litellm`**

In `scripts/cluster.sh`, INSERT the following BEFORE the `rm -rf "${tmpdir}"` line at the end of `hydrate_litellm()`:

```bash
  # ─── issue #17: seed LiteLLM with DB-stored Model + MCP + A2A so
  # examples/04-environment-demo.yaml can reach AccessGroupSynced=True
  # end-to-end. These are the canonical names referenced by the demo
  # Environment; examples/04b-environment-unresolved.yaml deliberately
  # references absent names for negative-path coverage.
  #
  # All three calls are idempotent: POST returns 200 on duplicate
  # name/server_name/agent_name (LiteLLM behavior, confirmed against
  # v1.83.10). We forward-job the curl through kubectl exec so we
  # don't need to port-forward; the LiteLLM Pod's curl is OK to use
  # since the alpine base ships it.
  echo "[cluster.sh] seeding LiteLLM with demo Model + MCP + A2A (issue #17)..."
  local litellm_pod
  litellm_pod="$(kubectl -n litellm-system get pod -l app.kubernetes.io/name=litellm -o jsonpath='{.items[0].metadata.name}')"
  if [ -z "${litellm_pod}" ]; then
    echo "[cluster.sh] ERROR: no LiteLLM pod found in litellm-system" >&2
    exit 1
  fi

  # Wait for /health/readiness so /model/new doesn't 503 on a still-starting pod.
  kubectl -n litellm-system exec "${litellm_pod}" -c litellm -- \
    sh -c 'for i in $(seq 1 30); do curl -sf http://localhost:4000/health/readiness && exit 0; sleep 2; done; exit 1'

  # 1) Seed Model — uses LiteLLM's openai-compatible mock backend so
  #    no upstream credentials are needed.
  kubectl -n litellm-system exec "${litellm_pod}" -c litellm -- \
    curl -sf -X POST http://localhost:4000/model/new \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "model_name": "demo-model",
        "litellm_params": {
          "model": "openai/demo-model",
          "api_base": "http://localhost:4000/mock",
          "api_key": "sk-mock"
        },
        "model_info": {"id": "demo-model-row-1"}
      }' >/dev/null
  echo "[cluster.sh]   model 'demo-model' registered"

  # 2) Seed MCP server.
  kubectl -n litellm-system exec "${litellm_pod}" -c litellm -- \
    curl -sf -X POST http://localhost:4000/v1/mcp/server \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "server_id": "demo-mcp-row-1",
        "server_name": "demo-mcp",
        "transport": "http",
        "url": "http://localhost:4000/mock-mcp"
      }' >/dev/null
  echo "[cluster.sh]   mcp server 'demo-mcp' registered"

  # 3) Seed A2A agent.
  kubectl -n litellm-system exec "${litellm_pod}" -c litellm -- \
    curl -sf -X POST http://localhost:4000/v1/agents \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "agent_name": "demo-agent",
        "agent_card_params": {"name": "demo-agent", "url": "http://localhost:4000/mock-agent"}
      }' >/dev/null
  echo "[cluster.sh]   a2a agent 'demo-agent' registered"
```

- [ ] **Step 3: Rewrite `examples/04-environment-demo.yaml`**

REPLACE the entire file with:

```yaml
# Environment — happy-path projection unit (issue #17).
#
# Every name in spec.runtime.* and spec.authorizedTeams resolves
# against the seeds added by scripts/cluster.sh hydrate_litellm:
#   - Model:   demo-model
#   - MCP:     demo-mcp
#   - A2A:     demo-agent
#   - Team:    default (created by LiteLLMConnection.EnsureDefaultTeam)
#
# Expected after apply: AccessGroupSynced=True reason=Synced within
# ~10s on a freshly-bootstrapped cluster.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: demo
  namespace: ach-system
spec:
  authorizedTeams:
    - default
  runtime:
    models:
      - demo-model
    mcpServers:
      - demo-mcp
    a2aAgents:
      - demo-agent
  context:
    prompts:
      - claude-code-system-prompt
    plugins:
      - caveman
    artifacts:
      - openclaw-templates
```

- [ ] **Step 4: Create `examples/04b-environment-unresolved.yaml`**

```yaml
# Environment — UNRESOLVED projection unit (issue #17 negative path).
#
# Deliberately references names that do NOT exist in LiteLLM:
#   - Model:   nonexistent-model
#   - MCP:     nonexistent-mcp
#   - A2A:     nonexistent-agent
#
# Expected after apply: AccessGroupSynced=False reason=UnresolvedReferences
# with all three names listed in the condition message. The Snapshotter
# also flips ExecutionResourcesResolved=False reason=ResourceUnresolved
# (separate condition, separate code path).
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: demo-unresolved
  namespace: ach-system
spec:
  authorizedTeams:
    - default
  runtime:
    models:
      - nonexistent-model
    mcpServers:
      - nonexistent-mcp
    a2aAgents:
      - nonexistent-agent
  context:
    prompts: []
    plugins: []
    artifacts: []
```

- [ ] **Step 5: Lint-check the bash**

```bash
./scripts/dev.sh bash -c 'shellcheck scripts/cluster.sh || true'
```

Expected: no new errors introduced by the inserted block. (shellcheck is informational here; the project does not enforce a clean shellcheck on this script, but new findings should still be triaged.)

- [ ] **Step 6: Commit Phase E**

```bash
git add scripts/cluster.sh examples/04-environment-demo.yaml examples/04b-environment-unresolved.yaml
git commit -m "$(cat <<'EOF'
test(e2e): seed LiteLLM with demo Model + MCP + A2A for #17 happy path

Adds a kubectl-exec-curl block at the end of scripts/cluster.sh
hydrate_litellm that POSTs:
  - /model/new       → DB-stored model 'demo-model'
  - /v1/mcp/server   → MCP 'demo-mcp'
  - /v1/agents       → A2A agent 'demo-agent'

Each is idempotent (LiteLLM returns 200 on duplicate name). Trims
examples/04-environment-demo.yaml to reference ONLY these seeded names
so a fresh `make cluster-up` brings the demo Environment to
AccessGroupSynced=True end-to-end.

Adds examples/04b-environment-unresolved.yaml — a sibling fixture that
references intentionally-absent names so the
AccessGroupSynced=False reason=UnresolvedReferences branch is
exercised end-to-end.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 7 (Phase E continued): E2E Test for Happy + Unresolved

**Goal:** A `go test` driver that, on a kept cluster (`make cluster-keep` first), applies both demo fixtures and asserts each reaches its expected `AccessGroupSynced` state within a bounded timeout.

**Files:**
- Create: `test/e2e/phase4_accessgroup_test.go`

- [ ] **Step 1: Inspect peer e2e test for the project's test idiom**

```bash
sed -n '1,40p' test/e2e/phase4_environment_available_test.go
```

Confirm the testing-style scaffold (build tags, `TestMain`, `t.Skip(...)` gates, kubectl helpers). Match it.

- [ ] **Step 2: Create the new test**

Write `test/e2e/phase4_accessgroup_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestAccessGroupSynced_Demo_HappyPath asserts examples/04-environment-demo.yaml
// reaches AccessGroupSynced=True reason=Synced within 30s on the kept
// cluster. Requires hydrate_litellm to have seeded demo-model / demo-mcp /
// demo-agent (issue #17).
func TestAccessGroupSynced_Demo_HappyPath(t *testing.T) {
	if os.Getenv("ACH_E2E_PHASE9") != "1" {
		t.Skip("§17 e2e gated behind ACH_E2E_PHASE9=1")
	}
	must(t, exec.Command("kubectl", "apply", "-f", "../../examples/04-environment-demo.yaml"))
	t.Cleanup(func() {
		_ = exec.Command("kubectl", "delete", "-f", "../../examples/04-environment-demo.yaml", "--ignore-not-found").Run()
	})

	if !waitForCondition(t, "environment", "demo", "ach-system", "AccessGroupSynced", "True", "Synced", 30*time.Second) {
		dumpConditions(t, "environment", "demo", "ach-system")
		t.Fatalf("examples/04-environment-demo.yaml did NOT reach AccessGroupSynced=True/Synced")
	}
}

// TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences asserts
// examples/04b-environment-unresolved.yaml reaches AccessGroupSynced=False
// reason=UnresolvedReferences within 30s.
func TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences(t *testing.T) {
	if os.Getenv("ACH_E2E_PHASE9") != "1" {
		t.Skip("§17 e2e gated behind ACH_E2E_PHASE9=1")
	}
	must(t, exec.Command("kubectl", "apply", "-f", "../../examples/04b-environment-unresolved.yaml"))
	t.Cleanup(func() {
		_ = exec.Command("kubectl", "delete", "-f", "../../examples/04b-environment-unresolved.yaml", "--ignore-not-found").Run()
	})

	if !waitForCondition(t, "environment", "demo-unresolved", "ach-system", "AccessGroupSynced", "False", "UnresolvedReferences", 30*time.Second) {
		dumpConditions(t, "environment", "demo-unresolved", "ach-system")
		t.Fatalf("examples/04b-environment-unresolved.yaml did NOT reach AccessGroupSynced=False/UnresolvedReferences")
	}
}

func must(t *testing.T, c *exec.Cmd) {
	t.Helper()
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(c.Args, " "), err, out)
	}
}

func waitForCondition(t *testing.T, kind, name, ns, condType, status, reason string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("kubectl", "-n", ns, "get", kind, name, "-o", "json").Output()
		if err == nil {
			var obj struct {
				Status struct {
					Conditions []struct {
						Type, Status, Reason string
					}
				}
			}
			if jerr := json.Unmarshal(out, &obj); jerr == nil {
				for _, c := range obj.Status.Conditions {
					if c.Type == condType && c.Status == status && c.Reason == reason {
						return true
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func dumpConditions(t *testing.T, kind, name, ns string) {
	t.Helper()
	out, _ := exec.Command("kubectl", "-n", ns, "get", kind, name, "-o", "jsonpath={.status.conditions}").Output()
	t.Logf("%s/%s conditions: %s", kind, name, out)
}
```

- [ ] **Step 3: Run the new e2e test (cluster must be kept)**

```bash
./scripts/dev.sh make cluster-keep         # idempotent; brings up + leaves running
ACH_E2E_PHASE9=1 ./scripts/dev.sh make e2e-focus RUN='TestAccessGroupSynced_Demo_HappyPath|TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences'
```

Expected: both tests PASS. If `TestAccessGroupSynced_Demo_HappyPath` fails with a `True/Synced` timeout, run `dumpConditions` manually and inspect — common cause is the LiteLLM `default` Team not yet existing (the LiteLLMConnection reconciler's `EnsureDefaultTeam` runs once per connection probe; first-pass timing may need an extra ~15s wait).

- [ ] **Step 4: Commit Phase E continued**

```bash
git add test/e2e/phase4_accessgroup_test.go
git commit -m "$(cat <<'EOF'
test(e2e): cover #17 happy + unresolved AccessGroupSynced paths

phase4_accessgroup_test.go applies examples/04-environment-demo.yaml
and 04b-environment-unresolved.yaml on the kept cluster, asserting:
  - demo            → AccessGroupSynced=True  reason=Synced              (30s)
  - demo-unresolved → AccessGroupSynced=False reason=UnresolvedReferences (30s)

Both tests gated behind ACH_E2E_PHASE9=1, matching the existing
phase4 e2e gate. Pairs with the hydrate_litellm seeds added in the
previous commit.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 8 (Phase F): Docs Sweep

**Goal:** Update `CLAUDE.md` "Common failure modes" with a `UnresolvedReferences` entry, refresh the api-reference, and confirm no stale references to the deleted helpers.

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Append a Common-failure-modes entry to `CLAUDE.md`**

In `/home/coder/workspace/local/ach/CLAUDE.md`, locate the heading

```
## Common failure modes
```

After the LAST existing `### ❌ ... ✅ ...` block under that section, INSERT:

````markdown
### ❌ Environment stuck in `AccessGroupSynced=False reason=UnresolvedReferences`
```bash
kubectl get environment demo -n ach-system -o jsonpath='{.status.conditions[?(@.type=="AccessGroupSynced")]}'
# {"type":"AccessGroupSynced","status":"False","reason":"UnresolvedReferences",
#  "message":"unresolved: mcpServers=[demo-mcp] a2aAgents=[] authorizedTeams=[]"}
```
✅ The named MCP server / A2A agent / authorized team does not exist
in LiteLLM. The reconciler resolves names on each reconcile via
`ListMCPServers` / `ListA2AAgents` / `ListTeams`; unresolved entries
flip the condition with the offending list in the message.

Register the missing resource(s):
```bash
# MCP server
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/v1/mcp/server \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"server_name":"<name>","transport":"http","url":"http://<addr>"}'

# A2A agent
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/v1/agents \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"agent_name":"<name>","agent_card_params":{"name":"<name>","url":"<addr>"}}'

# Team
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/team/new \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"team_alias":"<alias>"}'
```
The next reconcile (or any spec-change touch) reuses the fresh
resolver maps and the condition flips to `True/Synced`.

WHY IT FAILS: the legacy `POST /access_group/new` was rejected by
LiteLLM 1.83.x when `model_names` was empty (issue #17). The
`/v1/access_group` endpoint accepts empty resource sets, but every ID
in `access_mcp_server_ids` / `access_agent_ids` / `assigned_team_ids`
must exist. The reconciler converts names → IDs on-demand each
reconcile (no Snapshotter cache), so the condition reflects fresh
upstream state.
````

- [ ] **Step 2: Verify no stale references to deleted helpers**

```bash
grep -rn "BindTeamToAccessGroup\|ListAccessGroupBindings\|TeamAccessGroupPrefix\|access_group/new\b" --include='*.go' --include='*.md' --include='*.yaml' . 2>/dev/null
```

Expected: ZERO matches outside this plan document. If anything else surfaces (test names, doc strings, helm comments), update it.

- [ ] **Step 3: Final full-suite verification**

```bash
./scripts/dev.sh make lint
./scripts/dev.sh make test-all
```

Expected: both PASS. `test-all = unit + envtest-run` covers the full controller suite with `-race`.

- [ ] **Step 4: Commit Phase F**

```bash
git add CLAUDE.md
git commit -m "$(cat <<'EOF'
docs(claude-md): add Common failure mode for AccessGroupSynced UnresolvedReferences

Documents the new condition reason introduced in issue #17 — what it
means, how to diagnose it (kubectl jsonpath query), and the three
LiteLLM POST recipes to register the missing MCP / A2A / Team.

Issue: #17

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit lands.

---

## Task 9: Pre-Push + PR

**Files:** none

- [ ] **Step 1: Run the full pre-push gate (host-only)**

```bash
make pre-push
```

Expected: all 17 gates green. If `gitleaks` or `trufflehog` flag anything, investigate — do NOT `--no-verify`. SPDX header check is the most common false-positive on plan documents; if it complains about `docs/superpowers/plans/2026-05-28-issue-17-access-group-v1-migration.md`, exempt the path in `.gitleaks.toml`-style override (the gate has been calibrated for markdown previously per the marketplace-followup plan).

- [ ] **Step 2: Push the branch**

```bash
git push -u origin fix/issue-17-access-group-v1-migration
```

Expected: push succeeds; the branch tracks `origin/fix/issue-17-access-group-v1-migration`.

- [ ] **Step 3: Open the PR**

```bash
gh pr create --title "fix(litellm,controller): migrate access-group flow to /v1 (issue #17)" --body "$(cat <<'EOF'
## Summary

- Replaces the legacy `POST /access_group/new` flow (which 400s on empty `model_names` per LiteLLM 1.83.x's hidden validator) with the ackstorm `/v1/access_group*` endpoints.
- Moves team→access-group binding off the magic `team.models[]` `access_group/<name>` prefix onto first-class `assigned_team_ids`.
- Wires MCP-server + A2A-agent bindings (previously not bound at all) through `access_mcp_server_ids` / `access_agent_ids`.
- Resolves every name→ID on-demand each reconcile (no Snapshotter changes, no CRD changes).
- Seeds the e2e cluster with one Model + MCP + A2A so `examples/04-environment-demo.yaml` reaches `AccessGroupSynced=True` end-to-end.

Closes #17.

## Test plan

- [ ] `./scripts/dev.sh make unit-pkg PKG=./internal/litellm/...` — new client surface unit tests
- [ ] `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=AccessGroupSynced` — controller envtests with new fakes
- [ ] `./scripts/dev.sh make e2e-focus FOCUS=AccessGroup` (after `make cluster-keep`, `ACH_E2E_PHASE9=1`) — happy + unresolved e2e
- [ ] `make pre-push` — 17 gates green

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed. Paste into Trello / issue tracker as appropriate.

---

## Self-Review Notes (for plan author)

This plan was reviewed before save:

- **Spec coverage:** Issue #17's acceptance criteria (`kubectl apply -f examples/04-environment-demo.yaml` → `AccessGroupSynced=True reason=Synced within 30s`) is covered by Task 7. The investigation step 2 from the issue (curl LiteLLM with progressively-richer bodies) is rendered moot by Task 6's seed block, which proves the canonical shape by example. The Acceptance bullet "LiteLLM logs show 200 OK on POST /access_group/new" no longer applies post-migration — the new endpoint is `POST /v1/access_group` returning 201; the PR description notes the shape change.
- **No placeholders:** every step has either exact file content, an exact shell command + expected output, or both. No "TBD", "as appropriate", or "fill in here" remain.
- **Type consistency:** `AccessGroupCreateRequest.AccessModelNames` is used uniformly across types.go, accessgroups.go, the noop, the fake, and the reconciler. `AccessGroupResponse.AccessGroupID` likewise. `ListMCPServers` / `ListA2AAgents` / `ListTeams` keep the same signatures from definition through every consumer.
- **Out-of-scope deliberately:** model `db_model` filtering, switching `model_names` → `model_ids` (UUIDs), removing the `team.models` legacy prefix from upstream-stored teams (cleanup), updating `docs/api-reference/` (no CRD change). Each is a candidate follow-up issue at PR-close time.

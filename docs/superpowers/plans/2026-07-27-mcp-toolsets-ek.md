# MCP Toolsets for Environment Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional, first-class `Environment.spec.runtime.mcpToolsets` support so EK-backed ach-agent workloads can discover, hydrate, authorize, and call administrator-owned LiteLLM Toolsets through `/toolset/{name}/mcp` without changing raw MCP-server behavior or exposing Toolsets to PK/user CLI consumers.

**Architecture:** LiteLLM remains the Toolset source of truth and CRUD owner. ACH discovers names through `GET /v1/mcp/toolset`, projects those names into its existing snapshot/catalog and Environment row, resolves the Environment's selected names to their `toolset_id`s and grants **the IDs** on the existing `ach-env-<environment>` shell team's `object_permission.mcp_toolsets` (LiteLLM enforces by ID, not name), and enforces the same Environment-bound allowlist in the forwarder before proxying the full `/toolset/{name}/mcp...` path to LiteLLM. The hydrate response exposes a distinct `runtime.mcpToolsets` arm only to EK callers; PK responses retain their exact current shape, and local CLI adapters do not render Toolsets in this release.

**Tech Stack:** Go 1.24 through the Docker-backed Make targets, controller-runtime/Kubernetes CRDs, pgx/Postgres migrations, chi/`httputil.ReverseProxy`, Helm, stdlib tests/envtest/e2e, LiteLLM v1.93 Toolset APIs.

**Compatibility Source:** [LiteLLM MCP Toolsets](https://docs.litellm.ai/docs/mcp_toolsets) is the narrative reference, but the docs omit the route, list shape, and permission details — the **v1.93.0 source** is authoritative (verified at tag `v1.93.0`): `proxy_server.py` registers exactly `/toolset/{toolset_name}/mcp`; the management router serves `GET/POST /v1/mcp/toolset` (+ `DELETE /v1/mcp/toolset/{toolset_id}`) returning a **bare JSON array**; `schema.prisma` defines `object_permission.mcp_toolsets` as **toolset IDs** ("Toolset IDs granted to this key/team/user"). The production deployment is LiteLLM v1.93.0; ACH's checked-in e2e image is v1.87.1 (which also ships the Toolset feature) and Task 8 upgrades the e2e cluster pin to v1.93.0 to match production.

## Global Constraints

- LiteLLM—not ACH—owns Toolset creation, update, deletion, membership, and per-tool selection; ACH implements discovery, cataloging, Environment references, name resolution, access control, and consumer exposure only.
- Raw MCP servers in `runtime.mcpServers` and Toolsets in `runtime.mcpToolsets` are independent, coexisting axes. Never encode a Toolset under `/mcp/{name}`.
- The only Toolset data-plane route is `/toolset/{name}/mcp`; the forwarder also accepts an arbitrary tail after `/mcp` and preserves the upstream path and query exactly. Note LiteLLM v1.93.0 registers only the exact `/toolset/{toolset_name}/mcp` path — tail forwarding is a permissive ACH surface, not a LiteLLM contract.
- MVP runtime consumption is EK-only and intended for ach-agent. PK/user CLI Toolset hydration, CLI adapter projection, per-agent tool filters, glob expansion, ACH Toolset CRUD, BIP matching, and identity JWT forwarding are out of scope.
- Toolset authorization comes from the Environment shell team's `object_permission.mcp_toolsets`, not from LiteLLM access groups. The granted values are **LiteLLM toolset IDs** resolved from the Environment's names at reconcile time — LiteLLM's `/toolset/{name}/mcp` handler resolves the name to a toolset and denies unless its `toolset_id` is in the grant list. Existing access-group model/MCP/A2A grants remain unchanged.
- User shell teams (`ach-user-<email>`) must continue sending an explicit empty `mcp_toolsets` list; only `ach-env-<environment>` receives selected Toolset names.
- Keep `schemaVersion: "v1alpha1"`. PK hydrate JSON must remain byte-compatible by omitting `mcpToolsets`; EK hydrate JSON emits `mcpToolsets` **only when the Environment defines Toolsets** and omits the key otherwise — the CLI manifest decoder is strict (`DisallowUnknownFields`, `internal/cli/manifest/manifest.go:77`), so always emitting the key would break every old strict decoder hydrating with an EK even on Toolset-free Environments. Updated ACH CLI decoding accepts the additive arm but deliberately does not project it.
- The ach-agent harness source is not in this repository. ACH's deliverable is its EK hydrate/data-plane contract: each item has a stable Toolset `id` and `/toolset/{name}/mcp` endpoint, and the harness uses its Environment key for that endpoint. Production activation is gated on an ach-agent image that consumes this additive arm; Environments without Toolsets remain compatible with older images.
- Upgrade the checked-in e2e cluster's LiteLLM image from v1.87.1 to v1.93.0 **to match the deployed production proxy** (v1.87.1 also ships the Toolset feature — the bump is version alignment, not feature availability) while retaining Helm chart `1.84.0`; the new live compatibility tests are the release gate for that image/chart combination.
- Every changed Go, SQL, YAML, Markdown, generated CRD, Helm CRD copy, and generated API reference retains the repository's SPDX/license conventions.
- Host Go is unavailable. Wrapped test/build/cluster targets run directly as `make <target>`. The standalone generator targets are the documented exception in `references/makefile.md`, so invoke `gen-code`, `gen-manifests`, `gen-crd-ref-docs`, and `helm-sync*` through `./scripts/dev.sh make ...`. Run host-only `make clean-cache` after the feature is finished.

---

## File and Responsibility Map

| Responsibility | Files |
|---|---|
| Environment API/status and generated surfaces | `api/ach/v1alpha1/environment_types.go`, `api/ach/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/ach.ackstorm.ai_environments.yaml`, `deploy/helm/ach/crd-sources/ach.ackstorm.ai_environments.yaml`, `docs/api-reference/ach.ackstorm.ai.md` |
| Durable Environment/catalog projection | `db/migrations/000019_mcp_toolsets.up.sql`, `db/migrations/000019_mcp_toolsets.down.sql`, `internal/db/environments.go`, `internal/db/environments_test.go`, `internal/db/runtime_catalog.go`, `internal/db/runtime_catalog_test.go`, `internal/db/ui_objects.go`, `internal/db/ui_objects_test.go` |
| LiteLLM discovery and snapshot | `internal/litellm/types.go`, `internal/litellm/client.go`, `internal/litellm/noop.go`, `internal/litellm/mcp_toolsets.go`, `internal/litellm/mcp_toolsets_test.go`, `internal/snapshot/snapshot.go`, `internal/snapshot/snapshot_test.go` and the test clients enumerated in Task 3 |
| Shell-team grants and Environment reconciliation | `internal/litellm/shellteam.go`, `internal/litellm/shellteam_test.go`, `internal/controller/ach/environment_shellteam.go`, `internal/controller/ach/environment_shellteam_test.go`, `internal/controller/ach/environment_controller.go`, `internal/controller/ach/environment_accessgroup_test.go`, `internal/controller/ach/environment_projection_test.go` |
| Admin catalog | `internal/platformapi/admin/runtime/handler.go`, `internal/platformapi/admin/runtime/handler_test.go`, `internal/platformapi/server.go`, `internal/platformapi/server_test.go` |
| Environment/UI/hydrate read surfaces | `internal/platformapi/store/adapter.go`, `internal/platformapi/objects/handler.go`, `internal/platformapi/objects/handler_test.go`, `internal/platformapi/objects/export_test.go`, `internal/platformapi/hydrate/handler.go`, `internal/platformapi/hydrate/handler_escape_test.go`, `internal/platformapi/hydrate/handler_mcp_toolsets_test.go` |
| Forwarder and edge routing | `internal/forwarder/precheck/check.go`, `internal/forwarder/precheck/check_test.go`, `internal/forwarder/proxy/handlers.go`, `internal/forwarder/proxy/handlers_test.go`, `internal/forwarder/proxy/proxy.go`, `internal/forwarder/proxy/proxy_test.go`, `internal/forwarder/server.go`, `internal/forwarder/server_test.go`, `internal/forwarder/metrics/counters.go`, `internal/forwarder/metrics/counters_test.go`, `internal/gateway/routes.go`, `internal/gateway/routes_test.go` |
| CLI compatibility | `internal/cli/manifest/manifest.go`, `internal/cli/manifest/manifest_test.go`, `internal/cli/adapter/{claudecode,codex,gemini,opencode,pimono}/*_test.go`, `cmd/ach-cli/cmd/hydrate_test.go` |
| Live compatibility and examples | `test/e2e/cluster/01-base/litellm.values.yaml`, `scripts/cluster.sh`, `test/e2e/cluster/05-environment/demo.yaml`, `test/e2e/cluster/05-environment/demo-unresolved.yaml`, `test/e2e/runtime_catalog_test.go`, `test/e2e/phase4_invariants_test.go`, `examples/hydrate.json`, `examples/README.md` |
| Contract documentation | `CLAUDE.md`, `references/understanding.md`, `references/troubleshooting.md`, `docs/ach-project-spec.md`, `docs/developer-guide/jwt-forwarder.md` |

## Dependency Graph and Phase Order

```text
Task 1 API contract
  ├─> Task 2 database/UI projection
  ├─> Task 3 LiteLLM discovery + snapshot/catalog
  │     ├─> Task 4 Environment resolution + shell permission
  │     └─> Task 5 admin catalog
  └─> Task 6 EK hydrate contract
Tasks 2 + 6 ─> Task 7 forwarder + gateway
Tasks 1-7 ───> Task 8 LiteLLM v1.93 live gate
Tasks 1-8 ───> Task 9 docs, generated artifacts, and full verification
```

Each task ends in a reviewable implementation commit. Do not combine commits: the boundaries isolate schema/storage, upstream discovery, authorization, consumer contract, routing, live compatibility, and documentation.

---

### Task 1: Add the First-Class Environment API Axis

**Files:**
- Modify: `api/ach/v1alpha1/environment_types.go:9-54` (`RuntimeBlock`), `:170-224` (`UnresolvedRuntime`), `:235-242` (`Environment` comment)
- Test: `internal/controller/ach/environment_available_test.go`
- Generated in Step 4 and committed here: `api/ach/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/ach.ackstorm.ai_environments.yaml` (Helm CRD copy + API reference sync happen in Task 9)

**Interfaces:**
- Produces: `RuntimeBlock.MCPToolsets []string` with JSON name `mcpToolsets`; `UnresolvedRuntime.MCPToolsets []string`.
- Validation: the same max length and strict no-slash deny-pattern as `MCPServers`, because `{name}` occupies one chi route segment.

- [ ] **Step 1: Write the failing API/defaulting test**

Add a table case that marshals an Environment containing both axes and asserts they remain distinct:

```go
env.Spec.Runtime = achv1alpha1.RuntimeBlock{
    MCPServers:  []string{"raw-github"},
    MCPToolsets: []string{"developer-tools"},
}
if got := env.Spec.Runtime.MCPToolsets; !slices.Equal(got, []string{"developer-tools"}) {
    t.Fatalf("mcpToolsets = %v", got)
}
```

Add envtest admission cases rejecting `bad/name`, `bad\\name`, whitespace, `?`, `#`, `%`, control characters, and names longer than 253 bytes under `spec.runtime.mcpToolsets`, while accepting `developer-tools.v1`.

- [ ] **Step 2: Run the focused test and confirm the red state**

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS=TestEnvironmentMCPToolsetsAdmission`

Expected: compile failure because `RuntimeBlock.MCPToolsets` does not exist.

- [ ] **Step 3: Add the API fields and documentation**

Insert this field between `MCPServers` and `A2AAgents`:

```go
// MCPToolsets lists LiteLLM Toolset names (toolset_name). Toolsets are
// administrator-owned named selections of tools from one or more MCP servers.
// ACH resolves and grants names but never owns Toolset CRUD or membership.
// Names route through /toolset/{name}/mcp, so the strict route-segment pattern
// forbids slash and backslash in addition to URL metacharacters and whitespace.
// +optional
// +kubebuilder:default={}
// +kubebuilder:validation:items:MaxLength=253
// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
MCPToolsets []string `json:"mcpToolsets,omitempty"`
```

Add the corresponding defaulted `MCPToolsets []string` to `UnresolvedRuntime`. Update the runtime-axis prose in both places: the `(models, mcpServers, a2aAgents)` list in the `Environment` doc comment (`environment_types.go:237-239`) and the "three runtime reference lists" phrase in the `UnresolvedRuntime` doc comment (`environment_types.go:210`).

- [ ] **Step 4: Generate only the artifacts required to run admission tests**

Run: `./scripts/dev.sh make gen-code gen-manifests`

Expected: DeepCopy and the base Environment CRD include both new arrays and admission rules.

- [ ] **Step 5: Run the API/admission tests**

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS=TestEnvironmentMCPToolsetsAdmission`

Expected: PASS for accepted names and the complete rejection table.

- [ ] **Step 6: Commit the API slice**

```bash
git add api/ach/v1alpha1/environment_types.go api/ach/v1alpha1/zz_generated.deepcopy.go config/crd/bases/ach.ackstorm.ai_environments.yaml internal/controller/ach/environment_available_test.go
git commit -m "feat(api): add environment MCP toolsets"
```

---

### Task 2: Persist Toolsets in Environment and Runtime-Catalog Projections

**Files:**
- Create: `db/migrations/000019_mcp_toolsets.up.sql`
- Create: `db/migrations/000019_mcp_toolsets.down.sql`
- Modify: `internal/db/environments.go:46-79`, `:102-160`, `:172-306`
- Modify: `internal/db/environments_test.go`
- Modify: `internal/db/ui_objects.go:60-140`
- Modify: `internal/db/ui_objects_test.go`
- Modify: `internal/db/runtime_catalog.go:19-30`, `:54-89`
- Modify: `internal/db/runtime_catalog_test.go`
- Modify: `internal/platformapi/store/adapter.go:110-138`
- Modify: `internal/platformapi/objects/handler.go:40-70`
- Test: `internal/platformapi/objects/handler_test.go`, `internal/platformapi/objects/export_test.go`

**Interfaces:**
- Produces: `EnvironmentRow.RuntimeMCPToolsets []string` and runtime catalog kind `mcp_toolset`.
- Migration 000019 is additive on upgrade and removes Toolset rows before restoring the older CHECK constraint on downgrade.

- [ ] **Step 1: Write failing DB round-trip and UI takeover tests**

Extend Environment row fixtures with:

```go
RuntimeMCPServers:  []string{"raw-github"},
RuntimeMCPToolsets: []string{"developer-tools"},
```

Assert insert, update-to-empty, `GetEnvironmentByName`, `ListEnvironments`, and `ListEnvironmentsIncludingDraining` preserve the two arrays independently. Extend UI draft create/update/export/takeover tests so YAML contains:

```yaml
runtime:
  mcpServers:
    - raw-github
  mcpToolsets:
    - developer-tools
```

Add a runtime-catalog test with `mcp_toolset/developer-tools` and assert it is active on first replace and tombstoned when absent on the next replace.

- [ ] **Step 2: Run the storage and Objects API tests and confirm failure**

Run: `make test-unit-pkg PKG=./internal/platformapi/objects`

Run: `make test-integration`

Expected: compile failures for `RuntimeMCPToolsets` and SQL failures before migration 000019 exists.

- [ ] **Step 3: Add migration 000019**

Use this exact upgrade:

```sql
ALTER TABLE environments
    ADD COLUMN runtime_mcp_toolsets text[] NOT NULL DEFAULT '{}';

ALTER TABLE runtime_catalog_entries
    DROP CONSTRAINT runtime_catalog_entries_kind_check;
ALTER TABLE runtime_catalog_entries
    ADD CONSTRAINT runtime_catalog_entries_kind_check
    CHECK (kind IN ('model','mcp_server','mcp_toolset','a2a_agent','team'));
```

Use this exact downgrade order:

```sql
DELETE FROM runtime_catalog_entries WHERE kind = 'mcp_toolset';
ALTER TABLE runtime_catalog_entries
    DROP CONSTRAINT runtime_catalog_entries_kind_check;
ALTER TABLE runtime_catalog_entries
    ADD CONSTRAINT runtime_catalog_entries_kind_check
    CHECK (kind IN ('model','mcp_server','a2a_agent','team'));
ALTER TABLE environments DROP COLUMN runtime_mcp_toolsets;
```

- [ ] **Step 4: Thread the column through every row query and UI write**

Add `RuntimeMCPToolsets []string // spec.runtime.mcpToolsets` to `EnvironmentRow`. Add `runtime_mcp_toolsets` and its bound/scanned value to `upsertEnvironmentSQL`, `UpsertEnvironmentTx`, all three SELECT/Scan blocks, `internal/db/ui_objects.go` insert/update arguments (via `uiEnvironmentArgs`), `store.RowToView`, and `objects.specToRow`/`objects.rowToSpec`. Keep the arrays non-nil: mirror the existing `RuntimeMCPServers` handling everywhere; the UI write path normalizes via `orEmptyStrings` (`ui_objects.go:49` — UI-path-only helper).

Change the catalog replace signature to:

```go
func ReplaceRuntimeCatalog(
    ctx context.Context,
    pool *pgxpool.Pool,
    ns, connector string,
    models, mcpServers, mcpToolsets, a2aAgents, teams map[string]struct{},
    syncedAt time.Time,
) error
```

and add `"mcp_toolset": mcpToolsets` to its kind map.

- [ ] **Step 5: Run DB and Objects API tests**

Run: `make test-integration`

Run: `make test-unit-pkg PKG=./internal/platformapi/objects`

Expected: PASS, including clearing the column to `{}` and UI YAML round-trip.

- [ ] **Step 6: Commit storage and UI projection**

```bash
git add db/migrations/000019_mcp_toolsets.*.sql internal/db/environments.go internal/db/environments_test.go internal/db/ui_objects.go internal/db/ui_objects_test.go internal/db/runtime_catalog.go internal/db/runtime_catalog_test.go internal/platformapi/store/adapter.go internal/platformapi/objects
git commit -m "feat(storage): project environment MCP toolsets"
```

---

### Task 3: Discover LiteLLM Toolsets and Publish Them in the Snapshot

**Files:**
- Create: `internal/litellm/mcp_toolsets.go`
- Create: `internal/litellm/mcp_toolsets_test.go`
- Modify: `internal/litellm/types.go:175-195`
- Modify: `internal/litellm/client.go:58-75`
- Modify: `internal/litellm/noop.go`
- Modify: `internal/snapshot/snapshot.go:31-64`, `:199-305`
- Modify: `internal/snapshot/snapshot_test.go`
- Modify interface fakes (standalone full-method-set implementations that break on interface growth): `internal/connection/client.go`, `internal/controller/ach/main_wiring_envtest_test.go`, `internal/orphan/runnable_test.go`, `internal/platformapi/admin/integration_helpers_test.go`, `internal/platformapi/auth/sso_test.go`, `internal/platformapi/teams/lookup_test.go`, `internal/snapshot/snapshot_test.go` (its `fakeLiteLLM` is standalone). No change needed for fakes that embed `*litellm.NoopClient` or the interface (`access_group_fake_test.go`, `suite_test.go`, envkeys tests, `keystore/teamsresolver_test.go`) — they inherit the new method.

**Interfaces:**
- Produces: `ToolsetEntry{ToolsetID, ToolsetName, Description}`, `Client.ListMCPToolsets(context.Context) ([]ToolsetEntry, error)`, and `LiteLLMSnapshot.MCPToolsets map[string]struct{}`.
- Consumes: LiteLLM v1.93 `GET /v1/mcp/toolset`; ACH ignores membership/tool details because they remain LiteLLM-owned.

- [ ] **Step 1: Write failing wire-contract tests**

Test the v1.93.0 list shape — a **bare JSON array** (verified in source; there is no `{"toolsets":[...]}` wrapper in v1.87.1 or v1.93.0):

```json
[{"toolset_id":"ts-1","toolset_name":"developer-tools","description":"CI tools"}]
```

Assert `GET /v1/mcp/toolset`, master-key auth through `makeRequest` (the endpoint is **permission-filtered for non-admin keys** — a non-admin key with no grants gets `[]`, so discovery MUST use the master key), empty result as `ErrNotFound`, and malformed JSON as a path-specific decode error.

- [ ] **Step 2: Run the LiteLLM tests red**

Run: `make test-unit-pkg PKG=./internal/litellm`

Expected: compile failure because `ListMCPToolsets` and `ToolsetEntry` do not exist.

- [ ] **Step 3: Implement the narrow read-only client**

Add:

```go
type ToolsetEntry struct {
    ToolsetID   string `json:"toolset_id"`
    ToolsetName string `json:"toolset_name"`
    Description string `json:"description,omitempty"`
}
```

`ListMCPToolsets` must issue only `GET /v1/mcp/toolset`, decode the bare-array response, discard all fields except identity/catalog metadata (keep `ToolsetID` — Task 4 grants by ID), and return `ErrNotFound` for zero names. Add the method to `Client`, `RESTClient`, `NoopClient`, and every standalone fake listed above.

- [ ] **Step 4: Write failing snapshot atomicity tests**

Extend `fakeLiteLLM` with `toolsets`, `toolsetsErr`, and a call counter. Assert:

- a successful refresh publishes `MCPToolsets={"developer-tools"}` and persists kind `mcp_toolset`;
- `ErrNotFound` publishes an empty non-nil set;
- a Toolset list transport error marks the whole prior snapshot stale without partially publishing newer model/MCP/A2A/team data;
- empty Toolset names are skipped.

- [ ] **Step 5: Extend the snapshot refresh**

Add `MCPToolsets map[string]struct{}` to `LiteLLMSnapshot`, call `ListMCPToolsets` alongside the existing four list calls, normalize `ErrNotFound`, include its error in the all-or-nothing failure branch, construct the name set, pass it to `ReplaceRuntimeCatalog`, and include `mcpToolsets` in the success log.

- [ ] **Step 6: Run discovery and snapshot tests**

Run: `make test-unit-pkg PKG=./internal/litellm`

Run: `make test-unit-pkg PKG=./internal/snapshot`

Expected: PASS for both response shapes and all-or-nothing stale preservation.

- [ ] **Step 7: Commit discovery**

```bash
git add internal/litellm internal/snapshot internal/connection/client.go internal/controller/ach/main_wiring_envtest_test.go internal/orphan/runnable_test.go internal/platformapi/admin/integration_helpers_test.go internal/platformapi/auth/sso_test.go internal/platformapi/teams/lookup_test.go
git commit -m "feat(litellm): discover MCP toolsets"
```

---

### Task 4: Resolve Toolset Names and Reconcile Environment Shell-Team Permissions

**Files:**
- Modify: `internal/litellm/types.go:112-150` (`TeamObjectPermission` + its `MarshalJSON` nil→`[]` normalization)
- Modify: `internal/litellm/shellteam.go:62-90` (`ShellTeamPermissions`/`denyAllTeamRequest`/`NewShellTeamRequest`), `:120-149` (`ShellTeamDrifted`)
- Modify: `internal/litellm/shellteam_test.go`
- Modify: `internal/controller/ach/environment_shellteam.go:27-137`
- Modify: `internal/controller/ach/environment_shellteam_test.go`
- Modify: `internal/controller/ach/environment_controller.go:225-305`, `:589-606`, `:642-805`
- Modify: `internal/controller/ach/environment_accessgroup_test.go`

**Interfaces:**
- Produces: `EnvironmentShellTeamPermissions(toolsetIDs []string) *TeamObjectPermission`, `NewShellTeamRequest(env string, toolsetIDs []string)`, and `ShellTeamDrifted(entry TeamListEntry, desiredToolsetIDs []string) bool`. All three take **LiteLLM toolset IDs** — LiteLLM enforces `object_permission.mcp_toolsets` by `toolset_id`, never by name.
- Does not modify `AccessGroupCreateRequest`, `AccessGroupUpdateRequest`, or `AccessGroupResponse`.

- [ ] **Step 1: Write failing permission-shape tests**

Assert the Environment shell request serializes exactly (values are **toolset IDs**, not names):

```json
{
  "models":["__deny_all__"],
  "object_permission":{
    "mcp_servers":[],
    "mcp_access_groups":[],
    "mcp_toolsets":["ts-1"],
    "agents":["00000000-0000-0000-0000-000000000000"],
    "agent_access_groups":[]
  }
}
```

Also assert `denyAllTeamRequest` for a user shell writes `mcp_toolsets: []`, nil slices normalize to `[]` (via `TeamObjectPermission.MarshalJSON`), order/duplicates normalize to a sorted unique set, and add/remove drift is detected.

- [ ] **Step 2: Run shell-team tests red**

Run: `make test-unit-pkg PKG=./internal/litellm`

Expected: missing `MCPToolsets` field and old function signatures.

- [ ] **Step 3: Implement authoritative Toolset permission shaping**

Add this field to `TeamObjectPermission` and include it in nil-to-empty marshal normalization:

```go
MCPToolsets []string `json:"mcp_toolsets"`
```

Keep `ShellTeamPermissions()` as the deny-all/user-shell shape with empty Toolsets. Add `EnvironmentShellTeamPermissions(toolsetIDs)` which copies, drops empty values, sorts, deduplicates, and assigns only `MCPToolsets` over that base.

Change Environment shell create/repair/read-back drift checks to use the desired set. Preserve the ownership metadata gate, model sentinel, agent sentinel, and the current unverifiable-read logging behavior.

- [ ] **Step 4: Write failing reconciler tests**

Cover these cases in `environment_accessgroup_test.go` and `environment_shellteam_test.go`:

1. selected Toolset name resolves to its `toolset_id` and the **ID** is written to `ach-env-demo`;
2. raw MCP server IDs still go only to the access group;
3. Toolset names/IDs never appear in `AccessMCPServerIDs` or any access-group payload;
4. removing a Toolset repairs the shell to `mcp_toolsets: []`;
5. an absent Toolset yields both `UnresolvedRuntime.MCPToolsets=[name]` and `AccessGroupSynced=False/UnresolvedReferences`;
6. `ListMCPToolsets` failure yields `AccessGroupSynced=False/ResolveFailed`;
7. a shell update that returns the wrong Toolset set yields `ShellTeamFailed`;
8. user shell permissions stay empty.

- [ ] **Step 5: Implement Environment resolution and projection**

In the snapshot set-difference, add `env.Spec.Runtime.MCPToolsets \\ snap.MCPToolsets`, include its count in totals/messages, and write it into `EnvironmentRow.RuntimeMCPToolsets`.

In `reconcileAccessGroup`, list Toolsets fresh, normalize `ErrNotFound`, build a `name → toolset_id` map from the `ToolsetEntry` list, resolve each requested name to its ID, and fail `UnresolvedReferences` when any is missing. Pass the resolved **IDs** to `ensureShellTeam` (the CRD, status, catalog, and hydrate keep using names; only the LiteLLM grant is ID-typed). Do not add a Toolset field to any access-group request or drift function.

- [ ] **Step 6: Run unit and envtest coverage**

Run: `make test-unit-pkg PKG=./internal/litellm`

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS=TestAccessGroupSynced`

Expected: PASS, with the Toolset grant observable only on the Environment shell team.

- [ ] **Step 7: Commit authorization**

```bash
git add internal/litellm/types.go internal/litellm/shellteam.go internal/litellm/shellteam_test.go internal/controller/ach/environment_shellteam.go internal/controller/ach/environment_shellteam_test.go internal/controller/ach/environment_controller.go internal/controller/ach/environment_accessgroup_test.go
git commit -m "feat(operator): grant environment MCP toolsets"
```

---

### Task 5: Expose Toolsets in the Admin Runtime Catalog Without Adding CRUD

**Files:**
- Modify: `internal/platformapi/admin/runtime/handler.go:96-139`
- Modify: `internal/platformapi/admin/runtime/handler_test.go`
- Modify: `internal/platformapi/server.go`
- Modify: `internal/platformapi/server_test.go`

**Interfaces:**
- Produces: `GET /platform/admin/runtime/mcp-toolsets` and `mcpToolsets` in the combined catalog.
- Explicitly does not add `ach-cli runtime toolsets`, write endpoints, or EK access to admin catalog routes.

- [ ] **Step 1: Write failing handler and mount tests**

Seed catalog rows for `mcp_server/raw-github` and `mcp_toolset/developer-tools`. Assert the single-kind endpoint returns only `kind:"mcp_toolset"`, combined JSON contains non-null arrays for both `mcpServers` and `mcpToolsets`, and the existing AdminOnly middleware still rejects EK with `401 invalid_key_type`.

- [ ] **Step 2: Run tests red**

Run: `make test-unit-pkg PKG=./internal/platformapi/admin/runtime`

Expected: route/handler absent and combined catalog missing `mcpToolsets`.

- [ ] **Step 3: Add the read-only catalog surface**

Add:

```go
func MCPToolsetsHandler(d Deps) http.HandlerFunc {
    return kindHandler(d, "mcp_toolset")
}
```

Partition `mcp_toolset` rows into a separately initialized slice and render `"mcpToolsets": toolsets`. Mount only `GET /platform/admin/runtime/mcp-toolsets` under the existing admin group.

- [ ] **Step 4: Run catalog tests**

Run: `make test-unit-pkg PKG=./internal/platformapi/admin/runtime`

Run: `make test-unit-pkg PKG=./internal/platformapi`

Expected: PASS; no new write route exists.

- [ ] **Step 5: Commit catalog exposure**

```bash
git add internal/platformapi/admin/runtime internal/platformapi/server.go internal/platformapi/server_test.go
git commit -m "feat(platform-api): catalog MCP toolsets"
```

---

### Task 6: Add an EK-Only Hydrate Arm While Preserving PK/CLI Compatibility

**Files:**
- Create: `internal/platformapi/hydrate/handler_mcp_toolsets_test.go`
- Modify: `internal/platformapi/hydrate/handler.go:59-119`, response construction around `:260-390`
- Modify: `internal/platformapi/hydrate/handler_escape_test.go`
- Modify: `internal/cli/manifest/manifest.go:22-42`
- Modify: `internal/cli/manifest/manifest_test.go`
- Test unchanged rendering: `internal/cli/adapter/claudecode/claudecode_test.go`, `codex/codex_test.go`, `gemini/gemini_test.go`, `opencode/opencode_test.go`, `pimono/pimono_test.go`
- Modify: `cmd/ach-cli/cmd/hydrate_test.go`

**Interfaces:**
- Produces for EK: `runtime.mcpToolsets: [{"id":"developer-tools","endpoint":"<base>/toolset/developer-tools/mcp"}]` — **only when the Environment defines Toolsets**; a Toolset-free Environment omits the key so older strict decoders (including old ach-agent images) keep working.
- Produces for PK: no `mcpToolsets` JSON member at all, retaining compatibility with older strict CLI decoders.
- The current CLI decoder accepts the optional field but all five local adapters ignore it.

- [ ] **Step 1: Write the caller-type contract tests**

Use one Environment row with a raw MCP server and a Toolset. Assert:

- EK response contains both `mcpServers` and `mcpToolsets`, with Toolset path escaped through `url.PathEscape` and ending in `/mcp`;
- an EK response for a Toolset-free Environment omits the `mcpToolsets` key entirely (old strict decoders keep working);
- PK response contains `mcpServers` but no `mcpToolsets` key, for both empty and populated Environment Toolset lists;
- wrong-environment EK behavior remains `403 wrong_environment`.

- [ ] **Step 2: Run hydrate tests red**

Run: `make test-unit-pkg PKG=./internal/platformapi/hydrate`

Expected: missing response field.

- [ ] **Step 3: Implement caller-sensitive serialization**

Represent the new arm as a plain slice with `omitempty` — nil/empty means the key is absent, which is the required shape for both PK (always) and EK on a Toolset-free Environment:

```go
type RuntimeBlock struct {
    Models      []RuntimeItem `json:"models"`
    MCPServers  []RuntimeItem `json:"mcpServers"`
    MCPToolsets []RuntimeItem `json:"mcpToolsets,omitempty"`
    A2AAgents   []RuntimeItem `json:"a2aAgents"`
}
```

After caller-type resolution, populate `MCPToolsets` only for `keys.PrefixEk` and only when `len(row.RuntimeMCPToolsets) > 0`, appending one item per name with `BaseURL + "/toolset/" + url.PathEscape(name) + "/mcp"`. Never merge Toolsets into `MCPServers`.

- [ ] **Step 4: Make the current CLI decoder additive but non-consuming**

Add the following field to `manifest.RuntimeBlock`:

```go
MCPToolsets []ContentRef `json:"mcpToolsets,omitempty"`
```

Add a decode test proving the new EK response parses. Add adapter regression tests passing a populated Toolset arm and asserting output bytes/state keys are identical to the same manifest with that arm empty; this makes “PK/user CLI consumption is out of scope” executable rather than implicit.

- [ ] **Step 5: Run hydrate, manifest, and adapter tests**

Run: `make test-unit-pkg PKG=./internal/platformapi/hydrate`

Run: `make test-unit-pkg PKG=./internal/cli/manifest`

Run: `make test-unit-pkg PKG=./internal/cli/adapter/...`

Expected: PASS; PK golden snippets remain unchanged and EK decoding accepts the additive arm.

- [ ] **Step 6: Commit consumer contract**

```bash
git add internal/platformapi/hydrate internal/cli/manifest internal/cli/adapter/*/*_test.go cmd/ach-cli/cmd/hydrate_test.go
git commit -m "feat(hydrate): expose MCP toolsets to environment keys"
```

---

### Task 7: Add the EK-Only `/toolset/{name}/mcp` Forwarder and Gateway Route

**Files:**
- Modify: `internal/forwarder/precheck/check.go:39-87`, `:98-112`
- Modify: `internal/forwarder/precheck/check_test.go`
- Modify: `internal/forwarder/proxy/handlers.go:91-177`
- Modify: `internal/forwarder/proxy/handlers_test.go`
- Modify: `internal/forwarder/proxy/proxy.go:60-130`, `:159-196`
- Modify: `internal/forwarder/proxy/proxy_test.go`
- Modify: `internal/forwarder/server.go:40-90`
- Modify: `internal/forwarder/server_test.go`
- Modify: `internal/forwarder/metrics/counters.go`, `internal/forwarder/metrics/counters_test.go`
- Modify: `internal/gateway/routes.go:15-37`, `internal/gateway/routes_test.go`

**Interfaces:**
- Produces: `precheck.CheckMCPToolset`, `proxy.HandlerMCPToolset`, authenticated routes `/toolset/{name}/mcp` and `/toolset/{name}/mcp/*`, metrics label `/toolset`, and gateway prefix `/toolset/`.
- No BIP lookup, JWT signing, `Authorization` forwarding, path collapsing, tag injection, or PK team-union logic occurs on this route.

- [ ] **Step 1: Write failing precheck tests**

Add `toolsetResourceKind` and test:

- EK bound to an active Environment containing the exact Toolset name passes;
- missing/terminating/wrong Environment and absent name return `ErrUnauthorizedResource`;
- PK returns `ErrInvalidKeyType` without calling `TeamsResolver`;
- raw MCP membership does not authorize a same-named Toolset and vice versa.

- [ ] **Step 2: Run precheck tests red**

Run: `make test-unit-pkg PKG=./internal/forwarder/precheck`

Expected: `CheckMCPToolset` absent.

- [ ] **Step 3: Implement the EK-only precheck**

Extend `runtimeList` to return `row.RuntimeMCPToolsets`. Implement `CheckMCPToolset` with an explicit key-type guard:

```go
func CheckMCPToolset(ctx context.Context, kc middleware.KeyContext, name string, deps Deps) error {
    if kc.KeyType != keys.PrefixEk {
        return ErrInvalidKeyType
    }
    return checkEk(ctx, kc, name, deps, toolsetResourceKind)
}
```

- [ ] **Step 4: Write failing handler/director/router tests**

Assert:

1. `/toolset/developer-tools/mcp` and `/toolset/developer-tools/mcp/` reach the handler;
2. `/toolset/developer-tools/mcp/v1/message?session=x` reaches LiteLLM with the exact path/query;
3. `/toolset/developer-tools`, `/toolset/developer-tools/tools`, and `/mcp/developer-tools` do not reach the Toolset handler;
4. the Director sends the caller's decrypted LiteLLM key as `X-Litellm-Api-Key: Bearer <key>` for `/toolset`, matching LiteLLM's MCP auth parser;
5. client `Authorization`, `x-litellm-*`, and `x-ach-*` headers are stripped;
6. no BIP resolver or signer call occurs and upstream `Authorization` is absent;
7. PK receives `401 invalid_key_type`; disallowed EK receives `403 unauthorized_resource`;
8. metrics use route `/toolset` and the existing closed outcome values;
9. gateway preserves the entire Toolset path.

- [ ] **Step 5: Implement the handler and path-preserving proxy branch**

Add `HandlerMCPToolset` as a dedicated handler: retrieve `{name}`, run `CheckMCPToolset`, classify through the existing precheck error functions, record `/toolset` metrics, and invoke the shared reverse proxy directly. Do not call `handlerNamed`, because that function necessarily resolves BIPs and signs JWTs.

Add `/toolset` to `routeFor`. In the Director, treat `/toolset` as an MCP-auth route for the `Bearer ` prefix but do not call `mcpServerPath`; leave `URL.Path`, `RawPath`, and `RawQuery` untouched.

Register exactly:

```go
r.Handle("/toolset/{name}/mcp", proxy.HandlerMCPToolset(hdeps))
r.Handle("/toolset/{name}/mcp/*", proxy.HandlerMCPToolset(hdeps))
```

Add `{Prefix: "/toolset/", Upstream: forwarder}` to `gateway.ServiceRoutes`.

- [ ] **Step 6: Run forwarder and gateway tests**

Run: `make test-unit-pkg PKG=./internal/forwarder/...`

Run: `make test-unit-pkg PKG=./internal/gateway`

Expected: PASS, including exact tail preservation and zero JWT/BIP calls.

- [ ] **Step 7: Commit routing**

```bash
git add internal/forwarder internal/gateway
git commit -m "feat(forwarder): proxy scoped MCP toolsets"
```

---

### Task 8: Upgrade the E2E LiteLLM Pin and Prove v1.93 Compatibility End-to-End

**Files:**
- Modify: `test/e2e/cluster/01-base/litellm.values.yaml:26-37` (image/chartVersion block)
- Modify: `scripts/cluster.sh:79-84` (fixture name constants), `reconcile_litellm` at `:241-447`, `verify_all` at `:722-806`
- Modify: `test/e2e/cluster/05-environment/demo.yaml`
- Modify: `test/e2e/cluster/05-environment/demo-unresolved.yaml`
- Modify: `test/e2e/runtime_catalog_test.go`
- Modify: `test/e2e/phase4_invariants_test.go:94-175`
- Verify unchanged: `examples/hydrate.json`
- Modify: `examples/README.md`

**Interfaces:**
- Uses Toolset fixture name `demo-toolset`, containing the `echo` tool from `demo-mcp-nojwt`.
- The shell-team grant is verified from LiteLLM `GET /team/info`; the data path is verified through the public ACH gateway, not by bypassing the forwarder.

- [ ] **Step 1: Add the failing synced fixture and live assertions before changing the pin**

Add to `demo.yaml`:

```yaml
runtime:
  mcpToolsets:
    - demo-toolset
```

Add `nonexistent-toolset` to the unresolved fixture (matching its existing `nonexistent-*` naming convention). Extend Environment tests to assert `unresolvedRuntime.mcpToolsets`, runtime catalog tests to find `mcp_toolset/demo-toolset`, and phase-4 invariants to test denied/allowed Toolset routes, PK rejection, and unchanged raw `/mcp` success. (Note: `TestAccessGroupSynced_Demo_HappyPath` lives in `test/e2e/phase4_accessgroup_test.go` — extend its shell-team assertion there if needed.)

- [ ] **Step 2: Run the focused e2e test and confirm the red state**

Run: `make e2e-focus RUN='TestPhase4Invariants'`

Expected: failure because `demo-toolset` is not yet seeded in LiteLLM — the Environment reports `UnresolvedReferences` and `/toolset/demo-toolset/mcp` 404s. Do NOT expect a version-related failure: v1.87.1 already ships the Toolset API, route, and `mcp_toolsets` permission; the red state here is the missing fixture seed, and it reproduces on either version.

- [ ] **Step 3: Upgrade the e2e cluster's LiteLLM pin to v1.93.0**

Change only the image tag to:

```yaml
image:
  repository: ghcr.io/berriai/litellm-database
  tag: v1.93.0
```

Retain `chartVersion: 1.84.0`, `pullPolicy: IfNotPresent`, custom auth, database image overrides, and existing model/MCP/A2A fixture behavior. Update comments to state that v1.93.0 is pinned to **match the deployed production proxy** and is the release the Toolset gate is verified against (`/v1/mcp/toolset`, `/toolset/{name}/mcp`, and ID-typed `object_permission.mcp_toolsets` also exist in v1.87.1 — the bump is version alignment, not feature availability).

- [ ] **Step 4: Seed Toolsets idempotently in `reconcile_litellm`**

After MCP servers exist, query `GET /v1/mcp/toolset`, delete every stale row named `demo-toolset` by `toolset_id`, resolve the current `demo-mcp-nojwt` `server_id` into the shell variable `mcp_nojwt_server_id`, then POST this JSON with the variable expanded by the existing `jq`-based seeding pattern:

```json
{
  "toolset_name":"demo-toolset",
  "description":"ACH e2e Toolset",
  "tools":[{"server_id":"${mcp_nojwt_server_id}","tool_name":"echo"}]
}
```

Use the existing bounded readiness/seed style and fail immediately if the server ID, Toolset ID/name, or POST response is absent. Capture the created Toolset's `toolset_id` from the POST response (assert non-empty) — Step 5's `/team/info` assertion needs it, because the shell-team grant is the ID, not the name. Extend `verify_all` to require `demo-toolset` from `/v1/mcp/toolset` before applying/gating Environments.

- [ ] **Step 5: Assert the live permission and data path**

Using an EK bound to `demo`, assert:

- `GET /team/info?team_id=ach-env-demo` contains `object_permission.mcp_toolsets=["<demo-toolset's toolset_id>"]` (the ID captured at seeding — LiteLLM grants are ID-typed) plus all existing deny-all fields;
- MCP initialize and `tools/list` through `/toolset/demo-toolset/mcp` return the `echo` tool;
- a JSON-RPC `tools/call` through `/toolset/demo-toolset/mcp` returns the echo payload;
- `/toolset/disallowed/mcp` is `403 unauthorized_resource`;
- the same allowed request with PK is `401 invalid_key_type`;
- the backend capture has no ACH identity JWT, proving the MVP exclusion;
- `/mcp/demo-mcp-jwt` still exercises the existing BIP/JWT route independently.

- [ ] **Step 6: Update the golden without exposing Toolsets to PK**

Keep `examples/hydrate.json` byte-for-byte unchanged for the PK golden. Add an EK-specific structural assertion in e2e rather than changing the PK golden; document in `examples/README.md` that the golden intentionally represents the supported PK CLI response and therefore omits EK-only `mcpToolsets`.

- [ ] **Step 7: Run the live gates**

Run: `make cluster-up`

Run: `make e2e-focus RUN='TestRuntimeCatalogAdminOnly'`

Run: `make e2e-focus RUN='TestAccessGroupSynced_Demo_HappyPath'`

Run: `make e2e-focus RUN='TestPhase4Invariants'`

Expected: all PASS on LiteLLM v1.93.0, including raw MCP coexistence and Toolset no-JWT behavior.

- [ ] **Step 8: Commit the compatibility gate**

```bash
git add test/e2e/cluster/01-base/litellm.values.yaml scripts/cluster.sh test/e2e/cluster/05-environment test/e2e/runtime_catalog_test.go test/e2e/phase4_invariants_test.go examples/README.md
git commit -m "test(e2e): gate MCP toolsets on LiteLLM v1.93"
```

---

### Task 9: Synchronize Generated Artifacts, Documentation, and Final Gates

**Files:**
- Modify generated: `api/ach/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/ach.ackstorm.ai_environments.yaml`, `deploy/helm/ach/crd-sources/ach.ackstorm.ai_environments.yaml`, `docs/api-reference/ach.ackstorm.ai.md`
- Modify docs: `CLAUDE.md`, `references/understanding.md`, `references/troubleshooting.md`, `docs/ach-project-spec.md`, `docs/developer-guide/jwt-forwarder.md`, `examples/agent-runtime/README.md`

**Interfaces:**
- Documents the final public contract and its exclusions; introduces no implementation behavior.

- [ ] **Step 1: Update contract documentation before generation**

Make these exact documentation changes:

- `CLAUDE.md`: add `mcpToolsets` to Environment runtime axes; state LiteLLM ownership; describe shell-team `mcp_toolsets`; add `/toolset/{name}/mcp...` to forwarder/gateway critical paths; mark EK/ach-agent only and no BIP/JWT/PK CLI.
- `references/understanding.md`: update snapshot/catalog/database/runtime/hydrate/forwarder sections and the projection table column list; distinguish Toolsets from raw MCP servers.
- `references/troubleshooting.md`: add a Toolset decision tree covering unresolved name, shell permission absent, PK `invalid_key_type`, v1.87.1 missing route/API, and identity-dependent backend unsupported.
- `docs/ach-project-spec.md`: add the fourth Environment runtime axis and LiteLLM ownership boundary.
- `docs/developer-guide/jwt-forwarder.md`: state `/mcp` and `/a2a` remain the only JWT routes; `/toolset` deliberately strips client Authorization and forwards no ACH JWT in MVP.
- `examples/agent-runtime/README.md`: document the ach-agent compatibility gate: only an image that understands EK hydrate `runtime.mcpToolsets` may consume a Toolset-enabled Environment; omission stays backward-compatible.

- [ ] **Step 2: Regenerate and synchronize all API artifacts**

Run: `./scripts/dev.sh make gen-code gen-manifests`

Run: `./scripts/dev.sh make helm-sync`

Run: `./scripts/dev.sh make gen-crd-ref-docs`

Expected: only the Environment DeepCopy, base CRD, Helm CRD source, and ACH API-reference output change.

- [ ] **Step 3: Run fast verification**

Run: `make test-unit`

Run: `make qa-lint-changed`

Run: `./scripts/dev.sh make helm-sync-check`

Expected: PASS with no generated drift.

- [ ] **Step 4: Run controller and security gates**

Run: `make test-envtest`

Run: `make qa-security`

Expected: PASS.

- [ ] **Step 5: Run the mandatory full live gate**

Run: `make e2e-full`

Expected: PASS on LiteLLM v1.93.0. This is mandatory because the change touches controller, platform-api, forwarder, API, Helm CRDs, and e2e fixtures.

- [ ] **Step 6: Inspect the final diff and clean the toolchain cache**

Run: `git diff --check`

Run: `git status --short`

Expected: only scoped implementation, tests, generated artifacts, fixtures, and documentation are present.

Run: `make clean-cache`

- [ ] **Step 7: Commit documentation and generated artifacts**

```bash
git add CLAUDE.md references/understanding.md references/troubleshooting.md docs/ach-project-spec.md docs/developer-guide/jwt-forwarder.md docs/api-reference/ach.ackstorm.ai.md examples/agent-runtime/README.md api/ach/v1alpha1/zz_generated.deepcopy.go config/crd/bases/ach.ackstorm.ai_environments.yaml deploy/helm/ach/crd-sources/ach.ackstorm.ai_environments.yaml
git commit -m "docs: document EK MCP toolsets"
```

## Final Acceptance Checklist

- [ ] An Environment may omit `mcpToolsets`; omission behaves as an empty list and does not change existing environments.
- [ ] Toolset names resolve through LiteLLM discovery and appear separately in CR status, Postgres, admin catalog, Objects API, and EK hydrate.
- [ ] `ach-env-<environment>` is authoritative for `object_permission.mcp_toolsets`, granted as **LiteLLM toolset IDs** resolved from the Environment's names; access groups remain unchanged and user shells stay empty.
- [ ] Removing a name from the Environment removes it from the shell team on the next successful reconcile.
- [ ] `/toolset/{name}/mcp` and every tail under it preserve path/query to LiteLLM, accept only EK, and precheck exact Environment membership.
- [ ] Toolset requests never perform BIP resolution, mint an ACH JWT, forward caller Authorization, expand globs, or apply a per-agent tool filter.
- [ ] `/mcp/{name}` continues to serve raw MCP servers with its existing path-collapse and optional BIP/JWT behavior.
- [ ] PK hydrate remains byte-compatible; EK hydrate omits `mcpToolsets` on Toolset-free Environments (old strict decoders keep working); local adapters do not render Toolsets; current CLI decoding tolerates an EK Toolset arm.
- [ ] No ACH Toolset CRUD endpoint, CRD, database ownership table, or CLI management command exists.
- [ ] The e2e cluster's LiteLLM pin is v1.93.0 (matching the deployed production proxy) and the live gate proves list/create/permission/route semantics before release.

## Self-Review Record

- **Spec coverage:** Every approved requirement maps to Tasks 1-9. Ownership and exclusions are in Global Constraints; Environment/API and resolution are Tasks 1/3/4; additive shell permission is Task 4; EK hydrate/ach-agent contract is Task 6; exact route/precheck/no-JWT behavior is Task 7; v1.87.1→v1.93.0 compatibility is Task 8; generated artifacts, docs, and mandatory gates are Task 9.
- **Scope decomposition:** This is one vertical feature rather than independent projects. The task/commit boundaries are independently reviewable but only the ordered whole produces a usable capability.
- **Completeness scan:** The plan contains no deferred implementation markers, unspecified error handling, generic “write tests” steps, or unnamed files/functions. The external fixture is fixed as `demo-toolset` using `demo-mcp-nojwt/echo`.
- **Type consistency:** `MCPToolsets` is the Go field, `mcpToolsets` the CRD/hydrate/catalog JSON field, `runtime_mcp_toolsets` the Environment projection column, `mcp_toolset` the catalog kind, and `mcp_toolsets` the LiteLLM `object_permission` field throughout. Names are the ACH-facing identifier everywhere (CRD, status, catalog, hydrate, route); the LiteLLM grant alone carries `toolset_id`s, resolved from names at reconcile. `ListMCPToolsets`, `CheckMCPToolset`, and `/toolset/{name}/mcp` retain those exact names across producer and consumer tasks.
- **Compatibility check:** PK responses omit the new field, EK responses add the arm only when populated (omitted on Toolset-free Environments, so old strict decoders survive), CLI adapters ignore it, access groups are untouched, raw MCP routes remain separate, and user shells receive an explicit empty Toolset list. LiteLLM wire semantics (ID-typed grants, bare-array list, exact `/toolset/{name}/mcp` route) verified against the v1.93.0 source, not the docs.
- **Security check:** Authorization is fail-closed at both shell-team and forwarder layers; only Environment-bound EKs pass; no client Authorization or ACH identity JWT reaches identity-dependent Toolsets; ownership/adoption gates on shell teams remain intact.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-27-mcp-toolsets-ek.md`.

Two implementation options are supported by the required worker skill:

1. **Subagent-Driven (recommended):** use `superpowers:subagent-driven-development`, one fresh implementer per task with review between commits.
2. **Inline Execution:** use `superpowers:executing-plans`, execute the tasks in order with checkpoints after each commit.

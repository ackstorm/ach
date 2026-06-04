# Plan — `ach admin list` object inventory (read-only)

**Date:** 2026-06-04
**Author:** planner (separate agent executes — this doc is self-contained; do
not assume prior conversation context).
**Goal:** Admin-only, kubectl-free, read-only inventory of all ACH-defined
objects with version + sync status, sourced from the Postgres projections (the
SoT read path). No live cross-checks.

## Locked decisions (do not re-litigate)

- **Per-kind REST endpoints** under `/platform/admin/<kind>`, each paginatable
  (`{items, next_cursor}`, in-memory offset slice — mirror the environments
  `ListHandler`). `ach admin list all` fans out parallel GETs from the CLI.
- **Projection-only sync** — read stored conditions + `last_successful_refresh`
  staleness straight from Postgres. No LiteLLM/k8s live check.
- **Admin-gated** — reuse `AdminOnly()` middleware (pk- allowlist).
- **Exclude soft-deleted** rows (`deletion_timestamp IS NULL`) — mirror
  `ListEnvironments`. Showing terminating objects is a possible follow-up, NOT
  this plan.

## Kinds, tables, helpers, sync source

| CLI kind | Table | List helper | Version | Sync source |
|----------|-------|-------------|---------|-------------|
| environments | `environments` | `ListEnvironments` ✅ (+ reuse `GET /platform/environments`) | `resource_version` | `available_condition` |
| plugins | `plugins` | **new** `ListPlugins` | `resource_version` | refresh staleness |
| prompts | `prompts` | **new** `ListPrompts` | `resource_version` | staleness ⚠ false-green |
| artifacts | `artifacts` | **new** `ListArtifacts` | `resource_version` | staleness ⚠ false-green |
| marketplaces | `marketplace_plugins` | **new** `ListAllMarketplacePlugins` | (use `updated_at`) | refresh staleness |
| bips | `backend_identity_policies` | `ListAllBIPs` ✅ | `observed_generation` | projected presence |
| litellm-connections | `litellm_connections` | **new** `ListLitellmConnections` | `resource_version` | projected presence |
| external-refs | `external_refs` | **new** `ListExternalRefs` | (use `updated_at`) | refresh staleness |

⚠ **false-green** (memory `plugin-scoping-followups`): only `context.plugins`
content-presence gates the Environment `ExecutionResourcesResolved` condition
(Task B9, reason `UnresolvedContextPlugins`). `prompts`/`artifacts` are
name-only / pass-through — their `last_successful_refresh` reflects "name
resolved", not "content present + current". The view MUST render their sync as
`fresh*` (asterisk) with a footnote, never bare `fresh`. Do not imply a
guarantee the operator does not enforce.

## Uniform view DTO (server + CLI share this shape)

Define once in the new server package; CLI decodes the same fields.

```go
type AdminObjectView struct {
	Kind       string            `json:"kind"`                 // "plugin","environment",...
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Version    string            `json:"version,omitempty"`    // resource_version | observed_generation | ""
	Sync       string            `json:"sync"`                 // see SYNC semantics
	SyncReason string            `json:"syncReason,omitempty"` // e.g. failed sub-condition
	UpdatedAt  string            `json:"updatedAt,omitempty"`  // RFC3339
	Origin     string            `json:"origin,omitempty"`
	Locked     bool              `json:"locked"`
	Extra      map[string]string `json:"extra,omitempty"`      // kind-specific: target,endpoint,contentType,scope,marketplace
}
```

### SYNC semantics (computed server-side with `time.Now()`)

- **environments** (from `available_condition` jsonb → `metav1.Condition`):
  `Status==True` → `Available`; `False` → `Degraded` + `SyncReason` =
  whichever of AccessGroupSynced / ExecutionResourcesResolved is False (read
  their jsonb too); `Unknown`/absent → `Pending`.
- **content (plugins/prompts/artifacts/marketplaces/external-refs)**:
  `LastSuccessfulRefresh == nil` → `never`;
  `now > LastSuccessfulRefresh + MaxStalenessSeconds` → `STALE` (`SyncReason` =
  staleness age, e.g. `2h over`); else `fresh`.
  For **prompts/artifacts** render `fresh*` (footnote at table bottom).
- **bips / litellm-connections**: `projected` (presence). (`deletion_timestamp`
  rows already excluded, so no `terminating` state.)

---

## Commit 1 — db: missing List helpers

**Files:** `internal/db/plugins.go`, `prompts.go`, `artifacts.go`,
`marketplace_plugins.go`, `litellm_connections.go`, `external_refs.go`.

For each, add a `List<Kind>(ctx, pool, [ns]) ([]<Kind>Row, error)` mirroring
`internal/db/environments.go:187 ListEnvironments`:

```go
// ListEnvironments pattern to mirror (env version):
//   WHERE namespace = $1 AND deletion_timestamp IS NULL
//   ORDER BY name ASC
//   → scan each row into <Kind>Row, return []<Kind>Row
```

- Reuse each table's **existing `Get<Kind>ByName` SELECT column list verbatim**
  (e.g. plugins: `namespace, name, storage_location, last_successful_refresh,
  max_staleness_seconds, deletion_timestamp, resource_version, updated_at` —
  `internal/db/plugins.go:124`). Same `<Kind>Row` struct already defined; do
  not invent new structs.
- `marketplace_plugins` / `external_refs` have no `namespace` PK — list all
  rows; key is (`marketplace_name`,`name`) / (`kind`,`name`). Check the table
  schema (`internal/db/migrations/000001_init.up.sql`) for the exact columns
  and Row struct before writing the SELECT.
- WHERE: include `deletion_timestamp IS NULL` for tables that have the column
  (plugins/prompts/artifacts/litellm_connections); marketplace_plugins /
  external_refs have no deletion_timestamp — list all.
- ORDER BY name ASC (or the table's PK) for stable output.

**Tests:** one table-driven unit test per helper next to the file
(`*_test.go`), using the existing db test harness (check how
`environments_test.go` / `plugins_test.go` set up the pool — pgxmock or a real
test pool). Assert column scan + ordering + soft-delete exclusion.

**Verify:** `make test-unit-pkg PKG=./internal/db/...`
**Commit:** `feat(db): add projection List helpers for admin inventory`

---

## Commit 2 — platform-api: admin read endpoints

**New package:** `internal/platformapi/admin/inventory/` (keep the existing
`admin/` write handlers untouched).

1. `view.go` — the `AdminObjectView` struct above + per-kind mappers
   (`pluginRowToView(r db.PluginRow) AdminObjectView`, etc.) + the SYNC
   computation helpers. Mappers own the false-green `fresh*` marker for
   prompts/artifacts (or pass a flag the renderer honors — pick one, document
   it inline).
2. `handler.go` — one `http.HandlerFunc` per kind. Each:
   - call the db `List<Kind>` helper via the pool already on `admin.Deps`
     (the Revoke handlers prove `Deps` has DB access — reuse the same field;
     do NOT add new wiring unless the pool field is genuinely absent).
   - map rows → `[]AdminObjectView`.
   - paginate **in memory** exactly like
     `internal/platformapi/environments/handler.go:61 ListHandler`: parse
     `?limit` (default 100, clamp ≤500), `?cursor` (base64 offset, the
     decode/encode helpers at handler.go:100-170), slice `[offset:end]`, emit
     `render.JSON(w, 200, map[string]any{"items": items, "next_cursor": next})`.
3. Register routes in `internal/platformapi/admin/mount.go` inside the existing
   `Mount` (already under `AdminOnly`):

```go
func Mount(deps Deps) func(r chi.Router) {
	return func(r chi.Router) {
		r.Use(AdminOnly(deps.Allowlist, deps.Audit, deps.Namespace))
		r.Post("/keys/revoke", RevokeKeyHandler(deps))
		r.Post("/users/{email}/revoke-keys", RevokeUserKeysHandler(deps))
		r.Post("/refresh", ForceRefreshHandler(deps))
		// new — read inventory:
		r.Get("/plugins", inventory.PluginsHandler(deps))
		r.Get("/prompts", inventory.PromptsHandler(deps))
		r.Get("/artifacts", inventory.ArtifactsHandler(deps))
		r.Get("/marketplaces", inventory.MarketplacesHandler(deps))
		r.Get("/bips", inventory.BIPsHandler(deps))
		r.Get("/litellm-connections", inventory.LitellmConnectionsHandler(deps))
		r.Get("/external-refs", inventory.ExternalRefsHandler(deps))
	}
}
```

   - `environments` gets **no new route** — CLI uses existing
     `GET /platform/environments` (admin sees all via the admin bypass in
     `environments/handler.go`).
   - If `inventory` importing `admin.Deps` creates an import cycle, pass the
     concrete pool/store into the inventory handlers instead of the whole Deps.

**Tests:** httptest unit tests per handler (mock/stub the db List). Assert
AdminOnly gating (ek- → 401/403), pagination envelope, SYNC mapping for each
state (fresh/STALE/never/Available/Degraded). Pattern: copy
`environments/handler_test.go`.

**Verify:** `make test-unit-pkg PKG=./internal/platformapi/...` then
`make qa-lint-changed`. Read-only — no envtest needed.
**Commit:** `feat(platform-api): admin read endpoints for object inventory`

---

## Commit 3 — ach-cli: `ach admin list`

**Files:** `cmd/ach-cli/cmd/admin.go` (+ new `internal/cli/render/inventory.go`).

1. `newAdminListCmd()` — positional arg `<kind|all>`, validated against the
   kind set {environments,plugins,prompts,artifacts,marketplaces,bips,
   litellm-connections,external-refs,all}. Wire under `newAdminCmd()` next to
   `keys`/`users`/`refresh`. Add `-o, --output table|json|yaml` (default
   `table`).
2. `runAdminList` — for each requested kind, GET its endpoint with the cursor
   loop from `cmd/ach-cli/cmd/env_keys.go:349 runEnvKeysList`:

```go
all := []render.AdminObjectView{}
cursor := ""
for {
	path := buildAdminListPath(kind, cursor, limit) // /platform/admin/<kind>?cursor=&limit=
	var resp struct {
		Items      []render.AdminObjectView `json:"items"`
		NextCursor string                   `json:"next_cursor"`
	}
	if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	all = append(all, resp.Items...)
	if resp.NextCursor == "" {
		break
	}
	cursor = resp.NextCursor
}
```

   - `kind == "all"` → run every kind concurrently (`errgroup.Group`), collect
     into `map[string][]AdminObjectView`. Environments branch hits
     `/platform/environments` and maps `EnvironmentView` → `AdminObjectView`.
   - `hc` = the existing `httpclient.Client` (`Do(ctx, method, path, nil,
     &out)` — `internal/cli/httpclient/client.go:113`). Bearer resolved via the
     existing `resolveEnvKeysBearer` / admin credential path already used by
     `admin keys revoke`.
3. Rendering in `internal/cli/render/inventory.go` (tabwriter lives in
   `render`, never inline — mirror `render/ek.go:58 FormatEkList`):
   - `FormatAdminInventory(map[string][]AdminObjectView) string` — grouped by
     kind, columns `KIND  NAME  VERSION  SYNC  AGE  ORIGIN`, sorted by name.
     Append the `* prompts/artifacts: name-resolved; content presence not
     gated` footnote only when any `fresh*` row is present.
   - json/yaml output: marshal the map directly (sigs.k8s.io/yaml for yaml,
     already a dep).
4. **Build the host CLI binary** before manual test — `make build-cli-host`
   (produces `bin/ach-cli-host`, CGO=0, auto-routes to devtools). NOTE: the
   memory note claiming "no make target" is stale; `build-cli-host` exists.

**Tests:** unit-test `FormatAdminInventory` (golden-ish string asserts incl.
the footnote trigger) + arg validation. `make test-unit-pkg PKG=./internal/cli/...`
**Commit:** `feat(cli): ach admin list inventory command`

---

## Commit 4 — docs

- Admin command reference (wherever `admin keys`/`refresh` are documented —
  grep `docs/` + `examples/README.md`) — add `admin list` usage + the false-green
  caveat for prompts/artifacts.
- `examples/` snippet showing `ach admin list all` output.
- CLAUDE.md: add `admin list` to the platform-api `pk_`/`ek_` lifecycle / admin
  surface description ONLY if a contract line changes; likely a one-line note,
  not a MANDATORY-table edit. Same-commit doc hygiene rule applies.
**Commit:** `docs(cli): document ach admin list inventory`

---

## Out of scope / deferred
- Live cross-check vs LiteLLM/k8s (rejected fork).
- User-scoped (non-admin) inventory.
- Watch/`-w` streaming.
- Showing terminating (soft-deleted) objects.
- Closing the prompts/artifacts false-green gating (separate follow-up; this
  plan only surfaces it honestly).

## Verification gate (whole feature, before push)
- Per-commit: the unit/lint commands listed above.
- Feature touches `internal/platformapi/` → **`make e2e-full` green required
  before push** (CLAUDE.md hard rule). db + cli commits are unit-isolated.
- Push only through the pre-push gate (18 gates); never `--no-verify`.

## Open checks for the executor (verify, don't assume)
1. `admin.Deps` field name for the pgx pool (used by Revoke handlers) — reuse
   it; if read handlers would create an import cycle with `inventory`, inject
   the pool/store directly.
2. `marketplace_plugins` / `external_refs` exact columns + Row struct (no
   namespace/deletion_timestamp) — read `000001_init.up.sql` before the SELECT.
3. Whether `bips` `observed_generation` is the right "version" to show, vs
   `resource_version` (BIP row has both? check `backend_identity_policies.go`).
4. Admin credential resolution for a GET in `admin.go` — confirm the same
   bearer path as `admin keys revoke` works for GET.

# Phase 5: Content Service + Cross-component Observability — Pattern Map

**Mapped:** 2026-05-27
**Files analyzed:** 28 new/modified files (Go + SQL + Helm)
**Analogs found:** 26 / 28 (2 files have no direct analog — `internal/metrics/` package is greenfield, sendfile E2E gate is novel)

This map pins, file by file, which existing ACH source to copy the shape from.
Every file in Phase 5 must respect:

- SPDX header (`// SPDX-License-Identifier: Apache-2.0`) — see `hack/boilerplate.go.txt` and every existing `*.go`.
- pgx error discipline: `pgconn.PgError` class 08/57 → raw err (transient); else wrap with non-secret identifiers (see `internal/db/errors.go` `isTransientPgErr`).
- chi router idioms: explicit per-kind routes, `RegisterRoutes(r chi.Router, d Deps)` shape (see existing `internal/contentservice/handler.go` lines 32–56).
- Constructor-time validation: `errors.New("...nil ...")` guards (see `internal/keystore/keystore.go:NewCachedResolver` lines 94–110).
- §15.5 error envelope: `internal/platformapi/render/json.go:Error(w, status, code, message, requestID)` reused verbatim.

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `db/migrations/000004_cs_projection.up.sql` | migration | DDL | `db/migrations/000003_litellm_token.up.sql` + `000001_init.up.sql` | role-match |
| `db/migrations/000004_cs_projection.down.sql` | migration | DDL | `db/migrations/000003_litellm_token.down.sql` | exact |
| `internal/db/environments.go` | db | CRUD | `internal/db/external_refs.go` | exact |
| `internal/db/plugins.go` | db | CRUD + CTE | `internal/db/external_refs.go` + `internal/db/marketplace_plugins.go` | exact |
| `internal/db/prompts.go` | db | CRUD | `internal/db/external_refs.go` | exact |
| `internal/db/artifacts.go` | db | CRUD | `internal/db/external_refs.go` | exact |
| `internal/controller/ach/environment_controller.go` (EXTEND) | reconciler | k8s↔DB projection | self (current file lines 252–289) | self-extend |
| `internal/controller/ach/plugin_projection_controller.go` | reconciler | k8s→DB | `internal/controller/ach/plugin_controller.go` + existing `EnvironmentReconciler.Reconcile` | role-match |
| `internal/controller/ach/prompt_projection_controller.go` | reconciler | k8s→DB | `internal/controller/ach/prompt_controller.go` | exact |
| `internal/controller/ach/artifact_projection_controller.go` | reconciler | k8s→DB | `internal/controller/ach/artifact_controller.go` | exact |
| `internal/contentservice/handler.go` (REWRITE) | handler | request-response | `internal/platformapi/server.go` + current handler.go RegisterRoutes block | role-match |
| `internal/contentservice/pipeline.go` | service | request-response | `internal/keystore/keystore.go:Resolve` (chained-gate idiom) | role-match |
| `internal/contentservice/authz.go` | service | gate-fn | `internal/controller/ach/environment_controller.go:reconcileAccessGroup` (per-step branches) | role-match |
| `internal/contentservice/envcache/cache.go` | service | Redis cache | `internal/keystore/teamsresolver.go:redisCachedTeamsResolver` | exact |
| `internal/contentservice/stream.go` | handler | file-I/O (sendfile) | current `internal/contentservice/handler.go` lines 119–125 (replaces `http.ServeContent`) | self-rewrite |
| `internal/contentservice/errors.go` | service | error mapping | `internal/platformapi/render/json.go:Error` + `internal/platformapi/middleware/middleware.go:RecoverPanic` | role-match |
| `internal/contentservice/content_type.go` | utility | RETAINED | (unchanged) | identity |
| `internal/contentservice/paths.go` | utility | RETAINED with `scope`-dispatch refactor | (unchanged signature) | identity |
| `internal/contentservice/k8s.go` | utility | REMOVED | n/a | delete |
| `internal/contentservice/handler_test.go` (REWRITE) | test | integration | `internal/keystore/keystore_test.go` + current handler_test.go scaffold | role-match |
| `internal/contentservice/pipeline_test.go` | test | unit (table-driven) | `internal/db/external_refs_test.go` table-driven shape | role-match |
| `internal/contentservice/stream_test.go` | test | unit | current `internal/contentservice/handler_test.go` lines 73–96 | role-match |
| `internal/contentservice/authz_test.go` | test | unit | `internal/keystore/teamsresolver_test.go` | role-match |
| `internal/metrics/registry.go` | metrics | greenfield | (no in-repo analog — wraps `prometheus/client_golang`) | none |
| `internal/metrics/forwarder.go` | metrics | counters | `internal/forwarder/metrics/counters.go` (signatures) | role-match |
| `internal/metrics/contentservice.go` | metrics | counters | parallel to forwarder.go | role-match |
| `internal/metrics/shared.go` | metrics | shared counter | (greenfield) | none |
| `internal/metrics/buckets.go` | config | constants | `internal/keystore/keystore.go:defaultTTL` (const block pattern) | role-match |
| `internal/metrics/metrics_test.go` | test | unit | `internal/db/db_test.go` | role-match |
| `internal/forwarder/metrics/counters.go` (REWRITE bodies) | shim | counters | current `internal/forwarder/metrics/counters.go` (signatures kept) | self-shim |
| `cmd/ach/cmd/content_service.go` (REWIRE) | command | bootstrap | `cmd/ach/cmd/platform_api.go:buildPlatformAPIDeps` + self | role-match |
| `deploy/helm/ach/templates/operator-deployment.yaml` (annotate) | helm | scrape annotation | self (current file lines 44–47, 116–119) | self-extend |
| `deploy/helm/ach/templates/forwarder-deployment.yaml` (annotate) | helm | scrape annotation | self | self-extend |
| `deploy/helm/ach/templates/platform-api-deployment.yaml` (annotate) | helm | scrape annotation | self | self-extend |
| `examples/prometheus-servicemonitor.yaml` | example | k8s manifest | (greenfield reference only) | none |

---

## Pattern Assignments

### `db/migrations/000004_cs_projection.up.sql` (migration, DDL)

**Analog:** `db/migrations/000003_litellm_token.up.sql` (for shape) + `db/migrations/000001_init.up.sql` (for full CREATE TABLE statements).

**Header comment pattern** (from `000003_litellm_token.up.sql` lines 1–32 — explain rationale + IF NOT EXISTS rationale):

```sql
-- 000004_cs_projection.up.sql
--
-- Phase 5 spec v4 §5.2 reversal — DB projection layer for ACH CRDs
-- consumed by Content Service (D-13). Adds four tables: environments,
-- plugins, prompts, artifacts. PK = (namespace, name) per CRD.
--
-- IF NOT EXISTS provides defense-in-depth against partial-apply re-run
-- drift; golang-migrate transaction-per-migration semantics +
-- db.Migrate's ErrNoChange collapse already make re-application a
-- clean no-op in the happy path.
```

**Table shape** — mirror columns from `internal/db/external_refs.go:ExternalRef` (`storage_location text`, `last_successful_refresh timestamptz`, `max_staleness_seconds bigint`) AND add `namespace text NOT NULL`, `deletion_timestamp timestamptz NULL`, `resource_version text`, `updated_at timestamptz DEFAULT now()`. Use `CREATE TABLE IF NOT EXISTS` + indexes:

```sql
CREATE TABLE IF NOT EXISTS environments (
    namespace text NOT NULL,
    name text NOT NULL,
    authorized_teams text[] NOT NULL DEFAULT '{}',
    context_prompts text[] NOT NULL DEFAULT '{}',
    context_plugins text[] NOT NULL DEFAULT '{}',
    context_artifacts text[] NOT NULL DEFAULT '{}',
    runtime_mcp_servers text[] NOT NULL DEFAULT '{}',
    runtime_a2a_agents text[] NOT NULL DEFAULT '{}',
    available_condition jsonb NULL,
    access_group_synced_condition jsonb NULL,
    execution_resources_resolved_condition jsonb NULL,
    deletion_timestamp timestamptz NULL,
    resource_version text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);
```

(Repeat shape for `plugins`, `prompts`, `artifacts` — each with their kind-specific columns per D-13. `artifacts` adds `scope text NOT NULL CHECK (scope IN ('object','directory'))`.)

### `db/migrations/000004_cs_projection.down.sql` (migration, DDL)

**Analog:** `db/migrations/000003_litellm_token.down.sql` lines 1–17. Same `DROP INDEX … ; DROP TABLE …` ordering convention.

```sql
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS prompts;
DROP TABLE IF EXISTS plugins;
DROP TABLE IF EXISTS environments;
```

---

### `internal/db/environments.go` (db, CRUD)

**Analog:** `internal/db/external_refs.go` (lines 41–124).

**Imports + package doc** (copy from lines 1–39):

```go
// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the environments projection table (Phase 5 D-13,
// spec v4 §5.2 reversal). The Environment reconciler upserts the row in
// the same transaction as its Status write; Content Service reads via
// GetEnvironmentByName.

package db

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)
```

**Struct shape** (from `ExternalRef` lines 46–54 — pure value type, no methods):

```go
type EnvironmentRow struct {
    Namespace        string
    Name             string
    AuthorizedTeams  []string
    ContextPrompts   []string
    ContextPlugins   []string
    ContextArtifacts []string
    // ... etc per D-13
    DeletionTimestamp *time.Time
    ResourceVersion   string
    UpdatedAt         time.Time
}
```

**UPSERT pattern** (from `UpsertExternalRef` lines 67–92): `INSERT ... ON CONFLICT (namespace, name) DO UPDATE SET col = EXCLUDED.col`. Each value binds via `$N` (T-02-03-01 — zero string concatenation). pgconn class 08/57 → raw err.

**GET pattern** (from `GetExternalRef` lines 100–124): `pgx.ErrNoRows → (nil, nil)`. Returns `*EnvironmentRow`.

**Soft-delete pattern** (NEW — no exact analog; combine `Delete*` shape with `UPDATE ... SET deletion_timestamp = now()`):

```go
func SoftDeleteEnvironment(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
    const sql = `UPDATE environments SET deletion_timestamp = now() WHERE namespace=$1 AND name=$2`
    // Same isTransientPgErr / fmt.Errorf wrap pattern
}
```

**Hard-delete** (from `DeleteExternalRef` lines 152–161): Called after finalizer drain.

---

### `internal/db/plugins.go` (db, CRUD + §12.3 CTE)

**Analog:** `internal/db/external_refs.go` (CRUD shape) + `internal/db/marketplace_plugins.go` (alphabetical-lowest cursor at lines 84–121).

Same CRUD shape as `environments.go` above.

**ResolvePluginByName §12.3 CTE** — single CTE returning `(source, namespace, name, storage_location, last_successful_refresh, max_staleness_seconds)`:

```go
func ResolvePluginByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*PluginResolution, error) {
    const sql = `
        WITH plugin_match AS (
            SELECT 'plugin'::text AS source, namespace, name, storage_location,
                   last_successful_refresh, max_staleness_seconds
            FROM plugins
            WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
        ),
        marketplace_match AS (
            SELECT 'marketplace'::text AS source, marketplace_name AS namespace,
                   name, storage_location,
                   last_successful_refresh, max_staleness_seconds
            FROM marketplace_plugins
            WHERE name = $2
            ORDER BY marketplace_name ASC
            LIMIT 1
        )
        SELECT * FROM plugin_match
        UNION ALL
        SELECT * FROM marketplace_match WHERE NOT EXISTS (SELECT 1 FROM plugin_match)
        LIMIT 1
    `
    // ... QueryRow + Scan + errors.Is(err, pgx.ErrNoRows) → (nil, nil)
}
```

**Planner note:** The current `marketplace_plugins` table uses `(marketplace_name, name)` PK (see `internal/db/marketplace_plugins.go` lines 36–45). The CTE column names above already match the live schema. Verify in planning if column rename was applied between Phase 2 and Phase 5.

---

### `internal/db/prompts.go` and `internal/db/artifacts.go` (db, CRUD)

**Analog:** `internal/db/environments.go` (this file) — identical UPSERT/GET/SoftDelete shape. `prompts` adds `content_type text NULL` column; `artifacts` adds `scope text NOT NULL CHECK (scope IN ('object','directory'))`.

---

### `internal/controller/ach/environment_controller.go` (EXTENDED, not replaced)

**Per D-15: SAME controller — add the projection write to the existing Reconcile().** Do NOT create a second EnvironmentReconciler.

**Insertion point:** Between the existing in-memory condition compose (lines 252–286) and the `r.Status().Update(ctx, &env)` call at line 287. Add a `db.UpsertEnvironment(...)` call BEFORE the `Status().Update`. Per D-Discretion: DB write FIRST (load-bearing), K8s `Status().Update` SECOND (best-effort — failure logs warning, continues).

**Pattern to add** (insert after line 286, before line 287):

```go
// Spec v4 §5.2: write the projection row BEFORE the K8s status update.
// Postgres is authoritative; the K8s status subresource is best-effort
// (used only for `kubectl describe` UX).
if r.DB != nil {
    if err := achdb.UpsertEnvironment(ctx, r.DB, achdb.EnvironmentRow{
        Namespace:        env.Namespace,
        Name:             env.Name,
        AuthorizedTeams:  env.Spec.AuthorizedTeams,
        ContextPrompts:   env.Spec.Context.Prompts,
        ContextPlugins:   env.Spec.Context.Plugins,
        ContextArtifacts: env.Spec.Context.Artifacts,
        // ... per D-13 column list
        ResourceVersion: env.ResourceVersion,
    }); err != nil {
        return ctrl.Result{}, fmt.Errorf("db upsert environment: %w", err)
    }
}
```

**Deletion path extension** (insert at lines 117–143): Add `SoftDeleteEnvironment` call AFTER the existing ek_ drain (line 130) but BEFORE `RemoveFinalizer` (line 136). Don't hard-delete — CS-09 says serve until full removal; the row is preserved for in-flight reads.

---

### `internal/controller/ach/plugin_projection_controller.go` (NEW reconciler)

**Critical caveat per D-15:** A `PluginReconciler` ALREADY EXISTS at `internal/controller/ach/plugin_controller.go` — it owns the §10.3 cache-refresh loop (fetch → stage → fsync → rename(2) → UPSERT to `external_refs`). Phase 5's new reconciler is a SECOND reconciler on the same `Plugin` kind for the PROJECTION table write only. Confirm with planner whether to:
1. ADD projection write to the existing `PluginReconciler.Reconcile` (mirror the EnvironmentReconciler D-15 extension), OR
2. SHIP a second reconciler in `plugin_projection_controller.go` (CONTEXT.md D-14 phrasing).

D-15 explicitly says "**For `Plugin`/`Prompt`/`Artifact`: there ARE no existing reconcilers**" — this is **incorrect** per the actual repo state (see `internal/controller/ach/plugin_controller.go`). Planner must reconcile this drift. Recommended: option (1) — extend existing reconciler, do NOT add a second.

**If extending existing PluginReconciler:** Pattern from `internal/controller/ach/plugin_controller.go:Reconcile` lines 79–228. Insert the DB UPSERT to `plugins` table right before `r.Status().Update(ctx, &cr)` at line 208 (similar to the Environment D-15 pattern).

**Insertion point (steady-state path, after `setExternalRefCondition` at line 187, before line 208):**

```go
// Spec v4 §5.2 projection write — Phase 5 D-15.
if r.DB != nil {
    if err := achdb.UpsertPlugin(ctx, r.DB, achdb.PluginRow{
        Namespace:             cr.Namespace,
        Name:                  cr.Name,
        StorageLocation:       finalPath,
        LastSuccessfulRefresh: time.Now().UTC(), // or from result
        MaxStalenessSeconds:   int64(spec.Refresh.MaxStaleness.Duration.Seconds()),
        ResourceVersion:       cr.ResourceVersion,
    }); err != nil {
        return ctrl.Result{}, fmt.Errorf("db upsert plugin projection: %w", err)
    }
}
```

**Deletion path:** Insert `achdb.SoftDeletePlugin(...)` after line 102 (existing `DeleteExternalRef`).

---

### `internal/controller/ach/prompt_projection_controller.go` and `artifact_projection_controller.go`

**Analog:** Same shape as `plugin_projection_controller.go` above. See `internal/controller/ach/prompt_controller.go` and `internal/controller/ach/artifact_controller.go` for the existing reconcilers to extend (same D-15 caveat — these files already exist).

Artifact-specific: include `Scope: cr.Spec.Scope` in `ArtifactRow` upsert (CRD has scope ∈ {object, directory}).

---

### `internal/contentservice/handler.go` (REWRITE)

**Analog:** Current file (lines 32–56 for `RegisterRoutes` shape) + `internal/platformapi/server.go` for the Deps struct + middleware chain idiom.

**RegisterRoutes pattern** (KEEP existing lines 32–56, but extend `Deps`):

```go
type Deps struct {
    CacheRoot  string
    Pool       *pgxpool.Pool                 // NEW — D-16
    Redis      *redis.Client                 // NEW
    Resolver   keystore.Resolver             // NEW — Phase 3 D-08 reused
    Teams      keystore.TeamsResolver        // NEW — Phase 4 D-17 reused
    EnvCache   envcache.Cache                // NEW — D-07
    LiteLLM    litellm.Client                // NEW (for caller="content_service" label)
    Metrics    *metrics.ContentServiceCollectors // NEW — D-09
    AuditLog   *slog.Logger                  // NEW — D-Discretion
    Logger     *slog.Logger
}

func RegisterRoutes(r chi.Router, d Deps) {
    if d.Logger == nil { d.Logger = slog.Default() }
    r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
    })
    r.Get("/metrics", d.Metrics.Handler())       // OBS-03 D-10
    r.Get("/content/prompt/{name}",   d.serve(kindPrompt))
    r.Get("/content/plugin/{name}",   d.serve(kindPlugin))
    r.Get("/content/artifact/{name}", d.serve(kindArtifact))
}
```

**serve() pipeline orchestrator** (per D-16 pseudocode):

```go
func (d Deps) serve(kind string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        out, errResp := pipeline(ctx, d, kind, r)
        if errResp != nil {
            d.writeError(w, r, errResp)
            return
        }
        stream(w, r, out.File, out.ContentType, out.Size)
    }
}
```

**Disable compression middleware on this mux** (D-01): the chi router passed to `RegisterRoutes` MUST NOT have `chi/middleware.Compress` registered. Verify at the wiring site in `cmd/ach/cmd/content_service.go`.

---

### `internal/contentservice/pipeline.go` (NEW)

**Analog:** `internal/keystore/keystore.go:Resolve` lines 127–166 (chained-gate idiom: cache check → singleflight → DB → cache populate). The 6-step gate sequence in D-04 maps to a single `pipeline()` function returning `(*resolvedRow, *errResp)`.

**Function shape:**

```go
type resolvedRow struct {
    File        *os.File  // opened in step 8 per D-02 (early open)
    Size        int64
    ContentType string
    AuditKind   string  // "prompt"|"plugin"|"artifact"
    AuditName   string
}

type errResp struct {
    HTTPStatus int
    Code       string  // from D-03 table
    Message    string
}

func pipeline(ctx context.Context, d Deps, kind string, r *http.Request) (*resolvedRow, *errResp) {
    // Gate 1: Authn — keystore.Resolver.Resolve("x-ach-key")
    // Gate 2: Env header validation (pk_ requires, ek_ optional/match)
    // Gate 3: Env row lookup via envcache
    // Gate 4 (pk_ only): Team intersect via keystore.TeamsResolver
    // Gate 5: Context allowlist check (CHEAPER FIRST per D-04 divergence)
    // Gate 6: Content resolution + §12.3 precedence (db.ResolvePluginByName, etc.)
    // Gate 7: Staleness gate (now - last_successful_refresh > max_staleness)
    // Gate 8 (D-02): Open *os.File EARLY, return for stream() to drain.
}
```

**Spec divergence callout** (per D-04): step 5 BEFORE step 6 (cheaper-first; spec §5.1 says opposite). Document this in the function comment so auditors/reviewers see it.

---

### `internal/contentservice/authz.go` (NEW)

**Analog:** `internal/controller/ach/environment_controller.go:reconcileAccessGroup` lines 319–396 (multi-step branch pattern with closed-set outcomes).

Per-gate functions:
- `resolveAuthn(ctx, d, r) (*keystore.KeyInfo, *errResp)` — wraps `d.Resolver.Resolve`. Empty/nil → `401 expired_or_revoked`. Wrong prefix → `400 invalid_key_format`.
- `resolveEnv(ctx, d, info, header) (*envcache.EnvRow, *errResp)` — pk_ requires header, ek_ matches bound env.
- `enforceTeams(ctx, d, info, envRow) *errResp` — pk_ only. LiteLLM error → `503 litellm_unreachable`.
- `enforceAllowlist(envRow, kind, name) *errResp` — pure function, no I/O.
- `resolveContent(ctx, d, kind, name) (*contentRow, *errResp)` — calls `db.ResolvePluginByName` for plugins, direct projection-row lookup for prompts/artifacts.
- `checkStaleness(row) *errResp` — pure function (read columns from resolved row).

**Pattern (closed-set outcome per step)** — copy the `metav1.Condition` return idiom from `reconcileAccessGroup` lines 326–336 (each branch returns a typed result, never falls through):

```go
func resolveAuthn(ctx context.Context, d Deps, r *http.Request) (*keystore.KeyInfo, *errResp) {
    plaintext := r.Header.Get("x-ach-key")
    // ...prefix check...
    if !strings.HasPrefix(plaintext, "pk_") && !strings.HasPrefix(plaintext, "ek_") {
        return nil, &errResp{400, "invalid_key_format", "malformed key prefix"}
    }
    info, err := d.Resolver.Resolve(ctx, plaintext)
    if err != nil { return nil, &errResp{500, "internal_error", "internal error"} }
    if info == nil { return nil, &errResp{401, "expired_or_revoked", "key expired or revoked"} }
    return info, nil
}
```

---

### `internal/contentservice/envcache/cache.go` (NEW)

**Analog:** `internal/keystore/teamsresolver.go:redisCachedTeamsResolver` (lines 103–196) — exact same Redis read-through + singleflight + 60s TTL idiom.

**Mirror these elements verbatim:**
- `const teamsCacheKeyPrefix = "ach:teams:"` → `const envCacheKeyPrefix = "ach:env:"` (CONTEXT D-07).
- Singleflight on miss: `r.sf.Do(key, func() (any, error) { return r.loader(ctx, ns, name) })`.
- 60s TTL ceiling (`defaultTTL` constant — see `internal/keystore/keystore.go:22`).
- Best-effort cache write (`_ = r.rdb.Set(...).Err()`).
- Malformed entry → fall through, do NOT DEL.
- Compile-time interface assertion: `var _ Cache = (*redisCachedEnvCache)(nil)`.

**Key shape** (CONTEXT D-07): `ach:env:<namespace>/<name>`. Value: JSON-serialized `EnvRow` (subset fields CS needs).

**Constructor** (mirror `NewCachedTeamsResolver` lines 130–142): refuse nil base/redis.

---

### `internal/contentservice/stream.go` (NEW — D-01 custom serve)

**Analog:** Current `internal/contentservice/handler.go` lines 119–125 (response-header set block) but **replacing** `http.ServeContent` with raw `io.Copy`.

**Per CONTEXT Specifics block — concrete code to copy:**

```go
func stream(w http.ResponseWriter, _ *http.Request, f *os.File, contentType string, size int64) error {
    w.Header().Set("Content-Type", contentType)
    w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
    w.Header().Set("Cache-Control", "no-store")  // CHANGED from "public, max-age=300"
    w.WriteHeader(http.StatusOK)
    _, err := io.Copy(w, f)  // sendfile(2) via *os.File.WriteTo → *net.TCPConn
    return err
}
```

**Critical:** Do NOT call `http.ServeContent`. Range/INM/IMS headers are explicitly ignored (CS-08). E2E gate via `strace -e trace=sendfile` (planner pins the exact command per CONTEXT Specifics).

---

### `internal/contentservice/errors.go` (NEW)

**Analog:** `internal/platformapi/render/json.go:Error` lines 52–62 (envelope writer) + `internal/platformapi/middleware/middleware.go:RecoverPanic` lines 61–89 (audit emission alongside error write).

**writeError pattern:**

```go
func (d Deps) writeError(w http.ResponseWriter, r *http.Request, e *errResp) {
    reqID := middleware.RequestIDFromCtx(r.Context())
    // Reuse Phase 3 envelope writer verbatim — same `application/json; charset=utf-8`
    // Content-Type and same {error:{code,message},request_id} shape.
    render.Error(w, e.HTTPStatus, e.Code, e.Message, reqID)
    // Emit one audit event per D-Discretion + Phase 3 OBS-01.
    if d.AuditLog != nil {
        audit.EmitAudit(r.Context(), d.AuditLog, audit.Event{
            Action:    "content.get",
            Outcome:   e.Code,  // matches response body code (D-03)
            RequestID: reqID,
            Target:    &audit.Target{Kind: kindFromCtx(r), Name: chi.URLParam(r, "name")},
        })
    }
    // Inc metrics (cardinality-disciplined per D-09 — no request_id label).
    d.Metrics.IncRequest(kindFromCtx(r), e.Code)
}
```

**D-03 mapping table:** Embed as a `var errMap = map[string]errResp{...}` or inline branches in each gate; planner picks. The MESSAGE strings are hard-coded (T-03-02-02 — never echo raw upstream errors).

---

### `internal/contentservice/paths.go` (RETAINED with `scope`-dispatch refactor)

**Analog:** Self (current file lines 29–48). Current artifact branch returns 2 candidates (`.tar.gz` first, then bare). Per D-17, **refactor:** Once `resolveContent` returns the resolved row with explicit `Scope` field, `ResolvePath` becomes deterministic (1 candidate based on scope).

**New signature suggestion** (planner refines):

```go
func ResolvePath(cacheRoot, kind, name, scope string) (string, error) {
    // scope used only for kind=artifact; ignored otherwise.
}
```

Or keep current signature, drop the 2-candidate walk from the handler (handler now knows `scope` from the resolved row).

---

### `internal/contentservice/k8s.go` (REMOVED)

Per D-17. The `PromptContentTypeLookup` function and `NewK8sPromptLookup` go away — `content_type` now comes from the `prompts.content_type` projection column.

---

### `internal/contentservice/pipeline_test.go` (NEW — table-driven unit)

**Analog:** `internal/db/external_refs_test.go` for fixtures + `internal/keystore/teamsresolver_test.go` for the mock-Resolver pattern. Per D-20: ~50 cases covering each error code in D-03 table.

**Test scaffold pattern** (from `internal/keystore/keystore_test.go`):

```go
func TestPipeline_Gates(t *testing.T) {
    tests := []struct {
        name        string
        keyResolver keystore.Resolver  // mock
        teamsRes    keystore.TeamsResolver  // mock
        envCache    envcache.Cache  // mock
        pool        *pgxpool.Pool   // mock or testcontainer
        request     *http.Request
        wantCode    string  // from D-03
        wantStatus  int
    }{
        {"pk_ missing env header", ..., "missing_environment", 400},
        {"pk_ empty team intersect", ..., "unauthorized_team", 403},
        // ... ~50 cases
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) { /* ... */ })
    }
}
```

---

### `internal/contentservice/stream_test.go` (NEW)

**Analog:** Current `internal/contentservice/handler_test.go` lines 73–96 (`TestHandler_PromptBody` — header-assertion pattern).

**Adapt:** Replace `Cache-Control: public, max-age=300` assertion with `no-store`. Add explicit assertions that Range/INM/IMS headers in the request do NOT affect status (always 200, never 206). Verify identity transfer (no `Transfer-Encoding: chunked` header).

---

### `internal/contentservice/handler_test.go` (REWRITE)

**Analog:** `internal/db/external_refs_test.go` (testcontainers Postgres setup) + `internal/keystore/teamsresolver_test.go` (mock LiteLLM via httptest.Server). Per D-20 integration plan: testcontainers Postgres + Redis + httptest LiteLLM, plus existing `examples/` fixture YAMLs reused for path inputs.

---

### `internal/metrics/registry.go` (NEW — greenfield)

**No in-repo analog.** Wraps `github.com/prometheus/client_golang/prometheus` + `promhttp.HandlerFor`. Per D-09:

```go
// SPDX-License-Identifier: Apache-2.0
package metrics

import (
    "net/http"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRegistry() *prometheus.Registry {
    return prometheus.NewRegistry()  // process-local; NOT default
}

func Handler(reg *prometheus.Registry) http.Handler {
    return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
```

**Prometheus dependency** — already pulled transitively (see `go.mod:139` — `github.com/prometheus/client_golang v1.23.2 // indirect`). Phase 5 promotes it to direct (`go mod tidy` after first import).

---

### `internal/metrics/forwarder.go` (NEW)

**Analog:** `internal/forwarder/metrics/counters.go` (signatures — keep verbatim) + (no in-repo Prometheus registration analog).

**Struct factory shape** (per D-09 — typed methods, no raw label strings at call sites):

```go
type ForwarderCollectors struct {
    requests     *prometheus.CounterVec   // forwarder_requests_total
    duration     *prometheus.HistogramVec // forwarder_request_duration_seconds
    jwtSigned    *prometheus.CounterVec   // forwarder_jwt_signed_total
    jwtSuppressed *prometheus.CounterVec  // forwarder_jwt_suppressed_total
}

func NewForwarderCollectors(reg *prometheus.Registry) *ForwarderCollectors {
    c := &ForwarderCollectors{
        requests: prometheus.NewCounterVec(prometheus.CounterOpts{
            Name: "forwarder_requests_total",
        }, []string{"route", "key_type", "outcome"}),
        // ... etc
    }
    reg.MustRegister(c.requests, c.duration, c.jwtSigned, c.jwtSuppressed)
    return c
}

func (c *ForwarderCollectors) IncRequest(route, keyType, outcome string) {
    c.requests.WithLabelValues(route, keyType, outcome).Inc()
}
```

**Label-value enums** — pinned per §18.5 (CONTEXT canonical-ref line 249). Reference inline in doc comments per the existing `internal/forwarder/metrics/counters.go` IncRequests doc (lines 26–35).

---

### `internal/metrics/contentservice.go` (NEW)

**Analog:** `internal/metrics/forwarder.go` (this file above) — same factory shape. Metrics per OBS-06 + CONTEXT decisions:
- `content_service_requests_total{kind, outcome}`
- `content_service_request_duration_seconds{kind}` — buckets from `buckets.go`
- `content_service_bytes_served_total{kind}`

Cardinality discipline: NO `request_id` or `owner_email` labels (CONTEXT line 32).

---

### `internal/metrics/shared.go` (NEW)

**No in-repo analog — greenfield.** Per D-09:

```go
func MustRegisterLitellmUnreachable(reg *prometheus.Registry) *prometheus.CounterVec {
    c := prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "litellm_unreachable_total",
    }, []string{"caller"})  // caller ∈ {forwarder, content_service, platform_api, operator}
    reg.MustRegister(c)
    return c
}
```

Single counter, shared across all 4 services. Each service Inc's with its own `caller` label.

---

### `internal/metrics/buckets.go` (NEW)

**Analog:** `internal/keystore/keystore.go:22` (const block declaration idiom).

```go
package metrics

import "github.com/prometheus/client_golang/prometheus"

// ContentServiceDurationBuckets extends DefBuckets to artifact-tarball tail.
var ContentServiceDurationBuckets = []float64{
    0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// ForwarderDurationBuckets = prometheus.DefBuckets per D-11.
var ForwarderDurationBuckets = prometheus.DefBuckets
```

---

### `internal/forwarder/metrics/counters.go` (REWRITE bodies — keep signatures)

**Analog:** Self (current file lines 1–67). Per D-19: thin shim — keep `IncRequests`/`IncJWTSigned`/`IncJWTSuppressed`/`IncLiteLLMUnreachable` signatures verbatim so existing call sites in `internal/forwarder/proxy/handlers.go`, `internal/forwarder/bip/index.go` need no edits.

**New body pattern:**

```go
var collectors *metrics.ForwarderCollectors  // wired by cmd/ach/cmd/forwarder.go

func InitCollectors(c *metrics.ForwarderCollectors) {  // called once at startup
    collectors = c
}

func IncRequests(route, keyType, outcome string) {
    if collectors != nil {
        collectors.IncRequest(route, keyType, outcome)
    }
}
```

Nil-tolerant — Phase 4 unit tests that don't wire collectors still work.

---

### `internal/metrics/metrics_test.go` (NEW)

**Analog:** `internal/db/db_test.go` for the basic test scaffold; `internal/keystore/keystore_test.go` for table-driven cases.

Key cases per D-20:
- `TestNewRegistry_IsolatedFromDefault` — collectors registered on the returned Registry do NOT appear in the global default registry.
- `TestLitellmUnreachable_AllCallers` — `MustRegister...` called once, each of 4 caller labels Inc's successfully without re-register panic.
- `TestForwarderCollectors_Cardinality` — assert label keys match §18.5 normative set.

---

### `cmd/ach/cmd/content_service.go` (REWIRE)

**Analog:** `cmd/ach/cmd/platform_api.go:buildPlatformAPIDeps` lines 164–265 (Postgres pool + Redis + LiteLLM REST + keystore.NewCachedResolver chain) + self (graceful shutdown structure at lines 107–151).

**New env-var validation surface** (mirror lines 73–142 of platform_api.go):

```go
type contentServiceConfig struct {
    CacheRoot        string
    DBURL            string
    Pepper           []byte
    LiteLLMBaseURL   string
    LiteLLMMasterKey string
    RedisAddr        string
    RedisPassword    string
    RedisTLS         bool
    RedisDB          int
    BindAddr         string  // default ":8082"
    Namespace        string
}
```

**Deps build pattern** (mirror `buildPlatformAPIDeps` lines 167–265): open pgx pool → open Redis → build LiteLLM REST client → build `keystore.NewDBResolver` + `keystore.NewCachedResolver` → build `keystore.NewLiteLLMTeamsResolver` + `keystore.NewCachedTeamsResolver` → build `envcache.New(redis)` → build `metrics.NewRegistry()` + `NewContentServiceCollectors`.

**Server config delta** (D-Discretion + D-01):

```go
srv := &http.Server{
    Addr:              addr,
    Handler:           r,
    ReadHeaderTimeout: 5 * time.Second,
    WriteTimeout:      0,  // D-01: artifact tarballs may exceed any deadline; rely on ctx cancellation
}
```

**Remove informer/manager.Manager** (per D-Discretion: "No new Environment informer for CS"). The Phase 1 stub at lines 75–98 creates a manager just for the Prompt cache; **delete entirely** in Phase 5. CS reads ONLY from Postgres + envcache.

---

### `deploy/helm/ach/templates/operator-deployment.yaml` (annotate)

**Analog:** Self (current file). Per D-12: add scrape annotations to the Pod template metadata (insert at the existing `metadata.annotations:` block around line 44, alongside `kubectl.kubernetes.io/default-container: manager`).

**Operator metrics port nuance:** Operator uses controller-runtime metricsserver on `:8443` by default. Scrape annotation must reflect that — keep current `metrics` port (`8080`) as the ACH-namespaced metrics port via the operator-side `metrics.Registry().MustRegister(...)` per D-10.

**Pattern to add** (insert under existing `metadata.annotations:` block):

```yaml
template:
  metadata:
    annotations:
      kubectl.kubernetes.io/default-container: manager
      prometheus.io/scrape: "true"
      prometheus.io/port: "8080"  # operator-side metrics
      prometheus.io/path: "/metrics"
```

For content-service container (around line 117) the port differs — `:8082` — but the same Pod hosts both, so a single annotation at Pod level can't cleanly cover both. Planner picks: annotate the Pod for one port and rely on Prometheus `relabel_configs`, OR ship separate `Service` objects with `metricsPort` and annotate the Service.

---

### `deploy/helm/ach/templates/forwarder-deployment.yaml` and `platform-api-deployment.yaml` (annotate)

**Analog:** Same shape as operator-deployment.yaml above. Forwarder port = `:8080` traffic listener; Platform API port = `:8080` traffic listener (per `cmd/ach/cmd/platform_api.go:140` default `ACH_PLATFORM_API_BIND_ADDRESS=:8080`).

---

### `examples/prometheus-servicemonitor.yaml` (NEW)

**No in-repo analog.** Per D-12 — example `ServiceMonitor` (Prometheus Operator CRD) for users who don't use the pod-scrape-annotation path. Documented as opt-in; NOT installed by the Helm chart by default.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ach
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: ach
  endpoints:
    - port: metrics       # operator
      path: /metrics
    - port: cs-http
      path: /metrics
    - port: traffic       # forwarder + platform-api
      path: /metrics
```

---

## Shared Patterns

### SPDX License Header
**Source:** `hack/boilerplate.go.txt` + every `*.go` in `internal/`.
**Apply to:** Every new `*.go` file in Phase 5. First line, no blank line above.

```go
// SPDX-License-Identifier: Apache-2.0
```

Pre-push gate 15 enforces (`scripts/pre-push-check.sh`).

### pgx Error Classification
**Source:** `internal/db/external_refs.go` lines 82–89, `internal/db/check_extend.go`, `internal/db/errors.go:isTransientPgErr`.
**Apply to:** All new `internal/db/*.go` files + the reconciler DB-write paths.

```go
if _, err := pool.Exec(ctx, sql, ...); err != nil {
    if isTransientPgErr(err) {
        return err  // raw — controller-runtime exponential backoff
    }
    return fmt.Errorf("db: UpsertEnvironment(%s/%s): %w", ns, name, err)
}
```

Never echo `pgErr.Message` (may contain bound parameter values — T-02-03-03).

### chi Router + Per-Kind Routes (NOT URL params)
**Source:** Current `internal/contentservice/handler.go` lines 53–55. **KEEP this idiom** per CONTEXT D-Discretion.

```go
r.Get("/content/prompt/{name}",   d.serve(kindPrompt))
r.Get("/content/plugin/{name}",   d.serve(kindPlugin))
r.Get("/content/artifact/{name}", d.serve(kindArtifact))
```

Unknown `{kind}` → chi default 404 at router layer. Non-GET (including HEAD) → chi default 405.

### Constructor-Time Nil Guards
**Source:** `internal/keystore/keystore.go:NewCachedResolver` lines 94–110.

```go
func NewCachedEnvCache(rdb *redis.Client, pool *pgxpool.Pool) (Cache, error) {
    if rdb == nil { return nil, errors.New("envcache: nil redis client") }
    if pool == nil { return nil, errors.New("envcache: nil pgx pool") }
    return &redisCachedEnvCache{rdb: rdb, pool: pool, ttl: defaultTTL}, nil
}
```

### Redis Read-Through + Singleflight Cache
**Source:** `internal/keystore/teamsresolver.go:redisCachedTeamsResolver.Resolve` lines 161–196.
**Apply to:** `internal/contentservice/envcache/cache.go`.

5-step pattern (cache GET → singleflight DB → cache SET best-effort → propagate result):

```go
if raw, err := r.rdb.Get(ctx, key).Bytes(); err == nil {
    var v EnvRow
    if jsonErr := json.Unmarshal(raw, &v); jsonErr == nil { return &v, nil }
}
result, sfErr, _ := r.sf.Do(key, func() (any, error) { return r.loadFromDB(ctx, ns, name) })
if sfErr != nil { return nil, sfErr }
// ...best-effort Set with 60s TTL...
```

### Error Envelope (§15.5)
**Source:** `internal/platformapi/render/json.go:Error` lines 52–62.
**Apply to:** Every ACH-originated 4xx/5xx from Content Service.

```go
render.Error(w, status, code, message, requestID)
// Wire format:
// {"error":{"code":"<code>","message":"<msg>"},"request_id":"req_<ulid>"}
```

The `code` MUST match the audit `outcome` (D-03 table — body code == audit outcome).

### Request ID Middleware
**Source:** `internal/platformapi/middleware/middleware.go:RequestID` lines 45–52.
**Apply to:** The Content Service chi router via shared middleware. Reuse the existing `internal/platformapi/middleware.RequestID` directly (it's chi-independent).

Server-generated only — caller-supplied `X-Request-Id` IGNORED (T-03-05-06).

### Audit Event Emission
**Source:** `internal/platformapi/middleware/middleware.go:RecoverPanic` lines 76–84 (`audit.EmitAudit` call shape) + `internal/audit/emit.go:EmitAudit` lines 96–98.
**Apply to:** Every Content Service GET — one event per request, on every outcome (success and failure).

```go
audit.EmitAudit(ctx, d.AuditLog, audit.Event{
    Action:    "content.get",
    Outcome:   outcomeCode,   // matches response body code per D-03
    Actor:     keyInfo.OwnerEmail,
    RequestID: reqID,
    KeyID:     keyInfo.KeyID,
    Target:    &audit.Target{Kind: kind, Name: name},
})
```

### Status Dual-Write Order (D-Discretion)
**Source:** `internal/controller/ach/environment_controller.go` lines 286–289 (current K8s `Status().Update` block).
**Apply to:** Every projection reconciler.

**Order:** DB UPSERT (load-bearing) → K8s `Status().Update` (best-effort; log warning on failure, continue):

```go
if err := achdb.UpsertX(ctx, r.DB, row); err != nil {
    return ctrl.Result{}, err  // DB failure IS fatal — DB is authoritative
}
if err := r.Status().Update(ctx, &cr); err != nil {
    logger.Error(err, "k8s status update failed (best-effort)")
    // Do NOT return err — DB write succeeded.
}
return ctrl.Result{RequeueAfter: ...}, nil
```

### Helm Pod Scrape Annotation (D-12)
**Source:** `deploy/helm/ach/templates/operator-deployment.yaml` line 44–47 (`annotations:` block under `template.metadata`).

```yaml
template:
  metadata:
    annotations:
      prometheus.io/scrape: "true"
      prometheus.io/port: "<service traffic port>"
      prometheus.io/path: "/metrics"
```

### Test Scaffolds — testcontainers + httptest
**Source:** `internal/db/external_refs_test.go` (testcontainers Postgres setup) + `internal/keystore/teamsresolver_test.go` (mock LiteLLM via `httptest.Server`).
**Apply to:** Phase 5 integration tests in `internal/contentservice/handler_test.go` rewrite.

Build tag: `//go:build integration` (see `external_refs_test.go` line 1) — run via `make test-integration` or `go test -tags=integration`.

---

## No Analog Found

| File | Role | Reason |
|---|---|---|
| `internal/metrics/registry.go` | metrics | First Prometheus registration in the repo (currently only indirect dep via controller-runtime). |
| `internal/metrics/shared.go` | metrics | Cross-service shared counter — first of its kind. |
| `examples/prometheus-servicemonitor.yaml` | example | No prior ServiceMonitor in the repo. |
| sendfile E2E gate (in `test/e2e/`) | test | `strace -e trace=sendfile` assertion is novel; planner pins exact form. |

For these, planner consults `RESEARCH.md` for the Prometheus + ServiceMonitor library patterns (Prometheus Operator docs, `prometheus/client_golang` examples).

---

## Critical Drift Flags for Planner

1. **D-15 phrasing vs. repo reality:** CONTEXT D-15 says "For `Plugin`/`Prompt`/`Artifact`: there ARE no existing reconcilers." This is **incorrect** — `internal/controller/ach/plugin_controller.go`, `prompt_controller.go`, `artifact_controller.go` ALL exist (shipped in Phase 2 for the §10.3 cache-refresh loop). Planner must decide whether to (a) extend the existing reconciler with a projection UPSERT, or (b) ship a second reconciler in `*_projection_controller.go` and risk reconcile-race. **Recommended: option (a) — extend existing reconcilers** (mirrors the Environment D-15 pattern, single-controller-per-kind invariant).

2. **D-04 cheaper-first divergence:** Document in `pipeline.go` doc comment AND in audit dashboard (CONTEXT explicitly flags this for VERIFICATION).

3. **`Cache-Control` change:** Current handler sets `public, max-age=300`; new handler sets `no-store`. Existing test `TestHandler_PromptBody` at `handler_test.go:86` asserts the old value — rewrite during D-20 test pass.

4. **Operator Pod hosts BOTH operator + content-service containers** (`operator-deployment.yaml` lines 104–155 — co-located via RWO PVC). The single Pod has TWO metrics ports (`:8080` operator-side, `:8082` content-service-side). Pod-level scrape annotations can name only ONE port — planner picks: split annotations per container (Prometheus supports `prometheus.io/port` on multiple containers via relabeling), OR ship two `Service` objects, OR fall back to ServiceMonitor.

5. **`marketplace_plugins` schema:** Current `MarketplacePlugin` struct (`internal/db/marketplace_plugins.go:37–45`) uses `(MarketplaceName, Name)` PK. The §12.3 CTE in `ResolvePluginByName` must reference `name` (NOT `plugin_name` as the CONTEXT Specifics block sketches). Verify in planning.

---

## Metadata

**Analog search scope:**
- `internal/contentservice/` (current §8 stub)
- `internal/db/` (12 files — projection-row patterns)
- `internal/controller/ach/` (35 files — reconciler patterns)
- `internal/keystore/` (Phase 3 + Phase 4 cache patterns)
- `internal/forwarder/metrics/` (counter-hook stubs)
- `internal/platformapi/render/` + `internal/platformapi/middleware/` (envelope + middleware)
- `cmd/ach/cmd/` (cobra subcommand bootstraps)
- `db/migrations/` (existing migrations 000001–000003)
- `deploy/helm/ach/templates/` (Helm chart shape)

**Files scanned:** ~55 source files (Go + SQL + YAML).
**Pattern extraction date:** 2026-05-27.

# Phase 5: Content Service + Cross-component Observability - Context

**Gathered:** 2026-05-27
**Status:** Ready for planning
**Mode:** `/gsd:discuss-phase 5` (interactive — 4 areas, 6 single-question turns including spec-v4 scope decision)

<domain>
## Phase Boundary

Phase 5 turns the §8 stub Content Service (`internal/contentservice/handler.go` from commit 3266513 — raw file streamer with no auth, `Cache-Control: public, max-age=300`, `http.ServeContent` honoring `Range`/`If-Modified-Since`/`If-None-Match`) into the full §15.6 surface AND adds the §18.5 normative Prometheus metric set to all four Hub components (Operator, Platform API, Forwarder, Content Service). Concretely Phase 5 delivers:

- **CS-01..CS-11 (Content Service runtime contract):**
  - `GET /content/{kind}/{name}` with `{kind} ∈ {prompt, plugin, artifact}` — explicit per-kind chi routes; other `{kind}` values → chi 404; non-GET (and non-HEAD if we register it) → chi 405. (CS-01)
  - Authentication via `keystore.KeyResolver` (Phase 3 D-08 reused verbatim): `pk_` triggers §7.1 atomic check-and-extend (Phase 3 D-10), `ek_` resolves Redis→Postgres-on-miss with `status='active'` enforcement and the §8.1 `last_used_at` debounce UPDATE. Empty/expired → `401 expired_or_revoked`; malformed prefix → `401 invalid_key_format`. (CS-02)
  - Environment context resolution:
    - `pk_` REQUIRES `x-ach-environment` header (omitted → `400 missing_environment`).
    - Environment lookup against Postgres projection (see "Spec v4 §5.2 reversal" below) — not found → `404 environment_not_found`. Terminating-but-still-projected Environment continues to serve per §6.5 / CS-09.
    - `pk_` Team membership intersected with `Environment.spec.authorizedTeams[]` via `keystore.TeamsResolver` (Phase 4 D-17 reused verbatim; 60s Redis TTL, single-flight, LiteLLM `UserInfoByEmail` on miss). Empty intersection → `403 unauthorized_team`. LiteLLM unreachable during Teams resolution → `503 litellm_unreachable` (CS-11, OBS-05 — same body code on every surface, no asymmetry).
    - `ek_` MAY include `x-ach-environment`; mismatch with bound Environment → `403 wrong_environment`. (CS-03)
  - **Two-step content authorization — cheaper-first reordered (see D-04 + spec-divergence note):** check `<name>` ∈ `Environment.spec.context.<kindPlural>[]` FIRST, then resolve to a backing `Plugin`/`Prompt`/`Artifact` row (and, for plugin only, fall back to `marketplace_plugins` per §12.3). Not-in-context → `403 unauthorized_content`; not-resolved → `404 content_not_found`. (CS-04 reordered)
  - §12.3 plugin precedence on EVERY request from Postgres (no resolution cache): `plugins` row with `name=X` wins; otherwise the `marketplace_plugins` row whose `marketplace_name` sorts alphabetically lowest wins (Unicode code-point, case-sensitive). (CS-05, SC#3)
  - Per-kind `Content-Type` policy: prompt = `Prompt.spec.contentType` else `application/octet-stream` (upstream-derived hint dropped — we don't sniff at request time); plugin = `application/gzip`; artifact `scope: object` = `application/octet-stream`; artifact `scope: directory` = `application/gzip`. `Artifact.spec.scope` selects path between `artifact/<name>` (object) and `artifact/<name>.tar.gz` (directory). (CS-06, CS-07)
  - Response headers: `Content-Length` exact (from `f.Stat().Size()`); `Cache-Control: no-store`; identity transfer (no `Transfer-Encoding: chunked`, no Content-Encoding compression at CS tier — disable any compression middleware on this mux). (CS-06, SC#1)
  - Conditional/partial requests IGNORED in v1alpha1 — `Range`/`If-None-Match`/`If-Modified-Since`/`If-Match`/`If-Unmodified-Since` not inspected; always `200` full body, never `206`. Achieved by replacing `http.ServeContent` with custom serve (D-01). (CS-08, SC#1)
  - §10 staleness gate on every request: read `external_refs.last_successful_refresh` + `max_staleness_seconds` (or `marketplace_plugins` equivalents) from Postgres at request time. `now - last_successful_refresh > max_staleness` → `503 stale_cache_expired`. NULL `last_successful_refresh` (PVC-loss recovery per OP-11) → `503 stale_cache_expired`. (CS-10, SC#4)
  - In-flight read survives Operator atomic `rename(2)` revision swap: open file early in pipeline, hold `*os.File` until stream completion; old inode survives via the open FD even after the directory entry is replaced. (SC#4)
  - Error envelope per Phase 4 D-21: `{"error":{"code","message"},"request_id":"req_..."}` on every ACH-originated 4xx/5xx. (CS-11)

- **OBS-03..OBS-06 (cross-component metrics):**
  - Each component exposes `/metrics` on its main traffic listener via the shared `internal/metrics/` package (D-09).
  - Forwarder: replace Phase 4 D-18 counter-hook stubs (`internal/forwarder/metrics/`) with real `prometheus.CounterVec`/`HistogramVec` registrations. `forwarder_requests_total{route, key_type, outcome}`, `forwarder_request_duration_seconds{route, key_type, status_class}`, `forwarder_jwt_signed_total{kind}`, `forwarder_jwt_suppressed_total{kind, reason}`. (OBS-04)
  - Content Service: `content_service_requests_total{kind, outcome}`, `content_service_request_duration_seconds{kind}`, `content_service_bytes_served_total{kind}`. Cardinality discipline — no `request_id`/`owner_email`. (OBS-06)
  - Shared cross-component: `litellm_unreachable_total{caller}` ONE collector registered in `internal/metrics/`, each service calls `.WithLabelValues("forwarder"|"content_service"|"platform_api"|"operator").Inc()`. (OBS-05)
  - Platform API (audit-side counters from Phase 3 D-18 stub pattern → real) — minimal Phase 5 work since Platform API's metric set isn't expanded by §18.5, but the `/metrics` endpoint MUST be exposed.
  - Operator metrics — controller-runtime auto-emits standard controller metrics; Phase 5 adds the §18.5 ACH-namespaced counters via the shared package.

- **Spec v4 §5.2 reversal — DB projection layer (folded into Phase 5):**
  - Per `spec/ach_hub_spec_v20260515_FINALv4.md` line 13: "Platform API, Forwarder, and Content Service no longer hold informers over ACH CRDs; they read CRD spec/status from Postgres. Only the ACH Operator watches Kubernetes."
  - Phase 5 ADDS Postgres projection tables for the ACH CRDs that Content Service reads: `environments`, `plugins`, `prompts`, `artifacts`. Schema details deferred to planning (D-08 + D-13).
  - Operator becomes the K8s↔DB projector: a new reconciler per CRD writes spec subset + status to the projection row in the same transaction as the K8s state change. Dual-write status: Postgres authoritative, K8s `status` subresource best-effort (for `kubectl describe` UX of GitOps users).
  - Content Service reads ONLY Postgres for content authorization (no informer, no in-memory CRD cache outside the explicit Redis env-row cache). RBAC delta: Content Service ServiceAccount drops `get/list/watch` on ACH CRDs.
  - **Phase 4 carry-forward:** Forwarder + Platform API still use informers for BIP/Environment lookups (already shipped). Migration of those reads to Postgres is flagged in `<deferred>` as a Phase 5b candidate. Phase 5 itself does NOT touch Forwarder or Platform API read paths.

Phase 5 explicitly **excludes**:

- CLI work (Phase 6+).
- `Range` / `If-None-Match` / `If-Modified-Since` honor — explicit v1alpha1 ignore (CS-08); §20 backlog.
- HTTP `206 Partial Content` — §20 backlog.
- Cross-marketplace `Synced=False, reason=NameConflict` status writes — already shipped in earlier phase (per spec v6 line 114); Phase 5 only consumes the precedence rule.
- Compression at Content Service tier (`Content-Encoding: gzip` for plugin/artifact tarballs is the upstream-baked encoding, not a CS-layer transform). Disable any chi compression middleware on the CS mux.
- HEAD support contractually — chi `r.Get` registers GET only; HEAD will return chi's default 405. (CS-01 says HEAD MAY be supported but not contractual; leaving 405 is the lowest-surprise default).
- Forwarder + Platform API informer→Postgres migration. Deferred to Phase 5b.
- ServiceMonitor CRD in Helm chart — pod scrape annotations (Prometheus default) ship instead; example `ServiceMonitor` lives under `examples/` for deployers using the Prometheus Operator (D-12).
- `last_used_at` debounce shape for Content Service `ek_` requests — reuses the existing Phase 3 implementation verbatim; Phase 5 just calls it.
- Audit events on Content Service GET — already shipped via Phase 3 OBS-01/OBS-02 hooks (`outcome ∈ §18.2 enum`). Phase 5 confirms wiring but does not redesign the audit envelope.

</domain>

<decisions>
## Implementation Decisions

### Streaming + header policy

- **D-01:** **Custom serve, no `http.ServeContent`.** New handler body:
  ```go
  f, err := os.Open(path); defer f.Close()
  fi, err := f.Stat()
  w.Header().Set("Content-Type", contentType)
  w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
  w.Header().Set("Cache-Control", "no-store")
  w.WriteHeader(http.StatusOK)
  io.Copy(w, f) // Linux sendfile(2) engages via *os.File.WriteTo → *net.TCPConn
  ```
  - Range/INM/IMS/IM/IUMS request headers NEVER inspected — full body always.
  - No compression middleware on the CS mux (`chi/middleware.Compress` MUST NOT be registered on this router).
  - `http.Server.WriteTimeout` for content-service traffic listener: pre-decision `0` (no deadline) — large artifact tarballs may exceed 30s. Rely on `Request.Context()` cancellation for client-disconnect propagation. Planner to validate against expected artifact size distribution.
  - E2E test gate: `strace -e trace=sendfile,sendfile64 -p $(pgrep ach)` during a content GET MUST show at least one sendfile syscall covering the file size. Wire into the Phase 5 E2E suite.

- **D-02:** **Open file early in pipeline (before staleness gate).** Open the cache file as the FIRST side-effecting step after path resolution, hold the `*os.File` until response stream completion. This is what makes SC#4 "in-flight read against an old inode survives Operator atomic rename" actually true — the open FD pins the inode even after the directory entry is replaced.

- **D-03:** **Explicit error mapping** (CS-11 envelope, response body == audit outcome):

  | HTTP | code (body + audit) | trigger |
  |------|----|----|
  | 400  | `missing_environment` | pk_ without `x-ach-environment` |
  | 400  | `invalid_key_format` | malformed key prefix |
  | 401  | `expired_or_revoked` | pk_ §7.1 zero rows OR ek_ status != active |
  | 403  | `unauthorized_team` | pk_ empty Team intersection |
  | 403  | `wrong_environment` | ek_ header ≠ bound env |
  | 403  | `unauthorized_content` | name not in spec.context (cheaper-first per D-04) |
  | 404  | `environment_not_found` | Environment row missing |
  | 404  | `content_not_found` | name in allowlist but no resolved row |
  | 503  | `litellm_unreachable` | LiteLLM error during Teams resolve |
  | 503  | `stale_cache_expired` | now-lsr > max_staleness OR lsr NULL |
  | 500  | `internal_error` | any unhandled |
  | 405  | (chi default) | non-GET (and HEAD) |

### Authz pipeline + gate order

- **D-04:** **Cheaper-first ordering — explicit divergence from spec §5.1 step order.** Sequence:
  1. **Authn:** `keystore.KeyResolver.Resolve(ctx, "x-ach-key")` — Phase 3 D-08 reused. Branch on key_type prefix (`pk_` → §7.1 atomic check-and-extend; `ek_` → Redis→DB-on-miss with status check + `last_used_at` debounce). Fail → `401 expired_or_revoked` / `401 invalid_key_format`.
  2. **Env header validation:** pk_ missing `x-ach-environment` → `400 missing_environment`. ek_ header present but ≠ bound env → `403 wrong_environment`.
  3. **Env row lookup:** read `environments` row by (namespace, name) via env cache (D-07). Row missing → `404 environment_not_found`.
  4. **pk_ Team intersect:** `keystore.TeamsResolver.Resolve(ctx, owner_email)` (Phase 4 D-17 reused). Intersect with `environments.authorized_teams[]`. Empty → `403 unauthorized_team`. LiteLLM error → `503 litellm_unreachable`.
  5. **Context allowlist (CHEAPER):** check `<name> ∈ environments.context_<kindPlural>[]` from same already-loaded env row. Not in list → `403 unauthorized_content`.
  6. **Content resolution + §12.3 precedence:** Postgres lookup for `plugin/prompt/artifact` by name. For plugins: `plugins` row by name (CRD-derived projection) wins; absence falls back to `marketplace_plugins` row with alphabetically-lowest `marketplace_name`. No row → `404 content_not_found`. For prompt/artifact: direct projection-row lookup.
  7. **Staleness gate:** read `last_successful_refresh` + `max_staleness_seconds` from the row resolved in step 6 (for plugins from marketplace fallback: read `marketplace_plugins` row's columns; for CRD-resourced rows: read the projection's columns OR the linked `external_refs` row if the projection references one — planner to pin per kind). `now - lsr > max_staleness` OR `lsr IS NULL` → `503 stale_cache_expired`.
  8. **Open file** (D-02), stream (D-01).

  **Spec divergence callout:** Spec §5.1 step order (v10 fix per spec line 27) puts content resolution BEFORE allowlist check, yielding 404 for "name not in spec.context AND not in any CRD" and 403 for "name in CRD but not in spec.context". Cheaper-first inverts the order, so "name not in spec.context AND not in any CRD" returns `403 unauthorized_content` (allowlist fires first). User-confirmed in discussion. Side effect: this is a deliberate info-leak narrowing (Environment grant state never leaks "does the cluster have this resource"). Planner MUST capture this in the §15.6 outcome-table commentary AND VERIFICATION MUST flag the response-code divergence in the audit dashboard.

### Caches

- **D-05:** **Reuse `keystore.KeyResolver` (Phase 3 D-08) verbatim** for pk_/ek_ resolution. No new keystore work in Phase 5. Same cache TTL (60s), same singleflight, same Redis namespace (`ach:key:<credential_hash>`).

- **D-06:** **Reuse `keystore.TeamsResolver` (Phase 4 D-17) verbatim** for pk_ Team membership resolution. Same 60s Redis TTL on `ach:teams:<owner_email>`. Same LiteLLM `UserInfoByEmail` miss path. Same `503 litellm_unreachable` semantics. New caller: `caller="content_service"` on `litellm_unreachable_total` counter Inc.

- **D-07:** **New `internal/contentservice/envcache` Redis-backed Environment row cache.** 60s TTL (matches §5.1 cache budget). Key: `ach:env:<namespace>/<name>`. Value: JSON-serialized projection row (just the fields CS needs — `authorized_teams[]`, `context_prompts[]`, `context_plugins[]`, `context_artifacts[]`, `deletion_timestamp`). Single-flight on miss via `golang.org/x/sync/singleflight`. Reuses Phase 3 D-09 `go-redis` client. Invalidation: best-effort TTL — no explicit invalidation when Operator updates the projection row (60s convergence window is acceptable per CS-03 cache budget).

- **D-08:** **No caching for §12.3 plugin resolution + staleness reads.** Direct Postgres queries on every request — SC#3 and CS-10 say so verbatim. pgx prepared statements + connection pool (Phase 1 wrapper) absorb the hot-path cost.

### /metrics topology + shared metrics package

- **D-09:** **New `internal/metrics/` package** owns:
  - `func NewRegistry() *prometheus.Registry` — process-local, NOT the global default registry (avoid controller-runtime collector pollution on the chi mux).
  - `func Handler(reg *prometheus.Registry) http.Handler` — chi-mountable, wraps `promhttp.HandlerFor`.
  - Per-service collector factories: `NewForwarderCollectors(reg) ForwarderCollectors`, `NewContentServiceCollectors(reg) ContentServiceCollectors`. Each struct exposes typed `Inc`/`Observe` methods (avoid raw label-value bugs).
  - **Shared cross-service collector:** `func MustRegisterLitellmUnreachable(reg *prometheus.Registry) *prometheus.CounterVec` — declared once per process, returned `CounterVec` is called with `.WithLabelValues("forwarder"|"content_service"|"platform_api"|"operator")`.

- **D-10:** **`/metrics` on main chi mux per service.** Forwarder `:8080`, Platform API `:8083` (Phase 3 default), Content Service `:8082` (Phase 1 default). Operator KEEPS controller-runtime metricsserver (separate :8443) — adds the ACH-namespaced collectors to the operator-side `Registry()` via `runtime.NewSchemeBuilder`-style wiring (read controller-runtime docs during planning; pre-decision is `metrics.Registry.MustRegister(...)` against the global controller-runtime registry).

- **D-11:** **Histogram bucket choices** (§18.5 doesn't pin):
  - `forwarder_request_duration_seconds`: `prometheus.DefBuckets` (0.005..10s) — Forwarder is a thin proxy; tail beyond 10s is upstream LiteLLM's problem (SSE long-poll OK as `+Inf` bucket).
  - `content_service_request_duration_seconds`: extend tail to artifact-tarball sizes — `[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, +Inf]`. Planner adjusts after measuring real artifact sizes.

- **D-12:** **Helm chart + ServiceMonitor.** Ship Prometheus pod scrape annotations on all 4 service templates:
  ```yaml
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "<service traffic port>"
    prometheus.io/path: "/metrics"
  ```
  (Operator uses `:8443/metrics` per ctrl-rt default.) Add `examples/prometheus-servicemonitor.yaml` for Prometheus-Operator users (not included by default). Phase 5 deliverable.

### DB projection layer (spec v4 §5.2)

- **D-13:** **New DB tables** (schema details to planning, but contract pinned now):
  - `environments(namespace, name, ...)` — at minimum `authorized_teams text[]`, `context_prompts text[]`, `context_plugins text[]`, `context_artifacts text[]`, `runtime_mcp_servers text[]`, `runtime_a2a_agents text[]`, `deletion_timestamp timestamptz NULL`, `resource_version text`, `updated_at timestamptz`. PK = `(namespace, name)`. Status columns dual-written by Operator: `available_condition`, `access_group_synced_condition`, `execution_resources_resolved_condition` (JSON-encoded per condition).
  - `plugins(namespace, name, ...)` — at minimum `storage_location text`, `last_successful_refresh timestamptz NULL`, `max_staleness_seconds bigint`, `deletion_timestamp timestamptz NULL`, `resource_version text`, `updated_at timestamptz`. PK = `(namespace, name)`.
  - `prompts(namespace, name, ...)` — at minimum `storage_location text`, `content_type text NULL` (the `spec.contentType` override), `last_successful_refresh timestamptz NULL`, `max_staleness_seconds bigint`, `deletion_timestamp timestamptz NULL`. PK = `(namespace, name)`.
  - `artifacts(namespace, name, ...)` — at minimum `storage_location text`, `scope text NOT NULL CHECK (scope IN ('object','directory'))`, `last_successful_refresh timestamptz NULL`, `max_staleness_seconds bigint`, `deletion_timestamp timestamptz NULL`. PK = `(namespace, name)`.
  - Migration file: `db/migrations/000004_cs_projection.up.sql` (+ `down.sql`). Operator runs migrations on startup via Phase 1 D-18 mechanism.

- **D-14:** **New Operator reconcilers** — one per CRD kind not yet projected:
  - `internal/controller/ach/environment_projection_controller.go` — Owns `Environment`. On every reconcile: open transaction → UPSERT `environments` row → set K8s `status` subresource best-effort → commit. On deletion: UPDATE `deletion_timestamp = NOW()` (NOT delete the row — CS-09 says "until fully removed from K8s" and the row is the projection of K8s state; Operator removes row only after finalizer drain). Reuse Phase 1 finalizer pattern.
  - `internal/controller/ach/plugin_projection_controller.go`, `prompt_projection_controller.go`, `artifact_projection_controller.go` — same shape. Each writes its kind-specific columns.
  - **Status dual-write rule:** Postgres write is the load-bearing barrier (consumers read DB); K8s status subresource write is best-effort, retried but never blocks reconciler success. Failure to write K8s status emits a warning event but reconciler returns success.

- **D-15:** **Existing Operator reconcilers extended, not duplicated:**
  - The Phase 1 `Environment` controller (which today handles finalizer + AccessGroupSynced rollup per §7/§9 fixes) is the SAME controller that gains the projection write. Don't add a second `Environment` controller — add the projection write to the existing reconciler's Reconcile() method, transactionally with the existing status writes.
  - For `Plugin`/`Prompt`/`Artifact`: there ARE no existing reconcilers (only the CRD types in `api/ach/v1alpha1/`). Phase 5 SHIPS the first reconciler per kind, which is projection-only at v1alpha1 scope. No finalizers, no status rollup logic needed.

### Handler shape (rewrite vs wrap)

- **D-16:** **Rewrite `internal/contentservice/handler.go` end-to-end.** Pipeline-style serve():
  ```go
  func (d Deps) serve(kind string) http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          ctx := r.Context()
          out := pipeline(ctx, d, kind, r) // returns resolvedRow or *errResp
          if out.err != nil { d.writeError(w, r, out.err); return }
          // D-01 + D-02 streaming
          stream(w, r, out.row, out.contentType)
      }
  }
  ```
  - Old `handler_test.go` rewritten against new fixtures (testcontainers Postgres + Redis + httptest LiteLLM). Existing fixture YAMLs in `examples/` REUSED for path inputs but augmented with seed SQL for the projection tables.
  - `internal/contentservice/paths.go` `ResolvePath` retained (filesystem-layer logic stays valid).
  - `internal/contentservice/content_type.go` `ContentTypeForFile` retained but with the override-precedence rules pinned per D-03.
  - `cmd/ach/cmd/content_service.go` wires the new `Deps` struct (now needs `*pgxpool.Pool`, `*redis.Client`, `litellm.RESTClient`, `keystore.KeyResolver`, `keystore.TeamsResolver`, `envcache.Cache`, `metrics.ContentServiceCollectors`).

### Code structure

- **D-17:** **`internal/contentservice/` package layout:**
  - `internal/contentservice/handler.go` — REWRITTEN. RegisterRoutes + serve() pipeline orchestrator.
  - `internal/contentservice/pipeline.go` — NEW. 6-gate `pipeline(ctx, deps, kind, r)` function returning `(row, contentType, err)`.
  - `internal/contentservice/authz.go` — NEW. Per-gate functions: `resolveAuthn`, `resolveEnv`, `enforceTeams`, `enforceAllowlist`, `resolveContent` (with §12.3), `checkStaleness`.
  - `internal/contentservice/envcache/cache.go` — NEW. D-07 Redis-backed Environment row cache.
  - `internal/contentservice/stream.go` — NEW. D-01 custom serve.
  - `internal/contentservice/errors.go` — NEW. D-03 error mapping + writer (uses shared error envelope from `internal/httpenvelope/` if it exists from Phase 3, else create it).
  - `internal/contentservice/content_type.go` — RETAINED (unchanged).
  - `internal/contentservice/paths.go` — RETAINED. `ResolvePath` signature unchanged; pipeline calls it AFTER content resolution to map row → on-disk path. Artifact dispatches on `scope` column (no more 2-candidate walk).
  - `internal/contentservice/k8s.go` — REMOVED (PromptContentTypeLookup goes away; content_type comes from `prompts.content_type` column).
  - `internal/contentservice/handler_test.go` — REWRITTEN.

- **D-18:** **`internal/db/` additions:**
  - `internal/db/environments.go` — `UpsertEnvironment`, `GetEnvironmentByName`, `SoftDeleteEnvironment` (deletion_timestamp set, row retained).
  - `internal/db/plugins.go` — `UpsertPlugin`, `GetPluginByName`, `SoftDeletePlugin`. Plus `ResolvePluginByName` that implements §12.3 priority (plugins-first, marketplace_plugins fallback alphabetically-lowest) as a single CTE.
  - `internal/db/prompts.go` — `UpsertPrompt`, `GetPromptByName`, `SoftDeletePrompt`.
  - `internal/db/artifacts.go` — same shape.

- **D-19:** **`internal/metrics/` package layout** (new):
  - `internal/metrics/registry.go` — `NewRegistry`, `Handler`.
  - `internal/metrics/forwarder.go` — `ForwarderCollectors` struct + `NewForwarderCollectors(reg)`.
  - `internal/metrics/contentservice.go` — same for CS.
  - `internal/metrics/shared.go` — `MustRegisterLitellmUnreachable(reg)`.
  - `internal/metrics/buckets.go` — D-11 bucket constants.
  - Forwarder's existing `internal/forwarder/metrics/` package becomes a thin shim: keep the existing `Inc*` functions but back them with `internal/metrics.ForwarderCollectors`. Existing call sites in `internal/forwarder/proxy/handlers.go`, `internal/forwarder/bip/index.go`, etc. unchanged.

### Tests

- **D-20:** **Test plan** mirrors Phase 3 + Phase 4 patterns:
  - **Unit:**
    - `pipeline_test.go` — table-driven; per-gate denial / pass cases against mock `KeyResolver` / `TeamsResolver` / `Cache` / `*pgxpool.Pool`. ~50 cases covering each error code in D-03.
    - `stream_test.go` — verify custom serve sets `no-store`, `Content-Length`, identity transfer; verify Range/INM/IMS headers DON'T affect status (always 200, always full body).
    - `authz_test.go` — §12.3 precedence cases (CRD-wins, marketplace-alphabetically-lowest-wins, none → 404).
    - `metrics_test.go` — registry isolation; shared `litellm_unreachable_total` callable from all 4 simulated callers without re-register panic.
  - **Envtest:**
    - Projection reconciler per kind — apply CR, assert DB row UPSERT; mutate CR, assert DB row updates; delete CR, assert `deletion_timestamp` set then row removed after finalizer drain.
  - **Integration (testcontainers):**
    - Postgres + Redis + mock LiteLLM `httptest` — full pipeline end-to-end for happy path + each error code in D-03.
    - sendfile(2) verification: `strace -f -e trace=sendfile,sendfile64 -p <pid>` during stream; assert ≥1 sendfile syscall in output.
  - **E2E (Ginkgo, `test/e2e/`):** kind + Helm + real Dex/LiteLLM/Postgres/Valkey — exercise CS SCs #1-#5 + OBS SC#5 (curl `/metrics` on each service, grep for the required collectors). Driven by `make e2e-full`. Add `make wait-content-service` if not already present per CLAUDE.md "Waiting for state" table (it's listed; verify).

### Claude's Discretion

- **`http.Server.WriteTimeout = 0` on content-service traffic listener** — long artifact tarballs may exceed any non-zero deadline; rely on `Request.Context()` cancellation. Planner may revise if measured artifact sizes show otherwise.
- **HEAD method:** chi default 405 (CS-01 says HEAD MAY be supported but not contractual; doing nothing is the lowest-surprise choice).
- **Helm scrape annotations** (D-12) — chosen over ServiceMonitor as default. Example ServiceMonitor manifest ships under `examples/`.
- **Histogram bucket extension** for `content_service_request_duration_seconds` (D-11) — calibrated to artifact tarball expectation.
- **Operator runs migrations on startup** via the Phase 1 D-18 mechanism — new migration `000004_cs_projection.up.sql` follows the existing naming pattern.
- **Status dual-write order in projection reconcilers** (D-14): DB write FIRST (load-bearing), K8s status best-effort SECOND. A K8s status write failure does NOT fail the reconciler (logs warning, continues). Rationale: spec v4 says "Postgres authoritative, K8s subresource best-effort."
- **No new Environment informer for CS** — content service does NOT register a manager.Manager. It uses the Postgres projection + envcache exclusively. RBAC drop: remove `get/list/watch` on ACH CRDs from content service ServiceAccount.
- **`audit` package usage** — Phase 5 emits one audit event per Content Service GET (consistent with Phase 3 OBS-01 hook pattern). `target.kind = "<prompt|plugin|artifact>"`, `target.name = <name>`, `outcome` matches the response body code from D-03 table.
- **chi route registration** — explicit per-kind (`/content/prompt/{name}`, `/content/plugin/{name}`, `/content/artifact/{name}`) NOT a `{kind}` URL param. Keeps the kind allow-list at the routing layer (Phase 1 §8 pattern carried forward).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### ACH Hub Spec (source of truth)

- `spec/ach_hub_spec_v20260515_FINALv4.md` (entire file is the canonical spec — v4 supplants §5.2 globally per line 13).
- `spec/ach_hub_spec_v20260515_FINALv4.md` line 13–24 — **v4 §5.2 reversal**: ACH CRD reads from Postgres for Platform API / Forwarder / Content Service; Operator is sole K8s watcher; status dual-write. THE spec change driving the projection-layer work in D-13/D-14.
- `spec/ach_hub_spec_v20260515_FINALv4.md` line 27 — v10 fix pinning the §15.6 404 vs 403 two-step order. D-04 cheaper-first ordering is an explicit divergence; planner MUST document this.
- `spec/ach_hub_spec_v20260515_FINALv4.md` line 29 — §18.5 metric label enums (route ∈ /v1, /gemini, /mcp, /a2a; CS kind ∈ prompt/plugin/artifact; Operator kind ∈ prompt/plugin/artifact/marketplace).
- `spec/ach_hub_spec_v20260515_FINALv4.md` line 33 — Content Service LiteLLM-outage unified body code = `litellm_unreachable` (no asymmetry with audit).
- `spec/ach_hub_spec_v20260515_FINALv4.md` §5.1 — per-component RBAC + Content Service authorization narrative. Read v4-revised version.
- `spec/ach_hub_spec_v20260515_FINALv4.md` §6.5 — Environment drain order (Content Service serves until full removal — CS-09, D-14 deletion_timestamp soft-delete pattern).
- `spec/ach_hub_spec_v20260515_FINALv4.md` §7.1 — `pk_` atomic check-and-extend SQL CTE. Reused via Phase 3 D-10.
- `spec/ach_hub_spec_v20260515_FINALv4.md` §8.1 — `ek_` binding + `last_used_at` debounce. Reused via Phase 3 D-11.
- `spec/ach_hub_spec_v20260515_FINALv4.md` §10 — staleness contract; `last_successful_refresh` + `max_staleness` from Postgres at request time (CS-10, D-04 step 7).
- `spec/ach_hub_spec_v20260515_FINALv4.md` §12.3 — Plugin precedence: Plugin CRD wins; else `marketplace_plugins` row with alphabetically-lowest `marketplace_name`. D-04 step 6 + D-18 `ResolvePluginByName` CTE.
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.5 — error envelope shape `{"error":{"code","message"},"request_id"}`.
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.6 — Content Service API normative reference; per-kind Content-Type, identity transfer, no-store, sendfile, ignore Range/INM/IMS, HEAD optional, 405 otherwise.
- `spec/ach_hub_spec_v20260515_FINALv4.md` §18.2 — audit envelope + outcome enum (Phase 3 owns this; Phase 5 emits one event per CS GET).
- `spec/ach_hub_spec_v20260515_FINALv4.md` §18.5 — Prometheus normative metric set. Names, types, label keys, label-value enums ALL normative. New values added additively within an API version.

### Planning Artifacts

- `.planning/PROJECT.md` — Hub stack pinned (Go, chi, pgx, go-redis); LiteLLM coupling; `pk_` permanent first-class.
- `.planning/REQUIREMENTS.md` — Phase 5 maps to CS-01..CS-11 + OBS-03..OBS-06 (15 REQ-IDs).
- `.planning/ROADMAP.md` Phase 5 entry (line 246–260) — Goal, depends-on (Phase 1/2/3), 5 SCs. **Note:** SC#5 mentions `forwarder_jwt_*` and `content_service_*` collector names verbatim. D-09 + D-19 implement.
- `.planning/STATE.md` — pre-Phase-5 position (Phase 4 executing per recent main commits; §7/§8/§9 spec-section commits landed in batch).
- `.planning/phases/01-foundation-crds-db-schema-operator-skeleton-multi-tenancy/01-CONTEXT.md` — Phase 1 carry-forward: kubebuilder layout, `internal/db` pgxpool wrapper, RBAC pattern, MULTI-01..04, `cmd/ach/cmd/content_service.go` Phase 1 stub (extended by §8 commit).
- `.planning/phases/03-hub-identity-platform-api/03-CONTEXT.md` — Phase 3 carry-forward: chi router (D-01), middleware chain (D-02), `keystore.KeyResolver` (D-08), `PkCheckAndExtend` (D-10), `EkResolve` (D-11), manager.Manager idiom (D-20), counter-hook stub pattern.
- `.planning/phases/04-hub-forwarder-jwt-trust-path/04-CONTEXT.md` — Phase 4 carry-forward: `keystore.TeamsResolver` (D-17, reused verbatim by CS), error envelope (D-21), counter-hook stub pattern (D-18) NOW upgraded to real emission by Phase 5 D-09.
- `TODO` (project root, 5 lines) — minor cleanup items, not Phase 5 blockers.

### Pre-Phase-5 Implementation State (commits to read before planning)

- `3266513 feat(§8): Content Service /content/{prompt,plugin,artifact}/{name} routes` — the §8 raw-file handler being REWRITTEN per D-16.
- `4947d94 feat(§9): Environment Available composite-condition rollup` — Environment controller adds Available condition; Phase 5 D-15 extends this controller with DB projection write.
- `eb065c5 feat(§7): Environment AccessGroupSynced reconciler + parallel §4 fixes` — sibling Environment reconciler logic; planner cross-checks before adding projection write to avoid stomp.
- `internal/db/external_refs.go` — existing pattern for projection-row UPSERT + `last_successful_refresh` / `max_staleness_seconds` columns. D-13 + D-14 mirror this shape.
- `internal/db/marketplace_plugins.go` — same pattern; §12.3 alphabetical fallback queries this table.

### External Libraries (new dependencies + reuses)

- `github.com/prometheus/client_golang/prometheus` + `prometheus/promhttp` — NEW direct dep (if not already pulled by controller-runtime transitively; verify in planning).
- (existing) `github.com/go-chi/chi/v5` — chi router.
- (existing) `github.com/redis/go-redis/v9` — envcache + reused KeyResolver/TeamsResolver.
- (existing) `golang.org/x/sync/singleflight` — envcache miss dedup.
- (existing) `github.com/jackc/pgx/v5` — projection table CRUD.
- (existing) `sigs.k8s.io/controller-runtime` — operator reconciler manager.
- (existing) `github.com/oklog/ulid/v2` — `req_<ulid>` continued.

### Predecessor / Memory References

- `[[feedback_spec_source_of_truth]]` — `spec/ach_hub_spec_v20260515_FINALv4.md` is canonical; v4 §5.2 reversal supersedes any v3-era informer assumption in earlier CONTEXT.md files.
- `[[feedback_ach_pk_runtime_first_class]]` — pk_ on runtime is permanent first-class; CS treats pk_ uniformly with ek_ on all gates.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets

- **`internal/keystore/`** (Phase 3 D-08 + Phase 4 D-17) — `KeyResolver` + `TeamsResolver` ready. CS imports both; passes the same `caller="content_service"` label on the shared `litellm_unreachable_total` counter Inc on TeamsResolver miss-path errors.
- **`internal/db/external_refs.go`** + **`internal/db/marketplace_plugins.go`** (Phases 1/2) — Projection-row CRUD pattern + `last_successful_refresh`/`max_staleness_seconds` columns. D-13/D-14 mirror this shape exactly for the new four projection tables.
- **`internal/contentservice/paths.go`** (§8 commit) — `ResolvePath` filesystem-layer logic stays valid; pipeline calls AFTER content resolution to map row → on-disk path. Artifact path dispatches on `scope` column (D-13) — no more 2-candidate walk.
- **`internal/contentservice/content_type.go`** (§8 commit) — `ContentTypeForFile` retained with override-precedence pinned per D-03.
- **`internal/credhash/`** (Phase 1) — `Hash`, `Equal` reused via `keystore`.
- **`internal/audit/`** (Phase 2) — `audit.NewLogger` used for the one CS GET audit event per request (D-Discretion).
- **`internal/config/`** (Phases 1–4) — env-var helpers; Phase 5 adds no new env vars beyond optional `CONTENT_SERVICE_METRICS_BIND_ADDRESS` (defaults to same as traffic listener — `/metrics` on `:8082`).
- **`internal/controller/ach/environment_controller.go`** (Phase 1 + §7/§9 commits) — EXTENDED by Phase 5 D-15 to add the projection write (transactional with existing status writes). Do NOT add a second Environment controller.
- **`api/ach/v1alpha1/{plugin,prompt,artifact}_types.go`** (Phase 1) — types exist; reconcilers do NOT. Phase 5 ships first reconciler per kind (projection-only).
- **`db/migrations/000003_litellm_token.up.sql`** — most recent migration; Phase 5 adds `000004_cs_projection.up.sql`.

### Established Patterns

- **Single-binary cobra layout** — `cmd/ach/cmd/content_service.go` grows in place.
- **Containerized toolchain** — every `go`/`make`/`controller-gen` via `./scripts/dev.sh`.
- **chi router + middleware chain** — Phase 3 D-01/D-02 carried forward verbatim for the CS mux.
- **Counter-hook stubs → real emission** — Phase 4 D-18 stubs replaced; same Inc call sites, new backing collector.
- **Logging** — `log/slog`; `x-ach-key` redacted to `<prefix>_***` in access logs.
- **Tests** — Ginkgo + Gomega + envtest for projection reconcilers; testcontainers-go for Postgres + Redis; `httptest.Server` for LiteLLM mock; `test/e2e/` Ginkgo for sendfile + /metrics gates.
- **Wait targets** — `make wait-content-service` already in CLAUDE.md table; verify it's defined in Makefile (add if missing per CLAUDE.md "Waiting for state").
- **Pre-push gate** — `make pre-push` (17-gate) MUST pass; SPDX on every new `*.go`; gates 16+17 = `make lint` + `make unit`.

### Integration Points

- **Phase 1 → Phase 5:** `cmd/ach/cmd/content_service.go` stub + ResolvePath + ContentTypeForFile; pgxpool wrapper; migrations runner; RBAC scaffolding (drop ACH-CRD read perms from CS SA per spec v4).
- **Phase 2 → Phase 5:** `external_refs` + `marketplace_plugins` rows (already populated); `audit.Logger` for the one CS GET event.
- **Phase 3 → Phase 5:** `keystore.KeyResolver`, `PkCheckAndExtend`, `EkResolve`, chi+middleware+envelope idioms, `litellm.RESTClient`.
- **Phase 4 → Phase 5:** `keystore.TeamsResolver` (D-17 reused), counter-hook stub pattern → real emission, error envelope shape (D-21).
- **Phase 5 → Phase 5b (deferred):** Forwarder + Platform API still informer-reading per Phase 4 D-08; migrate to Postgres projection in a follow-up.
- **Phase 5 → Phase 6:** CLI `ach hydrate` exercises Content Service `downloadUrl` per CS endpoints — Phase 6 depends on CS being live.
- **Phase 5 → Phase 7:** CLI hydrate engine consumes streamed artifact tarballs; sendfile-streamed body must be hash-stable for CLI dual-hash drift detection.

</code_context>

<specifics>
## Specific Ideas

- **Custom serve snippet** (D-01):
  ```go
  func writeFile(w http.ResponseWriter, contentType string, f *os.File, size int64) error {
      w.Header().Set("Content-Type", contentType)
      w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
      w.Header().Set("Cache-Control", "no-store")
      w.WriteHeader(http.StatusOK)
      _, err := io.Copy(w, f) // sendfile(2) via *os.File.WriteTo → *net.TCPConn
      return err
  }
  ```
- **`x-ach-key` redaction in access logs** — `<prefix>_***` shape; Phase 3 D-02 convention.
- **Error envelope on every ACH-originated response code** (D-03): single helper `writeError(w, r, code, httpStatus, msg)`; reuses Phase 3 D-19 envelope format.
- **§12.3 resolution CTE sketch** (D-18 `ResolvePluginByName`):
  ```sql
  WITH plugin_match AS (
      SELECT 'plugin'::text AS source, namespace, name, storage_location,
             last_successful_refresh, max_staleness_seconds
      FROM plugins WHERE namespace = $1 AND name = $2 AND deletion_timestamp IS NULL
  ),
  marketplace_match AS (
      SELECT 'marketplace'::text AS source, marketplace_namespace AS namespace,
             marketplace_name AS name, storage_location,
             last_successful_refresh, max_staleness_seconds
      FROM marketplace_plugins
      WHERE plugin_name = $2 AND marketplace_namespace = $1
      ORDER BY marketplace_name ASC LIMIT 1
  )
  SELECT * FROM plugin_match
  UNION ALL
  SELECT * FROM marketplace_match WHERE NOT EXISTS (SELECT 1 FROM plugin_match)
  LIMIT 1;
  ```
  Planner refines column names against actual `marketplace_plugins` schema during planning.
- **Pod scrape annotation block** for Helm templates (D-12):
  ```yaml
  metadata:
    annotations:
      prometheus.io/scrape: "true"
      prometheus.io/port: "{{ .Values.contentService.port }}"
      prometheus.io/path: "/metrics"
  ```
- **sendfile E2E gate**: `kubectl -n ach-system exec deploy/ach-content-service -- sh -c 'strace -e trace=sendfile,sendfile64 -p 1 -e signal=none' &` then curl, assert sendfile syscall appears. Planner pins exact command in `test/e2e/`.
- **WriteTimeout=0 on CS traffic listener** (D-Discretion) — explicit override of Phase 3 D-03 timeout matrix because large artifact tarballs may exceed 30s. Document in `cmd/ach/cmd/content_service.go`'s server config block with a "WHY" comment.
- **Operator status dual-write order**: DB FIRST, K8s SECOND. K8s status write failure → log warning + continue. Spec v4 line 14: "status is dual-written (Postgres authoritative, K8s subresource best-effort)".

</specifics>

<deferred>
## Deferred Ideas

Items intentionally out of Phase 5 (mapped to later phases, v1beta1 backlog, or permanently dropped):

### Phase 5b candidate — Forwarder + Platform API informer→Postgres migration

- **Forwarder reads still informer-based** (Phase 4 D-08 `manager.Manager` + `IndexField` for BIP + Environment + Secret) — these were shipped under the v3-era §5.2 model. Spec v4 §5.2 reversal applies to Forwarder too ("Platform API, Forwarder, and Content Service no longer hold informers over ACH CRDs"). Migration deferred to a new Phase 5b: add BIP projection table, port BIP `IndexField`-lookup to a DB query (alphabetically-LAST winner SQL); same for Environment lookups. Forwarder secret informer stays (Secret IS K8s native, not ACH CRD).
- **Platform API reads** — same situation; Phase 3 D-20 wired a manager.Manager informer. Migration to Postgres deferred to Phase 5b.
- **Rationale for deferring:** changing two more services' read paths in Phase 5 would double the blast radius; Phase 5 is already absorbing the full DB projection schema + 4 new reconcilers + the entire CS rewrite + the metrics rollout. Phase 5b is a focused follow-up with the projection tables already in place.

### Out of v1alpha1 (§20 backlog)

- HTTP `Range` / `206 Partial Content` — spec v6 line 124 §20 entry.
- `ETag` / `If-None-Match` conditional GET — spec v6 line 124 §20 entry.
- HA Content Service multi-replica with shared cache — single-replica per spec line 175 §5.1 deployment topology.
- HA Operator multi-replica — same.
- Per-MCP/A2A backend URL registry — superseded by single-upstream model (Phase 4 D-05).
- Cross-marketplace `Synced=False, reason=NameConflict` Operator status writes — already in marketplace lifecycle; Phase 5 only consumes precedence.
- `last_successful_refresh` invalidation event on Operator force-refresh — already covered by OP-11.

### Permanently dropped / out of scope

- Content Service informer cache over ACH CRDs — explicitly forbidden by spec v4 §5.2 reversal.
- `Synced=DuplicateTarget` reconciler (BIP) — permanently dropped per Phase 4 TODO.md §6 (carries forward).
- Range/IMS/INM honor in v1alpha1 — explicit "ignored" per CS-08 + SC#1.
- `pk_` server-side runtime-forbid toggle — `[[feedback_ach_pk_runtime_first_class]]`; permanent.
- Compression at CS tier — content is already tarball-encoded upstream; double-compression wastes CPU.
- Audit envelope redesign — Phase 3 OBS-01/02 owns; Phase 5 only calls.

### Engineer-pending verification debt from prior phases

- `scripts/uat-g1.sh` against live LiteLLM v1.83.10 — Phase 02.2 carry-forward.
- `scripts/uat-phase3.sh` against live kind+Helm — Phase 3 carry-forward.
- `scripts/uat-phase4.sh` (if it exists) — Phase 4 carry-forward; NOT a Phase 5 blocker but Phase 5 E2E extends the same harness.

### Spec-revision flags for planner

- **Phase 4 04-CONTEXT.md** references informer reads for Forwarder/Platform-API — that is stale under spec v4 §5.2 reversal. Phase 5 does NOT touch Phase 4 code (already shipped); the divergence is acknowledged and bookmarked for Phase 5b.
- **ROADMAP Phase 5 entry** (lines 246–260) reads the SCs against the v4-aware contract — no edit needed.

</deferred>

---

*Phase: 5-content-service-cross-component-observability*
*Context gathered: 2026-05-27*

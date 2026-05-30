# Phase 2: External Refs + Marketplace + Operator Reconciliation - Context

**Gathered:** 2026-05-15
**Status:** Ready for planning

<domain>
## Phase Boundary

Phase 2 turns the stub Operator from Phase 1 into a real refresh engine. Concretely, this phase delivers:

- **External-reference refresh per OP-03** for `Plugin`, `Prompt`, `Artifact` (single-resource external refs): fetch upstream → materialize into `.tmp/<random>` → `fsync` → `rename(2)` → DB UPDATE. Step 4 (`rename(2)`) is the load-bearing barrier; a crash between steps 4 and 5 is benign because the next reconcile republishes idempotently.
- **`PluginMarketplace` three-stage refresh per OP-06**: Stage-1 marketplace-file fetch+parse (failure aborts before any UPSERT/DELETE), Stage-2 per-plugin best-effort UPSERT (single-plugin failure does NOT roll back others; recorded in `status.message`), Stage-3 final DELETE sweep of vanished names.
- **Anchored RE2 include/exclude filters per OP-07** with Operator-prepended `^`; pattern compile failure → `Synced=False, reason=InvalidConfig`; `include` matching zero → `UpstreamInvalid`; `exclude` matching zero → silent no-op.
- **Cross-marketplace name conflict resolution per OP-08**: alphabetical priority on `PluginMarketplace.metadata.name` (Unicode code-point); losers report `Synced=False, reason=NameConflict`; `Plugin` CRD beats any marketplace-sourced name with the same `metadata.name`.
- **Plugin size cap enforcement per OP-09**: `ACH_PLUGIN_MAX_SIZE_MIB` (wired in Phase 1, default 50, Operator refuses to start on `0`/negative/non-numeric); oversized plugins flip `SourceReachable=False, reason=PluginTooLarge` and produce no cached file.
- **`ExecutionResourcesResolved` per OP-13**: query LiteLLM REST for the registered Model/MCP/A2A name set on each Environment reconcile, intersect with `Environment.spec.runtime.*`, write unresolved names into `status.unresolvedRuntime`, requeue Environments every 5 minutes in addition to event-driven reconciliation.
- **Orphan LiteLLM key cleanup per OP-15**: configurable interval `ACH_ORPHAN_CLEANUP_INTERVAL` (default `1h`, minimum `5m`, refuses to start below); orphan = LiteLLM key absent from active ACH DB rows AND ≥10min old AND owning `user_id` is ACH-managed; aborts cleanly on LiteLLM-unreachable; each revocation emits an audit event.

Phase 2 explicitly **excludes**: `pk_`/`ek_` creation, Dex SSO, Platform API endpoints (Phase 3); Forwarder routes + JWKS + JWT signing (Phase 4); Content Service streaming response body (Phase 5 — Phase 2 only writes the cached files Content Service will later serve); CLI work (Phases 6–7); Prometheus metric endpoints beyond what `controller-runtime` exposes by default (Phase 5).

</domain>

<decisions>
## Implementation Decisions

### LiteLLM Client (replaces Phase 1 NoopClient)

- **D-01:** Lift the sister project's `../ach_litellm/internal/litellm/` package verbatim into `ach/internal/litellm/`. Files copied: `client.go`, `transport.go`, `team.go`, `model.go`, `mcp.go`, `agents.go`, `keyinfo.go`, `errors.go`, `types.go`, `doc.go` (~2,400 lines total). Phase 1's `Client` interface (`DeleteAccessGroup`, `DeleteTag`) is preserved; the lifted code becomes the concrete implementation that fulfills it. `NoopClient` is retained for unit tests where wire traffic must be suppressed. Phase 2 extends the `Client` interface with new methods: `ListModels`, `ListMCPServers`, `ListA2AAgents` (for `ExecutionResourcesResolved`), `ListUserKeys`, `RevokeKey` (for orphan cleanup).
- **D-02:** LiteLLM client configuration uses `ACH_LITELLM_*`-prefixed env vars (matches Phase 1's `ACH_*` convention; sister's `LITELLM_OPERATOR_*` names are renamed during the lift):
  - `ACH_LITELLM_BASE_URL` (required; Operator refuses to start on missing/empty)
  - `ACH_LITELLM_MASTER_KEY` (required; sourced from `Secret`-mounted env via `envFrom`)
  - `ACH_LITELLM_AUTH_HEADER` (optional; default `Authorization: Bearer`, override to `x-litellm-api-key`)
  - `ACH_LITELLM_DANGEROUSLY_LOG_BODIES` (optional; debug escape hatch from sister `transport.go`)
- **D-03:** The lifted `transport.go` redaction discipline (default: log only `{method, path, status, latency_ms}`; never headers, never bodies) is preserved verbatim. Aligns with the [[feedback_litellm_operator_no_redaction_filter]] memory pattern — discipline over scrubbing.

### Source-Type Fetchers

- **D-04:** Six source types implemented as SDK-per-source-type under `internal/sources/<type>/`, each exposing a uniform `Fetcher` interface. Dependencies:
  - `internal/sources/github/`: `github.com/google/go-github/v62` (or current major) for metadata + `github.com/go-git/go-git/v5` for tarball materialization.
  - `internal/sources/gitlab/`: `github.com/xanzy/go-gitlab` + `go-git/v5`.
  - `internal/sources/bitbucket/`: `github.com/ktrysmt/go-bitbucket` + `go-git/v5`.
  - `internal/sources/s3/`: `github.com/aws/aws-sdk-go-v2/service/s3` (object scope + prefix listing for directory scope).
  - `internal/sources/gcs/`: `cloud.google.com/go/storage`.
  - `internal/sources/http/`: stdlib `net/http` + `If-None-Match` / `If-Modified-Since` conditional-GET handling per Hub §10.1.
- **D-05:** Fetchers return a streaming `io.ReadCloser` plus per-source metadata (commit SHA / ETag / GCS generation / Last-Modified). The reconciler is responsible for `.tmp/<random>` lifecycle (`os.CreateTemp(cacheRoot+"/.tmp", "")`), `fsync`, and atomic `rename(2)` — fetchers stay storage-agnostic.

### Refresh Scheduling

- **D-06:** Each external-reference reconcile (`Plugin`, `Prompt`, `Artifact`, `PluginMarketplace`) returns `ctrl.Result{RequeueAfter: spec.refresh.interval}`. Controller-runtime's workqueue exponential backoff handles transient error retry; the next `RequeueAfter` rearms after a successful reconcile.
- **D-07:** **Force-refresh annotation handling**: when `Platform API` (Phase 3) patches `ach.ackstorm.ai/force-refresh: <RFC3339-ts>` on a target CR, the Operator's existing watch delivers the event synchronously, the reconcile runs as if `refresh.interval` had fired, and the same `Update()` that writes `last_successful_refresh` also removes the annotation. The annotation patch is Platform API's only write surface on ACH CRDs (Phase 1 RBAC carve-out per MULTI-02).
- **D-08:** Environment reconcile returns `ctrl.Result{RequeueAfter: 5*time.Minute}` per Hub §6.4 to drive `ExecutionResourcesResolved` re-evaluation. Event-driven reconcile on `Environment` spec changes still fires immediately via the controller-runtime watch.

### Marketplace Stage-2 Concurrency

- **D-09:** Stage-2 marketplace plugin materialization runs **serially** (one plugin at a time inside the per-marketplace reconcile). Deterministic ordering for envtest, predictable PVC I/O, single status-message construction. Optimization to bounded-parallel workers is deferred until production marketplaces grow past ~50 plugins or wall-clock becomes a measured problem.
- **D-10:** Per-plugin Stage-2 failures are reported in `PluginMarketplace.status.message` as a structured one-line summary: `"stage-2: N plugin(s) failed: <name>: <reason>, <name>: <reason>"`. First 5 failures are listed verbatim; if more, a truncation suffix indicates the count (`"+M more"`). `Synced=True` is preserved when Stage-1 fetched and parsed successfully — per Hub §12.4 and Phase 2 SC #2 ("one-plugin upstream failure is recorded in `status.message` and other plugins still succeed"). Stage-1 fetch/parse failure flips `Synced=False` with `reason` drawn from the §10/§12 enum (`UpstreamInvalid`, `InvalidConfig`, `Unreachable`).

### Secret Reads for Fetcher Auth

- **D-11:** `corev1.Secret` informer cache wired via controller-runtime `mgr.GetCache().GetInformer(ctx, &corev1.Secret{})` in `cmd/operator/main.go`. Operator RBAC (`config/rbac/operator-role.yaml`) gains `get/list/watch` on `secrets` in the deployment namespace (MULTI-01 namespace-scoping preserved — `Role`/`RoleBinding`, never `ClusterRole`). Sub-millisecond reads after informer warmup; secret rotation observed on next reconcile.

### Plugin Size Cap Enforcement

- **D-12:** Cap enforced during streaming download via `io.LimitReader(body, max+1)` wrapping the fetcher's response body. `io.Copy(stagingFile, limited)` is monitored: if bytes written exceed `max`, the staging file is deleted (`os.Remove(stagingFile.Name())`), the reconcile flips `SourceReachable=False, reason=PluginTooLarge`, and no `marketplace_plugins`/`external_refs` row UPDATE occurs. Saves bandwidth + disk; no torn-byte `.tmp/` artifacts >cap appear under PVC inspection.

### ExecutionResourcesResolved Query Shape

- **D-13:** A central LiteLLM-snapshot `manager.Runnable` refreshes `ListModels` + `ListMCPServers` + `ListA2AAgents` into in-memory sets every ~5 minutes (TTL aligned with Hub §6.4 Environment requeue interval). Each Environment reconcile reads the snapshot and intersects `spec.runtime.{models,mcpServers,a2aAgents}` against the snapshot locally to compute `status.unresolvedRuntime` + the `ExecutionResourcesResolved` condition. Bounded LiteLLM load: 3 calls per ~5min regardless of Environment count.
- **D-14:** On LiteLLM-unreachable during snapshot refresh, the prior snapshot is preserved (logged with staleness age) and the next interval retries. Environments reconciled against a stale snapshot still write `ExecutionResourcesResolved` based on the cached data; the Operator emits a `litellm_unreachable_total{caller="operator"}` metric increment per the §18.5 contract (full Prometheus emitter wiring is Phase 5, but the increment hook is added here).

### Orphan LiteLLM Key Cleanup

- **D-15:** Orphan-cleanup loop is a controller-runtime `manager.Runnable` registered via `mgr.Add(orphan.NewRunnable(llm, db, auditLogger, interval))` in `cmd/operator/main.go`. Lifecycle is tied to the manager — graceful shutdown on SIGTERM via `ctx` cancellation, leader-election ready if/when v1beta1 introduces HA. `interval` is parsed from `ACH_ORPHAN_CLEANUP_INTERVAL` per OP-15 (default `1h`, minimum `5m`, Operator refuses to start on `0`, negative, non-parseable, or below minimum; unset uses default).
- **D-16:** Per-tick procedure: list ACH-managed `litellm_user_id` set (DB query over `personal_keys.litellm_user_id ∪ environment_keys.litellm_user_id` with `status='active'`), for each managed user `ListUserKeys` from LiteLLM, identify orphans per Hub §18.4 definition (not in active ACH rows AND ≥10min old AND owning user is ACH-managed), revoke each via `Client.RevokeKey`. LiteLLM-unreachable aborts the run cleanly; no state persisted between ticks (idempotent).

### Audit Event Emission

- **D-17:** Audit events flow through a dedicated stdout-JSON `slog.Handler` with a distinguishing top-level field (`"audit": true`). Kubernetes log collection (fluent-bit / Loki) picks them up alongside operational logs; downstream filtering by the `audit=true` predicate separates audit from ops without requiring a second log destination. v1alpha1 audit volume is low (orphan revocations, future Environment lifecycle events). File-based audit + dedicated audit PVC is deferred to a later milestone.
- **D-18:** Phase 2 emits audit events for orphan revocations only per Hub §18.4. Event shape: `{ts, audit:true, action:"operator.orphan-cleanup", target:{kind:"litellm_key", name:<litellm_key_id>}, outcome:"success"|"litellm_unreachable", request_id}`. Phase 3 will expand the audit surface (pk_/ek_ create+revoke, hydrate, admin operations); Phase 2 lays down the logger infrastructure.

### Claude's Discretion

- **Fetcher error classification → condition reason.** Mapping HTTP/SDK errors to the Hub `SourceReachable.reason` enum (`Unreachable`, `Unauthorized`, `NotFound`, `UpstreamInvalid`, `PluginTooLarge`, `StaleCacheExpired`) is mechanical — researcher/planner can derive from spec §10 + sister project's `errors.go` precedent.
- **`.tmp/<random>` naming scheme**: `os.CreateTemp(cacheRoot+"/.tmp", "stg-")` with go's default cryptographic random suffix is sufficient. Orphan `.tmp/` sweep already specified in Phase 1's `cachefs` package or to be added in Phase 2 with a 1h interval per Hub §10.3.
- **Test infra**: Ginkgo + Gomega + envtest established in Phase 1 continues. testcontainers-go wraps a real LiteLLM mock (or `httptest.Server` returning canned LiteLLM responses) for client unit tests; integration tests stand up a real Postgres via testcontainers-go (already wired in Phase 1).
- **Cache reconstruction on PVC loss (OP-11)**: Phase 1 wired `internal/cachefs.EnsureLayout`; Phase 2 adds the `UPDATE external_refs SET last_successful_refresh = NULL` reset that runs on Operator startup when the cache root is empty.
- **Metric emission stubs**: counter hooks (`operator_external_ref_refresh_total{kind,type,result}`, `litellm_unreachable_total{caller="operator"}`) are added inline; the full Prometheus emitter + `/metrics` endpoint wiring is Phase 5.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### ACH Hub Spec (source of truth)

- `ach_hub_spec_v20260515_FINALv4.md` §6.4 — Environment availability; `ExecutionResourcesResolved` derivation; 5-minute Environment requeue contract
- `ach_hub_spec_v20260515_FINALv4.md` §6.6 — condition reasons closed set (referenced by every status write in this phase)
- `ach_hub_spec_v20260515_FINALv4.md` §10 — external-reference refresh model, staleness predicate, `503 stale_cache_expired` contract
- `ach_hub_spec_v20260515_FINALv4.md` §10.1 — six source-type schemas (`github`, `gitlab`, `bitbucket`, `s3`, `gcs`, `http`) and their auth conventions
- `ach_hub_spec_v20260515_FINALv4.md` §10.3 — cache layout, `.tmp/` staging, atomic `rename(2)` publication, `external_refs` UPDATE sequence, rename-failure handling, cache reconstruction on PVC loss
- `ach_hub_spec_v20260515_FINALv4.md` §11 — `Plugin` wire format (Claude Code plugin), `ACH_PLUGIN_MAX_SIZE_MIB` start-time validation, `PluginTooLarge` semantics
- `ach_hub_spec_v20260515_FINALv4.md` §12 — `PluginMarketplace` model, RE2 filter semantics, `Synced` condition reasons
- `ach_hub_spec_v20260515_FINALv4.md` §12.1 — Claude Code marketplace schema (`name`, `owner`, `plugins[]` with source variants); npm source → `UnsupportedPluginSource`
- `ach_hub_spec_v20260515_FINALv4.md` §12.3 — name conflict resolution (Plugin CRD > alphabetically-lowest marketplace; `NameConflict` reason)
- `ach_hub_spec_v20260515_FINALv4.md` §12.4 — three-stage refresh lifecycle (Stage-1 fetch/parse, Stage-2 per-plugin best-effort, Stage-3 DELETE sweep)
- `ach_hub_spec_v20260515_FINALv4.md` §15.5 — force-refresh annotation contract (`ach.ackstorm.ai/force-refresh`); Operator removes annotation in same UPDATE as `last_successful_refresh`
- `ach_hub_spec_v20260515_FINALv4.md` §18.2 — audit event schema (`action`, `target`, `outcome`, `request_id`, no plaintext)
- `ach_hub_spec_v20260515_FINALv4.md` §18.4 — orphan LiteLLM key cleanup (interval, minimum, orphan definition, abort semantics, audit shape)
- `ach_hub_spec_v20260515_FINALv4.md` §18.5 — Prometheus metric set; Phase 2 emits hooks for `operator_external_ref_refresh_total{kind,type,result}` and `litellm_unreachable_total{caller="operator"}`

### Planning Artifacts

- `.planning/PROJECT.md` — project context, constraints (Go-on-both-sides, K8s-native, HTTPS-only)
- `.planning/REQUIREMENTS.md` — Phase 2 maps to: OP-03, OP-04 (rename-failure handling, paired with OP-03), OP-06, OP-07, OP-08, OP-09, OP-11 (cache reconstruction reset, paired with Phase 1's `cachefs`), OP-13, OP-15
- `.planning/ROADMAP.md` — Phase 2 entry: goal, depends-on, SC #1..#5
- `.planning/STATE.md` — current position (Phase 1 complete, Phase 2 starting)
- `.planning/phases/01-foundation-crds-db-schema-operator-skeleton-multi-tenancy/01-CONTEXT.md` — Phase 1 carry-forward (kubebuilder v4 layout, `internal/litellm.Client` interface, cache layout, containerized toolchain via `./scripts/dev.sh`, `ACH_PLUGIN_MAX_SIZE_MIB` already wired)

### Sister Project (source for the lifted LiteLLM client)

- `../ach_litellm/internal/litellm/client.go` — `Client` struct, `NewClient`, `makeRequest`, auth-header strategy with `LITELLM_OPERATOR_AUTH_HEADER` (renamed to `ACH_LITELLM_AUTH_HEADER` on lift)
- `../ach_litellm/internal/litellm/transport.go` — `redactingRoundTripper`; default log discipline (method/path/status/latency_ms only); `DANGEROUSLY_LOG_BODIES` escape hatch
- `../ach_litellm/internal/litellm/team.go` `model.go` `mcp.go` `agents.go` `keyinfo.go` — endpoint shapes; `ListModels`/`ListMCPServers`/`ListA2AAgents` callers can mirror these for ExecutionResourcesResolved
- `../ach_litellm/internal/litellm/errors.go` `types.go` — `Auth401Error` and error-classification idioms reused for Phase 2 fetcher reason mapping
- `../ach_litellm/internal/connection/` — `ConnectionCache` pattern: keep ACH's analog (cached LiteLLM snapshot D-13) idiomatically aligned

### External Libraries (new dependencies for Phase 2)

- `github.com/google/go-github/v62` — GitHub source fetcher
- `github.com/xanzy/go-gitlab` — GitLab source fetcher
- `github.com/ktrysmt/go-bitbucket` — Bitbucket source fetcher
- `github.com/go-git/go-git/v5` — Tarball materialization from git sources
- `github.com/aws/aws-sdk-go-v2` — S3 source fetcher (service/s3)
- `cloud.google.com/go/storage` — GCS source fetcher
- (existing) `sigs.k8s.io/controller-runtime` — Reconcile, watches, informer cache, `manager.Runnable`

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets (from Phase 1)

- **`internal/litellm/client.go`** (Phase 1): `Client` interface with `DeleteAccessGroup`/`DeleteTag`; `NoopClient` implementation. Phase 2 keeps the interface, extends it with `ListModels`/`ListMCPServers`/`ListA2AAgents`/`ListUserKeys`/`RevokeKey`, and adds the real implementation by lifting the sister `internal/litellm/` package. `NoopClient` is retained for unit tests.
- **`internal/cachefs/bootstrap.go`** (Phase 1): `EnsureLayout` ensures `prompt/`, `plugin/`, `marketplace/`, `artifact/`, `.tmp/` exist under the cache root; idempotent. Phase 2 calls into this on startup and uses `os.CreateTemp(cacheRoot+"/.tmp", "stg-")` for staging file allocation.
- **`internal/db/db.go`** (Phase 1): `pgxpool`-wrapped Postgres connection. Phase 2 adds the `external_refs` and `marketplace_plugins` UPSERT/UPDATE statements and the orphan-cleanup user enumeration query.
- **`internal/controller/ach/`** (Phase 1): per-kind reconcilers (`plugin_controller.go`, `prompt_controller.go`, `artifact_controller.go`, `pluginmarketplace_controller.go`, `environment_controller.go`, `backendidentitypolicy_controller.go`) with finalizer registration. Phase 2 fills in the refresh logic inside the existing Reconcile() bodies.
- **`internal/controller/ach/finalizers.go`** (Phase 1): finalizer constants and helpers. Phase 2 leverages the same hooks for external-ref cache cleanup (already real in Phase 1 per D-12 of Phase 1 CONTEXT).
- **`cmd/operator/main.go`** (Phase 1): controller manager bootstrapping. Phase 2 swaps `NoopClient` for the real implementation, registers the LiteLLM-snapshot Runnable and the orphan-cleanup Runnable via `mgr.Add()`, adds the Secret informer to the manager cache.
- **`config/rbac/operator-role.yaml`** (Phase 1): currently lists ACH CRD verbs + status subresource. Phase 2 amends to add `get/list/watch` on `secrets` (namespace-scoped per MULTI-01).

### Established Patterns (carry-forward from Phase 1)

- **Kubebuilder v4 + multigroup layout** mirroring `../ach_litellm/`.
- **Containerized toolchain**: every `go`/`kubebuilder`/`make` invocation goes through `./scripts/dev.sh`. Plans' `<action>` and `<verify>` blocks are read as "run via `./scripts/dev.sh`" by executor agents.
- **Logging**: `log/slog` for application logs (JSON in prod, text in dev) + `controller-runtime/pkg/log/zap` for the manager. Phase 2 adds a dedicated audit `slog.Handler`.
- **Tests**: Ginkgo + Gomega + envtest for controller tests; testcontainers-go for Postgres integration; `httptest.Server` for LiteLLM-client unit tests.
- **Config plumbing**: `os.Getenv` + small validation helper (sister convention). New Phase 2 knobs: `ACH_LITELLM_BASE_URL`, `ACH_LITELLM_MASTER_KEY`, `ACH_LITELLM_AUTH_HEADER`, `ACH_LITELLM_DANGEROUSLY_LOG_BODIES`, `ACH_ORPHAN_CLEANUP_INTERVAL`. `ACH_PLUGIN_MAX_SIZE_MIB` was wired in Phase 1 and is now consumed for the first time.

### Integration Points (forward + lateral)

- **Phase 1 → 2:** `internal/litellm.NoopClient` swap, `cachefs.EnsureLayout` invoked at refresh time, `ACH_PLUGIN_MAX_SIZE_MIB` consumed for the first time, Phase 1 finalizer-stub LiteLLM calls become real.
- **Phase 2 → 3:** Platform API's `/platform/admin/refresh` patches the `ach.ackstorm.ai/force-refresh` annotation Phase 2 reads; `/platform/env-keys` POST waits for `AccessGroupSynced=True` (which is set by Phase 2's Environment reconcile against real LiteLLM).
- **Phase 2 → 5:** Content Service streams the cached files Phase 2 produces; `last_successful_refresh` + `max_staleness` columns are read by Content Service for the staleness predicate.
- **Phase 2 → 4:** Forwarder reads `BackendIdentityPolicy` from the informer wired in Phase 1; Phase 2 does NOT touch `BackendIdentityPolicy.status` (that's OP-14/OP-16, Phase 4).

</code_context>

<specifics>
## Specific Ideas

- **Sister project = lift target, not just style reference.** Phase 1 already pattern-matched the sister project's repo layout. Phase 2 takes it further by literally copying `../ach_litellm/internal/litellm/` (~2,400 lines). Memory [[reference_ach_litellm_sister_project]] reaffirms this.
- **`ACH_LITELLM_*` prefix is the user's preferred normalization** — sister's `LITELLM_OPERATOR_*` env-var names are renamed during the lift to match Phase 1's `ACH_*` convention. Researcher/planner must produce a mapping table in the implementation plan for reviewer sanity.
- **Stage-2 status.message is the load-bearing observability surface** for marketplace partial failures. The structured one-line format (`"stage-2: N plugin(s) failed: <name>: <reason>, ..."`) must remain stable for operators grepping `kubectl describe`. Truncate at first 5 + `"+M more"`.
- **Plugin cap is enforced at write time, not read time.** Hub spec §11 says Content Service streams the archive without buffering. Phase 2's job is to ensure no oversized archive ever reaches the cached path — the `io.LimitReader` + delete-on-exceed pattern guarantees this.
- **Orphan-cleanup audit logger is the first slog handler with `"audit": true`**. Phase 3 will reuse the exact same handler for pk_/ek_ lifecycle events; Phase 2's job is to land the shape such that Phase 3 doesn't refactor it.

</specifics>

<deferred>
## Deferred Ideas

Discussion stayed within Phase 2 scope. Items intentionally out of Phase 2 (already mapped to later phases or out of v1alpha1):

- **`AccessGroupSynced` write path** during Environment reconcile against real LiteLLM (touched by Phase 2's switch to real `litellm.Client`, but the rich access-group reconciliation logic — sync of authorizedTeams, runtime models/MCPs/A2As as access-group entries — is Phase 3 territory tied to `ek_` minting). Phase 2 will write the condition; the full sync workflow lands when Platform API needs it.
- **Bounded-parallel Stage-2 plugin materialization** (currently serial per D-09). Re-evaluate if production marketplaces grow past ~50 plugins or measured wall-clock becomes painful.
- **Audit log to a dedicated file / PVC** (currently stdout JSON per D-17). Defer until audit volume justifies the operational complexity.
- **HA / leader election** for the orphan-cleanup loop. Single-replica `Recreate` Pod makes this unnecessary in v1alpha1; the `manager.Runnable` choice keeps the door open for v1beta1.
- **CRD schema extension for `status.failedPlugins[]`** (a richer per-plugin failure surface than `status.message`). Phase 2 stays within the spec's `status.message` contract.
- **Per-source-type retry/backoff tuning** beyond controller-runtime workqueue defaults.
- **`/metrics` endpoint wiring + ServiceMonitor manifests** — Phase 5 (Cross-component Observability).
- **`BackendIdentityPolicy` duplicate-target status reconciliation** (OP-14, OP-16) — Phase 4.
- **Pepper rotation tooling** — v1beta1 backlog per Hub §20 (carried forward from Phase 1).
- **Bitbucket username + app-password auth.** v1alpha1 supports Bitbucket via Bearer token (Repository Access Token) only — see Plan 02-02 Task 2 fetcher implementation and threat T-02-14. Adding username + app-password support would require either a second data-key on `SourceAuthSecretRef` (e.g. `UsernameKey`) or a per-source-type auth-shape extension; both break the uniform AuthSecretRef semantics v1alpha1 preserves. Re-evaluate in v1beta1 if deployers report Bitbucket Cloud accounts where Repository Access Tokens are not available.

</deferred>

---

*Phase: 2-external-refs-marketplace-operator-reconciliation*
*Context gathered: 2026-05-15*
</content>

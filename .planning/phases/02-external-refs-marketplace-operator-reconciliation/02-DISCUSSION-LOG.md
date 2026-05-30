# Phase 2: External Refs + Marketplace + Operator Reconciliation - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-05-15
**Phase:** 02-external-refs-marketplace-operator-reconciliation
**Areas discussed:** LiteLLM client strategy, Fetchers + scheduling, Plugin cap + ExecResourcesResolved, Orphan loop + audit plumbing

---

## LiteLLM Client Strategy

### Question 1 — Reuse approach

| Option | Description | Selected |
|--------|-------------|----------|
| Copy + adapt under `internal/litellm/` | Lift sister project's 7 files verbatim, fold into existing Client interface, extend with ACH-specific endpoints. One go.mod, NoopClient retired but kept for tests. | ✓ |
| Import as Go module dep | Add `github.com/ackstorm/ach-litellm` to go.mod; get upgrades for free, couples release cadences (sister has no v0 tag). | |
| Port patterns, write fresh | Reference sister for transport hygiene/auth/errors but write ACH-only endpoints from scratch. Smaller surface. | |

**User's choice:** Copy + adapt under `internal/litellm/`.
**Notes:** Aligns with Phase 1's mirror-the-sister-project pattern and memory [[reference_ach_litellm_sister_project]]. NoopClient retained for unit tests where wire traffic must be suppressed.

### Question 2 — Configuration wiring

| Option | Description | Selected |
|--------|-------------|----------|
| `ACH_LITELLM_*` env vars (rename sister) | Rename sister's `LITELLM_OPERATOR_*` to `ACH_LITELLM_*`. Operator refuses to start on missing/empty BASE_URL or MASTER_KEY. | ✓ |
| Keep sister env-var names | Identical to sister, easier upstream merges if we pull updates. Crosses Phase 1's `ACH_` prefix convention. | |
| CRD spec field, not env var | A new deployment-level CRD (or extended Environment) carrying LiteLLM endpoint. 7th CRD overhead. | |

**User's choice:** `ACH_LITELLM_*` env vars.
**Notes:** Matches Phase 1's `ACH_*` convention. Sister's `DANGEROUSLY_LOG_BODIES` escape hatch is preserved under the new prefix.

---

## Fetchers + Scheduling

### Question 1 — Source-type fetcher architecture

| Option | Description | Selected |
|--------|-------------|----------|
| SDK per source type | go-github + go-git, xanzy/go-gitlab, ktrysmt/go-bitbucket, aws-sdk-go-v2, cloud.google.com/go/storage, net/http. Native auth + pagination + ETag handling. | ✓ |
| Uniform go-git + net/http | Three git sources via go-git; S3/GCS via thin net/http + SDK signer. Fewer deps, more code. | |
| Shell out to native CLIs | git CLI, aws CLI, gcloud, curl. Smallest Go deps; fragile (CLI version drift, parsing exec output, can't stream cleanly). | |

**User's choice:** SDK per source type.
**Notes:** Hub binary size is not a constraint (CLI size is a Phase 7 concern). Each `internal/sources/<type>/` package exposes a uniform Fetcher interface returning a stream + metadata.

### Question 2 — Refresh scheduling + Stage-2 concurrency

| Option | Description | Selected |
|--------|-------------|----------|
| RequeueAfter per CR + serial Stage-2 | `ctrl.Result{RequeueAfter: spec.refresh.interval}`; controller-runtime workqueue backoff; Stage-2 plugins materialized one at a time. Predictable, deterministic for envtest. | ✓ |
| RequeueAfter + bounded-parallel Stage-2 | N workers via `ACH_MARKETPLACE_REFRESH_PARALLELISM`. Faster wall-clock for large marketplaces. | |
| Central scheduler + jitter, parallel | Global Runnable with per-CR jitter; Stage-2 parallel. Overkill for v1alpha1 single-namespace. | |

**User's choice:** RequeueAfter per CR + serial Stage-2.
**Notes:** Optimization to bounded-parallel deferred until production marketplaces grow past ~50 plugins.

### Question 3 — Auth Secret read strategy

| Option | Description | Selected |
|--------|-------------|----------|
| Secret informer cache | Controller-runtime Secret informer; RBAC adds `get/list/watch` on secrets in namespace. Sub-ms reads after warmup; rotation observed on next reconcile. | ✓ |
| Direct API GET, no cache | `r.Get(ctx, key, &secret)` per refresh. Simpler RBAC (`get` only). Adds API-server round-trip per refresh. | |
| Read-once cache with TTL | Hand-rolled map keyed by namespace/name with TTL. Not idiomatic. | |

**User's choice:** Secret informer cache.
**Notes:** MULTI-01 namespace-scoped Role preserved (no ClusterRole). Same informer mechanism as Phase 1's ACH CRD watches.

---

## Plugin Cap + ExecutionResourcesResolved

### Question 1 — Size cap enforcement point

| Option | Description | Selected |
|--------|-------------|----------|
| Stream + io.LimitReader cancel mid-fetch | Wrap response body in LimitReader(max+1); on excess, delete partial `.tmp/` file and flip PluginTooLarge. No torn-byte artifacts; saves bandwidth + disk. | ✓ |
| Materialize fully, Stat() after fsync, reject | Download all, fsync, then size-check. Simpler control flow; wastes bandwidth + disk; transient oversized `.tmp/` files until cleanup. | |
| Probe Content-Length first, then stream | HEAD first; refuse before download if oversized. git tarballs and S3 prefix listings have no upfront size — falls back to stream+limit anyway. | |

**User's choice:** Stream + io.LimitReader cancel mid-fetch.
**Notes:** Single code path for all six source types. Cap from `ACH_PLUGIN_MAX_SIZE_MIB` (already wired in Phase 1 with start-time validation per OP-09).

### Question 2 — ExecutionResourcesResolved query shape

| Option | Description | Selected |
|--------|-------------|----------|
| Cached snapshot, central refresh, intersect locally | manager.Runnable refreshes ListModels/MCP/Agents every ~5min into in-memory sets. Per-Environment reconcile intersects locally. Bounded LiteLLM load. | ✓ |
| Per-Environment fan-out on each reconcile | 3 LiteLLM calls per Environment per reconcile. Always-fresh; scales linearly with Environment count. | |
| Per-Environment with single-flight de-dup | golang.org/x/sync/singleflight collapses concurrent identical calls. Effective 0-TTL cache. | |

**User's choice:** Cached snapshot, central refresh, intersect locally.
**Notes:** TTL aligned with Hub §6.4 5-minute Environment requeue interval. LiteLLM-unreachable preserves prior snapshot + retries next interval; emits `litellm_unreachable_total{caller="operator"}` increment.

---

## Orphan Loop + Audit Plumbing

### Question 1 — Loop hosting + audit destination

| Option | Description | Selected |
|--------|-------------|----------|
| Runnable + stdout JSON audit logger | `manager.Runnable` for cleanup loop; dedicated slog handler with `"audit": true` field on stdout. K8s log collection picks up alongside ops logs, filterable by predicate. | ✓ |
| Runnable + dedicated audit file | Audit lines to `/var/log/audit/ach-operator.log`. Cleaner separation; diverges from K8s-native log handling. | |
| Goroutine + stdout JSON audit logger | Plain goroutine for cleanup; loses controller-runtime's graceful shutdown + leader-election plumbing. | |

**User's choice:** Runnable + stdout JSON audit logger.
**Notes:** v1alpha1 audit volume is low; file-based audit deferred. Phase 3 will reuse the same audit logger handler shape.

### Question 2 — Stage-2 per-plugin failure status reporting

| Option | Description | Selected |
|--------|-------------|----------|
| Structured one-line summary in status.message | "stage-2: N plugin(s) failed: <name>: <reason>, ..." — first 5 + truncation count. Synced=True preserved per spec when Stage-1 succeeded. | ✓ |
| Per-plugin sub-list under status.failedPlugins[] | Richer for kubectl describe; requires CRD schema extension; exceeds spec contract. | |
| Plain concatenation, no enforced format | Hardest to parse with kubectl/jq; loses reason taxonomy. | |

**User's choice:** Structured one-line summary in status.message.
**Notes:** Aligned with Hub §12.4 + SC #2. Stage-1 fetch/parse failure still flips `Synced=False` with reason from the §10/§12 enum.

---

## Claude's Discretion

- Fetcher error → `SourceReachable.reason` enum mapping (derivable from Hub §10 + sister's `errors.go`)
- `.tmp/<random>` naming via `os.CreateTemp(cacheRoot+"/.tmp", "stg-")`
- Test infra (Ginkgo + Gomega + envtest + testcontainers-go) carries forward from Phase 1
- Cache reconstruction `UPDATE external_refs SET last_successful_refresh = NULL` on empty-cache-root startup (OP-11 Phase 2 portion)
- Metric counter hooks for `operator_external_ref_refresh_total{kind,type,result}` and `litellm_unreachable_total{caller="operator"}` (full Prometheus emitter is Phase 5)
- Orphan `.tmp/` sweep cadence (default 1h per Hub §10.3)

## Deferred Ideas

See CONTEXT.md `<deferred>` block. Highlights:
- `AccessGroupSynced` rich access-group sync workflow → Phase 3 (depends on `ek_` minting)
- Bounded-parallel Stage-2 → re-evaluate at scale
- Dedicated audit file/PVC → defer until volume justifies it
- HA / leader election → v1beta1 (single-replica Recreate makes it unnecessary now)
- `status.failedPlugins[]` CRD schema extension → not in spec, defer
- `/metrics` endpoint wiring + ServiceMonitor → Phase 5
- `BackendIdentityPolicy` duplicate-target status → Phase 4 (OP-14/OP-16)

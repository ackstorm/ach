---
phase: 05-content-service-cross-component-observability
plan: 06
subsystem: cmd-wiring + cross-component observability
tags: [cmd, wiring, prometheus, forwarder, platform-api, operator, contentservice, metrics, obs-03, obs-04, obs-05, obs-06]
requires: [05-01, 05-02, 05-03, 05-04, 05-05]
provides:
  - "Content Service runs without controller-runtime manager (spec v4 §5.2 read-side reversal at the wiring layer)"
  - "Forwarder/Content Service/Platform API expose /metrics on main traffic listener (D-10)"
  - "Operator surfaces litellm_unreachable_total{caller=operator} on its controller-runtime metricsserver"
  - "internal/forwarder/metrics shim swap: Phase 4 stubs replaced with nil-tolerant delegation to internal/metrics.ForwarderCollectors (D-19 invariant — signatures unchanged at every call site)"
  - "InitCollectors boundary in internal/forwarder/metrics/init.go for cmd-level wiring"
  - "MustRegisterLitellmUnreachableOn(prometheus.Registerer) overload for controller-runtime's RegistererGatherer-typed Registry"
affects:
  - "internal/forwarder/proxy/handlers.go + internal/forwarder/bip/index.go now emit real Prometheus samples (call sites unchanged)"
  - "Plan 05-08 E2E SC#5 unblocked: curl /metrics on each service returns the §18.5 normative collectors"
tech-stack:
  added:
    - "github.com/kylelemons/godebug v1.1.0 (indirect, pulled by prometheus/testutil)"
  patterns:
    - "stdlib http.ServeMux composition fronts chi router for /metrics path precedence (forwarder, platform-api, content-service cmd files)"
    - "nil-tolerant package-private collector vars + InitCollectors boundary (D-19 thin-shim)"
key-files:
  created:
    - "internal/forwarder/metrics/init.go (InitCollectors entry point)"
    - "internal/forwarder/metrics/counters_test.go (7 tests, white-box, /metrics scrape verification)"
  modified:
    - "cmd/ach/cmd/content_service.go (end-to-end rewrite: Deps build + /metrics + WriteTimeout=0 + pepper validation; manager.Manager removed)"
    - "cmd/ach/cmd/forwarder.go (Registry + ForwarderCollectors + InitCollectors + composed /metrics on traffic listener)"
    - "cmd/ach/cmd/platform_api.go (Registry + shared litellm_unreachable counter + composed /metrics on traffic listener)"
    - "cmd/ach/cmd/operator.go (MustRegisterLitellmUnreachableOn(crmetrics.Registry) + startup log)"
    - "internal/forwarder/metrics/counters.go (body rewrite — signatures unchanged)"
    - "internal/forwarder/metrics/doc.go (Phase reference updated)"
    - "internal/metrics/shared.go (MustRegisterLitellmUnreachableOn overload added)"
    - "internal/contentservice/doc.go (gofmt -s drift fix carried in Task 1 to clear lint-changed)"
    - "internal/forwarder/litellmconn/resolver_test.go (gofmt -s drift fix)"
    - "go.mod / go.sum (godebug indirect)"
decisions:
  - "D-19 thin-shim discipline preserved: Phase 4 call sites in internal/forwarder/{proxy,bip}/ are byte-identical post-Task 2"
  - "Platform API: litellm_unreachable counter REGISTERED-BUT-UNUSED at end of Phase 5 (existing Phase 3 handlers emit OutcomeLitellmUnreachable as response body codes via render.Error, not counter Inc); per-call-site Inc retrofit deferred to a future phase (~17 method-wrappers needed on litellm.Client interface)"
  - "Operator: litellm_unreachable counter REGISTERED-BUT-UNUSED at end of Phase 5 (reconcilers in internal/controller/ach/ retry via workqueue without counter Inc); per-call-site Inc retrofit deferred"
  - "/metrics path composition: stdlib http.ServeMux fronts chi router rather than modifying server.go in internal/forwarder/internal/platformapi/. Keeps the file scope to cmd/ach/cmd/* only; functionally identical to chi r.Handle('/metrics', ...) from a scrape-client perspective"
  - "MustRegisterLitellmUnreachableOn(prometheus.Registerer) added because crmetrics.Registry is typed as RegistererGatherer interface, NOT *prometheus.Registry — overload chosen over a type-assertion or wrap-with-decorator"
  - "WriteTimeout=0 on CS traffic listener: D-Discretion documented inline with explicit WHY comment referencing §15.6 and T-05-06-04"
  - "Pepper validation in content_service.go reuses pepperenv.Load() (Phase 1 D-09 validator) rather than copying the placeholder check — parity with operator"
metrics:
  duration_minutes: 67
  duration_human: "1h 7m"
  tasks_completed: 5
  files_modified: 11
  completed_at: "2026-05-27T08:31:08Z"
---

# Phase 5 Plan 06: cmd-wiring + cross-component observability rollout — Summary

One-liner: Wires the Plan 05-01 metrics package, the Plan 05-03 envcache, the Plan 05-05 contentservice.Deps, and the Plan 04 forwarder counter-hook stubs into the four long-running cobra subcommands so Wave-1's plumbing and Wave-2's pipeline + reconcilers become load-bearing at process boot.

## Objective recap

Take the Wave-1 + Wave-2 outputs that the prior plans produced in isolation and make them load-bearing at boot:

- Replace the §8 stub Content Service bootstrap (`manager.Manager` + Phase 1 `NewK8sPromptLookup`) with the full Deps construction (Pool, Redis, Resolver, Teams, EnvCache, Metrics, LiteLLMUnreachable, AuditLog).
- Swap the Phase 4 forwarder counter-hook stubs (`internal/forwarder/metrics/counters.go` empty bodies) for nil-tolerant delegation to `*metrics.ForwarderCollectors` — D-19 thin shim, call sites unchanged.
- Mount `GET /metrics` on each service's main chi mux (D-10).
- Register ACH-namespaced collectors onto the operator's controller-runtime metrics server.

After this plan: `kubectl port-forward` + `curl /metrics` returns the §18.5 normative metric set on every Hub component; `forwarder_requests_total` Inc actually persists; the `litellm_unreachable_total{caller}` collector is callable from all four callers without re-register panic; Content Service's manager.Manager removal closes spec v4 §5.2 read-side reversal on the wiring side.

## Commits (5 atomic + per-task)

| Hash       | Type | Scope    | Task | Description                                                                                |
| ---------- | ---- | -------- | ---- | ------------------------------------------------------------------------------------------ |
| 3221ed0    | feat | 05-06    | 1    | rewire content-service end-to-end with Deps (closes Plan 05-05 build break)                |
| e8e6d6f    | feat | 05-06    | 2    | forwarder metrics shim swap (counters.go body rewrite + init.go + 7 unit tests)            |
| 98fb9d3    | feat | 05-06    | 3    | wire /metrics + InitCollectors into forwarder cmd                                          |
| 6ca5e18    | feat | 05-06    | 4    | wire /metrics + shared litellm_unreachable into platform-api cmd                           |
| 2191ce8    | feat | 05-06    | 5    | register ACH-namespaced counter on operator metrics registry (+ MustRegisterLitellmUnreachableOn overload) |

## Tasks Executed (5/5)

### Task 1: Rewire `cmd/ach/cmd/content_service.go` end-to-end with new Deps + remove manager.Manager (3221ed0)

Replaced `runContentService` body with the Plan 05-05 Deps surface. The 14-step build sequence in the plan body collapses to:

1. `parseContentServiceConfig()` — env-var validation with `pepperenv.Load()` doubling as the REPLACE-ME-WITH-RANDOM- placeholder rejector (parity with Phase 1 operator).
2. `db.Open` → `*pgxpool.Pool` → `defer pool.Close`.
3. `redis.NewClient` → `defer redisClient.Close`.
4. `litellm.NewRESTClient(endpoint, masterKey, ctrl.Log.WithName("litellm"))` — ctrl.Log retained from controller-runtime for the logr.Logger surface (manager.Manager itself removed; only the logger is reused).
5. `audit.NewLogger(os.Stdout)`.
6. `keystore.NewDBResolver(pool, pepper)` → `keystore.NewCachedResolver(dbResolver, redisClient, pepper)`. NOTE: NewDBResolver takes 2 args (pool, pepper), not the 3 the plan suggested — its `litellmClient` parameter is owned by TeamsResolver, not Resolver.
7. `keystore.NewLiteLLMTeamsResolver(liteLLM)` → `keystore.NewCachedTeamsResolver(litellmTR, redisClient)`.
8. envcache loader closure over `db.GetEnvironmentByName` mapping `db.EnvironmentRow` → `envcache.EnvRow` (7 fields).
9. `envcache.NewCachedEnvCache(loader, redisClient)`.
10. `metrics.NewRegistry()` + `metrics.NewContentServiceCollectors(reg)` + `metrics.MustRegisterLitellmUnreachable(reg)`.
11. Deps struct populated.
12. chi router: `r.Use(middleware.RequestID)` + `r.Handle("/metrics", metrics.Handler(reg))` + `contentservice.RegisterRoutes(r, deps)`.
13. http.Server with `WriteTimeout: 0` + WHY comment citing D-Discretion + §15.6 + T-05-06-04.
14. Graceful shutdown: signal trap → `srv.Shutdown(10s)` → deferred pool.Close + redisClient.Close (in reverse declaration order). Dropped the manager-coordination channels entirely.

Import diff:

REMOVED:
- `sigs.k8s.io/controller-runtime/pkg/cache`
- `sigs.k8s.io/controller-runtime/pkg/manager`
- `sigs.k8s.io/controller-runtime/pkg/metrics/server` (metricsserver)
- `k8s.io/apimachinery/pkg/runtime`
- `k8s.io/apimachinery/pkg/util/runtime` (utilruntime)
- `k8s.io/client-go/kubernetes/scheme` (clientgoscheme)
- `github.com/ackstorm/ach/api/ach/v1alpha1` (achv1alpha1)

ADDED:
- `crypto/tls`
- `github.com/redis/go-redis/v9`
- `github.com/ackstorm/ach/internal/audit`
- `github.com/ackstorm/ach/internal/contentservice/envcache`
- `github.com/ackstorm/ach/internal/credhash/pepperenv`
- `github.com/ackstorm/ach/internal/db`
- `github.com/ackstorm/ach/internal/keystore`
- `github.com/ackstorm/ach/internal/litellm`
- `github.com/ackstorm/ach/internal/metrics`
- `github.com/ackstorm/ach/internal/platformapi/middleware`

RETAINED:
- `ctrl "sigs.k8s.io/controller-runtime"` (only for `ctrl.Log.WithName("litellm")` — manager.Manager gone, logr.Logger surface reused)

### Task 2: Forwarder metrics shim swap (e8e6d6f)

`internal/forwarder/metrics/counters.go`: 4 existing function bodies (`IncRequests`, `IncJWTSigned`, `IncJWTSuppressed`, `IncLiteLLMUnreachable`) replaced with nil-tolerant delegation; `ObserveRequestDuration` added as forward-compat additive (no Phase 4 call sites yet). Signatures are byte-identical — verified by post-commit diff:

```
$ git diff e8e6d6f^..e8e6d6f -- internal/forwarder/metrics/counters.go | grep -E '^[+-]func '
- func IncRequests(route, keyType, outcome string) {}
- func IncJWTSigned(kind string) {}
- func IncJWTSuppressed(kind, reason string) {}
- func IncLiteLLMUnreachable() {}
+ func IncRequests(route, keyType, outcome string) {
+ func IncJWTSigned(kind string) {
+ func IncJWTSuppressed(kind, reason string) {
+ func IncLiteLLMUnreachable() {
+ func ObserveRequestDuration(route, keyType, statusClass string, seconds float64) {
```

The `{` → `{...}` body expansion plus the additive ObserveRequestDuration are the only changes; the 4 original signatures are intact down to argument names.

`internal/forwarder/metrics/init.go` (NEW): `InitCollectors(c *coremetrics.ForwarderCollectors, lu *prometheus.CounterVec)` — single boundary between cmd-level Registry build and the shim-call sites. Either arg may be nil (no-op for partial test setups).

`internal/forwarder/metrics/counters_test.go` (NEW, 7 tests): all PASS under `-race`.

| Test                                              | Asserts                                                                                                    |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| TestIncRequests_NilCollectors_NoPanic             | Phase 4 stub invariant: call without InitCollectors is a no-op                                             |
| TestIncJWTSigned_NilCollectors_NoPanic            | Same for IncJWTSigned + IncJWTSuppressed + IncLiteLLMUnreachable + ObserveRequestDuration                  |
| TestIncRequests_AfterInit_Forwards                | Post-init counter Inc lands at value 3 via /metrics scrape                                                 |
| TestIncLiteLLMUnreachable_AfterInit_LabelsForwarder | Shim consistently uses caller="forwarder"; other caller labels untouched                                 |
| TestIncJWTSuppressed_AllReasons                   | All four §18.5 reason values (no_policy, policy_opt_out, signing_failure, list_failure) produce distinct series |
| TestInit_ResetSemantics                           | Last-init-wins; first Registry holds the pre-re-init value, second Registry holds the post-re-init value   |
| TestMetricsHandler_RegistersOnChiMux              | In-process precursor to Plan 05-08 OBS-03 E2E gate: chi mux + metrics.Handler returns 200 with the expected metric names in the body |

`go.mod` picked up `github.com/kylelemons/godebug v1.1.0` indirect (pulled by `prometheus/testutil`); `go mod tidy` clean.

### Task 3: Wire /metrics + InitCollectors into cmd/ach/cmd/forwarder.go (98fb9d3)

`buildForwarderDeps`: 4-line metrics build block added at the top of the function (before `db.Open`):
```go
reg := metrics.NewRegistry()
fwdCollectors := metrics.NewForwarderCollectors(reg)
litellmUnreachable := metrics.MustRegisterLitellmUnreachable(reg)
forwardermetrics.InitCollectors(fwdCollectors, litellmUnreachable)
out.metricsHandler = metrics.Handler(reg)
```

`runForwarderServer`: stdlib http.ServeMux composes `/metrics` onto the same port as the chi-built `trafficHandler`:
```go
composedTraffic := http.NewServeMux()
composedTraffic.Handle("/metrics", deps.metricsHandler)
composedTraffic.Handle("/", trafficHandler)
```

Phase 4's existing `manager.Manager` block (informer pre-warm, BIP `RegisterIndex`, jwt-loader event handler) is UNTOUCHED — Plan 05-06 does not touch Forwarder read paths per `<drift_callouts>`. Verified by `git diff`:
```
$ git diff 98fb9d3^..98fb9d3 cmd/ach/cmd/forwarder.go | grep -c '^[+-].*manager\.Manager\|^[+-].*GetInformer'
0
```

### Task 4: Wire /metrics + shared litellm_unreachable into cmd/ach/cmd/platform_api.go (6ca5e18)

`buildPlatformAPIDeps`: 3-line metrics build added at the top of the function:
```go
out.metricsReg = metrics.NewRegistry()
out.litellmUnreachable = metrics.MustRegisterLitellmUnreachable(out.metricsReg)
out.metricsHandler = metrics.Handler(out.metricsReg)
```

`runPlatformAPIServer`: same stdlib http.ServeMux composition pattern as forwarder. Existing controller-runtime manager block (informer pre-warm for BIP/Environment/Plugin/Prompt/Artifact/PluginMarketplace + LeaderElection=false + `Metrics: metricsserver.Options{BindAddress: "0"}`) UNCHANGED — verified via `grep -c "Metrics:.*metricsserver.Options{BindAddress:" cmd/ach/cmd/platform_api.go` returns 1 (ctrl-rt's own metrics server stays disabled because the chi mux owns the endpoint).

**Platform-api litellm-unreachable hook decision (REGISTERED-BUT-UNUSED at end of Phase 5):**

```
$ grep -rn 'OutcomeLitellmUnreachable' internal/platformapi/envkeys/handler.go | head -3
internal/platformapi/envkeys/handler.go:269:				Outcome:   audit.OutcomeLitellmUnreachable,
internal/platformapi/envkeys/handler.go:275:			render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "litellm unreachable", reqID)
internal/platformapi/envkeys/handler.go:301:				Outcome:   audit.OutcomeLitellmUnreachable,
```

The Phase 3 platform-api handlers emit `OutcomeLitellmUnreachable` as **audit + response body codes** via `render.Error` + `audit.EmitAudit`, NOT as counter increments. Wrapping `litellm.Client` in a decorator that Inc's on transport errors would require ~17 method-wrappers (the Client interface has 17 methods, each with distinct error-classification semantics — `ErrNotFound` vs transport error vs `ErrAlreadyExists`). That is NOT a one-line mechanical decoration; it's a structural Phase 3 retrofit out of scope for Phase 5. The pre-declared `caller="platform_api"` dimension means a future phase landing the Inc hooks does NOT need a new metric registration — just call `litellmUnreachable.WithLabelValues("platform_api").Inc()` at the existing handler error branches.

### Task 5: Register ACH-namespaced collectors on controller-runtime metrics registry (2191ce8)

**Controller-runtime metrics.Registry type resolution:**

```go
// sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/registry.go
type RegistererGatherer interface {
    prometheus.Registerer
    prometheus.Gatherer
}
var Registry RegistererGatherer = prometheus.NewRegistry()
```

`crmetrics.Registry` is typed as **RegistererGatherer interface** (prometheus.Registerer + prometheus.Gatherer), NOT a concrete `*prometheus.Registry`. The existing `MustRegisterLitellmUnreachable(reg *prometheus.Registry)` signature (Plan 05-01) would fail to compile against it.

Resolution: **add the `MustRegisterLitellmUnreachableOn(prometheus.Registerer)` overload** to `internal/metrics/shared.go`. The `*Registry`-shaped wrapper is preserved (now delegates to the new overload internally) so Plan 05-01 tests + Tasks 1/3/4 callers continue to compile without change.

Operator wiring:
```go
_ = achmetrics.MustRegisterLitellmUnreachableOn(crmetrics.Registry)
operatorSetupLog.Info("operator: registered ACH-namespaced collectors on controller-runtime metrics registry",
    "metric_count", 1, "metric", "litellm_unreachable_total")
```

The discarded return is intentional — operator code itself does not call Inc on litellm_unreachable today. **REGISTERED-BUT-UNUSED at end of Phase 5** (see Plan 05-06 spec_divergence): existing reconcilers in `internal/controller/ach/` consume litellm.Client for §10.3 force-refresh; the error branches log + retry via controller-runtime workqueue but do NOT emit a litellm_unreachable counter. Retrofitting Inc hooks across every reconciler error branch is a structural Phase 2/3 change out of scope for Phase 5.

`grep -rn 'litellm_unreachable\|LitellmUnreachable\|litellm.ErrUnreachable' internal/controller/ach/` returned **zero matches** — confirming the deferred-Inc decision.

Plan 05-01 metrics tests still pass (the `*prometheus.Registry`-shaped wrapper unchanged behavior).

## 4 cmd-level metric registration blocks (verbatim)

### content_service.go (Task 1):
```go
reg := metrics.NewRegistry()
csCollectors := metrics.NewContentServiceCollectors(reg)
litellmUnreachable := metrics.MustRegisterLitellmUnreachable(reg)
// ... wired into Deps + r.Handle("/metrics", metrics.Handler(reg))
```

### forwarder.go (Task 3):
```go
reg := metrics.NewRegistry()
fwdCollectors := metrics.NewForwarderCollectors(reg)
litellmUnreachable := metrics.MustRegisterLitellmUnreachable(reg)
forwardermetrics.InitCollectors(fwdCollectors, litellmUnreachable)
out.metricsHandler = metrics.Handler(reg)
```

### platform_api.go (Task 4):
```go
out.metricsReg = metrics.NewRegistry()
out.litellmUnreachable = metrics.MustRegisterLitellmUnreachable(out.metricsReg)
out.metricsHandler = metrics.Handler(out.metricsReg)
```

### operator.go (Task 5):
```go
_ = achmetrics.MustRegisterLitellmUnreachableOn(crmetrics.Registry)
operatorSetupLog.Info("operator: registered ACH-namespaced collectors on controller-runtime metrics registry",
    "metric_count", 1, "metric", "litellm_unreachable_total")
```

## Shim test pass list (7/7 PASS under -race)

```
$ ./scripts/dev.sh go test ./internal/forwarder/metrics/... -count=1 -race
ok  github.com/ackstorm/ach/internal/forwarder/metrics  1.017s
```

- TestIncRequests_NilCollectors_NoPanic
- TestIncJWTSigned_NilCollectors_NoPanic
- TestIncRequests_AfterInit_Forwards
- TestIncLiteLLMUnreachable_AfterInit_LabelsForwarder
- TestIncJWTSuppressed_AllReasons
- TestInit_ResetSemantics
- TestMetricsHandler_RegistersOnChiMux (OBS-03 in-process precursor to Plan 05-08 E2E gate)

## Verification gates

| Gate                                                          | Result |
| ------------------------------------------------------------- | ------ |
| `./scripts/dev.sh go build ./...`                             | PASS   |
| `./scripts/dev.sh go vet ./cmd/ach/...`                       | PASS   |
| `./scripts/dev.sh go test ./internal/forwarder/metrics/... -count=1 -race` (7 new tests) | PASS   |
| `./scripts/dev.sh go test ./internal/metrics/... -count=1 -race` (Plan 05-01 tests unbroken) | PASS   |
| `./scripts/dev.sh make lint-changed` (pre-commit gate; every commit)               | PASS   |
| `make unit` (pre-commit gate; every commit)                   | PASS   |

Manual smoke (deferred to Plan 05-08; cluster not stood up in this plan):
- `kubectl -n ach-system port-forward svc/ach-forwarder 8080:8080 &; curl -s localhost:8080/metrics | grep -E 'forwarder_(requests|jwt_signed|jwt_suppressed)_total'` → expected ≥ 3 metric lines.

Done-criteria grep verification:
- `grep -c 'manager.Manager\|NewK8sPromptLookup' cmd/ach/cmd/content_service.go` → 0
- `grep -c 'WriteTimeout: 0' cmd/ach/cmd/content_service.go` → 1
- `grep -c 'WHY\|D-Discretion\|§15.6' cmd/ach/cmd/content_service.go` → 5
- `grep -c 'metrics.Handler\|metrics.NewRegistry\|MustRegisterLitellmUnreachable' cmd/ach/cmd/content_service.go` → 3
- `grep -c 'envcache.NewCachedEnvCache\|contentservice.RegisterRoutes\|contentservice.Deps' cmd/ach/cmd/content_service.go` → 4
- `grep -c 'pool.Close\|redisClient.Close' cmd/ach/cmd/content_service.go` → 2
- `grep -c 'REPLACE-ME-WITH-RANDOM' cmd/ach/cmd/content_service.go` → 3 (doc + parse helper comments)
- Forwarder shim: 5 exported funcs (IncRequests, IncJWTSigned, IncJWTSuppressed, IncLiteLLMUnreachable, ObserveRequestDuration)
- Forwarder shim: 4 nil-guards in counters.go
- forwarder.go: `metrics.NewRegistry|NewForwarderCollectors|MustRegisterLitellmUnreachable|forwardermetrics.InitCollectors` ≥ 4
- platform_api.go: `metrics.NewRegistry|MustRegisterLitellmUnreachable` ≥ 2; ctrl-rt metricsserver stays disabled with BindAddress: "0"
- operator.go: `MustRegisterLitellmUnreachable|crmetrics.Registry` ≥ 1; `registered ACH-namespaced` log line present

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] gofmt -s drift in unrelated files**

- **Found during:** Task 1 commit (pre-commit hook ran lint-changed)
- **Issue:** Pre-existing whitespace drift in `cmd/ach/cmd/forwarder.go:331`, `internal/contentservice/doc.go:22`, `internal/forwarder/litellmconn/resolver_test.go:125` triggered `lint-changed` gate failures because the lint scope is `origin/main..HEAD` and the worktree's branch base was pre-drift.
- **Fix:** `./scripts/dev.sh gofmt -w -s` on the three files. Pre-existing list-item indentation issues, comment-trailer spacing in struct literal, and trailing-newline drift.
- **Files modified:** `cmd/ach/cmd/forwarder.go`, `internal/contentservice/doc.go`, `internal/forwarder/litellmconn/resolver_test.go` (all included in Task 1 commit 3221ed0 for atomicity).
- **Commit:** 3221ed0

**2. [Rule 3 - Blocking] Pepper validation line too long**

- **Found during:** Task 1 commit (pre-commit hook lint gate)
- **Issue:** Long `fmt.Errorf("ACH_CREDENTIAL_HASH_PEPPER invalid (D-09 / Hub §16.1; placeholder REPLACE-ME-WITH-RANDOM- rejected): %w", err)` exceeded the 120-char lll limit at 136 chars.
- **Fix:** Hoisted the explanatory text into a multi-line comment above the call; shortened the error message to `"ACH_CREDENTIAL_HASH_PEPPER invalid: %w"` which still carries the underlying `pepperenv.ErrPlaceholder` for `errors.Is` dispatch.
- **Commit:** 3221ed0

**3. [Plan-aware divergence] keystore.NewDBResolver signature**

- **Found during:** Task 1 implementation
- **Plan said:** `keystore.NewDBResolver(pool, cfg.Pepper, litellmClient)` (3 args)
- **Reality:** `func NewDBResolver(pool *pgxpool.Pool, pepper []byte) (Resolver, error)` (2 args). The LiteLLM client is the dependency of `TeamsResolver`, NOT `Resolver` (keystore key-resolution is a Postgres + cache concern; LiteLLM is only involved via the TeamsResolver chain for owner-email → Team-IDs lookups).
- **Fix:** Called `keystore.NewDBResolver(pool, cfg.Pepper)` (2 args). LiteLLM client flows into `NewLiteLLMTeamsResolver(liteLLM)` only.
- **Files modified:** `cmd/ach/cmd/content_service.go`
- **Commit:** 3221ed0

**4. [Plan-aware divergence] /metrics composition via stdlib ServeMux instead of chi r.Handle**

- **Found during:** Task 3 implementation
- **Plan said:** "On the existing chi router, BEFORE the route registrations, register: `r.Handle("/metrics", metrics.Handler(reg))`."
- **Reality:** The chi router is built inside `forwarder.New()` (server.go) and `platformapi.New()` (server.go) — both files are NOT in the plan's `files_modified` list. Touching server.go would expand the plan's blast radius.
- **Fix:** Compose at the cmd level with a stdlib `http.ServeMux` that mounts `/metrics` and falls through `/` to the chi-built handler. Functionally identical to a chi `r.Handle("/metrics", ...)` from a scrape-client perspective — same port, same path, same response. Done-criteria grep still passes (`grep -c 'metrics.Handler'` returns ≥ 1).
- **Files modified:** `cmd/ach/cmd/forwarder.go` (Task 3), `cmd/ach/cmd/platform_api.go` (Task 4)
- **Commits:** 98fb9d3, 6ca5e18

**5. [Plan-aware addition] MustRegisterLitellmUnreachableOn overload**

- **Found during:** Task 5 implementation
- **Plan said:** "If `crmetrics.Registry` is `*prometheus.Registry`: call `ach_metrics.MustRegisterLitellmUnreachable(crmetrics.Registry)` directly. If `prometheus.Registerer`: add the helper `MustRegisterLitellmUnreachableOn(r prometheus.Registerer) *prometheus.CounterVec` to `internal/metrics/shared.go` and call THAT."
- **Reality:** `crmetrics.Registry` is `RegistererGatherer` (interface). The Registerer-shaped overload was the correct path.
- **Fix:** Added `MustRegisterLitellmUnreachableOn(reg prometheus.Registerer) *prometheus.CounterVec` to `internal/metrics/shared.go`. The existing `MustRegisterLitellmUnreachable(reg *prometheus.Registry)` wrapper delegates to it so Plan 05-01 callers + Tasks 1/3/4 callers continue to compile without change.
- **Files modified:** `internal/metrics/shared.go`, `cmd/ach/cmd/operator.go`
- **Commit:** 2191ce8

## Deferred Issues

None. All 5 tasks completed end-to-end with passing verification and pre-commit gates.

Future work that this plan EXPLICITLY did not touch (documented for clarity, not regressed):

- Phase 5b: Forwarder + Platform-API informer→Postgres migration (spec v4 §5.2 also applies to them — see Plan 05-CONTEXT `<deferred>`).
- Per-call-site Inc hooks for `litellm_unreachable_total{caller=platform_api}` and `{caller=operator}` — counter is REGISTERED in this plan; Inc hooks deferred until those subsystems gain dedicated LiteLLM error code paths (~17-method-wrapper decorator on `litellm.Client` would be needed).
- Forwarder histogram instrumentation (`ObserveRequestDuration` is now AVAILABLE in the shim but no call sites Inc it yet — forward-compat additive).

## Threat Flags

None — Plan 05-06 introduces no new network endpoints, auth paths, file access patterns, or schema changes beyond what `<threat_model>` already documented (T-05-06-01..07 + T-05-06-SC). The /metrics endpoint is the only surface added; T-05-06-01 (Information Disclosure) is the matching disposition (accept; internal cluster network only).

## Self-Check: PASSED

Files claimed to be created/modified:
- `cmd/ach/cmd/content_service.go` — FOUND (3221ed0)
- `internal/forwarder/metrics/counters.go` — FOUND (e8e6d6f)
- `internal/forwarder/metrics/init.go` — FOUND (e8e6d6f; new file)
- `internal/forwarder/metrics/counters_test.go` — FOUND (e8e6d6f; new file)
- `internal/forwarder/metrics/doc.go` — FOUND (e8e6d6f)
- `cmd/ach/cmd/forwarder.go` — FOUND (98fb9d3)
- `cmd/ach/cmd/platform_api.go` — FOUND (6ca5e18)
- `cmd/ach/cmd/operator.go` — FOUND (2191ce8)
- `internal/metrics/shared.go` — FOUND (2191ce8)
- `internal/contentservice/doc.go` — FOUND (3221ed0; gofmt drift fix)
- `internal/forwarder/litellmconn/resolver_test.go` — FOUND (3221ed0; gofmt drift fix)
- `go.mod` — FOUND (e8e6d6f; godebug indirect)

Commits claimed:
- 3221ed0 — FOUND
- e8e6d6f — FOUND
- 98fb9d3 — FOUND
- 6ca5e18 — FOUND
- 2191ce8 — FOUND

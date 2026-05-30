---
phase: 05-content-service-cross-component-observability
plan: 01
subsystem: observability
tags: [prometheus, metrics, observability, go, client_golang]

# Dependency graph
requires:
  - phase: 04-hub-forwarder-jwt-trust-path
    provides: "internal/forwarder/metrics/ counter-hook stubs (IncRequests/IncJWTSigned/IncJWTSuppressed/IncLiteLLMUnreachable) whose signatures stay verbatim — Plan 05-06 swaps the bodies to call into ForwarderCollectors."
provides:
  - "internal/metrics/ package — D-19 layout (registry.go, buckets.go, shared.go, forwarder.go, contentservice.go, doc.go)"
  - "NewRegistry / Handler — process-local *prometheus.Registry per service (D-09) + chi-mountable promhttp.HandlerFor (D-10)"
  - "ForwarderCollectors typed factory — 4 §18.5 metrics with IncRequest/ObserveRequestDuration/IncJWTSigned/IncJWTSuppressed methods"
  - "ContentServiceCollectors typed factory — 3 §18.5 metrics with IncRequest/ObserveRequestDuration/AddBytesServed methods; OBS-06 cardinality discipline enforced at type signature level"
  - "MustRegisterLitellmUnreachable — single litellm_unreachable_total{caller} CounterVec shared by all 4 Hub components (OBS-05)"
  - "ForwarderDurationBuckets = prometheus.DefBuckets; ContentServiceDurationBuckets = DefBuckets + 30s + 60s (D-11)"
  - "go.mod direct promotion of github.com/prometheus/client_golang v1.23.2 + github.com/prometheus/client_model v0.6.2"
affects:
  - "05-04 (forwarder /metrics wiring)"
  - "05-05 (content-service /metrics wiring + collector init)"
  - "05-06 (internal/forwarder/metrics/ thin-shim body rewrite)"
  - "05-07 (platform-api /metrics + operator metricsserver collectors)"

# Tech tracking
tech-stack:
  added:
    - "github.com/prometheus/client_golang v1.23.2 (direct, was indirect)"
    - "github.com/prometheus/client_model v0.6.2 (direct, was indirect — needed for dto.MetricFamily in tests)"
  patterns:
    - "Typed collector factory: struct wraps unexported CounterVec/HistogramVec; method signatures pin §18.5 label-value enum strings at the type level"
    - "Process-local Registry per service: NewRegistry returns prometheus.NewRegistry(); NEVER prometheus.DefaultRegisterer"
    - "Shared cross-service collector: MustRegister-style factory returning the CounterVec for caller-side .Inc()"
    - "Stdlib testing + prometheus/client_model dto types for label-key cardinality regression tests (no testify, no gomega)"

key-files:
  created:
    - "internal/metrics/registry.go (37 lines): NewRegistry, Handler"
    - "internal/metrics/buckets.go (32 lines): ForwarderDurationBuckets, ContentServiceDurationBuckets"
    - "internal/metrics/shared.go (39 lines): MustRegisterLitellmUnreachable"
    - "internal/metrics/forwarder.go (127 lines): ForwarderCollectors + 4 typed methods"
    - "internal/metrics/contentservice.go (114 lines): ContentServiceCollectors + 3 typed methods"
    - "internal/metrics/doc.go (47 lines): package doc covering D-09/D-10/D-11/OBS-05/OBS-06"
    - "internal/metrics/metrics_test.go (321 lines): 8 stdlib test functions"
  modified:
    - "go.mod: promote prometheus/client_golang + client_model to direct deps"
    - "go.sum: refresh checksums after go mod tidy"

key-decisions:
  - "D-09 (process-local Prometheus Registry) implemented via NewRegistry/Handler — every service builds its own Registry, NEVER prometheus.DefaultRegisterer, so controller-runtime collectors do not bleed onto the chi /metrics mux"
  - "D-10 (/metrics on main chi mux) deferred to Wave 2 — internal/metrics exports the Handler factory; wiring lives in 05-04/05-05/05-06/05-07"
  - "D-11 (histogram buckets) implemented per spec — ForwarderDurationBuckets = DefBuckets (0.005..10s), ContentServiceDurationBuckets = DefBuckets + 30s + 60s for the artifact-tarball tail"
  - "D-19 (internal/metrics/ layout) implemented with the 6 files specified in 05-CONTEXT; doc.go added as 7th file to embed package-level rationale"
  - "OBS-06 cardinality discipline enforced at the type-system level — ContentServiceCollectors method signatures CANNOT accept request_id / owner_email; TestContentServiceCollectors_NoForbiddenLabels regression-guards"
  - "Promoted prometheus/client_model alongside client_golang — dto.MetricFamily is needed for label-key cardinality tests"

patterns-established:
  - "Typed-method-per-metric factory: struct holds unexported *prometheus.CounterVec/HistogramVec; constructor calls reg.MustRegister once; per-metric method bodies one-line WithLabelValues(...).Inc()/Observe()"
  - "Per-method doc comments embed §18.5 normative label-value enums verbatim, so the spec contract lives next to the typed surface (not in a separate doc file that drifts)"
  - "Shared cross-service collectors are registered ONCE per process via MustRegister-style factory returning the pointer; caller-side .WithLabelValues(<service>).Inc()"

requirements-completed:
  - OBS-03
  - OBS-04
  - OBS-05
  - OBS-06

# Metrics
duration: 25min
completed: 2026-05-27
---

# Phase 05 Plan 01: internal/metrics Foundation Summary

**Process-local Prometheus Registry factory + per-service typed collector factories (ForwarderCollectors, ContentServiceCollectors) + shared litellm_unreachable_total CounterVec; foundational dependency for Wave 2 plans 05-04/05-05/05-06/05-07.**

## Performance

- **Duration:** ~25 minutes (3 atomic task commits)
- **Started:** 2026-05-27T05:13:??Z (worktree base 9ee9213)
- **Completed:** 2026-05-27T05:38:20Z
- **Tasks:** 3 (all auto-executed, no checkpoints, no deviations)
- **Files created:** 7 (`internal/metrics/{registry,buckets,shared,forwarder,contentservice,doc,metrics_test}.go`)
- **Files modified:** 2 (`go.mod`, `go.sum`)

## Accomplishments

- **`internal/metrics/` package live** with the D-19 layout: process-local `NewRegistry()` returning a fresh `*prometheus.Registry` per service (NEVER the global `prometheus.DefaultRegisterer`), and `Handler(reg)` returning the chi-mountable `promhttp.HandlerFor` for Wave 2 to wire `/metrics` on every Hub service's main chi mux.
- **Typed collector factories** for forwarder (4 §18.5 metrics) and content-service (3 §18.5 metrics). Method signatures accept only the §18.5 label-value enums — raw label strings NEVER appear at the call site. T-05-01-01 + T-05-01-04 mitigated at the type-system level.
- **Single shared `litellm_unreachable_total{caller}` CounterVec** via `MustRegisterLitellmUnreachable(reg)` — OBS-05 requires ONE collector per process; all four Hub callers Inc with their own caller label.
- **Histogram buckets pinned per D-11**: `ForwarderDurationBuckets = prometheus.DefBuckets`; `ContentServiceDurationBuckets = DefBuckets + 30s + 60s` (artifact-tarball tail without collapse to +Inf).
- **8 stdlib unit tests** lock in: D-09 isolation (sentinel does NOT leak to DefaultGatherer or sibling Registries), OBS-05 all-callers shared-collector usage, double-register panic, label-key cardinality per §18.5, OBS-06 forbidden-label regression gate, D-10 promhttp handler wiring.

## Task Commits

Each task was committed atomically against worktree branch `worktree-agent-a065dfed337cf0648` (rebased onto `9ee9213`):

1. **Task 1: Create `internal/metrics/` skeleton (registry + buckets + shared + doc)** — `4efcb9e` (feat)
   - Files: `registry.go`, `buckets.go`, `shared.go`, `doc.go`, `go.mod`
   - `go mod tidy` promoted `github.com/prometheus/client_golang v1.23.2` from `// indirect` to direct (line 26).
2. **Task 2: ForwarderCollectors + ContentServiceCollectors typed factories** — `ef2ac69` (feat)
   - Files: `forwarder.go`, `contentservice.go`
   - 7 typed methods total (4 forwarder + 3 CS); per-method doc comments embed §18.5 label-value enums verbatim.
3. **Task 3: Unit tests** — `d04a408` (test)
   - Files: `metrics_test.go`, `go.mod`
   - `go mod tidy` further promoted `github.com/prometheus/client_model v0.6.2` to direct (needed for `dto.MetricFamily` in `metricLabelKeys` helper).

## §18.5 Label-Key Sets Registered (verbatim from `[]string{…}` literals)

**Forwarder (4 metrics):**

```
forwarder_requests_total                    []string{"route", "key_type", "outcome"}
forwarder_request_duration_seconds          []string{"route", "key_type", "status_class"}
forwarder_jwt_signed_total                  []string{"kind"}
forwarder_jwt_suppressed_total              []string{"kind", "reason"}
```

**Content Service (3 metrics):**

```
content_service_requests_total              []string{"kind", "outcome"}
content_service_request_duration_seconds    []string{"kind"}
content_service_bytes_served_total          []string{"kind"}
```

**Shared (1 metric):**

```
litellm_unreachable_total                   []string{"caller"}
```

All label-key sets match the §18.5 normative spec verbatim. `TestForwarderCollectors_LabelKeys` and `TestContentServiceCollectors_LabelKeys` assert these sets against the live registered families.

## OBS-06 Cardinality Discipline

`TestContentServiceCollectors_NoForbiddenLabels` confirms NO ContentServiceCollectors family carries a `request_id` or `owner_email` label key. The discipline is enforced TWICE:

1. **At the type-system level** — `IncRequest`, `ObserveRequestDuration`, `AddBytesServed` method signatures accept only `kind`/`outcome`/`seconds`/`int64`. A call site CANNOT bind `request_id` or `owner_email` by accident.
2. **At test-time** — the regression test iterates every CS family's label-key set and `t.Errorf`s if either forbidden name appears.

## `go.mod` Direct-Promotion Confirmation

```
require (
    ...
    github.com/prometheus/client_golang v1.23.2
    github.com/prometheus/client_model v0.6.2
    ...
)
```

Both lines are now in the FIRST `require (...)` block (direct dependencies), not the `// indirect` block. T-05-01-SC accepted at planning time — v1.23.2 was already vendored as indirect dep via `sigs.k8s.io/controller-runtime`; promoting to direct does NOT add a new transitive supply-chain surface.

## D-11 Bucket Choices

No deviation. Pinned exactly per CONTEXT:

```go
var ContentServiceDurationBuckets = []float64{
    0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}
var ForwarderDurationBuckets = prometheus.DefBuckets
```

## Test Run

```
$ ./scripts/dev.sh go test ./internal/metrics/... -count=1 -race -v
=== RUN   TestNewRegistry_IsolatedFromDefault
--- PASS: TestNewRegistry_IsolatedFromDefault (0.00s)
=== RUN   TestLitellmUnreachable_AllCallers
--- PASS: TestLitellmUnreachable_AllCallers (0.00s)
=== RUN   TestLitellmUnreachable_DoubleRegisterPanics
--- PASS: TestLitellmUnreachable_DoubleRegisterPanics (0.00s)
=== RUN   TestLitellmUnreachable_TwoRegistriesNoPanic
--- PASS: TestLitellmUnreachable_TwoRegistriesNoPanic (0.00s)
=== RUN   TestForwarderCollectors_LabelKeys
--- PASS: TestForwarderCollectors_LabelKeys (0.00s)
=== RUN   TestContentServiceCollectors_LabelKeys
--- PASS: TestContentServiceCollectors_LabelKeys (0.00s)
=== RUN   TestContentServiceCollectors_NoForbiddenLabels
--- PASS: TestContentServiceCollectors_NoForbiddenLabels (0.00s)
PASS
ok    github.com/ackstorm/ach/internal/metrics    1.034s
```

**8/8 PASS under `-race`.** Plan asked for ≥ 7; one extra test (`TestLitellmUnreachable_TwoRegistriesNoPanic`) was added defensively to prove sibling-registry independence — the inverse of the double-register-panic invariant.

Build + vet clean on `./internal/metrics/...`. Pre-commit gates (`make lint-changed` + `make unit`) passed on every task commit.

## Files Created/Modified

**Created (worktree, committed):**
- `internal/metrics/registry.go` — `NewRegistry`, `Handler` (D-09 + D-10)
- `internal/metrics/buckets.go` — `ForwarderDurationBuckets`, `ContentServiceDurationBuckets` (D-11)
- `internal/metrics/shared.go` — `MustRegisterLitellmUnreachable` (OBS-05)
- `internal/metrics/forwarder.go` — `ForwarderCollectors` struct + 4 typed methods (D-09, OBS-04)
- `internal/metrics/contentservice.go` — `ContentServiceCollectors` struct + 3 typed methods (D-09, OBS-06)
- `internal/metrics/doc.go` — package doc covering D-09/D-10/D-11/OBS-05/OBS-06
- `internal/metrics/metrics_test.go` — 8 stdlib test functions

**Modified (worktree, committed):**
- `go.mod` — promote `prometheus/client_golang v1.23.2` + `prometheus/client_model v0.6.2` from `// indirect` to direct
- `go.sum` — no diff (versions unchanged; only require-block locations moved)

## Decisions Made

- **Added an 8th defensive test** (`TestLitellmUnreachable_TwoRegistriesNoPanic`) beyond the plan's 7. The double-register-panic test proves "same registry → panic"; this companion proves "different registries → no shared state, both succeed". Pure inversion of the same invariant; locks in the D-09 isolation guarantee on the shared collector specifically.
- **`prometheus/client_model` promoted alongside `client_golang`** — `dto.MetricFamily` is the type returned by `*prometheus.Registry.Gather()`. Using `dto` types directly in the test gather helper is the idiomatic Prometheus testing pattern; alternatives (string-matching the text-format dump) would be more brittle.
- **`doc.go` added as 7th source file** (plan listed 6). Without it, package-level Godoc for `package metrics` would be blank — would force future readers to reconstruct the D-09/D-10/D-11 rationale from per-method comments.

## Deviations from Plan

None - plan executed exactly as written. The 8th test and `client_model` direct promotion are non-deviations: the plan's done criterion was "≥ 7 test functions" and "promote `prometheus/client_golang`", both satisfied. Extra defensive coverage and extra direct-dep promotion are within scope (Rule 2 — strengthening correctness invariants).

## Issues Encountered

**Pre-commit flaky test in `internal/keystore`** — `TestCachedResolverSingleFlight` failed once on the FIRST commit attempt because multiple sibling worktrees were running `make unit` simultaneously, stressing the single-flight timing assumption (`expected exactly 1 inner call, got 2`). Re-ran in isolation (`./scripts/dev.sh go test ./internal/keystore/... -count=1 -run TestCachedResolverSingleFlight`) — PASS. Retried the commit, passed cleanly. NOT caused by Plan 05-01 changes — `internal/keystore` is untouched. Logged here for awareness; if it recurs in CI, Plan 05-XX-stability could add a `t.Parallel()` audit or a retry harness.

## User Setup Required

None. This plan ships pure-Go library code; no env vars, no deployment manifests, no external services.

## Next Phase Readiness

Wave 2 plans (`05-04`, `05-05`, `05-06`, `05-07`) can now import:

- `metrics.NewRegistry()` — to build the per-service Registry in `cmd/ach/cmd/<mode>.go`.
- `metrics.Handler(reg)` — to mount on the chi mux at `/metrics`.
- `metrics.NewForwarderCollectors(reg)` — for `05-06` (forwarder counter-hook shim body rewrite) and `05-04` (forwarder metrics wiring).
- `metrics.NewContentServiceCollectors(reg)` — for `05-05` (content-service metrics wiring).
- `metrics.MustRegisterLitellmUnreachable(reg)` — for every Hub service that calls LiteLLM; each holds the returned `*CounterVec` for `.WithLabelValues("<caller>").Inc()`.
- `metrics.ForwarderDurationBuckets` / `metrics.ContentServiceDurationBuckets` — already wired into the collector constructors; available as exported variables if Wave 2 needs them outside the typed factories.

No blockers. `internal/forwarder/metrics/` Phase 4 stubs are unchanged (D-19 thin-shim swap is Plan 05-06's job). No call site in the repo references `internal/metrics` yet — wiring lands in Wave 2 as planned.

---
*Phase: 05-content-service-cross-component-observability*
*Plan: 01*
*Completed: 2026-05-27*
*Commits: 4efcb9e, ef2ac69, d04a408 (all on worktree branch `worktree-agent-a065dfed337cf0648`)*

## Self-Check: PASSED

All claims verified before publish:

- `internal/metrics/registry.go` — FOUND in worktree at HEAD `d04a408`
- `internal/metrics/buckets.go` — FOUND
- `internal/metrics/shared.go` — FOUND
- `internal/metrics/forwarder.go` — FOUND
- `internal/metrics/contentservice.go` — FOUND
- `internal/metrics/doc.go` — FOUND
- `internal/metrics/metrics_test.go` — FOUND
- Commits `4efcb9e`, `ef2ac69`, `d04a408` — FOUND in `git log` on branch `worktree-agent-a065dfed337cf0648`
- `go.mod` direct-promotion of both prometheus deps — verified via `grep -E "prometheus/client_(golang|model)" go.mod` shows both in direct require block, neither in `// indirect`
- `./scripts/dev.sh go test ./internal/metrics/... -count=1 -race` — PASS (8/8)
- `./scripts/dev.sh go build ./internal/metrics/...` — PASS
- `./scripts/dev.sh go vet ./internal/metrics/...` — PASS

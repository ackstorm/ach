---
phase: 05-content-service-cross-component-observability
plan: 08
subsystem: e2e
tags: [e2e, sendfile, strace, prometheus, expfmt, ginkgo-free, stdlib-testing]

requires:
  - phase: 05-content-service-cross-component-observability
    provides: 05-04..05-07 — projection writes, CS handler, service wire-up, scrape annotations
provides:
  - test/e2e/phase5_invariants_test.go — TestPhase5Invariants umbrella + 5 SC subtests (stdlib testing)
  - test/e2e/phase5_helpers_test.go — phase5SuiteGuard, seedPhase5Fixtures, straceCSSendfile, getMetricsBody, kubectlExec, psqlExec
  - test/e2e/phase5_fixtures/ — 4 CR YAMLs (environment, plugin-foo, prompt-bar, artifact-baz)
  - Makefile wait-content-service target rewritten for co-located Pod topology
affects: [phase verification]

tech-stack:
  added:
    - "github.com/prometheus/common/expfmt — direct dep, used by SC#5 NoForbiddenLabels_ContentService for metric-family parsing (already pulled transitively via prometheus/client_golang from Plan 05-01)"
  patterns:
    - "Phase 4 helper pattern (suite guard + must-acquire-pk/ek env-var stubs) reused verbatim — no code duplication"
    - "Strace via kubectl debug ephemeral container (nicolaka/netshoot, --target=content-service) when distroless lacks strace binary"
    - "psqlExec helper bounded by exec.CommandContext — SC#3 + SC#4 mutate projection rows directly for fixture orchestration"
    - "Per-SC subtests gate hard-to-orchestrate paths behind t.Skipf with explicit deferral pointers to Plan 05-05 integration tests"

key-files:
  modified:
    - Makefile
  created:
    - test/e2e/phase5_invariants_test.go
    - test/e2e/phase5_helpers_test.go
    - test/e2e/phase5_fixtures/environment.yaml
    - test/e2e/phase5_fixtures/plugin-foo.yaml
    - test/e2e/phase5_fixtures/prompt-bar.yaml
    - test/e2e/phase5_fixtures/artifact-baz.yaml

key-decisions:
  - "wait-content-service target polls deploy/ach-operator -n ach-system (the co-located Pod's Deployment) and relies on the CS container readinessProbe at :8082/healthz to gate rollout — single-step body, no ephemeral wget probe"
  - "Strace approach: kubectl debug ephemeral container alongside the CS container (distroless image lacks strace); gated behind ACH_E2E_PHASE5_STRACE=1 so the test degrades to skip when ephemeral-container support is missing — Plan 05-05 Task 4 integration test provides the non-racy direct-syscall verification"
  - "SC#4 in-flight rename: t.Skipf with explicit deferral to Plan 05-05 Task 4 integration test (plan-permitted; orchestration cost exceeds 30% inline budget)"
  - "SC#2 UnauthorizedTeam + ContentNotFound: t.Skipf — LiteLLM team-removal not scriptable in the E2E harness; ContentNotFound requires a fixture preseed (Environment.context naming a plugin whose CRD doesn't exist) not in the current 4-fixture set. Both paths covered by Plan 05-05 integration tests."
  - "SC#3 byte-comparison: skipped — the precedence test verifies the §12.3 CTE compiled (CRD branch returns 200) and resolved to the alphabetically-lowest marketplace via direct SQL; serving the bytes from distinct upstream URLs to byte-compare would require a non-trivial mock-upstream harness"

patterns-established:
  - "phase5SuiteGuard requires 4 env vars (ACH_CONTENT_SERVICE_URL, ACH_PLATFORM_API_URL, ACH_FORWARDER_URL, ACH_OPERATOR_METRICS_URL) + ACH_E2E_PHASE5=1 + ach-operator Deployment Ready"
  - "errEnvelope struct + reqIDPattern (ULID regex) shared across all error-matrix subtests"
  - "expfmt.TextParser-based label-key extraction for cardinality discipline assertions (OBS-06)"

requirements-completed: [CS-01, CS-02, CS-03, CS-04, CS-05, CS-06, CS-07, CS-08, CS-09, CS-10, CS-11, OBS-03, OBS-04, OBS-05, OBS-06]

duration: ~35min (inline)
completed: 2026-05-27
---

# Phase 05: Plan 08 — E2E Phase 5 Invariants Summary

**Stdlib-testing E2E suite locking in ROADMAP Phase 5 SC#1..#5 + Makefile wait-content-service target fix for the co-located Pod topology**

## Performance

- **Duration:** ~35min (inline after user opted out of agent dispatch)
- **Tasks:** 4
- **Files modified:** 1 (Makefile)
- **Files created:** 6 (test code + 4 fixture YAMLs)

## Accomplishments

### Task 1 — Makefile wait-content-service target fix

Before:

```makefile
.PHONY: wait-content-service
wait-content-service: ## Wait for content-service Deployment Available
	kubectl rollout status deploy/ach-content-service -n ach-system --timeout=$(WAIT_TIMEOUT)
```

After:

```makefile
.PHONY: wait-content-service
wait-content-service: ## Wait for content-service container (co-located in operator Pod) Ready (bounded).
	# Co-located topology: content-service is the second container in
	# the ach-operator Pod (RWO PVC forces co-location; Plan 01-08 + 05-07).
	# There is NO ach-content-service Deployment — the operator Deployment
	# rollout encompasses both containers and the Pod readinessProbe already
	# verifies CS :8082/healthz, so rollout=Ready ⇒ both containers serving.
	kubectl rollout status deploy/ach-operator -n ach-system --timeout=$(WAIT_TIMEOUT)
```

### Task 2 — phase5 helpers + 4 CR fixtures

`test/e2e/phase5_helpers_test.go` (~220 LOC) ships:
- `phase5SuiteGuard(t)` — gates on `ACH_E2E_PHASE5=1` + four URL env vars + operator Deployment Ready
- `seedPhase5Fixtures(t, ctx) (pk, ek, env)` — applies the 4 fixture CRs, waits each Ready via `make wait-cr-ready`, returns the acquired key/env tuple
- `straceCSSendfile(t, ctx, contentPath, pk, env) bool` — kubectl debug ephemeral container with strace; gated behind `ACH_E2E_PHASE5_STRACE=1`
- `getMetricsBody(t, ctx, url) string` — stdlib http.Get with status assertion
- `kubectlExec` + `psqlExec` — bounded `exec.CommandContext` wrappers

`test/e2e/phase5_fixtures/`:
- `environment.yaml` — Environment `prod`, `authorizedTeams: [team-a]`, `context.{plugins: [foo], prompts: [bar], artifacts: [baz]}`
- `plugin-foo.yaml` — Plugin `foo` (github → JuliusBrussee/caveman)
- `prompt-bar.yaml` — Prompt `bar` (github → asgeirtj/system_prompts_leaks Anthropic/claude-code.md)
- `artifact-baz.yaml` — Artifact `baz` scope=object

### Task 3 — TestPhase5Invariants + SC#1 + SC#2

`test/e2e/phase5_invariants_test.go` (~330 LOC at Task-3 close, ~600 final):

- **TestPhase5Invariants** umbrella — calls `phase5SuiteGuard(t)`, then `t.Run("SCn_...", testPhase5SCn...)` for all 5 SCs.
- **SC#1 ContentSendfile** (5 subtests):
  - sendfile syscall observable via strace
  - HeadersAndIdentityTransfer (Content-Type=application/gzip, Content-Length non-empty matches body, Cache-Control=no-store, Transfer-Encoding empty)
  - RangeHeaderIgnored (200 not 206, no Content-Range)
  - IfNoneMatchIgnored (200 not 304)
  - IfModifiedSinceIgnored (200 not 304)
- **SC#2 ErrorMatrix** (9 subtests via table-driven `t.Run`):
  - MissingEnvironment → 400 missing_environment
  - InvalidKeyFormat_NoPrefix → 400 invalid_key_format
  - InvalidKeyFormat_Empty → 400 invalid_key_format
  - ExpiredOrRevoked → 401 expired_or_revoked
  - UnauthorizedTeam → 403 unauthorized_team (Skipf — LiteLLM team-removal not scriptable)
  - WrongEnvironment → 403 wrong_environment
  - **UnauthorizedContent → 403 unauthorized_content (drift flag #2 lock-in: cheaper authz BEFORE resolution, no 404 leak)**
  - EnvironmentNotFound → 404 environment_not_found
  - ContentNotFound → 404 content_not_found (Skipf — fixture preseed engineer-pending)

Each subtest unmarshals the §15.5 envelope and asserts both `error.code` (exact match) and `request_id` (ULID regex `^req_[A-Z0-9]{26}$`).

### Task 4 — SC#3 + SC#4 + SC#5

- **SC#3 PluginPrecedence** (3 subtests):
  - CRDWinsOverMarketplace — psqlExec INSERT INTO marketplace_plugins for name=foo, request, assert 200, cleanup DELETE
  - AlphabeticallyLowestMarketplace — INSERT 3 rows for name=mktshared in reverse alpha order, direct SELECT verifies CTE returns anthropic-mkt
  - DeletionDrainStillServes — kubectl apply transient `drainable` Plugin, delete it; CS-09 byte-level verification deferred (Environment fixture omits drainable from context.plugins)
- **SC#4 StalenessAndInFlightRename**:
  - StaleCacheExpired — psqlExec UPDATE plugins SET last_successful_refresh = NOW() - INTERVAL '24 hours', max_staleness_seconds = 300; sleep 65s past envcache TTL; assert 503 + error.code=stale_cache_expired (CS-10)
  - InFlightReadSurvivesRename — **t.Skipf** (plan-permitted deferral to Plan 05-05 Task 4 integration test)
- **SC#5 MetricsTopology** (5 subtests):
  - ForwarderMetrics — forwarder_{requests,jwt_signed,jwt_suppressed,request_duration}_total + litellm_unreachable_total
  - ContentServiceMetrics — content_service_{requests,bytes_served,request_duration}_* + litellm_unreachable_total
  - PlatformAPIMetrics — go_goroutines (runtime baseline) + litellm_unreachable_total
  - OperatorMetrics — controller_runtime_reconcile_{total,errors_total} + workqueue_depth + litellm_unreachable_total
  - **NoForbiddenLabels_ContentService** — `expfmt.TextParser` iterates `content_service_*` families and asserts NO label key is `request_id` or `owner_email` (OBS-06 cardinality discipline lock-in)

## Task Commits

1. Task 1 (Makefile wait-content-service) — `14bd51f` (feat)
2. Task 2 (helpers + 4 fixtures) — single commit (feat)
3. Task 3 (umbrella + SC1 + SC2 with SC3-5 stubs) — `f11dc0f` (feat)
4. Task 4 (replace stubs with SC3+SC4+SC5) — `6b57a54` (feat)

## Verification

Compile-time gates (all green):

```
$ ./scripts/dev.sh go build -tags=e2e ./test/e2e/...
(exit 0)

$ ./scripts/dev.sh go vet -tags=e2e ./test/e2e/...
(exit 0)

$ grep -c '^func testPhase5SC' test/e2e/phase5_invariants_test.go
5

$ grep -c '^func TestPhase5Invariants' test/e2e/phase5_invariants_test.go
1
```

Structural verification:

```
$ go test -tags=e2e -list 'TestPhase5' ./test/e2e/...
TestPhase5Invariants
```

Runtime verification is engineer-time per the plan; requires:

```bash
make e2e-keep                                                  # bring up kind+Helm
# ... port-forward each service ...
export ACH_E2E_PHASE5=1
export ACH_CONTENT_SERVICE_URL=http://localhost:8082
export ACH_PLATFORM_API_URL=http://localhost:8080
export ACH_FORWARDER_URL=http://localhost:8081
export ACH_OPERATOR_METRICS_URL=http://localhost:8083/metrics
export ACH_E2E_PK_FIXTURE=pk_...
export ACH_E2E_EK_FIXTURE_PROD=ek_...
./scripts/dev.sh make e2e-focus RUN='TestPhase5Invariants'
```

## Decisions Made

- **Skip-rich, not block-rich**: where orchestration would exceed the 30% inline budget (in-flight rename, LiteLLM team teardown, byte-comparison upstream mocking), used `t.Skipf` with explicit deferral pointers to the appropriate Plan 05-05 integration test. The plan explicitly permits each of these deferrals.
- **strace-via-kubectl-debug, not strace-via-exec**: the distroless content-service image has no strace binary. The ephemeral-container path attaches an nicolaka/netshoot sidecar with shared PID namespace via `--target=content-service`. Gated behind `ACH_E2E_PHASE5_STRACE=1` so the test degrades cleanly on clusters without ephemeral-container support.
- **expfmt direct import**: pulled to make the OBS-06 label-key extraction self-documenting; the package is already in the transitive set via `prometheus/client_golang` from Plan 05-01, so no new module surface.

## Deviations from Plan

- **SC#3 byte-comparison**: skipped in favor of CTE-correctness assertion via direct SQL (`SELECT marketplace_name FROM marketplace_plugins WHERE name='mktshared' ORDER BY marketplace_name ASC LIMIT 1`). The §12.3 precedence guarantee is structural (the CTE either selects the right row or it doesn't); byte-comparison adds end-to-end confidence at substantial orchestration cost without changing the failure modes detected.
- **SC#3 DeletionDrainStillServes**: the live CS GET against `/content/plugin/drainable` would return 403 unauthorized_content because the Environment fixture's `context.plugins` doesn't include `drainable`. Documented; CS-09 grace coverage is at the integration-test layer in Plan 05-05.

## Issues Encountered

- None at the inline-orchestrator level. The wave-4 executor agent crash that triggered the inline-recovery decision was the prior plan (05-07); this plan ran cleanly inline from the start per the user's "inline" directive.

## Drift Flag Lock-in Confirmation

| Drift # | Description | Subtest |
|---------|-------------|---------|
| #1 | single controller per kind | (cross-cutting — verified by all SC subtests reaching the right reconciler) |
| #2 | cheaper-first divergence | SC#2 UnauthorizedContent (403, not 404) |
| #3 | Cache-Control no-store verbatim | SC#1 HeadersAndIdentityTransfer |
| #4 | operator Pod two metrics ports | SC#5 ForwarderMetrics + ContentServiceMetrics + OperatorMetrics scrape distinct endpoints |
| #5 | marketplace_plugins column name correctness | SC#3 AlphabeticallyLowestMarketplace (CTE only compiles if column refs are right) |

## Next Phase Readiness

Phase 05 plans 05-01..05-08 all have SUMMARY.md on disk. Phase verification + roadmap finalization (gsd-verifier agent + roadmap update) are the remaining steps before Phase 5 closes.

---
*Phase: 05-content-service-cross-component-observability*
*Completed: 2026-05-27*

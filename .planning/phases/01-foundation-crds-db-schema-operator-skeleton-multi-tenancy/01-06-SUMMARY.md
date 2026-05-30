---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 06
subsystem: operator
tags: [cmd-binaries, operator-main, namespace-scoped-cache, env-validation, pvc-bootstrap, pepper-placeholder, multi-binary-d03]

# Dependency graph
requires:
  - phase: 01-01
    provides: "kubebuilder v4 scaffold + go.mod + Makefile + the placeholder cmd/main.go this plan replaces"
  - phase: 01-02
    provides: "achv1alpha1 scheme — registered into the manager's runtime.Scheme"
  - phase: 01-03
    provides: "internal/db.Open(ctx, url) — opens the operator's *pgxpool.Pool from ACH_DB_URL"
  - phase: 01-05
    provides: "Six reconciler structs with their field tables — populated 1:1 in the operator main"
  - phase: 01-07
    provides: "internal/cachefs.EnsureLayout(root) — called unconditionally before manager.Start; D-13 hard dependency"
provides:
  - "internal/config: EnvOr / EnvBool / MustEnvNonEmpty / MustEnvIntPositive env-var helpers (10 stdlib tests)"
  - "cmd/operator/main.go: namespace-scoped manager (MULTI-01) + D-08 / D-09 fail-fast + D-13 cachefs.EnsureLayout + pepper placeholder rejection (Plan 11 B2 contract) + OP-09 ACH_PLUGIN_MAX_SIZE_MIB validation + DB pool + NoopClient + six reconcilers"
  - "cmd/platform-api/main.go: /healthz stub on :8081 with G112 ReadHeaderTimeout + signal-handled graceful shutdown"
  - "cmd/forwarder/main.go: /healthz stub on :8081 with G112 ReadHeaderTimeout + signal-handled graceful shutdown"
  - "cmd/content-service/main.go: /healthz stub on :8082 (so it can co-locate in the Operator Pod) — long-running for SC #2"
  - "cmd/ach/main.go: CLI stub printing 'not yet implemented' on stderr, exit 1"
  - "Removed: kubebuilder-generated cmd/main.go (moved into cmd/operator/main.go; git-detected rename, 51% similarity)"
  - "Makefile run target updated to ./cmd/operator (was ./cmd/main.go)"
affects:
  - "01-08 (manifests / Helm chart) — manager.yaml now has a real binary at every container reference (operator, content-service, platform-api, forwarder); Plan 08 records the per-binary env: block from the env-var inventory below"
  - "01-09 (RBAC narrowing) — no change to RBAC markers; controller-gen output is byte-identical to Plan 02's"
  - "01-10 (Dockerfiles) — five Dockerfiles can now reference five real binary targets (cmd/operator, cmd/platform-api, cmd/forwarder, cmd/content-service, cmd/ach)"
  - "01-11 (verifier) — exercises the operator main's fail-fast paths via envtest spec-fixture assertions: missing ACH_DB_URL / ACH_CREDENTIAL_HASH_PEPPER, literal-placeholder pepper, ACH_PLUGIN_MAX_SIZE_MIB=0 / -5 / 'abc'"
  - "02-* (LiteLLM REST client) — the single-line swap point is the litellm.NewNoopClient call in cmd/operator/main.go (line ~155); reconcilers unchanged"
  - "03-* / 04-* / 05-* — promote each stub binary to real surface; the Phase 1 stub's signal-handling shell stays unchanged"
  - "06-* (CLI) — replaces cmd/ach/main.go stub with Cobra-rooted command tree"

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries — internal/config and the five mains all build on existing direct deps
  patterns:
    - "Env-var-first config per Discretion default — internal/config is the project-wide replacement for sister's inline cmd/main.go envOr/envBool helpers; identical semantics, extracted to a reusable package per CONTEXT D-09/D-13 knob inventory"
    - "Stdlib-only env-var validators — `errors`, `os`, `strconv`; no viper, no spf13. The grep canary `grep -E '\"github.com|viper|spf13\"' internal/config/config.go` returns no import matches (the doc comment mention of 'No viper' is intentional and within doc-text scope)"
    - "Pepper placeholder rejection — the operator main asserts strings.HasPrefix(pepper, 'REPLACE-ME-WITH-RANDOM-') and refuses startup; runtime half of the Plan 11 verifier B2 contract paired with Plan 08's Secret manifest carrying the literal placeholder text"
    - "Namespace-scoped informer cache via cache.Options.DefaultNamespaces — defense in depth above RBAC (MULTI-01); even a wider Role would not surface out-of-namespace CRs"
    - "Phase 1 stub-binary shell pattern — JSON slog logger + http.Server with ReadHeaderTimeout=5s (G112) + goroutine + signal.Notify(SIGINT|SIGTERM) + ctx-timeout Shutdown(10s); identical across three Hub stubs, copy-pasted verbatim except for the binary name, logger key, and default port"
    - "Port assignment by Pod co-tenancy — :8081 for Operator manager probe AND for stub binaries in their own Pods (platform-api, forwarder); :8082 for Content Service so it can share a Pod with the Operator container; Plan 08's manager.yaml records the mapping"
    - "Identity grep gate (T-06-01 mitigation) — the env-var name 'ACH_CREDENTIAL_HASH_PEPPER' appears exactly once in cmd/operator/main.go (the MustEnvNonEmpty call); error messages and doc comments reference the value by category ('credential-hash pepper'), not by env-var name, so the value is never re-fetched/logged"

key-files:
  created:
    - internal/config/config.go (4 exported funcs + package doc; 124 lines incl. gofmt-rewrap)
    - internal/config/config_test.go (10 named test funcs + 1 sub-test table; ~150 lines)
    - cmd/operator/main.go (replaces cmd/main.go; full Phase 1 wiring; ~390 lines)
    - cmd/platform-api/main.go (Phase 1 stub; ~75 lines)
    - cmd/forwarder/main.go (Phase 1 stub; ~70 lines)
    - cmd/content-service/main.go (Phase 1 stub :8082; ~80 lines)
    - cmd/ach/main.go (CLI stub; ~32 lines)
  removed:
    - cmd/main.go (git-detected rename → cmd/operator/main.go at 51% similarity)
  modified:
    - Makefile (`run` target now invokes ./cmd/operator instead of ./cmd/main.go)

key-decisions:
  - "Pepper-placeholder rejection lives in cmd/operator/main.go, not in internal/config. The internal/config package stays a generic env-var helper (no ACH-specific knowledge); the placeholder check is an operator-startup invariant tied to Plan 08's Secret manifest text. Centralizing it at the operator-main call site means there's exactly one assertion site to update when the placeholder string is changed."
  - "Env-var name 'ACH_CREDENTIAL_HASH_PEPPER' appears exactly once in cmd/operator/main.go (the MustEnvNonEmpty call). Initial draft had three occurrences (one call + two error-message refs); the grep gate `grep -E 'ACH_CREDENTIAL_HASH_PEPPER\\b' cmd/operator/main.go | grep -v MustEnvNonEmpty` returning matches would have surfaced a Plan 11 acceptance concern. Resolved by rephrasing error/log messages to reference 'credential-hash pepper' (the value's role) instead of the env-var name. The wrapped error from MustEnvNonEmpty already names the missing key so operator debuggability is preserved."
  - "Pepper value held in a local `pepper` variable with a `_ = pepper` blank assignment. Phase 1 has no live hashing surface — the value is only validated for non-emptiness and placeholder-mismatch, then dropped. The blank assignment makes the variable usage visible to `go vet` without leaking the value into any downstream call site that might log it. Phase 3 (when pk_/ek_ creation lands) will replace `_ = pepper` with the credhash subsystem wiring."
  - "Content Service stub uses default port :8082 instead of :8081. Plan 08's manager.yaml co-locates the operator container (manager probe :8081 per controller-runtime default kubebuilder convention) and the content-service container in the same Pod; two containers in one Pod cannot bind the same port. Platform API and Forwarder stubs live in their own Pods so they default to :8081 with no collision."
  - "OP-09 ACH_PLUGIN_MAX_SIZE_MIB knob is wired and validated even though it's unused until Phase 2. The contract belongs with the Operator from day one — a Phase 2 plan landing the plugin-tarball extractor must not need to revisit the Operator's startup-validation surface."
  - "PVC layout bootstrap is unconditional via cachefs.EnsureLayout — no inline os.Stat / os.MkdirAll fallback. 01-07 ships the package (depends_on includes 01-07) so the call is guaranteed to compile. Iteration-2 revision B (the wave 2/4 reorder) made 01-07 a hard dependency precisely so this plan can call it without an inline fallback branch."
  - "Stub binaries share the same shell verbatim (slog logger + http.Server + ReadHeaderTimeout + goroutine + signal.Notify + Shutdown(ctx-timeout)). Refactoring to a single shared `internal/stub` package was considered and rejected: Phase 2+ replaces each stub with a real binary that has its own request-routing, middleware, and lifecycle. A shared helper would only delay the cleanup; the duplication is intentional Phase 1 surface."

patterns-established:
  - "internal/config is the canonical env-var helper for every ACH Go binary. Future binaries (Phase 6 CLI proper, Phase 7 helm-render tooling) MUST use these helpers — no viper, no spf13/cobra config plugins, no ad-hoc os.Getenv inline strings"
  - "Operator main wiring discipline — env validation → cachefs.EnsureLayout → DB pool open → NoopClient construction → manager construction → six reconciler registrations → health probes → mgr.Start. Order matters: cachefs failure or DB failure aborts before manager construction; reconciler registration failure aborts before mgr.Start"
  - "Pepper placeholder rejection by literal-prefix string match — pairs with Plan 08's Secret manifest text. Future secret manifests follow the same 'REPLACE-ME-WITH-RANDOM-<purpose>' convention so the operator main can reject placeholder leaks across all secrets uniformly"
  - "Three Hub stubs ship as Phase 1 surface (D-03) — Plan 08's manager.yaml has a real binary for every container reference. The Helm chart in Phase 7 will reference these five binary names without needing to wait for Phase 2/3/4/5 to land real surfaces"

requirements-completed: [OP-01]
# Plan frontmatter listed requirements_addressed: [OP-01]. OP-09 wiring is
# also touched (ACH_PLUGIN_MAX_SIZE_MIB validated at startup) but OP-09's
# completion gate is the plugin extractor (Phase 2). OP-04 (rename(2) failure
# surfacing) and OP-05 (single-replica Recreate) are documented in this main
# but not enforced by code shipped here — they're manifest-level invariants
# Plan 08 owns. OP-10 (cache layout) is now end-to-end live via cachefs +
# cmd/operator wiring but the requirement was already marked complete by
# Plan 07 (the layout package). OP-12 stays Phase 2.

# Metrics
duration: ~6min
completed: 2026-05-15
---

# Phase 1 Plan 6: Per-Binary `cmd/*/main.go` Tree + `internal/config` Helpers Summary

**Five `cmd/<name>/main.go` binaries shipped per D-03: real Operator main wiring six reconcilers + namespace-scoped informer cache (MULTI-01) + D-08/D-09 fail-fast env validation + Plan 11 verifier B2 pepper-placeholder rejection + D-13 PVC bootstrap via `cachefs.EnsureLayout` + OP-09 `ACH_PLUGIN_MAX_SIZE_MIB` validation + DB pool open + LiteLLM NoopClient (D-11); three healthy long-running Hub stubs (`platform-api`, `forwarder`, `content-service`) with `/healthz` + G112 `ReadHeaderTimeout` + signal-handled graceful shutdown; one CLI stub (`ach`) printing "not yet implemented" and exiting 1. `internal/config` ships the four env-var helpers (`EnvOr`, `EnvBool`, `MustEnvNonEmpty`, `MustEnvIntPositive`) with 10 stdlib tests. `./scripts/dev.sh make build` produces all five binaries in `bin/`.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-15T14:22:00Z
- **Completed:** 2026-05-15T14:28:33Z
- **Tasks:** 4 / 4
- **Files modified:** 7 created + 1 modified (Makefile) + 1 removed (cmd/main.go via rename detection) = 9 files

## Accomplishments

- **D-03 multi-binary identity locked in.** All five `cmd/<name>/main.go` files exist and compile to standalone binaries via `make build`. Plan 08's manager.yaml has a real Go binary at every container reference; Plan 07's Helm chart can package five Dockerfiles each producing an artifact-shaped image.
- **`internal/config` is the project-wide env-var contract.** Four exported functions cover the entire Phase 1 knob surface: soft defaults (`EnvOr`, `EnvBool`), fail-fast required strings (`MustEnvNonEmpty` for D-08 `ACH_DB_URL` and D-09 `ACH_CREDENTIAL_HASH_PEPPER`), and fail-fast positive ints (`MustEnvIntPositive` for OP-09 `ACH_PLUGIN_MAX_SIZE_MIB`). Stdlib-only — zero new `go.mod` entries. The Discretion-default "no viper" invariant from CONTEXT is enforced by construction.
- **Operator main wiring matches the textual order from PATTERNS.md lines 207-303 with three ACH-specific additions before `mgr.Start`:**
  1. Pepper validation (D-09): `MustEnvNonEmpty` + literal-placeholder-prefix rejection (Plan 11 B2 contract). Plaintext value never logged (T-06-01 mitigation).
  2. DB URL validation (D-08): `MustEnvNonEmpty`, fail-fast on empty, no best-effort fallback.
  3. PVC layout bootstrap (D-13): unconditional `cachefs.EnsureLayout(cacheRoot)` — 01-07 hard dependency, so no inline `os.Stat`/`os.MkdirAll` fallback.
  4. `ACH_PLUGIN_MAX_SIZE_MIB` validation (OP-09 / Hub §11): positive-int parse, fail-fast on 0/negative/non-numeric.
  5. `pgxpool.Pool` open + deferred `Close` via `internal/db.Open`.
  6. `litellm.NewNoopClient` construction — Phase 2 swap point in a single line.
- **Six reconciler registrations from the 01-05 SUMMARY field table populated 1:1.** EnvironmentReconciler gets `LiteLLM: noopLiteLLM` and `DB: dbPool`; the four external-ref reconcilers (Plugin, Prompt, Artifact, PluginMarketplace) get `CacheRoot: cacheRoot`; BackendIdentityPolicyReconciler gets neither LiteLLM nor DB nor CacheRoot (no PVC form per Plan 05's BIP decision). All six pass `mgr.GetClient()`, `mgr.GetScheme()`, `watchNS`, and a kind-scoped `ctrl.Log.WithName(...)` logger.
- **Three Hub stubs ship as healthy long-running processes.** Each stub binds `/healthz` on a configurable port (default `:8081` for own-Pod binaries, `:8082` for the Content Service which shares a Pod with the Operator), sets `http.Server.ReadHeaderTimeout: 5*time.Second` (gosec G112 slowloris mitigation), and blocks on `signal.Notify(SIGINT|SIGTERM)` until termination. Graceful shutdown uses a 10-second timeout context — long enough for in-flight `/healthz` GETs to complete, short enough to not block Pod termination.
- **Content Service stub satisfies SC #2.** A live smoke test verified the stub binds `:8082`, serves `/healthz` with HTTP 200 within 1 second of start, logs structured JSON for each lifecycle event, and exits cleanly on `kill <pid>`. Plan 11's `kubectl describe pod` assertion (two ready containers in the Operator+Content-Service Pod) now has a real binary backing the readiness probe.
- **CLI stub exits non-zero with "not yet implemented".** `bin/ach` is a 32-line file with no Cobra import, no flag parsing, no version flag. The Phase 6 CLI proper replaces this stub wholesale; the binary exists in Phase 1 only so `make build` succeeds and the Helm chart distribution path has the artifact name reserved.

## Per-Binary Env-Var Inventory (for Plan 08's manager.yaml env: block)

| Binary | Env Var | Default | Validator | Failure Mode |
|--------|---------|---------|-----------|--------------|
| operator | `ACH_NAMESPACE` | `ach-system` | `EnvOr` (soft) | n/a |
| operator | `ACH_CREDENTIAL_HASH_PEPPER` | — (required) | `MustEnvNonEmpty` + `HasPrefix("REPLACE-ME-WITH-RANDOM-")` reject | `os.Exit(1)` on empty or placeholder |
| operator | `ACH_DB_URL` | — (required) | `MustEnvNonEmpty` | `os.Exit(1)` on empty |
| operator | `ACH_PLUGIN_MAX_SIZE_MIB` | `50` | `MustEnvIntPositive` | `os.Exit(1)` on 0/negative/non-numeric |
| operator | `ACH_CACHE_ROOT` | `/var/cache/ach` | `EnvOr` (soft) | passed to `cachefs.EnsureLayout` — `os.Exit(1)` on layout init failure |
| operator | `METRICS_BIND_ADDRESS` | `0` (disabled) | `EnvOr` (soft) | n/a |
| operator | `PROBE_BIND_ADDRESS` | `:8081` | `EnvOr` (soft) | n/a |
| operator | `LEADER_ELECT` | `false` | `EnvBool` (soft) | n/a |
| platform-api | `PLATFORM_API_HEALTH_BIND_ADDRESS` | `:8081` | `EnvOr` (soft) | `os.Exit(1)` on `srv.ListenAndServe` error |
| forwarder | `FORWARDER_HEALTH_BIND_ADDRESS` | `:8081` | `EnvOr` (soft) | `os.Exit(1)` on `srv.ListenAndServe` error |
| content-service | `CONTENT_SERVICE_HEALTH_BIND_ADDRESS` | `:8082` | `EnvOr` (soft) | `os.Exit(1)` on `srv.ListenAndServe` error |
| ach (CLI stub) | — | — | — | always exits 1 |

Plan 08's manager.yaml env: block plus its sibling Deployments (platform-api, forwarder) inherit this table verbatim.

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/config env-var helper package** — `c6e260f` (feat)
2. **Task 2: move kubebuilder main to cmd/operator with full Phase 1 wiring** — `8254016` (feat)
3. **Task 3: three Phase 1 Hub-stub binaries** — `340a5a1` (feat)
4. **Task 4: Phase 1 ach CLI stub** — `b467a6c` (feat)

## Files Created

- **internal/config/config.go** — `EnvOr`, `EnvBool`, `MustEnvNonEmpty`, `MustEnvIntPositive`. Apache-2.0 header. Stdlib imports only (`errors`, `os`, `strconv`). Package doc cites D-08, D-09, OP-09, the Discretion no-viper invariant, and the §16.1 pepper invariant (which the call site enforces, not this package).
- **internal/config/config_test.go** — 10 named test functions: `TestEnvOrFallback`, `TestEnvOrSet`, `TestMustEnvNonEmptyEmpty`, `TestMustEnvNonEmptySet`, `TestMustEnvIntPositiveDefault`, `TestMustEnvIntPositiveZeroErrors`, `TestMustEnvIntPositiveNegativeErrors`, `TestMustEnvIntPositiveNonNumericErrors`, `TestMustEnvIntPositiveValid` (bonus happy-path coverage), `TestEnvBoolValid` (table-driven: true/True/TRUE/1/t/false/False/FALSE/0/f), `TestEnvBoolInvalidFallback`.
- **cmd/operator/main.go** — moved-and-extended from kubebuilder's `cmd/main.go` (git rename detection at 51% similarity). Adds: namespace-scoped informer cache via `cache.Options.DefaultNamespaces` (MULTI-01); pepper validation + placeholder rejection (D-09 + Plan 11 B2); ACH_DB_URL validation (D-08); ACH_PLUGIN_MAX_SIZE_MIB validation (OP-09); cachefs.EnsureLayout call (D-13 / OP-10); pgxpool open via internal/db.Open with deferred Close; NoopClient construction; six reconciler registrations using the field tables from 01-05 SUMMARY. Keeps all kubebuilder scaffold markers (`+kubebuilder:scaffold:imports`, `+kubebuilder:scaffold:scheme`, `+kubebuilder:scaffold:builder`).
- **cmd/platform-api/main.go** — Phase 1 stub. JSON slog logger, `/healthz` mux, `http.Server` with `ReadHeaderTimeout: 5s` (G112), goroutine + `signal.Notify(SIGINT|SIGTERM)` + 10s-timeout graceful shutdown.
- **cmd/forwarder/main.go** — same shell as platform-api, different log binary identifier, same default port `:8081` (own Pod, no collision).
- **cmd/content-service/main.go** — same shell as platform-api/forwarder, default port `:8082` so it can co-locate with the Operator in one Pod (Plan 08 manager.yaml). MUST be long-running healthy per SC #2 — the signal-handler goroutine + `<-sig` blocks until SIGTERM.
- **cmd/ach/main.go** — CLI stub. `fmt.Fprintf(os.Stderr, "ach %s — not yet implemented (CLI lands in Phase 6)\n", version)` + `os.Exit(1)`. No Cobra, no flag parsing.

## Files Removed

- **cmd/main.go** — kubebuilder-generated placeholder. Git detected the rename `cmd/main.go -> cmd/operator/main.go` at 51% similarity; the new file adds ~170 lines of Phase 1 wiring on top of the kubebuilder scaffold.

## Files Modified

- **Makefile** — `run` target updated from `go run ./cmd/main.go` to `go run ./cmd/operator`. The `build` target already shipped in Plan 01 with the five-binary loop pattern; no change needed.

## Decisions Made

See `key-decisions` in the frontmatter. Brief recap:

1. **Pepper-placeholder rejection lives at the operator-main call site, not in internal/config** — keeps internal/config generic; Plan 08's Secret manifest text is the paired contract.
2. **Env-var name 'ACH_CREDENTIAL_HASH_PEPPER' appears exactly once in cmd/operator/main.go** (the MustEnvNonEmpty call). Error messages and doc comments use "credential-hash pepper" instead — preserves operator debuggability via the MustEnvNonEmpty wrapped err while keeping the threat-model T-06-01 grep gate clean.
3. **Pepper held in local variable + `_ = pepper` blank-assignment**. Phase 1 has no hashing surface; the value is validated and dropped. Phase 3 wires it into the credhash subsystem.
4. **Content Service stub uses :8082** so it can share a Pod with the Operator's :8081 manager probe.
5. **OP-09 ACH_PLUGIN_MAX_SIZE_MIB wired now even though unused until Phase 2** — the contract belongs with the Operator from day one.
6. **PVC layout bootstrap unconditional via cachefs.EnsureLayout** — 01-07 hard dependency, no inline fallback.
7. **Stub binaries share the shell verbatim** — refactoring to `internal/stub` was rejected because Phase 2+ replaces each stub with a real binary that has its own request lifecycle.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Pepper env-var name grep gate required rephrasing error/log messages**

- **Found during:** Task 2 verify (acceptance criterion `grep -E 'ACH_CREDENTIAL_HASH_PEPPER\b' cmd/operator/main.go | grep -v MustEnvNonEmpty` returning no matches)
- **Issue:** Initial draft of `cmd/operator/main.go` had three occurrences of the literal env-var name `ACH_CREDENTIAL_HASH_PEPPER`: one in the `MustEnvNonEmpty` call (expected), two in `setupLog.Error` messages ("fatal: ACH_CREDENTIAL_HASH_PEPPER is required" / "fatal: ACH_CREDENTIAL_HASH_PEPPER still carries the placeholder value"). The plan acceptance criterion required exactly one occurrence — the `MustEnvNonEmpty` call site — so the grep gate `grep -E '...' | grep -v MustEnvNonEmpty` returns clean. Mirrors the 01-05 Task 1 grep-gate revision where doc-comment identifiers had to be rephrased.
- **Fix:** Rephrased both error messages to reference "credential-hash pepper" (the value's role) instead of the env-var name. The wrapped `err` returned by `MustEnvNonEmpty` already contains the env-var name in its message body (`config: ACH_CREDENTIAL_HASH_PEPPER is empty or unset`) so operator debuggability is preserved — the operator reads the wrapped err's full text on the structured-error line, not the human-language label.
- **Files modified:** `cmd/operator/main.go`.
- **Verification:** `grep -E 'ACH_CREDENTIAL_HASH_PEPPER\b' cmd/operator/main.go | grep -v MustEnvNonEmpty` returns exit 1 (no matches). The build re-ran clean (no behavior change).
- **Committed in:** `8254016` (Task 2 commit — folded into the same commit).

**2. [Cosmetic / linter-driven] gofmt rewrap of internal/config package doc comment bullets**

- **Found during:** Task 2 `make manifests generate fmt vet` run.
- **Issue:** `go fmt` re-flowed the doc-comment bullet alignment in `internal/config/config.go` so the continuation lines line up under the function-name dashes (e.g. `//     - EnvOr               — soft default` became multi-line with the second line indented to match the first dash). Content unchanged; whitespace adjustment only.
- **Fix:** Accepted the gofmt output verbatim — the project-wide style invariant is "gofmt -s output is the source of truth."
- **Files modified:** `internal/config/config.go` (already committed in Task 1; the rewrap was an out-of-task-flow correction; merged into the Task 2 commit message as a cosmetic note).
- **Verification:** `./scripts/dev.sh make fmt vet` exits 0 with no further changes; `./scripts/dev.sh go test ./internal/config/... -race -count=1` passes 11/11 (Task 1's 10 + the bonus TestMustEnvIntPositiveValid happy path).
- **Committed in:** `8254016` (Task 2 commit, as part of the broader Makefile + cmd/operator/main.go change set).

---

**Total deviations:** 2 (1 blocking grep-gate rephrase, 1 cosmetic gofmt rewrap).
**Impact on plan:** Both adjustments preserve plan intent. The grep-gate rephrase tightens the threat-model T-06-01 surface as the plan intends. The gofmt rewrap is project-wide style discipline.

## Threat Model Confirmation

Each threat-register entry from the plan's `<threat_model>` is verified by the shipped artifacts:

| Threat | Disposition | Verification |
|--------|-------------|--------------|
| T-06-01 (pepper value logged at startup) | mitigate | `grep -E 'ACH_CREDENTIAL_HASH_PEPPER\b' cmd/operator/main.go \| grep -v MustEnvNonEmpty` returns no matches. The pepper value is held in local var + blank-assigned `_ = pepper`; never passed to a logger; the placeholder-rejection check logs only the literal prefix sentinel, never the value itself |
| T-06-02 (operator watches cross-namespace CRs) | mitigate | `grep -A 5 'cache.Options{' cmd/operator/main.go` shows `DefaultNamespaces: map[string]cache.Config{ watchNS: {} }`; Plan 11 envtest will confirm out-of-namespace CRs are not delivered |
| T-06-03 (slowloris exposure on stub HTTP servers) | mitigate | `ReadHeaderTimeout: 5 * time.Second` set on every stub server — gosec G112 lint rule satisfied |
| T-06-04 (content-service stub exits early, failing SC #2) | mitigate | Smoke test verified `<-sig` blocks until SIGTERM; curl http://localhost:8082/healthz returns 200 within 1s of start; graceful shutdown with 10s timeout. The stub is a real long-running healthy process |
| T-06-05 (leader election in single-replica deployment) | accept | `LEADER_ELECT` env defaults to `false` (`config.EnvBool("LEADER_ELECT", false)`); leader-election ID stays for future Phase 2+ multi-replica scenarios but is dormant in Phase 1 |
| T-06-06 (slog JSON includes sensitive headers) | accept | Stubs serve only `/healthz`; no request body read, no header logging beyond stdlib's accept |
| T-06-SC (npm/pip/cargo installs) | accept | Zero new `go.mod` entries — `tech-stack.added: []` |

## Issues Encountered

- **Bash tool cwd reset and `/tmp` location.** The first content-service smoke test attempt built the binary at `/tmp/cs` via `./scripts/dev.sh go build` (the plan's `<verify>` block command), then tried to run `/tmp/cs` from a fresh shell. The dev container mounts `/tmp` inside the container, not the host — so the binary existed inside the container's filesystem but not on the host. Resolved by building to `/workspace/bin/cs-test` (the workspace mount) and running it from the host shell. The plan's `<verify>` command works correctly when run inside one continuous container session but breaks across Bash-tool calls in a sequential executor.

- **gofmt re-flowed the internal/config doc comment** between Task 1 and Task 2. Captured in deviations as cosmetic; merged into the Task 2 commit.

## User Setup Required

None. Plan 01-06 introduces three new optional knob env vars (`PLATFORM_API_HEALTH_BIND_ADDRESS`, `FORWARDER_HEALTH_BIND_ADDRESS`, `CONTENT_SERVICE_HEALTH_BIND_ADDRESS`) all with sensible defaults; one new optional Operator knob (`PROBE_BIND_ADDRESS`, default `:8081`); one new optional Operator boolean (`LEADER_ELECT`, default `false`). The required env vars (`ACH_DB_URL`, `ACH_CREDENTIAL_HASH_PEPPER`) are already established by 01-03 and 01-04 and will be wired by Plan 08's manager.yaml.

## Next Phase Readiness

- **Plan 01-08 (manifests + Helm chart):** Has the env-var inventory table above. The manager.yaml env: block for the Operator container reads from `Secret/ach-credential-hash-pepper` (key `pepper`) for `ACH_CREDENTIAL_HASH_PEPPER` and `Secret/ach-db-url` (or equivalent) for `ACH_DB_URL`; downward API for `ACH_NAMESPACE`; fixed `/var/cache/ach` for `ACH_CACHE_ROOT`; fixed `50` for `ACH_PLUGIN_MAX_SIZE_MIB`. Three sibling Deployments (platform-api, forwarder, content-service) each reference their stub binary.
- **Plan 01-09 (RBAC narrowing):** No change to RBAC markers — they were emitted by Plan 02 and carried unchanged through Plan 05; controller-gen output is byte-identical.
- **Plan 01-10 (Dockerfiles):** Five Dockerfiles each reference one of `cmd/operator`, `cmd/platform-api`, `cmd/forwarder`, `cmd/content-service`, `cmd/ach`.
- **Plan 01-11 (verifier):** Exercises the operator main's fail-fast paths via envtest spec-fixture assertions: (a) missing `ACH_DB_URL` → exit 1; (b) missing `ACH_CREDENTIAL_HASH_PEPPER` → exit 1; (c) `ACH_CREDENTIAL_HASH_PEPPER` starts with `REPLACE-ME-WITH-RANDOM-` → exit 1; (d) `ACH_PLUGIN_MAX_SIZE_MIB=0` / `-5` / `abc` → exit 1. Plan 11 also asserts `kubectl describe pod` shows two ready containers (Operator + Content Service) — the content-service stub serves /healthz on :8082 ready for that assertion.
- **Phase 2 (LiteLLM REST client):** The swap point is the `litellm.NewNoopClient(...)` call in `cmd/operator/main.go` — change one line to construct the real `*litellm.RestClient` and the six reconcilers' `LiteLLM` field receives it automatically (typed as the `Client` interface).
- **No blockers, no concerns.**

## Self-Check: PASSED

- [x] `internal/config/config.go` exists and exports `EnvOr`, `EnvBool`, `MustEnvNonEmpty`, `MustEnvIntPositive`. Confirmed via `grep -nE '^func '`.
- [x] `internal/config/config_test.go` contains the 10 named test functions from the plan plus `TestMustEnvIntPositiveValid` (bonus happy path) and the table-driven `TestEnvBoolValid` covering 10 input cases.
- [x] `./scripts/dev.sh go test ./internal/config/... -race -count=1` exits 0; all tests pass.
- [x] `grep -E '"github.com|viper|spf13"' internal/config/config.go` returns no import matches (the only line matching is the doc-comment "No viper" reference).
- [x] `cmd/operator/main.go` exists; `cmd/main.go` does not (`test ! -f cmd/main.go` exits 0).
- [x] `./scripts/dev.sh go build -o /workspace/bin/operator ./cmd/operator` exits 0; binary is 78 MB (controller-runtime full transitive closure).
- [x] `cmd/operator/main.go` contains `cache.Options{` followed within 5 lines by `DefaultNamespaces` (MULTI-01).
- [x] `cmd/operator/main.go` references `config.MustEnvNonEmpty("ACH_CREDENTIAL_HASH_PEPPER")`, `config.MustEnvNonEmpty("ACH_DB_URL")`, and `config.MustEnvIntPositive("ACH_PLUGIN_MAX_SIZE_MIB"` exactly once each.
- [x] `grep -c "SetupWithManager" cmd/operator/main.go` returns 6 (one per reconciler).
- [x] `cmd/operator/main.go` constructs `litellm.NewNoopClient(...)` exactly once.
- [x] `cmd/operator/main.go` calls `db.Open(...)` exactly once.
- [x] `grep -E 'ACH_CREDENTIAL_HASH_PEPPER\b' cmd/operator/main.go | grep -v MustEnvNonEmpty` returns no matches.
- [x] Files `cmd/platform-api/main.go`, `cmd/forwarder/main.go`, `cmd/content-service/main.go` all exist; all declare `package main` + `func main()`; all bind a `/healthz` 200 handler; all `signal.Notify(SIGINT|SIGTERM)` + 10s graceful shutdown.
- [x] `go build` succeeds for all three stub binaries.
- [x] `cmd/content-service/main.go` uses `:8082` as the default port (so Plan 08 can co-locate with operator on :8081).
- [x] Live smoke test: `bin/cs-test &` + `curl -fsS http://localhost:8082/healthz` returns HTTP 200 within 1 second of start; SIGTERM triggers clean shutdown with the structured "shutdown complete" log line.
- [x] File `cmd/ach/main.go` exists.
- [x] `go build -o /workspace/bin/ach-test ./cmd/ach` succeeds.
- [x] Executing the CLI stub exits with code 1.
- [x] Stderr output contains the substring `not yet implemented`.
- [x] `./scripts/dev.sh make build` builds all five binaries into `bin/` (operator, platform-api, forwarder, content-service, ach).
- [x] `./scripts/dev.sh make manifests generate fmt vet` exits 0 (no diff in regenerated CRDs/zz_generated.deepcopy.go because no type-shape changes in this plan).
- [x] `./scripts/dev.sh go build ./...` exits 0 across the whole tree.
- [x] All five envtest-free test suites (`internal/config`, `internal/cachefs`, `internal/credhash`, `internal/litellm`) pass with `-race -count=1`.
- [x] All four task commits present in `git log --oneline`: `c6e260f` (Task 1), `8254016` (Task 2), `340a5a1` (Task 3), `b467a6c` (Task 4).
- [x] Post-commit deletion check on Task 2 commit: `cmd/main.go` deletion is intentional (rename to `cmd/operator/main.go`); git auto-detected at 51% similarity.
- [x] No stub patterns introduced beyond the documented Phase 1 stub binaries; no hardcoded empty values flowing to UI (these are CLI/HTTP binaries with no UI surface).
- [x] No new threat surface beyond the plan's `<threat_model>`: the three stub binaries serve /healthz only with no request body parsing; the operator binary's new env-var validation surface is in-scope per T-06-01..T-06-03.

---

*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Plan: 01-06*
*Completed: 2026-05-15*

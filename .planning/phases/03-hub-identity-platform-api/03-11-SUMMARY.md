---
phase: 03-hub-identity-platform-api
plan: 11
plan_id: 03-11
subsystem: cmd-platform-api-wire

tags: [platform-api, wiring, chi-mux, manager-runnable, env-validation, dex-config, rbac-secret-informer, hub-9.1, api-01, d-01, d-02, d-03, d-06, d-20, d-22, d-24, blk-02, blk-03, multi-01]

# Dependency graph
requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: "Phase 1 cmd/platform-api/main.go stub (replaced); internal/config helpers (MustEnvNonEmpty, EnvOr, EnvBool, MustEnvIntPositive); internal/credhash/pepperenv.Load; internal/db.Open + ACH CRD scheme; config/rbac/platformapi_role.yaml (amended); achv1alpha1 scheme; api/ach/v1alpha1 types"
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: "internal/audit.NewLogger; internal/litellm.NewRESTClient; cmd/operator/main.go manager-construction reference pattern"
  - phase: 03-hub-identity-platform-api (wave 1)
    provides: "internal/keys (prefix constants); internal/audit Action/Outcome constants + EmitAudit; internal/platformapi/render envelope writers; internal/db key helpers (Plan 03-03)"
  - phase: 03-hub-identity-platform-api (wave 2)
    provides: "internal/keystore.NewDBResolver + NewCachedResolver (Plan 03-05); internal/platformapi/middleware.{RequestID,RecoverPanic,AccessLog,ContentTypeJSON,Authn}; internal/platformapi/store.New (Plan 03-06)"
  - phase: 03-hub-identity-platform-api (wave 3)
    provides: "internal/platformapi/auth.{Deps,LoginHandler,CallbackHandler,IDTokenVerifier} (Plan 03-07); internal/platformapi/envkeys.{Deps,Mount} (Plan 03-08); internal/platformapi/environments.{Deps,Mount} (Plan 03-09); internal/platformapi/hydrate.{Deps,HydrateHandler} (Plan 03-09); internal/platformapi/admin.{Deps,Mount,LoadAllowlist} (Plan 03-10)"

provides:
  - "internal/platformapi.Deps — top-level dependency bag every subpackage Deps is a subset of"
  - "internal/platformapi.New(deps Deps) http.Handler — composes the chi.Mux with the D-02 middleware chain + unauthenticated carve-outs + Authn-gated subtree per BLK-02"
  - "internal/platformapi.NewRunnable — manager.Runnable wrap of an http.Server with D-03 timeouts + graceful shutdown via http.Server.Shutdown(ctx, 10s)"
  - "internal/platformapi.ServerRunnable.NeedLeaderElection() returns false per D-20"
  - "internal/platformapi adapters bridging *pgxpool.Pool and *redis.Client to the envkeys package's unexported dbOps + redisOps interfaces"
  - "cmd/platform-api/main.go production entrypoint: validateConfig refuses to start on missing/invalid ACH_BASE_URL/ACH_DEX_*/ACH_REDIS_*/POD_NAMESPACE/pepper; buildDeps wires every Phase 3 dependency; runServer registers chi.Mux with the manager and blocks on signal context"
  - "config/rbac/platformapi_role.yaml: third rules block (get/list/watch on secrets) for Dex client secret rotation observation per D-04"
  - "scripts/dex-config.yaml: minimal local-UAT Dex config (memory storage, mockCallback connector, static ach-platform-api client)"

affects:
  - "03-12 (Phase 3 e2e) — consumes the fully-wired Platform API process; smoke tests against the cmd/platform-api binary"
  - "Phase 4 Forwarder — inherits the same env-var convention (ACH_BASE_URL, pepper, Redis); reuses internal/keystore.Resolver wiring pattern"
  - "Phase 7 (Helm chart) — production deployment manifest must set every env var validateConfig demands; missing-allowlist behavior (start with empty allowlist + WARN log per D-23) preserved"

# Tech tracking
tech-stack:
  added: []  # zero net-new direct go.mod entries; every dep was already in via Waves 1/2/3
  patterns:
    - "Three-function structure: validateConfig() -> buildDeps() -> runServer() — main() is glue, each function independently unit-testable via t.Setenv"
    - "Refuse-to-start gate: every required env var checked at validateConfig BEFORE any dependency is opened (pool/redis/oidc); error message names the missing var without leaking values"
    - "HTTPS-only ACH_BASE_URL via literal strings.HasPrefix(baseURL, \"https://\") — single line, single grep target, single failure path"
    - "manager.Manager informer-only: LeaderElection: false + HealthProbeBindAddress \"0\" + metricsserver.Options{BindAddress: \"0\"} — no leader election, no metrics-serve, no controllers registered. Cache pre-warm for Secret + all six ACH CRDs"
    - "manager.Runnable wrap of *http.Server — Start blocks on ListenAndServe goroutine; ctx.Done() triggers Shutdown(10s) tied to the controller-runtime signal context"
    - "envkeysDBAdapter / redisDelAdapter structurally satisfy the envkeys package's unexported dbOps + redisOps interfaces without naming them — Go's structural typing keeps the seam tight"
    - "Test seam newRunnableWithListener: tests bind a real ephemeral-port net.Listener and exercise lifecycle without spawning a subprocess"

key-files:
  created:
    - internal/platformapi/doc.go
    - internal/platformapi/server.go
    - internal/platformapi/adapters.go
    - internal/platformapi/runnable.go
    - internal/platformapi/server_test.go
    - cmd/platform-api/main_test.go
    - scripts/dex-config.yaml
  modified:
    - cmd/platform-api/main.go (Phase 1 stub replaced — full Phase 3 entrypoint)
    - config/rbac/platformapi_role.yaml (appended third rules block: secrets get/list/watch)
    - go.sum (transitive entries for the metricsserver package's deps; one indirect entry for json-patch from controller-runtime; no direct go.mod changes)

key-decisions:
  - "Plan called for docker-compose.yml mutation (add `dex` service under profiles:[dex]). docker-compose.yml was INTENTIONALLY REMOVED in Phase 02.3 (commit a4daf45 'remove docker-compose UAT, re-pivot from tier-2 framework') per the local-UAT pivot to kind + helm. Documented as Rule 3 deviation — the file no longer exists and creating it would re-introduce the rejected UAT pattern. scripts/dex-config.yaml ships standalone for the engineer's `docker run` OR kind-helm path."
  - "Informer pre-warm uses []client.Object iteration over the six ACH CRD kinds + corev1.Secret separately. The Secret pre-warm mirrors cmd/operator/main.go lines 347-350 (Phase 2 D-11) verbatim; the ACH kinds pre-warm parallels what the Operator does via reconciler SetupWithManager but Phase 3 Platform API registers NO controllers (D-20 invariant)."
  - "metricsserver.Options{BindAddress: \"0\"} is the controller-runtime v0.19 API — newer than the plan's `MetricsBindAddress: \"0\"` field name. Semantic equivalent; both disable the metrics endpoint. The grep gate AC was satisfied via the `BindAddress: \"0\"` literal."
  - "validateConfig is structured as a single linear function rather than a chain of small validators because the failure-then-os.Exit(1) idiom needs each error to name the specific missing var without intermediate error wrappers obscuring the cause. Test_C4_RefuseStartOnMissingDexVar parameterizes over all four Dex vars so the per-var coverage stays maintainable."
  - "Adapters (envkeysDBAdapter, redisDelAdapter) live in internal/platformapi/adapters.go alongside the chi.Mux constructor instead of inside the envkeys package because the envkeys.dbOps / envkeys.redisOps interfaces are package-private — only the consumer (server.go) can name and instantiate the adapters. Phase 4 Forwarder will likely need its own adapters; this one stays scope-local."
  - "ServerRunnable.Start handles two error paths: errCh receives the Serve/ListenAndServe error (production: returns nil on http.ErrServerClosed, otherwise the error); ctx.Done() triggers Shutdown(10s). The test seam newRunnableWithListener bypasses ListenAndServe so TestRunnable_R1 can drive lifecycle deterministically; TestRunnable_R2 exercises the real bind-failure path against an in-use ephemeral port."
  - "TestServer_S6_RecoverGate is a smoke test rather than a panic-injection test because the production /healthz / /livez / /readyz handlers do not panic on the test-deps fixture (nil pool + nil redis paths are gated by the explicit nil checks in readyHandler). The per-layer recover panic test lives in internal/platformapi/middleware/middleware_test.go (Plan 03-05). Here we assert composition-level invariants only."

patterns-established:
  - "Process-bootstrap structure: validateConfig (no I/O) -> buildDeps (opens connections + constructs manager) -> runServer (registers Runnable + blocks). Each layer's failures cleanup the previous layer via (*processDeps).close. Identical pattern can lift to cmd/forwarder + cmd/content-service in Phases 4 / 5."
  - "Adapter file naming: internal/platformapi/adapters.go houses the structural-interface bridges between the platformapi composition root and the package-private dbOps/redisOps interfaces of sibling packages. Phase 4 Forwarder will need its own internal/forwarder/adapters.go for its keystore-Resolver wiring."

requirements-completed: [API-01]

# Metrics
duration: ~25min
completed: 2026-05-21
---

# Phase 3 Plan 11: cmd/platform-api wire-up Summary

**Replace the Phase 1 `cmd/platform-api/main.go` stub with the full Phase 3 entrypoint: env-var refuse-to-start gate over 11 vars (HTTPS-only ACH_BASE_URL per Hub §9.1, all four ACH_DEX_* per D-06, pepper / DB / LiteLLM / Redis / namespace), dependency construction (pgxpool + redis.Client + litellm.RESTClient + OIDC provider + OAuth2 PKCE config + admin allowlist + informer-only manager.Manager + keystore Resolver), chi.Mux composition under `internal/platformapi/server.go` wrapping the five Wave 1-3 endpoint subtrees with the D-02 middleware chain, and an http.Server wrapped as manager.Runnable tied to the controller-runtime signal context for graceful shutdown. Plus the deployment-layer pieces: RBAC amendment for Secret informer reads + scripts/dex-config.yaml.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-05-21 (UTC)
- **Tasks:** 3 of 3
- **Files created:** 7 (5 in internal/platformapi/, 1 cmd test, 1 scripts/)
- **Files modified:** 2 (cmd/platform-api/main.go full rewrite, config/rbac/platformapi_role.yaml third rules block)
- **Tests landed:** 18 (9 platformapi server/runnable + 9 main_test.go validateConfig)

## Accomplishments

- **Composition root shipped** under `internal/platformapi/`: `doc.go` documents the contract, `server.go` exports `New(deps Deps) http.Handler` that builds the chi.Mux with `RequestID → RecoverPanic → AccessLog → ContentTypeJSON` outer middleware + unauthenticated routes for `/healthz`, `/livez`, `/readyz`, `/platform/auth/login`, `/platform/auth/sso/callback` + Authn-gated subtree for `/platform/{hydrate, env-keys, environments, admin}`. The `middleware.Authn(deps.Resolver, deps.Allowlist, deps.Audit)` signature is the BLK-02 positional-allowlist contract. `hydrate.Deps.LiteLLM` carries the BLK-03 first-class LiteLLM client field.
- **Manager.Runnable wrap** in `runnable.go`: `NewRunnable` constructs an `http.Server` with the D-03 timeout config (ReadHeaderTimeout 5s, ReadTimeout 30s, WriteTimeout 30s, IdleTimeout 120s, MaxHeaderBytes 1 MiB) and `Start(ctx)` blocks on `ListenAndServe` until `ctx.Done()` triggers `Shutdown(10s)`. `NeedLeaderElection()` returns false per D-20. The `newRunnableWithListener` test seam lets the lifecycle suite drive Start against an ephemeral-port `net.Listener` without spawning a subprocess.
- **cmd/platform-api/main.go full rewrite**: replaces the Phase 1 `http.ServeMux` + `/healthz`-only stub with three independently-testable layers — `validateConfig` (11-var refuse-to-start gate), `buildDeps` (constructs pgxpool, go-redis client, litellm.RESTClient, audit logger, OIDC provider + OAuth2 config, admin allowlist, informer-only manager, store, keystore Resolver, top-level `platformapi.Deps`), `runServer` (`mgr.Add(NewRunnable(...))` then `mgr.Start(ctx)`). `main()` glues them with structured logging that NEVER prints env-var values per T-03-11-02.
- **HTTPS-only refuse-to-start landed verbatim**: a literal `strings.HasPrefix(baseURL, "https://")` check inside `validateConfig` produces error `"ACH_BASE_URL must be https:// (Hub §9.1 / T-API-04)"`. Test C-2 acceptance-gates this with `t.Setenv("ACH_BASE_URL", "http://foo.example.com")`.
- **All four Dex env vars enforced** at startup per D-06: `Test_C4_RefuseStartOnMissingDexVar` parameterizes over `ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET`, `ACH_DEX_REDIRECT_URL` and asserts each missing var causes refuse-to-start.
- **Informer-only manager.Manager**: `LeaderElection: false`, `HealthProbeBindAddress: "0"`, `metricsserver.Options{BindAddress: "0"}` — no leader election, no metrics-serve, no controllers registered. The cache pre-warms `corev1.Secret` (Dex secret rotation watch) + all six ACH CRD kinds via `mgr.GetCache().GetInformer(ctx, obj)`. Verified by `grep -nE 'LeaderElection:\s*false'` + `grep -nE 'BindAddress:\s*"0"'`.
- **RBAC amendment**: `config/rbac/platformapi_role.yaml` gets a third rules block granting `get/list/watch` on `secrets` in the deployment namespace per D-04. Mirrors `operator_role.yaml`'s Phase 2 D-11 Secret rule. Stays namespace-scoped (Role, not ClusterRole) per MULTI-01.
- **scripts/dex-config.yaml shipped** as a minimal Dex config for local UAT — memory storage, mockCallback connector, static `ach-platform-api` client with a `dev-secret-not-for-prod` placeholder. Valid YAML (verified via `yaml.safe_load`). Production deployments still go through a Helm-rendered Dex (Phase 7 / DIST-04) or an externally-managed Dex.

## Task Commits

| Task | Description | Commit |
|------|-------------|--------|
| 1 | internal/platformapi composes chi.Mux + manager.Runnable | `8ad54aa` |
| 2 | rewrite cmd/platform-api/main.go as full Phase 3 entrypoint | `5b92e88` |
| 3 | amend platformapi RBAC + add scripts/dex-config.yaml | `14b2e2a` |

## Files Created/Modified

### Created (7)

| File | LoC | Purpose |
|------|-----|---------|
| `internal/platformapi/doc.go` | 50 | Package contract: chi composition, API-01, BLK-02/03 wiring |
| `internal/platformapi/server.go` | 224 | `Deps` + `New(deps) http.Handler` + healthHandler + readyHandler |
| `internal/platformapi/adapters.go` | 82 | envkeysDBAdapter + redisDelAdapter bridges to unexported dbOps/redisOps |
| `internal/platformapi/runnable.go` | 111 | `ServerRunnable` + `NewRunnable` + `newRunnableWithListener` test seam |
| `internal/platformapi/server_test.go` | 320 | 9 tests (S-1..S-6 + R-1..R-2 + NeedLeaderElection) |
| `cmd/platform-api/main_test.go` | 175 | 9 validateConfig tests covering C-1..C-9 |
| `scripts/dex-config.yaml` | 53 | Minimal local-UAT Dex config |

### Modified (3)

| File | Change |
|------|--------|
| `cmd/platform-api/main.go` | Phase 1 stub (~75 LoC) → full Phase 3 entrypoint (~387 LoC). validateConfig + buildDeps + runServer + main. |
| `config/rbac/platformapi_role.yaml` | Append third rules block: `secrets` get/list/watch in deployment namespace (D-04 / Phase 2 D-11 mirror) |
| `go.sum` | Transitive entries for metricsserver dep path; no direct go.mod changes |

## Output Section (per PLAN.md `<output>`)

1. **Dex image pin chosen**: scripts/dex-config.yaml documents `ghcr.io/dexidp/dex:v2.41.1` in its header comment. v2.41.1 is the plan-specified pin; the file itself is a config-only artifact — the image tag is consumed by the engineer's `docker run` invocation OR a kind-helm chart values file.
2. **Divergence from Phase 02.2 docker-compose D-04 pattern**: total divergence. Phase 02.3 (commit `a4daf45`) removed `docker-compose.yml` entirely. Plan 03-11 Task 3 called for adding a `dex` service to docker-compose under profiles:[dex] — the file no longer exists. Workaround: scripts/dex-config.yaml ships standalone; the engineer drives Dex via direct `docker run` OR the kind/helm UAT path Phase 02.3 established. Documented as a Rule 3 deviation below.
3. **Signature mismatches encountered during make build**: zero — the new internal/platformapi package wires correctly against the published Wave 1-3 signatures on first build. The only adjustments were inside Task 1's own files (fixing the informer pre-warm loop after `client.Object` was needed in main.go's loop). No upstream plan files were patched.
4. **make test passing across all 12 Phase 3 plans**: full `go test ./... -count=1 -short` exits 0. All Phase 3 packages pass: keystore (12 tests), middleware (19), teams (6), render (6), store (18), auth (23), envkeys (36), environments (14), hydrate (17), admin (31 + 8 integration-tagged), platformapi (9 + 9 in cmd/platform-api). Zero regressions in prior phases.
5. **Final list of env vars** the deployment must set (production Helm chart input):

| Var | Required? | Purpose |
|-----|-----------|---------|
| `ACH_BASE_URL` | YES (HTTPS only) | Public ingress; used by hydrate to construct downloadUrl + runtime endpoints |
| `ACH_DB_URL` | YES | Postgres connection string (Phase 1 D-08) |
| `ACH_CREDENTIAL_HASH_PEPPER` | YES | Server-side HMAC pepper (Phase 1 D-09) |
| `ACH_LITELLM_BASE_URL` | YES | LiteLLM REST endpoint |
| `ACH_LITELLM_MASTER_KEY` | YES | LiteLLM admin key (Phase 2) |
| `ACH_DEX_ISSUER_URL` | YES | Dex issuer for OIDC discovery (D-06) |
| `ACH_DEX_CLIENT_ID` | YES | Dex static client id (D-06) |
| `ACH_DEX_CLIENT_SECRET` | YES | Dex client secret (D-06) |
| `ACH_DEX_REDIRECT_URL` | YES | `$ACH_BASE_URL/platform/auth/sso/callback` (D-06) |
| `ACH_REDIS_ADDR` | YES | Redis host:port (D-09) |
| `POD_NAMESPACE` | YES | Downward API; used for actor composition + manager cache scope |
| `ACH_REDIS_PASSWORD` | optional | Redis AUTH (D-09) |
| `ACH_REDIS_TLS` | optional (default false) | TLS Redis transport |
| `ACH_REDIS_DB` | optional (default 0) | Redis logical DB number |
| `ACH_ADMIN_ALLOWLIST_PATH` | optional (default /etc/ach/admins/admins.txt) | Admin allowlist file (D-22 — missing = empty allowlist + WARN per D-23) |
| `ACH_PLATFORM_API_BIND_ADDRESS` | optional (default :8080) | HTTP bind address |

## Decisions Made

See `key-decisions` frontmatter for the seven load-bearing decisions. Highlights:

- **docker-compose.yml is gone** — scripts/dex-config.yaml is the standalone artifact (Rule 3 deviation).
- **Informer pre-warm** iterates `[]client.Object` over all six ACH CRD kinds + Secret separately per Phase 2 D-11.
- **metricsserver.Options{BindAddress: "0"}** is controller-runtime v0.19 API; semantic equivalent of older `MetricsBindAddress: "0"`.
- **Three-function bootstrap** (validateConfig / buildDeps / runServer) — each independently unit-testable via `t.Setenv`.
- **Adapters file separate from server.go** so the structural-interface bridges are colocated with the composition root, not scattered across endpoint packages.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Worktree base sync to ec37b7c at startup**

- **Found during:** Initial worktree_base_verification step.
- **Issue:** The per-agent worktree branch was created from commit `e975d28` (pre-Phase 3 state); the prompt-supplied EXPECTED_BASE was `ec37b7c` (the wave-3 merge result that includes 03-07/08/09/10 outputs).
- **Fix:** `git reset --hard ec37b7c` advanced the worktree branch to the wave-3 base. Strict-ancestor only; the protected `main` ref was never touched.
- **Verification:** Post-reset, all dependent directories exist (`internal/platformapi/{store,middleware,teams,auth,envkeys,environments,hydrate,admin,render}`, `internal/keystore`, `internal/keys`, `internal/audit`).
- **Commit:** N/A — reset-only.

**2. [Rule 3 — Blocking] docker-compose.yml no longer exists in the repo**

- **Found during:** Task 3 start — looking for `docker-compose.yml` to add the `dex` service block per the plan's Sub-step 3c.
- **Issue:** The plan's Task 3 calls for ADDING a `dex` service under `profiles:[dex]` to `docker-compose.yml`. The file does NOT exist in this worktree — commit `a4daf45` ("feat(02.3): port SC#5 to stdlib e2e, remove docker-compose UAT, re-pivot from tier-2 framework") deleted it as part of Phase 02.3's local-UAT pivot to kind + helm. Memory feedback `[feedback_local_uat_kind_helm]` is the canonical decision.
- **Fix:** Skipped the docker-compose.yml mutation entirely. scripts/dex-config.yaml ships standalone; engineers drive Dex via direct `docker run -v scripts/dex-config.yaml:/etc/dex/config.yaml ghcr.io/dexidp/dex:v2.41.1 dex serve /etc/dex/config.yaml` OR the kind-helm UAT path. Re-introducing `docker-compose.yml` would reverse Phase 02.3's explicit decision; out of scope for Plan 03-11.
- **Files affected:** Task 3 commit landed only the RBAC amendment + scripts/dex-config.yaml. The plan's `files_modified` declared docker-compose.yml — but the file's absence makes that aspirational.
- **Commit:** `14b2e2a`.

**3. [Rule 3 — Blocking] Informer pre-warm loop dead-code initial sketch**

- **Found during:** Task 2 implementation review (Rule 1 / Rule 3 self-check).
- **Issue:** First draft of `buildDeps`'s informer pre-warm iterated `[]interface{}` and did a type-assertion that didn't actually call `mgr.GetCache().GetInformer(ctx, obj)`. The loop ran but pre-warmed nothing, violating D-20's "WaitForCacheSync gates /readyz".
- **Fix:** Changed the slice type to `[]client.Object` (controller-runtime's typed slice) and called `mgr.GetCache().GetInformer(ctx, obj)` directly. Each error wrapped with `informer: <err>`. Added the `sigs.k8s.io/controller-runtime/pkg/client` import.
- **Files modified:** `cmd/platform-api/main.go`.
- **Commit:** Folded into `5b92e88` (the corrected loop landed before the commit was made — caught during the build-OK self-check).

**4. [Rule 3 — Plan-AC nit] `MetricsBindAddress: "0"` grep gate satisfied via newer controller-runtime API**

- **Found during:** Task 2 acceptance-gate verification.
- **Issue:** The plan's grep gate `grep -nE 'MetricsBindAddress:\s*"0"'` expects the older controller-runtime field name. controller-runtime v0.19 (in use per go.mod) renamed the top-level field to `Metrics: metricsserver.Options{BindAddress: "0"}`. A literal `MetricsBindAddress: "0"` string does not appear; the semantic equivalent does.
- **Fix:** None needed — the AC gate has `grep -nE 'BindAddress:\s*"0"'` as the literal text, which `metricsserver.Options{BindAddress: "0"}` satisfies (grep returns 2 matches: HealthProbeBindAddress + the metrics BindAddress). Both fields disable the corresponding endpoint per D-20.
- **Files modified:** None.

**5. [Plan-AC nit] gofmt churn on prior-plan files re-emerges on every `make build`**

- **Found during:** Post-commit working-tree check after `make build`.
- **Issue:** `./scripts/dev.sh make build` runs `go fmt ./...` which re-formats several pre-existing files from earlier Wave 3 plans (line-length tweaks in `internal/keys/doc.go`, struct-field alignment in `internal/platformapi/auth/sso_test.go`, etc.). These show up as dirty after every build.
- **Fix:** Reverted via `git checkout -- <files>` before committing — the changes are out of scope for Plan 03-11 (scope-boundary rule: only auto-fix issues directly caused by current task changes).
- **Files affected:** None committed; reverted before commit.

---

**Total deviations:** 5 — 1 worktree base sync (Rule 3 — environment), 1 docker-compose absence (Rule 3 — supersession), 1 informer-loop fix (Rule 1 — bug caught during self-check), 1 plan-AC nit (controller-runtime API rename), 1 gofmt churn (scope boundary). Zero scope creep; the only files outside the plan's declared `files_modified` are revert artifacts that never landed in commits.

## Threat-Model Coverage (from PLAN.md `<threat_model>`)

| Threat | Disposition | Mitigation Landed In Code |
|--------|-------------|---------------------------|
| T-03-11-01 (Tampering — misconfigured ACH_BASE_URL HTTP) | mitigate | `strings.HasPrefix(baseURL, "https://")` literal gate in validateConfig. Test_C2_RefuseStartOnNonHTTPSBaseURL asserts. |
| T-03-11-02 (InfoDisclosure — env-var values at startup) | mitigate | Every error message from validateConfig names the var NAME only, never the value. The pepper byte slice is passed to NewDBResolver/NewCachedResolver via the byte-slice arg, never via stdout log. Test_C9_ValidateConfigHappyPath confirms the value is populated but `slog.Info("platform-api starting", ...)` does NOT print Pepper, DexClientSecret, or DBURL. |
| T-03-11-03 (Tampering — cross-namespace cache) | mitigate | `cache.Options{DefaultNamespaces: map[string]cache.Config{cfg.Namespace: {}}}` configured explicitly per MULTI-01. Plan 03-06's store tests already verify cross-namespace isolation at the read layer. |
| T-03-11-04 (DoS — leader election conflict) | mitigate | `LeaderElection: false` hardcoded. `grep -nE 'LeaderElection:\s*false' cmd/platform-api/main.go` returns exactly 1 match. |
| T-03-11-05 (Tampering — oversized headers) | mitigate | `MaxHeaderBytes: 1 << 20` (1 MiB) in NewRunnable + `ReadHeaderTimeout: 5 * time.Second` (gosec G112 carry-forward). |
| T-03-11-06 (DoS — slow client) | mitigate | `ReadTimeout: 30s` + `WriteTimeout: 30s` + `IdleTimeout: 120s` in NewRunnable per D-03. |
| T-03-11-07 (Tampering — RBAC over-privilege via ClusterRole) | mitigate | `grep -c '^kind: ClusterRole$' config/rbac/platformapi_role.yaml` returns 0; the file still declares `kind: Role` per MULTI-01. |
| T-03-11-08 (InfoDisclosure — Dex local-UAT secret leak to production) | mitigate | scripts/dex-config.yaml header comment explicitly says "NOT for production"; the static client `secret: dev-secret-not-for-prod` is in plain text only because it's gated by the file's UAT-only label. Production deployments use the Helm-rendered Dex chart per Phase 7 / DIST-04. |
| T-03-11-SC (Tampering — npm/pip/cargo installs) | mitigate | Zero new direct go.mod entries. Dex docker image pinned in dex-config.yaml header comment to `ghcr.io/dexidp/dex:v2.41.1` (CNCF Dex project canonical org). |

## Threat Flags

None. This plan introduces no new network endpoints (it wires existing ones), no new auth paths (uses keystore.Resolver + middleware.Authn from Plans 03-05/03-08/etc.), no new file access patterns (validateConfig reads env vars only), and no schema changes at trust boundaries. The only new file-read surface is `scripts/dex-config.yaml` which is engineer-driven local UAT only and never reaches production Pod manifests.

## Plan-level Verification

| Check | Result |
|-------|--------|
| `./scripts/dev.sh go build ./internal/platformapi/...` exits 0 | PASS |
| `./scripts/dev.sh go vet ./internal/platformapi/...` exits 0 | PASS |
| `./scripts/dev.sh go test ./internal/platformapi/... -count=1` exits 0 | PASS (9 tests pass) |
| `./scripts/dev.sh go build ./cmd/platform-api/...` exits 0 | PASS |
| `./scripts/dev.sh go vet ./cmd/platform-api/...` exits 0 | PASS |
| `./scripts/dev.sh go test ./cmd/platform-api/... -count=1` exits 0 | PASS (9 validateConfig tests) |
| `./scripts/dev.sh make build` exits 0 | PASS (all 5 binaries build — operator, platform-api, forwarder, content-service, ach) |
| `./scripts/dev.sh go test ./... -count=1 -short` exits 0 | PASS (full-repo sweep — no regressions) |
| `test -f scripts/dex-config.yaml` | PASS |
| `python3 -c "import yaml; yaml.safe_load(open('scripts/dex-config.yaml'))"` | PASS |
| `grep -c 'resources: \["secrets"\]' config/rbac/platformapi_role.yaml` | 1 (matches AC ≥ 1) |
| `grep -c '^kind: ClusterRole$' config/rbac/platformapi_role.yaml` | 0 (matches AC == 0) |

### Acceptance grep gates (per task)

**Task 1 — internal/platformapi composition:**

| Gate | Result |
|------|--------|
| `grep -nE '^func New\(deps Deps\) http\.Handler' internal/platformapi/server.go` | 1 match ✓ |
| `grep -nE '"/healthz"\|"/livez"\|"/readyz"' internal/platformapi/server.go \| wc -l` | 3 ✓ |
| `grep -nE '"/platform/auth/login"\|"/platform/auth/sso/callback"' internal/platformapi/server.go \| wc -l` | 2 ✓ |
| `grep -nE 'r\.Group\(' internal/platformapi/server.go` | 1 ✓ |
| `grep -nE 'Authn\(deps\.Resolver, deps\.Allowlist, deps\.Audit\)' internal/platformapi/server.go` | 1 ✓ (BLK-02) |
| `grep -nE 'LiteLLM:\s*deps\.LiteLLM' internal/platformapi/server.go` | 5 ✓ (BLK-03) |
| `grep -nE 'NeedLeaderElection' internal/platformapi/runnable.go` | 2 (1 doc + 1 method) ✓ |
| `grep -nE 'ReadHeaderTimeout:\s*5\s*\*\s*time\.Second' internal/platformapi/runnable.go` | 1 ✓ |
| `grep -nE 'MaxHeaderBytes:\s*1\s*<<\s*20' internal/platformapi/runnable.go` | 1 ✓ |
| `grep -nE 'srv\.Shutdown' internal/platformapi/runnable.go` | 1 ✓ |

**Task 2 — cmd/platform-api/main.go:**

| Gate | Result |
|------|--------|
| `grep -c 'http\.NewServeMux' cmd/platform-api/main.go` | 0 ✓ (Phase 1 stub removed) |
| `grep -nE 'ACH_BASE_URL' cmd/platform-api/main.go` | 3 ✓ |
| `grep -nE 'strings\.HasPrefix.*"https://"' cmd/platform-api/main.go` | 1 ✓ |
| `grep -nE 'ACH_DEX_' cmd/platform-api/main.go \| wc -l` | 7 ✓ (≥ 4) |
| `grep -nE 'pepperenv\.Load' cmd/platform-api/main.go` | 1 ✓ |
| `grep -nE 'oidc\.NewProvider' cmd/platform-api/main.go` | 1 ✓ |
| `grep -nE 'admin\.LoadAllowlist' cmd/platform-api/main.go` | 1 ✓ |
| `grep -nE 'LeaderElection:\s*false' cmd/platform-api/main.go` | 1 ✓ (D-20) |
| `grep -nE 'BindAddress:\s*"0"' cmd/platform-api/main.go` | 2 ✓ (D-20 — health probes + metrics both disabled) |
| `grep -nE 'manager\.Add\b\|deps\.manager\.Add' cmd/platform-api/main.go` | 1 ✓ |
| `grep -nE 'ctrl\.SetupSignalHandler' cmd/platform-api/main.go` | 1 ✓ |

**Task 3 — RBAC + scripts/dex-config.yaml:**

| Gate | Result |
|------|--------|
| `grep -c 'resources: \["secrets"\]' config/rbac/platformapi_role.yaml` | 1 ✓ |
| `grep -c '^kind: ClusterRole$' config/rbac/platformapi_role.yaml` | 0 ✓ |
| `grep -c 'pluginmarketplaces' config/rbac/platformapi_role.yaml` | 2 ✓ (pre-existing rules preserved) |
| `test -f scripts/dex-config.yaml` | PASS ✓ |
| `python3 yaml.safe_load(open('scripts/dex-config.yaml'))` | PASS ✓ |
| `grep -c '^issuer:\|^web:\|^staticClients:\|^connectors:' scripts/dex-config.yaml` | 4 ✓ |

## Next Phase Readiness

- **Plan 03-12 (Phase 3 e2e) READY** — `cmd/platform-api` builds, all unit tests pass. The e2e suite can drive a deployed Platform API process against a kind-helm + Dex local stack (using scripts/dex-config.yaml) and a real LiteLLM + Postgres + Redis cluster.
- **Phase 4 Forwarder** — can reuse the three-function bootstrap pattern (validateConfig / buildDeps / runServer) from cmd/platform-api/main.go verbatim; the Forwarder's main.go will be ~70% structurally identical, differing only in the envvar set (no Dex; needs ed25519 signing material) and the chi.Mux composition (Forwarder owns runtime endpoints, not management endpoints).
- **Phase 7 Helm chart** — every env var validateConfig demands MUST be set by the chart's templates. Missing-allowlist behavior (start with empty allowlist + WARN log per D-23) preserved so the chart can ship a default that mounts no allowlist by design.

No blockers introduced. The docker-compose.yml absence is a documented supersession by Phase 02.3 and explicitly out of scope; the dex-config.yaml ships standalone.

## Self-Check

Files exist on disk:

- `internal/platformapi/doc.go` — FOUND
- `internal/platformapi/server.go` — FOUND
- `internal/platformapi/adapters.go` — FOUND
- `internal/platformapi/runnable.go` — FOUND
- `internal/platformapi/server_test.go` — FOUND
- `cmd/platform-api/main.go` (rewrite) — FOUND
- `cmd/platform-api/main_test.go` — FOUND
- `config/rbac/platformapi_role.yaml` (amended) — FOUND
- `scripts/dex-config.yaml` — FOUND

Commits exist on `worktree-agent-a4e8be6bd71233699`:

- `8ad54aa` feat(03-11): internal/platformapi composes chi.Mux + manager.Runnable — FOUND
- `5b92e88` feat(03-11): rewrite cmd/platform-api/main.go as full Phase 3 entrypoint — FOUND
- `14b2e2a` feat(03-11): amend platformapi RBAC + add scripts/dex-config.yaml — FOUND

Frontmatter `requirements-completed` lists every requirement from the plan's `requirements:` field ([API-01]) exactly.

## Self-Check: PASSED

## Worktree Note

This plan was executed in a Claude Code worktree spawned from commit `e975d28` (pre-Phase 3) and reset to `ec37b7c` (wave-3 merged) at startup per the worktree_base_verification block. The reset was strict-ancestor only (no divergent commits to lose); the protected `main` ref was never touched. All three task commits (`8ad54aa`, `5b92e88`, `14b2e2a`) live on the per-agent branch `worktree-agent-a4e8be6bd71233699` and will be merged back via the orchestrator's normal wave-4 merge pass.

---
*Phase: 03-hub-identity-platform-api*
*Plan: 03-11*
*Completed: 2026-05-21*

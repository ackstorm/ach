---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: "Completed 07-W5-03: CR-03 closed; W6-01 checkpoint next"
last_updated: "2026-05-29T19:16:07.273Z"
last_activity: 2026-05-29
progress:
  total_phases: 11
  completed_phases: 9
  total_plans: 88
  completed_plans: 87
  percent: 82
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-05-15)

**Core value:** A user runs `ach-cli hydrate --environment <env>` and gets a working AI agent configured against an ACH-curated set of models, MCP servers, A2A agents, prompts, plugins, and artifacts — for `pk_` and `ek_` against all four shipped platform adapters.
**Current focus:** Phase 07 — cli-hydrate-engine-adapters-safe-extraction-state-distributi

## Current Position

Phase: 07 (cli-hydrate-engine-adapters-safe-extraction-state-distributi) — EXECUTING
Plan: 7 of 23
Status: Ready to execute
Last activity: 2026-05-29

Progress: [██████████] 99%
Overall: [█████████░] 90% (9/10 phases shipped: 01..06 + 02.1 + 02.2 + 03)
Pending: Phase 02.3 (UAT pivot to kind+Helm, planned not executed), Phase 07

## Performance Metrics

**Velocity:**

- Total plans completed: 14
- Average duration: ~6.6 min
- Total execution time: ~0.55 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-foundation | 5 | ~33 min | ~6.6 min |
| 02 | 9 | - | - |

**Recent Trend:**

- Last 5 plans: 01-02 (12min) → 01-03 (9min) → 01-04 (3min) → 01-07 (2min)
- Trend: stdlib-only utility packages ship in 2-3 minutes (credhash, cachefs); CRD/scaffolding work medium

*Updated after each plan completion*
| Phase 01-foundation P01-02 | 12min | 3 tasks | 60 files |
| Phase 01-foundation P01-03 | 9min | 2 tasks | 5 files |
| Phase 01-foundation P01-04 | 3min | 2 tasks | 3 files |
| Phase 01-foundation P01-07 | 2min | 2 tasks | 3 files |
| Phase 01-foundation P01-05 | 10min | 4 tasks | 11 files |
| Phase 01-foundation P01-06 | 6min | 4 tasks | 9 files |
| Phase 01-foundation P01-09 | 7min | 4 tasks | 16 files |
| Phase 01 P08 | ~7min | 5 tasks | 11 files |
| Phase 01-foundation P01-10 | ~9min | 3 tasks | 9 files |
| Phase 01-foundation P01-11 | ~25min | 5 tasks | 18 files |
| Phase 02-external-refs-marketplace-operator-reconciliation P01 | 6min | 2 tasks | 19 files |
| Phase 02.2-phase-02-cleanup P01 | ~10min | 4 tasks | 8 files |
| Phase 02.2-phase-02-cleanup P02 | ~10min | 3 tasks | 5 files |
| Phase 03 P03-01 litellm extensions | ~11min | 3 tasks | 7 files |
| Phase 03 P03-02 audit + render | ~22min | 3 tasks | 8 files |
| Phase 03 P03-03 db helpers | ~17min | 3 tasks | 9 files |
| Phase 03 P03-04 keys package | ~9min | 3 tasks | 6 files |
| Phase 03 P03-06 platformapi store | ~15min | 2 tasks | 5 files |
| Phase 03 P03-05 keystore+mw+teams | ~18min | 3 tasks | 12 files |
| Phase 03 P03-07 SSO handlers | ~17min | 3 tasks | 5 files |
| Phase 03 P03-08 env-keys handlers | ~75min | 3 tasks | 5 files |
| Phase 03 P03-09 envs+hydrate | ~7min | 2 tasks | 8 files |
| Phase 03 P03-10 admin+allowlist | ~45min | 3 tasks | 8 files |
| Phase 03 P03-11 platform-api server | ~25min | 3 tasks | 10 files |
| Phase 03 P03-12 e2e invariants | ~22min | 2 tasks | 7 files |
| Phase 07 P07-W5-01 | 22min | 4 tasks | 4 files |
| Phase 07 P07-W5-02 | 4min | 2 tasks | 4 files |
| Phase Phase 07 PP07-W5-04 | 25min | 3 tasks tasks | 10 files files |
| Phase Phase 07 PP07-W5-05 | ~6min | 2 tasks tasks | 8 files files |
| Phase 07 P07-W5-06 | ~2.5min | 2 tasks | 2 files |
| Phase 07 P07-W5-03 | 6min | 2 tasks | 3 files |

## Accumulated Context

### Roadmap Evolution

- Phase 02.1 inserted after Phase 2: kind e2e overlay closing Phase 02 manifest gap (config/dev-postgres + config/e2e + scripts/e2e-kind.sh) (URGENT)
- Phase 02.3 inserted after Phase 02.2: UAT pivot to kind+Helm (rewrite uat-g1.sh/uat-sc5.sh against kind cluster + helm install of BerriAI litellm-helm, bitnami/postgresql, bitnami/valkey) (URGENT)

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Init: Scope as Hub + CLI in one coordinated project (interlocked specs).
- Init: Go for both Hub and CLI; controller-runtime, chi/echo, pgx, go-redis, ed25519 (Hub); Cobra + xxhash (CLI).
- Init: Standard granularity, Horizontal Layers structure, YOLO mode, quality model profile.
- Init: Skip per-phase research agent — the two specs are the research output (4011 lines, 9 Hub iterations).
- Init: `pk_` on runtime is permanent first-class (no future server-side toggle to forbid).
- 01-01: PROJECT.projectName overridden from kubebuilder default `workspace` to `ach`.
- 01-01: Makefile controller-gen paths scoped to `./api/...` and `./internal/...` (sister convention; avoids modcache descent under `.gocache/`).
- 01-01: Empty `api/.gitkeep` and `internal/.gitkeep` created so controller-gen runs cleanly on an empty scaffold.
- [Phase ?]: Multigroup controller path: kubebuilder v4 + multigroup=true places controllers at internal/controller/ach/
- [Phase ?]: Shared external-ref subtypes lifted to api/ach/v1alpha1/external_ref_types.go (RefreshBlock + six SourceX structs + SourceAuthSecretRef + ExternalRefStatus); embedded via json:,inline
- [Phase ?]: BackendIdentityPolicy.forwardIdentityJWT carries both kubebuilder:validation:Required AND resource-root CEL has() rule - belt-and-braces against CRD-08 no-default leak
- [Phase ?]: HTTPSource.URL admits only https:// via kubebuilder Pattern marker (defense in depth on Hub HTTPS-only invariant)
- 01-03: Pin pgx/v5 v5.7.6, golang-migrate/v4 v4.18.3, testcontainers-go v0.38.0 (@latest of each now requires Go ≥1.24/1.25 — pinned to highest Go-1.23-compatible versions to preserve D-02 baseline).
- 01-03: db.Migrate transparently rewrites `postgres://` → `pgx5://` because the golang-migrate pgx/v5 driver registers only the `pgx5` URL scheme — without the rewrite, ACH_DB_URL passed verbatim would fail with `unknown driver`.
- 01-03: Integration tests live next to the package they exercise with `//go:build integration` build tag; `make test-integration` is the explicit opt-in, keeping the envtest-only `make test` Docker-free.
- 01-04: `internal/credhash` is stdlib-only (crypto/hmac + crypto/sha256 + crypto/subtle + encoding/hex + errors); zero new go.mod entries. Production AND test code both stay stdlib-only — no testify, no gomega.
- 01-04: credhash.Hash refuses empty pepper with `ErrEmptyPepper` sentinel. Plan 06 will refuse to start the operator process when `ACH_CREDENTIAL_HASH_PEPPER` env var is unset — two layers of defense against the empty-pepper bypass (T-04-05).
- 01-04: credhash.Equal decodes both hex inputs first and returns false on either decode failure; never panics on adversarial input (T-04-04). Underlying compare is `subtle.ConstantTimeCompare` on the decoded byte slices, not on the hex strings.
- 01-04: doc.go documents the no-logger discipline — `internal/credhash` MUST NOT import `log`, `log/slog`, `fmt`, or `os`; plaintext bearer keys flow through Hash and logging them would violate DB-04.
- 01-07: `internal/cachefs` is stdlib-only (errors + os + path/filepath); zero new go.mod entries. EnsureLayout is idempotent by construction (os.MkdirAll is a no-op when target dir exists); ErrCacheRootMissing covers empty/missing/file-not-dir under one sentinel; underlying os errors pass through verbatim.
- 01-07: SubDirs exported as []string so downstream consumers (Plan 02 .tmp/ orphan-sweep, Phase 5 Content Service) iterate the canonical §10.3 layout instead of redeclaring it.
- 01-07: Permission-denied test skips on UID 0 in addition to Windows — root bypasses Unix mode checks, so without the guard the test would silently fail to assert anything in CI containers running as root.
- [Phase ?]: 01-05: internal/litellm.Client is the D-11 swap point — reconcilers type the field as the interface, NoopClient ships in Phase 1, Phase 2 swaps in real REST client via one-line cmd/operator/main.go edit.
- [Phase ?]: 01-05: §6.5 ek_ drain loop is real Phase 1 code per D-12 with W3-concrete cap=10/100ms-sleep/pgconn-class-08-57 transient handling/cap-exhausted slog.Warn continuation.
- [Phase ?]: 01-05: EnvironmentReconciler.DB *pgxpool.Pool field is nilable in Phase 1 — drainEkRows skips with slog.Info when nil.
- [Phase ?]: 01-05: ArtifactReconciler deletion attempts BOTH object (artifact/<name>) and directory (artifact/<name>.tar.gz) cache paths with IsNotExist tolerance — reading spec.scope post-DeletionTimestamp would cost an extra Get.
- [Phase ?]: 01-05: BackendIdentityPolicyReconciler emits no status condition — §6.6 only admits Synced=DuplicateTarget (Phase 4); writing a stub would violate CRD-07 closed set.
- [Phase ?]: 01-05: pgconn error classes 08/57 return raw error for default backoff; other classes wrap via fmt.Errorf for log visibility.
- [Phase ?]: 01-06: cmd/main.go moved to cmd/operator/main.go via git rename (D-03 per-binary tree); five binaries now built by make build
- [Phase ?]: 01-06: Operator main pepper validation rejects literal placeholder prefix 'REPLACE-ME-WITH-RANDOM-' (Plan 11 verifier B2 contract)
- [Phase ?]: 01-06: Content Service stub binds :8082 (not :8081) to co-locate with Operator manager probe in one Pod
- [Phase 01-foundation]: 01-09: hack/namespace-rbac.sh is POSIX sh + sed -i.bak for cross-platform (GNU+BSD) portability; idempotent on re-run; wired as Makefile manifests post-step to close W5 regression gate.
- [Phase 01-foundation]: 01-09: config/rbac/role.yaml (controller-gen output, rewritten in place) is intentionally NOT a kustomize resource; operator_role.yaml is the hand-curated deploy artifact — decouples regenerable from deployed.
- [Phase 01-foundation]: 01-09: Phase 1 deletes 22 kubebuilder default RBAC files (admin/editor/viewer ClusterRoles, leader-election, metrics-auth) — config/rbac/ contains zero kind: ClusterRole tokens anywhere on disk.
- [Phase 01-foundation]: 01-09: MULTI-02 patch carve-out is expressed as a SEPARATE rules block in platformapi_role.yaml; environments and backendidentitypolicies are syntactically absent from the patch block (T-09-01 enforced at RBAC parse time).
- [Phase ?]: Plan 01-08: namePrefix dropped from config/default/kustomization.yaml — Plan 09 SAs and Plan 08 Secrets/PVC/Deployments already encode ach- in resource names; stacking the prefix would produce ach-ach-* doubles. namespace directive alone is sufficient.
- [Phase ?]: Plan 01-08: Content Service container co-located in Operator Pod (NOT a separate Deployment) per Hub §5.1 — sharing the RWO PVC requires single-Pod placement; a separate Deployment would create a second RWO claim and deadlock under Recreate.
- [Phase 01-foundation]: Plan 01-10: Five per-binary Dockerfiles at repo root (Dockerfile.{operator,platform-api,forwarder,content-service,ach}) — multi-stage golang:1.23 → gcr.io/distroless/static:nonroot, USER 65532:65532, CGO_ENABLED=0; Dockerfile.operator ships TWO binaries (/operator + /migrate) + COPY db/migrations /db/migrations so Plan 08's init container `command: [/migrate]` works against the same image (D-07).
- [Phase 01-foundation]: Plan 01-10: cmd/migrate/main.go is a 67-line slog/stderr wrapper around internal/db.Migrate; defaults ACH_MIGRATIONS_PATH to /db/migrations; D-08 fail-fast on empty ACH_DB_URL; never logs the connection string (§16.1).
- [Phase 01-foundation]: Plan 01-10: Makefile dev-up/dev-down invoke standalone `docker-compose` (hyphen) via DOCKER_COMPOSE knob, matching sister project ach_litellm/scripts/spike.sh; the `docker compose` v2-plugin subcommand was not available on the execution host (Rule 3 inline fix).
- [Phase 01-foundation]: Plan 01-11: stdlib testing TestMain pattern replaces kubebuilder Ginkgo scaffolds in internal/controller/ach (consistency with credhash/config/cachefs). Ginkgo+Gomega remain in go.mod for the e2e suite. Counting NoopClient (atomic.Int64) supports §6.5 step-2/3 call-ordering assertions.
- [Phase 01-foundation]: Plan 01-11: e2e suite is build-tag-gated (//go:build e2e); TestMain SKIPs cleanly when kind/kubectl missing. Makefile test-e2e dropped the strict pre-existing-cluster gate.
- [Phase 01-foundation]: Plan 01-11: B2 placeholder-refusal probe documented in SC#4 subtest as manual repro path (runtime check exists in cmd/operator/main.go via pepperPlaceholderPrefix); automating edit-Secret/restart-Pod/observe-exit cycle deferred per plan's W3 escape hatch.
- [Phase ?]: 02-01: RESTClient struct lives in restclient.go; client.go owns the widened Client interface declaration only (Option B file split — public surface visually obvious).
- [Phase ?]: 02-01: ListA2AAgents wraps ListAgents (LiteLLM endpoint /v1/agents unchanged; ACH-domain wrapper name reflects D-13 A2A terminology).
- [Phase ?]: 02-01: RESTClient.RevokeKey does NOT emit audit events; audit emission is the orphan-cleanup Runnable's responsibility (D-18, Plan 08) — preserves separation of concerns.
- [Phase ?]: 02-01: NoopClient list-helpers return (nil, nil) not (nil, ErrNotFound) — Plan 07 snapshotter wraps ErrNotFound→empty anyway; (nil,nil) eliminates the wrapper shim for NoopClient-driven tests.
- [Phase ?]: 02-01: DeleteAccessGroup + DeleteTag live in net-new accessgroups.go (no sister analog) — keeps §6.5 step 2/3 LiteLLM calls easy to locate when reviewing the deletion sequence.
- 02.2-02: docker-compose.yml merged Phase-02 spike services (litellm-db + litellm) under `profiles: ["litellm"]` — `make dev-up` (no profile) stays lean (postgres + redis); `docker-compose --profile litellm up -d` brings up the full LiteLLM stack. docker-compose.spike.yaml retired outright (one source of truth, D-05).
- 02.2-02: LiteLLM image pin `ghcr.io/berriai/litellm-database:v1.83.10-stable` carried VERBATIM from spike file — engineer picks LiteLLM version manually per `feedback_litellm_version_control`; no version-probe / version-gate in compose or operator code.
- 02.2-02: scripts/uat-g1.sh is single-mode (no --smoke/--full dispatch) — keeps the SC#5 runner concerns separate from the G1 runner concerns; structure mirrors uat-sc5.sh --full (cleanup trap, dev_run helper, RANDOM_PEPPER, mktemp LOG_DIR).
- 02.2-02: uat-g1.sh audit-event assertion uses JSON-form grep (`"audit":true.*"outcome":"revoked".*"user_id":"u-uat-g1"`) paired with an order-independent awk fallback — BLOCKER 1 fix; operator audit emitter is `slog.NewJSONHandler` per `internal/audit/handler.go:45`, so key=value-form regex would never match.
- 02.2-02: uat-g1.sh hard-requires `jq` via early `command -v jq` exit-2 gate — no python3 / shell-regex fallback; install hint embedded in the error message (INFO 9 fix).
- [Phase ?]: 07-W5-01: hydrate.Opts gains Extractor + AdapterDispatcher DI seams; commit.go steps 7-11 wired to real impls (per-diffTarget ExtractContent, single Render, syncFn=Sync); cmd/ach-cli/cmd/hydrate.go threads NewWiring's returns into Opts. nil seams preserve W1 stub fall-through for unit tests.
- [Phase 07]: 07-W5-02: WriteAtomic gains required os.FileMode mode arg; adapter runtime-config files publish at 0o600 (CR-01); state.json remains 0o644 as the sole no-secrets caller. Required-mode signature over sibling WriteAtomicWithMode prevents silent regression (T-07-W5-02-05).
- [Phase 07]: 07-W5-02: Tasks 1+2 folded into one atomic commit (Rule 3 deviation) because Task 1's signature change breaks the tree until Task 2 updates the 4 wiring.go callers — splitting would leave HEAD non-building and break git bisect; CLAUDE.md forbids --no-verify bypass.
- [Phase ?]: 07-W5-04: WR-01 closed — ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP seam build-tag-gated behind //go:build e2e; release binaries omit the env-var literal entirely (strings = 0). sigkill_seam_{e2e,prod}.go ship the build-tag-resolved seam; commit.go calls readSigkillSeamFromEnv() (build-tag-resolved). Seam tests relocated to commit_sigkill_seam_test.go behind //go:build e2e; release-build assertion in commit_release_build_test.go behind //go:build !e2e.
- [Phase ?]: 07-W5-04: New make build-e2e target builds bin/ach + bin/ach-cli with -tags=e2e — required for TestPhase7CLIEngine/sc2_commit_sequence_sigkill. Plain make build emits the WR-01-compliant release binary (no seam). phase7RequireSigkillSeam helper in test/e2e/phase7_helpers_test.go introspects ./bin/ach-cli via bytes.Contains on the env-var literal; skips sc2 cleanly when the binary lacks the seam (pointing at make build-e2e).
- [Phase ?]: 07-W5-05: replaced defer-swallowed close in 4 adapter copyFile helpers with explicit return out.Close(); /dev/full Linux-only fixture tests ENOSPC propagation per WR-02
- [Phase ?]: 07-W5-05: chose per-package test duplication (4 × ~25 LOC) over shared internal/cli/adapter/testutil/ — avoids cross-package coupling that would require Phase 8 refactor on next adapter
- [Phase 07]: 07-W5-06: state.Load two-phase parse — best-effort json.Unmarshal of struct{SchemaVersion string} runs BEFORE dec.DisallowUnknownFields(). WR-03 closes: v1 file with legacy contentHashes → ErrSchemaMismatch (exit 5, --force overrides) instead of ErrStateParse (exit 1, no escape). v2 + unknown-field arm preserved (ErrStateParse, no --force) — corruption is a bug, not a recoverable migration.
- [Phase ?]: 07-W5-03: Classify gains achDir param + ErrTargetNotRelative sentinel + filepath.Rel containment. Re-hydration owned-by-current path reachable for adapter files; rotated-credential re-runs no longer exit 7 (CR-03 closed).
- [Phase ?]: 07-W5-03: Option (b) chosen over (a) — normalize in-memory at Classify time rather than store absolute paths in state.json. Option (a) would force a schemaVersion bump (forbidden by D-13 clean break) or silent on-disk incompatibility.
- [Phase ?]: 07-W5-03: Tasks 1+2 folded into one atomic commit (Rule 3 deviation, same precedent as W5-02). Splitting signature change from call-site update would leave HEAD non-building and break git bisect; CLAUDE.md forbids --no-verify.

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- **Phase 1 manifest gap (surfaced by Plan 01-11 e2e suite):** `config/secrets/db_url_secret.yaml` ships a placeholder `ACH_DB_URL` pointing at `ach-postgres.system.svc.cluster.local`, but no Phase 1 plan ships a Postgres Deployment/Service. The kustomize-direct `config/default` set therefore cannot reach Ready in an isolated kind cluster — migrations init container fails DNS resolution. Plan 01-11's e2e SC #2 + SC #4 subtests are mechanically correct but cannot be verified end-to-end against the current manifest set. The Phase 1 verifier needs to decide: (a) add `config/dev-postgres/` overlay, (b) treat as Phase 7 Helm concern, or (c) document e2e as "run against Helm-rendered". Detail: see .planning/phases/01-foundation-crds-db-schema-operator-skeleton-multi-tenancy/01-11-SUMMARY.md "Issues Encountered".

- **Phase 2 spec shift (HALTED 2026-05-16):** Hub spec adopted Postgres-as-SoT model (commit 78a0f1d) mid-execution. Phase 2 plans 02-03/05/06/07 were authored against the prior K8s-informer-as-SoT model and require replanning. Specifically:
  - 02-03 (DB layer) must add 6 new `<kind>_objects` tables (environment/plugin/prompt/artifact/pluginmarketplace/backendidentitypolicy), `notify_object_change()` PL/pgSQL trigger function + 6 per-table triggers (channel `ach_object_change`), 4 component-scoped Postgres roles (ach_operator/ach_platform_api/ach_forwarder/ach_content_service), and `force_refresh_requested_at` columns on `external_refs` and `marketplace_plugins`.
  - 02-05 (Plugin/Prompt/Artifact reconcilers) must dual-write status to K8s `.status` AND `<kind>_objects.status_json`; force-refresh now polls `force_refresh_requested_at` instead of consuming an annotation.
  - 02-06 (PluginMarketplace reconciler) must dual-write to `pluginmarketplace_objects`; same force-refresh shift.
  - 02-07 (LiteLLM snapshot + Environment derivation) must dual-write Environment status to `environment_objects.status_json`.
  - 02-09 (cmd/operator wire-up) must select the `ach_operator` Postgres role at startup; no LISTEN required for the Operator.
  - New scope to consider folding into Phase 2 vs deferring: §15.7 UI Objects API, §15.8 GitOps Export, BackendIdentityPolicy → backendidentitypolicy_objects sync.
  - Rogue 02-02 task-1 commit (4d4568b) reverted as b2b9eec — source-fetcher framework was unaffected by the spec shift but needed to be re-derived inside the replan inventory rather than carried forward partially.
  - Resume path: `/gsd:plan-phase 02 --replan` (or equivalent), then `/gsd:execute-phase 02 --wave 1`.

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| *(none)* | | | |

## Session Continuity

Last session: 2026-05-29T19:16:07.260Z
Stopped at: Completed 07-W5-03: CR-03 closed; W6-01 checkpoint next
Resume file:
None
Resume action:

1. ~~`/gsd-phase --insert 02.2 UAT pivot to kind+Helm`~~ — done (Phase 02.3 inserted, commit `b16c18f`).
2. ~~`/gsd-discuss-phase 02.3`~~ first version — done (commit `1685953`, scope=kind+helm UAT scripts only). Superseded by re-scope.
3. ~~`/gsd-discuss-phase 02.3`~~ re-scoped — done this session. CONTEXT.md + DISCUSSION-LOG.md rewritten with full tier-1/2/3 framework + dev-iteration principles. 32 decisions (D-01..D-32). 12 open items for researcher. Goal: replace test/e2e/, scripts/uat-*.sh, scripts/e2e-kind.sh with test/tier2/ (Ginkgo) + test/tier3/ (CRD gen+validate) + scripts/cluster.sh + ~25 Makefile dev-loop targets.
4. **Next:** `/gsd-plan-phase 02.3` — phase-researcher resolves 12 open items (ToolHive Service DNS pattern, chart version pins, LiteLLM mcp_servers schema, etc.), then planner produces wave-grouped plans. Likely 8-12 plans across 4-5 waves.
5. `/gsd-execute-phase 02.3` — build it (~4-5 weeks).
6. Once 02.3 lands and Tier 2 + Tier 3 are green: resume `/gsd-execute-phase 3 --auto`.

Open scope concern to surface at plan-phase: the original Phase 02.3 ROADMAP entry still describes "UAT pivot to kind+Helm" — narrower than the actual rescoped phase. Update ROADMAP.md entry as part of plan-phase or first execute commit.

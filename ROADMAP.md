# Roadmap: ACH — Agent Capability Hub (v1alpha1)

## Overview

ACH ships as a coordinated Hub + CLI in seven phases. The Hub is built bottom-up — CRDs and Postgres schema first, then the Operator's external-reference + marketplace machinery (the substrate that feeds Content Service), then the user-facing Platform API with the full `pk_`/`ek_` identity model, then the Forwarder with its JWT trust path, then the Content Service with cross-component observability. The CLI then lands in two phases: foundation (config, login, env-keys, admin commands) followed by the hydrate engine with all four platform adapters, safe extraction, state management, and distribution. By Phase 7 a fresh `ach login` against a deployed Hub followed by `ach hydrate --environment <env>` produces a working AI-agent workspace for any of Claude Code, Codex CLI, Gemini CLI, or OpenCode — the Core Value spelled in PROJECT.md.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Foundation — CRDs, DB Schema, Operator Skeleton, Multi-tenancy** - Repo bootstrap, all six CRDs with CEL admission, Postgres schema with peppered HMAC credentials, Operator scaffold with finalizer drain and cache layout, namespace-scoped RBAC. — completed 2026-05-15
- [x] **Phase 2: External Refs + Marketplace + Operator Reconciliation** - External-reference refresh on RWO PVC with atomic publication, three-stage marketplace refresh, plugin size-cap enforcement, ExecutionResourcesResolved against LiteLLM REST, orphan-key cleanup loop. (completed 2026-05-17)
- [x] **Phase 3: Hub Identity & Platform API** - Dex SSO, `pk_`/`ek_` lifecycle with asymmetric revocation, hydrate endpoint, env-key + environment listing endpoints, admin endpoints with allowlist + force-refresh patch, structured JSON audit events. (completed 2026-05-21; verifier PASS 38/38 must-haves; live UAT engineer-pending)
- [ ] **Phase 4: Hub Forwarder & JWT Trust Path** - Forwarder skeleton on `/v1`/`/gemini`/`/mcp`/`/a2a`, complete header rewrite contract, JWKS publication, Ed25519 signing with manual rotation, BackendIdentityPolicy informer + alphabetical duplicate-target rule.
- [ ] **Phase 5: Content Service + Cross-component Observability** - Content Service streaming via `sendfile(2)`, scope-aware authorization for `pk_` vs `ek_`, marketplace name resolution, full Prometheus metric set across all four Hub components.
- [ ] **Phase 6: CLI Foundation** - `ach` Go binary skeleton with multi-deployment registry, synthetic mode, login/logout/whoami/config/env/env-keys command surface, admin commands with exit-3 semantics.
- [ ] **Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution** - Hydrate commit sequence with lock + atomic state v2, dual-hash drift detection, four platform adapters with merge strategies, safe tar extraction, OCI image + standalone binaries + Helm chart + InitContainer reference.

## Phase Details

### Phase 1: Foundation — CRDs, DB Schema, Operator Skeleton, Multi-tenancy

**Goal**: A deployer can `kubectl apply -f` the ACH CRDs, install the Operator into a namespace, create an `Environment`/`Plugin`/`Prompt`/`Artifact`/`PluginMarketplace`/`BackendIdentityPolicy`, and watch CEL admission accept valid specs and reject invalid ones — with Postgres tables created, finalizers attached, and the Operator's RBAC scoped to its own namespace.
**Depends on**: Nothing (first phase)
**Requirements**: CRD-01, CRD-02, CRD-03, CRD-04, CRD-05, CRD-06, CRD-07, CRD-08, DB-01, DB-02, DB-03, DB-04, DB-05, DB-06, OP-01, OP-02, OP-04, OP-05, OP-10, OP-11, OP-12, MULTI-01, MULTI-02, MULTI-03, MULTI-04
**Success Criteria** (what must be TRUE):

  1. `kubectl apply` of valid `Environment`/`Plugin`/`PluginMarketplace`/`Artifact`/`Prompt`/`BackendIdentityPolicy` CRs succeeds; missing `refresh.maxStaleness`, an `Artifact` of `type: http` with `scope: directory`, or a `BackendIdentityPolicy` with no `forwardIdentityJWT` is rejected by CEL admission with a readable error.
  2. The Operator + Content Service Pod runs as a single replica with `strategy: Recreate`, mounts an RWO PVC at `/var/cache/ach`, completes informer warmup, and reports Ready; `kubectl describe pod` shows two containers and one PVC.
  3. Deleting an `Environment` triggers the §6.5 drain — `deletionTimestamp` set, finalizer held while LiteLLM access-group + tag are removed (stubbed in Phase 1, real in Phase 2), `ek_` rows drained, then finalizer removed; the same drain pattern applied to external-reference CRDs removes their cached file before clearing the finalizer.
  4. `psql` against the deployed Postgres shows all four ACH tables with `pkid_`/`ekid_` `key_id` PKs, UNIQUE on `credential_hash` columns, and no plaintext key data anywhere; the HMAC pepper is sourced from a Kubernetes Secret outside the database.
  5. RBAC inspection confirms only the Operator's ServiceAccount carries write verbs on `ach.ackstorm.ai` kinds; other components have `get/list/watch` only (Platform API will gain `patch` for the force-refresh annotation in Phase 3); cross-namespace access is impossible.

**Plans**: 11 plans
Plans:
**Wave 1**

- [x] 01-01-PLAN.md [wave 1] — Repo bootstrap: kubebuilder init, go.mod, Makefile, .golangci.yml, hack/boilerplate (D-01, D-02) — completed 2026-05-15 (commit b79c625)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 01-02-PLAN.md [wave 2] — Six CRD kinds (Environment, Plugin, PluginMarketplace, Artifact, Prompt, BackendIdentityPolicy) with CEL admission rules (CRD-01..CRD-08)
- [x] 01-03-PLAN.md [wave 2] — Postgres §16 schema migration + internal/db pgxpool wrapper (DB-01..DB-06) — completed 2026-05-15 (commits e1e188c, 74006da)
- [x] 01-04-PLAN.md [wave 2] — internal/credhash HMAC-SHA-256 + constant-time hex compare (DB-03, DB-04; TDD) — completed 2026-05-15 (commits f5dbb6c, d0a3a34)
- [x] 01-07-PLAN.md [wave 2] — internal/cachefs.EnsureLayout PVC directory bootstrap (OP-10, OP-11) — completed 2026-05-15 (commits f1ff33e, 60d26e4)

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 01-05-PLAN.md [wave 3] — Six reconcilers with finalizers + internal/litellm.Client interface + NoopClient (OP-02, OP-04, OP-12, CRD-06, CRD-07)

**Wave 4** *(blocked on Wave 3 completion)*

- [x] 01-06-PLAN.md [wave 4] — Per-binary cmd/*/main.go tree (operator + 3 stubs + CLI stub) + internal/config helpers
- [x] 01-09-PLAN.md [wave 4] — Per-component RBAC manifests (4 SAs + Role + RoleBinding triplets, namespace-scoped) (MULTI-01..MULTI-04, OP-01)

**Wave 5** *(blocked on Wave 4 completion)*

- [x] 01-08-PLAN.md [wave 5] — Operator+ContentService Pod (2 containers, RWO PVC, Recreate, migrate init container) + stub Deployments + Secrets (OP-02, OP-05, OP-10)
- [x] 01-10-PLAN.md [wave 5] — 5 multi-stage Dockerfiles + cmd/migrate/main.go + docker-compose.yml (local dev) — completed 2026-05-15 (commits 29bfe47, 6f24cb3, 7e9bd16)

**Wave 6** *(blocked on Wave 5 completion)*

- [x] 01-11-PLAN.md [wave 6] — envtest + testcontainers-go + e2e integration tests covering all five Phase 1 Success Criteria — completed 2026-05-15 (commits 0e0aaeb, 091a130, 2dc9f7e, 5228eaf, 18c62df)

**UI hint**: no

### Phase 2: External Refs + Marketplace + Operator Reconciliation

**Goal**: The Operator continuously reconciles external content into the cache PVC: it fetches from `github`/`gitlab`/`bitbucket`/`s3`/`gcs`/`http`, publishes via atomic `rename(2)`, runs the three-stage marketplace refresh with anchored RE2 filters, enforces the plugin size cap, queries LiteLLM REST for `ExecutionResourcesResolved`, and reaps orphan LiteLLM keys on a configurable interval.
**Depends on**: Phase 1 (CRDs, Operator skeleton, cache layout, DB schema, finalizers)
**Requirements**: OP-03, OP-06, OP-07, OP-08, OP-09, OP-13, OP-15
**Success Criteria** (what must be TRUE):

  1. Creating a `Plugin` with `type: github` and a branch ref leads to a published `plugin/<name>.tar.gz` under the PVC root within one reconcile; killing the Operator between staging-write and the row UPDATE leaves no torn-byte file (re-reconcile detects the missing row and republishes idempotently).
  2. A `PluginMarketplace` referencing a Claude-Code-shaped marketplace file produces `marketplace/<name>/plugin/<plugin-name>.tar.gz` for each included plugin; an unsupported `npm` source flips `Synced=False, reason=UnsupportedPluginSource` without aborting the rest; a one-plugin upstream failure is recorded in `status.message` and other plugins still succeed; vanished plugin names are DELETE-swept.
  3. Two `PluginMarketplace` CRs exposing the same plugin name resolve via alphabetical priority on `metadata.name`; the loser reports `Synced=False, reason=NameConflict`; a `Plugin` CRD with the same name beats both marketplace entries.
  4. The Operator refuses to start when `ACH_PLUGIN_MAX_SIZE_MIB` is `0`, negative, or non-numeric; an oversized plugin flips `Synced=False, reason=PluginTooLarge` (`SourceReachable` stays `True` — the bytes were reached, just too large, per the `conditions.go` matrix) and the cached file is not produced. Verified deterministically at unit + envtest level (`TestMaterializeExternalRef_PluginTooLarge`, `TestPMR_Stage2_PluginTooLarge`, cmd/ach config tests) where the byte size is injected exactly; not re-asserted at e2e (no real plugin reliably exceeds the 1 MiB minimum cap once the pluginpack filter strips it — see #57).
  5. With LiteLLM reachable, an `Environment.spec.runtime.models[]` referencing a non-registered model surfaces in `status.unresolvedRuntime` and `ExecutionResourcesResolved=False, reason=ResourceUnresolved`; the orphan-key cleanup loop runs at the configured interval, refuses to start below the 5-minute floor, emits an audit event per revocation, and aborts cleanly on LiteLLM-unreachable.

**Plans**: 9 plans
Plans:
**Wave 1** *(parallel — foundations; no inter-plan dependencies)*

- [x] 02-01-PLAN.md [wave 1] — Lift sister internal/litellm package + ACH_LITELLM_* env renames + widen Client interface (D-01, D-02)
- [x] 02-02-PLAN.md [wave 1] — Six source-type fetcher SDKs (github/gitlab/bitbucket/s3/gcs/http) + Fetcher contract + package-legitimacy gate (D-04, D-05)
- [x] 02-03-PLAN.md [wave 1] — Schema migration 000002 (litellm_user_id columns) + internal/db helpers (UpsertExternalRef, UpsertMarketplacePlugin, ListMarketplacePlugins, DeleteMarketplacePlugin, ResetExternalRefRefreshOnEmptyCache, ListACHManagedLitellmUsers)
- [x] 02-04-PLAN.md [wave 1] — internal/audit handler (D-17 stdout-JSON + audit=true) + cachefs.IsEmpty + cachefs.SweepTmp helpers

**Wave 2** *(blocked on Wave 1 completion; parallel within wave)*

- [x] 02-05-PLAN.md [wave 2] — Plugin/Prompt/Artifact reconciler steady-state expansion (§10.3 fetch→stage→fsync→rename→UPSERT) + shared materializeExternalRef helper + ExternalRefStatus.UpstreamRev field (OP-03, OP-09, D-12)
- [x] 02-07-PLAN.md [wave 2] — LiteLLM snapshot Runnable + Environment.Reconcile ExecutionResourcesResolved derivation + Environment.status.unresolvedRuntime field (OP-13, D-13, D-14)
- [x] 02-08-PLAN.md [wave 2] — Orphan LiteLLM key cleanup Runnable (D-15 interval validation, D-16 per-tick procedure, D-18 audit event shape) (OP-15)

**Wave 3** *(blocked on 02-05; depends on shared conditions.go + materializeExternalRef)*

- [x] 02-06-PLAN.md [wave 3] — PluginMarketplace three-stage refresh + anchored RE2 filters + cross-marketplace name-conflict resolution (OP-06, OP-07, OP-08)

**Wave 4** *(blocked on every prior wave; final operator wiring)*

- [x] 02-09-PLAN.md [wave 4] — cmd/operator/main.go wiring: NoopClient→RESTClient swap, audit logger, source-fetcher registry, OP-11 cache-recovery, Secret informer pre-warm, three new Runnables registered, five reconciler injections + MustEnvDurationAtLeast helper

**UI hint**: no

### Phase 02.1: kind e2e overlay — config/dev-postgres + config/e2e + scripts/e2e-kind.sh to exercise Phase 02 SC#1-4 against a live kind cluster (matches sister project pattern; closes the Phase 1 manifest gap that ships no in-cluster Postgres) (INSERTED)

**Goal:** Ship the three local-cluster e2e assets (config/dev-postgres component, config/e2e overlay, scripts/e2e-kind.sh orchestrator + Makefile e2e-kind target) plus test/e2e/phase2_invariants_test.go (TestPhase2Invariants with 4 subtests) so Phase 02 SC#1–4 are verified-live against a kind cluster — closing the documented gap in 02-HUMAN-UAT.md without re-opening Phase 02. No new requirement IDs; this is the live-verification path for OP-03/06/07/08/09. OP-13 deliberately NOT claimed (scope_fence excludes SC#5/LiteLLM live; that work belongs to Phase 3 when a real LiteLLM Pod arrives).
**Requirements**: OP-03, OP-06, OP-07, OP-08, OP-09
**Depends on:** Phase 2
**Plans:** 1 plan

Plans:
- [x] 02.1-01-PLAN.md — kind e2e overlay + Phase 2 invariants test (config/dev-postgres + config/e2e + scripts/e2e-kind.sh + Makefile e2e-kind + test/e2e/phase2_invariants_test.go + 6 fixtures incl. marketplace_fixture_server.yaml)

**Status:** COMPLETE 2026-05-18 — `make e2e-kind` exits 0 from clean state; Phase 02 SC#1–4 transitioned pending → verified-live; HTTPS-only invariant on HTTPSource lifted to admit the hermetic in-cluster fixture-server path.

### Phase 02.2: Phase 02 cleanup — Gap G1 fix + LiteLLM-real UAT path + invariant-relaxation docs (INSERTED)

**Goal:** Close the deferred items from Phase 02.1 SUMMARY before Phase 3 arrives. Three concerns bundled into one phase, three plans:

1. **Plan 1 — Fix Gap G1 (`ListUserKeys` endpoint mismatch).** `internal/litellm/keyinfo.go:51` calls `/key/info?user_id=...` which LiteLLM v1.83.10 returns 404 for; the correct endpoint is `/key/list?user_id=...&return_full_object=true&include_team_keys=false`. Swap endpoint + update response struct shape (`token` field is the LiteLLM-internal key id, not `key_id`). Reconcile the namespace mismatch between ACH's `pkid_*`/`ekid_*` prefix and LiteLLM's opaque hex token via a translation column OR adapter logic.

2. **Plan 2 — LiteLLM-real UAT path via docker-compose.** Add a `litellm` service to `docker-compose.yml` (mirroring the Phase 02 SC#5 spike pattern: `docker-compose.spike.yaml` + `scripts/uat-sc5.sh`). Author `scripts/uat-g1.sh` that spins up the compose stack, seeds a user-key, lets the orphan loop tick once against the real LiteLLM, and asserts the audit-event shape. Kind overlay stays unchanged (LiteLLM intentionally unreachable; Phase 1 SC#3 stays SKIP under kind).

3. **Plan 3 — Docs update for invariant relaxations.** Phase 02.1 permanently lifted two Phase 02 invariants. Update the docs to match reality: (a) `CLAUDE.md` — clarify "HTTPS-only via deployment-configured ACH_BASE_URL" applies to Platform API base URL only, NOT to upstream HTTP sources (which now admit http://). (b) `internal/sources/http/fetcher.go` package doc + T-02-02-03 references throughout `.planning/phases/02-*/` — mark as lifted with cross-ref to Phase 02.1 commit `45b7558`. (c) Add a note that SCM `authSecretRef` is now optional (commit `94f24b5`) so future contributors don't reintroduce the requirement.

**Requirements:** none new. This phase closes verification debt against existing OP-13 (orphan-cleanup audit event per revocation, currently unverifiable until G1 fix lands) and documents two architectural concessions taken in Phase 02.1.

**Depends on:** Phase 02.1 (HTTPS-only and SCM-auth invariants must already be lifted; SUMMARY exists to reference).

**Plans:** 3 plans

Plans:
- [x] 02.2-01-PLAN.md — Fix Gap G1 (ListUserKeys endpoint + struct shape + namespace translation)
- [x] 02.2-02-PLAN.md — LiteLLM-real UAT via docker-compose (`scripts/uat-g1.sh`)
- [x] 02.2-03-PLAN.md — Docs update (CLAUDE.md + T-02-02-03 references + SCM-auth note)

**UI hint:** no

### Phase 02.3: UAT pivot to kind+Helm — SC#5 stdlib port + docker-compose deletion (INSERTED, re-scoped 2026-05-20)

**Goal:** Close the gap between the sister-adopted kind+Helm test infrastructure (commit `e1b943c`) and the deletion of every docker-compose-era UAT artifact. Three concrete deliverables: (1) port the assertions from the docker-compose-era `scripts/uat-sc5.sh` (Phase 02 SC#5 orphan cleanup + interval floor) and `scripts/uat-g1.sh` (Gap G1 audit JSON wire form) into `test/e2e/phase2_sc5_orphan_test.go` running against the in-cluster LiteLLM Helm release; (2) delete `scripts/uat-g1.sh`, `scripts/uat-sc5.sh`, `scripts/litellm-config.yaml`, `scripts/e2e-kind.sh`, `docker-compose.yml`, `internal/orphan/uat_test.go` and strip the matching Makefile targets (`dev-up`, `dev-down`, `uat-sc5`, `e2e-kind`, `e2e-kind-cleanup`, `DOCKER_COMPOSE`); (3) run `make e2e-focus FOCUS=TestPhase2SC5Orphan` against a real kind+Helm cluster, fix any Service-name constant if a chart convention differs, insert a forward-pointer footnote on `02-HUMAN-UAT.md` (per D-11-v3 — Phase 02 doc otherwise immutable), and write the `feedback_023_tier_framework_rejected.md` user-memory to prevent a future planner re-proposing tier-2/Ginkgo. The 2026-05-19 proposal to adopt a full tier-1/2/3 framework with Ginkgo + `test/tier2/` + `config/uat/` + ~25 dev-iteration Makefile targets was REJECTED by the user on 2026-05-20 — stdlib `test/e2e/` is the canonical e2e surface for ACH.
**Requirements:** No new REQ-IDs (test infrastructure; serves Phase 02 SC#5 live verification).
**Depends on:** Phase 02.2 (Postgres-as-SoT, `litellm_token` migration 000003, audit handler at `internal/audit/handler.go:45`) + the sister-adopted commit `e1b943c` substrate (`scripts/cluster.sh`, `test/e2e/cluster/`, Helm chart pins).
**Plans:** 3 plans across 2 waves (~1-2 days of focused work)
**Out of scope:** Ginkgo v2 migration, `test/tier2/` tree, `config/uat/` overlay, scripted Tier 3, ~25 dev-iteration Makefile target bouquet, sister's `litellm-operator` chart in-cluster, ToolHive MCP fixtures as test data, vendored OCI chart tarballs.

Plans:
**Wave 1** *(parallel — retrospective documentation; no working-tree mutations)*

- [ ] 02.3-01-PLAN.md [wave 1] — RETROSPECTIVE: SC#5 stdlib port (`test/e2e/phase2_sc5_orphan_test.go`) acceptance criteria + `phase2_invariants_test.go` header tweak (orchestrator step 2 already on disk)
- [ ] 02.3-02-PLAN.md [wave 1] — RETROSPECTIVE: docker-compose-era artifact deletion set (6 files via `git rm`) + Makefile strip + 3 comment cleanups (orchestrator step 3 already on disk)

**Wave 2** *(executor work — live validation + commit)*

- [ ] 02.3-03-PLAN.md [wave 2] — Live validation: `make e2e-focus FOCUS=TestPhase2SC5Orphan` against kind+Helm cluster + 02-HUMAN-UAT.md footnote (D-11-v3) + `feedback_023_tier_framework_rejected.md` memory write + MEMORY.md index + 1-2 atomic commits

### Phase 3: Hub Identity & Platform API

**Goal**: A user can complete a Dex SSO login against the Platform API, receive a `pk_`, list authorized Environments, mint and revoke `ek_` keys, hydrate against any authorized Environment, and (if listed in the admin allowlist) force-refresh content and revoke arbitrary keys — with every operation emitting a structured audit event and never leaking plaintext.
**Depends on**: Phase 1 (CRDs, DB, Operator), Phase 2 (Operator must publish `AccessGroupSynced=True` and exec-resource status before `ek_` create can succeed)
**Requirements**: KEY-01, KEY-02, KEY-03, KEY-04, KEY-05, KEY-06, KEY-07, KEY-08, KEY-09, KEY-10, KEY-11, API-01, API-02, API-03, API-04, API-05, API-06, API-07, API-08, API-09, API-10, API-11, API-12, OBS-01, OBS-02
**Success Criteria** (what must be TRUE):

  1. A first-time SSO login creates a LiteLLM user, adds them to the `default` Team, and returns a `pk_` plaintext exactly once; missing `default` Team yields `500 default_team_missing` with audit `outcome=default_team_missing`; ACH never sets a default `max_budget` on the new LiteLLM user.
  2. `POST /platform/hydrate` accepts both `pk_` and `ek_` per the §15.1 contract — `pk_` requires body `environment` (else `400 missing_environment`), `ek_` makes it optional (mismatch → `403 wrong_environment`); response carries `schemaVersion: "v1alpha1"` with both `runtime` and `context` blocks always present (with `[]` when empty) and never contains `pk_`/`ek_` plaintext.
  3. `POST /platform/env-keys` runs the §8.2 8-step flow — verifies Environment is non-terminating (else `404 environment_not_found`), waits for `AccessGroupSynced=True` via the informer cache (else `503 not_ready`), idempotently verify-or-create the LiteLLM user, create-then-insert with rollback on DB failure — and returns `key_id=ekid_…` + `plaintext` exactly once.
  4. Asymmetric revocation works as specified: a `pk_` revoked through `/platform/admin/keys/revoke` flips Postgres first then LiteLLM then invalidates Redis (Postgres flip is the visible barrier); an `ek_` revoke calls LiteLLM first then flips DB then invalidates Redis (LiteLLM is the runtime barrier). `DELETE /platform/env-keys/{key_id}` returns `204` only after LiteLLM acknowledges.
  5. Admin endpoints reject any `ek_` with `401 invalid_key_type`; non-allowlisted callers get `403 not_admin` BEFORE any other validation; the allowlist is read at process start from `/etc/ach/admins/admins.txt` and ConfigMap edits require a restart; `/platform/admin/refresh` patches `ach.ackstorm.ai/force-refresh` (the only non-Operator write to ACH CRDs) and returns `202 Accepted`.
  6. Every `pk_`/`ek_` create+revoke, every `Environment` create/update/delete, every hydrate, and every admin operation emits a structured JSON audit event with `actor=<namespace>/<sso-email>`, `outcome` from the §18.2 enum, `request_id`, and `key.id` in `pkid_`/`ekid_` form — never plaintext, never the credential hash; sliding-window pk_ extension is NOT its own event.

**Plans**: 12 plans
Plans:
**Wave 1** *(parallel — foundations; no inter-plan dependencies)*

- [x] 03-01-PLAN.md [wave 1] — LiteLLM client extensions (UserNew, UserInfoByEmail, TeamMemberAdd, KeyGenerate) — API-02 — completed 2026-05-20
- [x] 03-02-PLAN.md [wave 1] — Audit events package + Render JSON envelope (OBS-01, API-12) — completed 2026-05-20
- [x] 03-03-PLAN.md [wave 1] — DB helpers: PkCheckAndExtend, EkResolve, personal_keys/environment_keys CRUD, ListActiveACHKeyTokens (KEY-04, KEY-06) — completed 2026-05-20
- [x] 03-04-PLAN.md [wave 1] — internal/keys: bearer + keyID generators + ClassifyBearer (KEY-01) — completed 2026-05-20
- [x] 03-06-PLAN.md [wave 1] — internal/platformapi/store: informer-backed Environment reader (API-08) — completed 2026-05-20

**Wave 2** *(blocked on Wave 1)*

- [x] 03-05-PLAN.md [wave 2] — keystore Resolver + Redis cache + middleware chain (KEY-02) — completed 2026-05-20

**Wave 3** *(blocked on Wave 2 — handler endpoints; parallel)*

- [x] 03-07-PLAN.md [wave 3] — Dex SSO handler (LoginHandler, CallbackHandler) (KEY-03, KEY-09, KEY-10) — completed 2026-05-20
- [x] 03-08-PLAN.md [wave 3] — env-keys handlers (Create/List/Get/Revoke; §8.2 + §8.5) (KEY-05, KEY-08, KEY-11, API-05, API-06, API-07) — completed 2026-05-20
- [x] 03-09-PLAN.md [wave 3] — Environments list + Hydrate handlers (API-03, API-04) — completed 2026-05-20
- [x] 03-10-PLAN.md [wave 3] — Admin handlers (allowlist + revoke + force-refresh; pk_ DB-first per KEY-07) (KEY-07, API-09, API-10, API-11) — completed 2026-05-20

**Wave 4** *(blocked on Wave 3 — wiring)*

- [x] 03-11-PLAN.md [wave 4] — cmd/platform-api/main.go rewrite + chi server + RBAC amendment + docker-compose dex profile (API-01) — completed 2026-05-20 (docker-compose superseded by kind+helm per Phase 02.3)

**Wave 5** *(terminal — invariants suite)*

- [x] 03-12-PLAN.md [wave 5] — Phase 3 invariants test suite (SC#1..SC#6) + scripts/uat-phase3.sh (OBS-02) — completed 2026-05-21

**UI hint**: no

### Phase 4: Hub Forwarder & JWT Trust Path

**Goal**: The Forwarder accepts runtime traffic on `/v1`/`/gemini`/`/mcp`/`/a2a`, resolves `x-ach-key` via Redis→Postgres, performs the §5.1 step-4 pre-check on `/mcp`/`/a2a`, strips and rewrites every relevant header on every route, and signs an Ed25519 ACH JWT only when a matching `BackendIdentityPolicy` opts in — with JWKS published at `/.well-known/jwks.json` and a manual rotation procedure that respects the 24-hour overlap.
**Depends on**: Phase 1 (CRDs, DB, RBAC), Phase 3 (Postgres `personal_keys`/`environment_keys` rows must exist for the Forwarder to resolve real keys; admin allowlist concept established)
**Requirements**: FWD-01, FWD-02, FWD-03, FWD-04, FWD-05, FWD-06, FWD-07, FWD-08, FWD-09, FWD-10, FWD-11, OP-14, OP-16
**Success Criteria** (what must be TRUE):

  1. `curl -H "x-ach-key: <pk_…>" $ACH_BASE_URL/v1/chat/completions` resolves the key from Redis (≤60s TTL) with Postgres on miss, strips the client `Authorization`, every `x-litellm-*`, every `x-ach-*`, and every hop-by-hop header listed in §5.1, then writes `x-litellm-api-key: <shared>` and `x-litellm-key-id: <virtual>`; LiteLLM rejects the shared key without `x-litellm-key-id`; the access log shows the request only after `x-ach-key` redaction with no raw plaintext.
  2. A request to `/mcp/<name>` carrying an `ek_` whose bound Environment lacks `<name>` in `spec.runtime.mcpServers[]` returns `403 unauthorized_resource` with no JWT signed and no LiteLLM forward; a `pk_` to the same endpoint queries LiteLLM Team memberships (`503 litellm_unreachable` on outage) and grants/denies accordingly. `pk_` traffic carries no Environment tag; `ek_` traffic carries the Environment attribution tag via LiteLLM's tag mechanism.
  3. With a `BackendIdentityPolicy{target: {kind: MCPServer, name: <X>}, forwardIdentityJWT: true}` present, requests to `/mcp/<X>` carry `Authorization: Bearer <ACH-JWT>` whose header is `{alg: EdDSA, kid, typ: JWT}` and claims include `iss=$ACH_BASE_URL`, `sub=<owner-email>`, `aud=mcp:<X>`, `exp=iat+120s`, no `jti`. Without the policy or with `forwardIdentityJWT: false`, the request is forwarded with NO `Authorization` header. Two policies for the same target resolve via alphabetical `metadata.name` (alphabetically-LAST wins); duplicates coexist without status churn (no `DuplicateTarget` reason emitted — see TODO.md §6 + `feedback_bip_no_shadow_logic.md`).
  4. `GET $ACH_BASE_URL/.well-known/jwks.json` returns `application/jwk-set+json` with `Cache-Control: public, max-age=3600`, anonymously accessible, containing only OKP/Ed25519 JWKs with `kid`/`x` (and optionally `use: sig`/`alg: EdDSA`); the signing material lives in a single Kubernetes Secret `ach-jwt-signing-keys` that only the Forwarder ServiceAccount can read; the documented rotation procedure (publish-overlap-revoke) executes without a backend cache miss.
  5. The Forwarder refuses to start when `ACH_BASE_URL` does not begin with `https://`; the Operator (`OP-16`) is the sole writer of `BackendIdentityPolicy.status` and the Forwarder reads `spec` only via informer (no `status` reads at request time), keeping runtime authority decoupled from status-write latency.

**Plans**: TBD
**UI hint**: no

### Phase 5: Content Service + Cross-component Observability

**Goal**: `GET /content/{kind}/{name}` streams the right cached file for any caller authorized for the resolved Environment, never buffering a full body, with `pk_` running §7.1 check-and-extend first and `ek_` resolving Redis→Postgres; and every Hub component exposes the full normative Prometheus metric set from §18.5.
**Depends on**: Phase 1 (cache layout on RWO PVC, DB tables), Phase 2 (cached files actually present), Phase 3 (`pk_`/`ek_` rows + Environment.spec.context resolution + audit conventions)
**Requirements**: CS-01, CS-02, CS-03, CS-04, CS-05, CS-06, CS-07, CS-08, CS-09, CS-10, CS-11, OBS-03, OBS-04, OBS-05, OBS-06
**Success Criteria** (what must be TRUE):

  1. `curl -H "x-ach-key: <pk_…>" -H "x-ach-environment: <env>" $ACH_BASE_URL/content/plugin/<name>` returns `200` with `Content-Type: application/gzip`, exact `Content-Length`, `Cache-Control: no-store`, identity transfer encoding, and a body streamed via `sendfile(2)` (verified with `strace`); `Range`/`If-None-Match`/`If-Modified-Since` headers are silently ignored — full body, no `206`.
  2. `pk_` requests without `x-ach-environment` get `400 missing_environment`; `pk_` whose LiteLLM Teams don't intersect `Environment.spec.authorizedTeams[]` get `403 unauthorized_team`; `ek_` whose bound Environment doesn't match the (optionally-supplied) header get `403 wrong_environment`; a name not in `spec.context.<kindPlural>[]` yields `403 unauthorized_content`; an unknown name yields `404 content_not_found`; LiteLLM-unreachable on Team-cache miss yields `503 litellm_unreachable`.
  3. Plugin name resolution prefers a `Plugin` CRD with the requested `metadata.name`; on absence, the alphabetically-lowest `marketplace_name` wins; resolution executes against Postgres on every request; an Environment in deletion drain still serves content until fully removed (then `404 content_not_found`).
  4. Staleness check returns `503 stale_cache_expired` when `now - last_successful_refresh > max_staleness` for the underlying `external_refs` or `marketplace_plugins` row; an in-flight Content Service read against an old inode finishes successfully even when the Operator atomically publishes a new revision mid-request.
  5. `GET /metrics` on each of Platform API, Forwarder, Content Service, Operator returns the full §18.5 metric set: `ach_forwarder_requests_total{route, key_type, outcome}`, `ach_forwarder_jwt_signed_total{kind}`, `ach_forwarder_jwt_suppressed_total{kind, reason}`, `ach_content_service_requests_total{kind, outcome}`, `ach_content_service_bytes_served_total{kind}`, `ach_litellm_unreachable_total{caller}` (one counter spanning all four callers with no body/status/audit asymmetry), with no per-request labels (no `request_id`, no `owner_email`).

**Plans**:
- `docs/plans/2026-05-26-content-service-routes.md` — v1alpha1 anonymous routes (Hub §15.2 minimal contract per TODO §8). Phase 5 will layer `pk_`/`ek_` auth, environment scoping, marketplace resolution, staleness checks, and the §18.5 metric set on top.

**UI hint**: no

### Phase 6: CLI Foundation

**Goal**: A user can `ach login` against a deployed Hub, see the deployment registered in `~/.config/ach/config.yaml` with `0600`, run `ach whoami --verify`/`ach env list`/`ach env describe`/`ach env-keys {create,list,revoke}`, switch between deployments, run admin commands with exit-3 semantics on `403 not_admin`, and operate in synthetic mode (no config file) when `ACH_BASE_URL` + `ACH_API_KEY` are set.
**Depends on**: Phase 3 (Platform API endpoints must be live for the CLI to talk to), Phase 5 (`ach env describe` calls hydrate which expects working content path; admin force-refresh requires CRD patch)
**Requirements**: CLI-01, CLI-02, CLI-03, CLI-04, CLI-05, CLI-06, CLI-07, CLI-08, CLI-09, CLI-10, CLI-11, CLI-12, CLI-13
**Success Criteria** (what must be TRUE):

  1. `ach login` against a fresh deployment prompts for name + URL, completes Dex SSO, returns the `pk_` plaintext exactly once, writes `deployments.<name>.pk` to `~/.config/ach/config.yaml` with mode `0600` (parent dir `0700`), and sets `default:` if absent; non-HTTPS URLs are refused on read or write; a more-permissive file mode warns and is normalized on next write.
  2. `ach whoami --verify` performs asymmetric verification — `pk_` calls `GET /platform/environments?limit=1`, `ek_` calls `POST /platform/hydrate {}` — exiting `0` on `200`, `3` on `401`, `6` on network failure; verbose-mode HTTP logs redact `x-ach-key` to `<prefix>_***`; plaintext is only ever shown at the one-time return of `ach login` or `ach env-keys create`.
  3. Synthetic mode activates only when BOTH `ACH_BASE_URL` set AND a credential resolves from `ACH_API_KEY`/`--api-key`; in synthetic mode `ach login`/`config`/`logout`/`env-keys create --save-as` exit `1`, `--deployment`/`ACH_DEPLOYMENT` are rejected with exit `1`, and state files record `"deployment": "(env)"`; half-set synthetic config exits `1`.
  4. `ach env-keys revoke <ekid_…>` runs the §8.5 flow (LiteLLM-first), interactively confirms unless `--yes`; raw plaintext keys are rejected with `400 invalid_argument`; `ach admin keys revoke` accepts both `pkid_` and `ekid_`; `ach admin {keys revoke,users revoke-keys}` exits `3` on `403 not_admin`.
  5. `ach hydrate --environment <env>` with a `pk_` emits the §6.6 stderr warning (suppressed by `--no-warnings`) and requires `--environment` (omission exits `1`); with an `ek_` `--environment` is optional. Mutually-exclusive credential sources (`--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY`) exit `1` when more than one is presented.

**Plans**: TBD
**UI hint**: no

### Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution

**Goal**: The Core Value path works end-to-end: `ach hydrate --environment <env>` against a deployed Hub for any of the four shipped platforms (Claude Code, Codex, Gemini CLI, OpenCode) produces a working AI-agent workspace with safe-extracted content, dual-hash drift detection, atomic state v2 writes, and lock-protected concurrency — distributed as an OCI image, standalone binaries for five host platforms, a Homebrew tap, a documented K8s InitContainer pattern, and a Helm chart for the Hub itself.
**Depends on**: Phase 5 (Content Service must serve), Phase 6 (CLI command surface, config registry, synthetic mode)
**Requirements**: STATE-01, STATE-02, STATE-03, STATE-04, STATE-05, STATE-06, STATE-07, STATE-08, STATE-09, STATE-10, STATE-11, ADAPT-01, ADAPT-02, ADAPT-03, ADAPT-04, ADAPT-05, ADAPT-06, ADAPT-07, SAFE-01, SAFE-02, SAFE-03, SAFE-04, SAFE-05, SAFE-06, DIST-01, DIST-02, DIST-03, DIST-04
**Success Criteria** (what must be TRUE):

  1. `ach hydrate --environment <env>` in a fresh workspace produces a working configuration for each of the four platforms — `claude-code` pass-through populates `.claude/` with merged `.mcp.json` recording contributed `mcpServers.<id>` keys; `codex`/`gemini-cli`/`opencode` distribute Claude-format pieces into their respective runtime-config files (TOML/JSON) with merge strategies (`deep` for JSON/TOML, `composite` markdown blocks); platform autodetection picks the right adapter when `--platform` is omitted and exits `1` with a prompt on zero matches or a list on multi-match.
  2. Hydrate's commit sequence runs the §6.7 14 steps under an advisory `flock`-based lock: tmp swept, state read, scope-aware diff against `--include-runtime`/`--only-runtime`, hydrate POSTed, content GETted unconditionally with sha256 short-circuit on disk write, archives extracted into per-resource `tmp/<rand>/`, hashed and classified, adapter run, optional `--sync` deepest-first deletion with inverse-merge for `merge`+`keys[]` entries, atomic state write last (`tmp` → `fsync(fd)` → `rename(2)` → `fsync(parent_dir)`); a SIGKILL anywhere before the final rename leaves prior state intact.
  3. Drift detection (`hash` for on-disk + `sourceHash` for upstream pre-transformation, both `xxh3`) yields the four §8.4 outcomes — no-op, upstream-only-overwrite, local-edit-preserve (exit `2`), conflict-preserve (exit `2`); `--force` overwrites in any non-no-op case; manifest `schemaVersion != "v1alpha1"` aborts with exit `5` and no files written; same-`<ach-dir>` different-Environment aborts with exit `4` unless `--force`; state schema `!= "2"` exits `5` unless `--force`.
  4. Safe extraction rejects absolute paths, `..` segments, hardlinks, devices, FIFOs, sockets, pax-extended-header path injections unconditionally, and rejects symlinks by default (in-tree-only via `--allow-symlinks`); modes are masked to `mode & 0755` with setuid/setgid/sticky/group-write/world-write stripped; `ACH_MAX_EXTRACTED_PLUGIN_MIB`/`ACH_MAX_EXTRACTED_ARTIFACT_MIB`/`ACH_MAX_ARCHIVE_ENTRIES` enforced per resource with partial output discarded on overrun; gzip streamed (never fully buffered); auto-claim collision returns exit `7` with a refusal on different bytes (unless `--force`).
  5. Distribution artifacts are publishable: `ghcr.io/ackstorm/ach:<version>` runs `ach` as default entrypoint with no preset config; standalone binaries built for `linux-amd64`/`linux-arm64`/`darwin-amd64`/`darwin-arm64`/`windows-amd64` are downloadable via GitHub Releases and `brew install ackstorm/tap/ach`; the Helm chart deploys the Hub end-to-end (Operator+CS single-replica `Recreate` Pod with RWO PVC; Platform API + Forwarder Deployments ≥1 replica; `ach-jwt-signing-keys` Secret RBAC-scoped; admin allowlist ConfigMap mounted); the documented K8s InitContainer pattern hydrates a workspace volume in synthetic mode and the main container starts with the agent ready.

**Plans**: TBD
**UI hint**: no

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation — CRDs, DB Schema, Operator Skeleton, Multi-tenancy | 11/11 | Complete | 2026-05-15 |
| 2. External Refs + Marketplace + Operator Reconciliation | 9/9 | Complete   | 2026-05-17 |
| 3. Hub Identity & Platform API | 0/12 | Not started | - |
| 4. Hub Forwarder & JWT Trust Path | 0/TBD | Not started | - |
| 5. Content Service + Cross-component Observability | 0/TBD | Not started | - |
| 6. CLI Foundation | 0/TBD | Not started | - |
| 7. CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution | 0/TBD | Not started | - |

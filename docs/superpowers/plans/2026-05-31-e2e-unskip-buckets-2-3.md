# E2E — Un-skip Buckets 2 & 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut the `make e2e-full` SKIP count by converting four intentionally-skipped scenarios into real, green assertions against the synced cluster. After this plan, only the Phase 6 / Phase 7 CLI suites (Bucket 1 — need a human SSO `pk_`, not automatable headless) and the single `InFlightReadSurvivesRename` orchestration case remain skipped — every other Bucket 2 / Bucket 3 skip becomes a PASS.

**Context — three-bucket skip taxonomy (from the 104 PASS / 30 SKIP / 0 FAIL run on 2026-05-31):**
- **Bucket 1 (accepted, NOT in scope):** `TestPhase6CLI`, `TestPhase7CLIEngine`. Need a real `pk_` minted via interactive Dex SSO + a locally-built binary. Cannot run headless. Left skipped by design.
- **Bucket 2 (in scope):** `TestPluginFilter` — fully hermetic, gated only by an opt-in flag the harness never sets.
- **Bucket 3 (in scope):** four content-service / forwarder sub-cases. Investigation (2026-05-31) found **two of the four skip reasons are STALE** (the stated blocker no longer holds), one is feasible via a fixture the agent that wrote it didn't consider, and one is a genuine non-goal.

**Tech Stack:** GNU Make (`_e2e-run` env block), Go e2e tests (`-tags e2e`, `os.Getenv` gating, stdlib `testing`), kind + Helm synced cluster reached through the single gateway at `localhost:8080`, `kubectl`/`psqlExec`/`kubectlExec` test helpers, ach-mcp-echo capture endpoint (`/__capture/last`).

**DEPENDENCY (hard):** Builds on `docs/superpowers/plans/2026-05-29-e2e-harness-urls-and-gates.md` (already landed — `make e2e-run` exports the gateway URLs + `ACH_E2E_PHASE4/5/6/9 ?= 1` + `ACH_E2E_SC11C ?= 1`). Execute against a fully-synced cluster (`scripts/cluster.sh verify_all` green). Run the kept-cluster loop from CLAUDE.md "E2E debug loop".

---

## Why this is needed (evidence)

The 30 SKIPs in the green run decompose as:

| Skip | File:line | Bucket | Stated blocker | Investigation verdict (2026-05-31) |
|------|-----------|--------|----------------|-------------------------------------|
| `TestPhase6CLI` | `phase6_helpers_test.go:105` | 1 | needs `pk_` + built binary | ACCEPTED — out of scope |
| `TestPhase7CLIEngine` | `phase7_helpers_test.go:127` | 1 | needs `pk_` + e2e-tagged binary | ACCEPTED — out of scope |
| `TestPluginFilter` | `plugin_filter_test.go:94` | 2 | `ACH_E2E_PLUGIN_FILTER` unset | **No real blocker** — hermetic test; flip the flag |
| `SC3_JwtMintAndBipAlphaLast` | `phase4_invariants_test.go:271` | 3 | "mcp-echo doesn't echo Authorization; capture path not wired" | **STALE** — `capture.go:30-44` surfaces `authorization_seen`+`jwt_claims`+`jwt_present`. #50 dep stale. |
| `ContentNotFound` | `phase5_invariants_test.go:299` | 3 | "env.context must name a Plugin with no CRD; not in fixture set" | **Feasible** — content lookup is the LAST gate; name a ghost in a servable env's context |
| `UnauthorizedTeam` | `phase5_invariants_test.go:260` | 3 | "LiteLLM team-removal not scriptable" | **STALE/under-called** — test controls `envRow.AuthorizedTeams` (right side); sentinel team → empty intersection → 403. No LiteLLM mutation needed. |
| `InFlightReadSurvivesRename` | `phase5_invariants_test.go:543` | 3 | "orchestration > 30% context budget; integration-covered" | NON-GOAL — keep skipped, document |

### Evidence anchors

- **PluginFilter is hermetic:** `plugin_filter_test.go:100-113` builds a synthetic `.tar.gz` in-test, serves it via an in-cluster nginx ConfigMap (`pfSetupFixture:187-253`), applies a `Plugin` type:http CR at `*.svc.cluster.local`, waits `Synced=True`, then `kubectl exec`s into the operator's `content-service` container to assert the filter. No external GitHub, no LiteLLM, no rate limit. Uses `psqlExec` (`pfClearPriorState:272`) which is proven against `sts/ach-postgres`.
- **SC3 capture path exists:** `test/e2e/mcp-echo/capture.go:30-44` `captureView` exposes `authorization_seen` (raw header), `jwt_present` (bool), `jwt_claims` (`Verified{Iss,Sub,Aud,Kid,Iat,Exp}` per `jwt/verify.go:24-31`). Forwarder mints with `forwardIdentityJWT` toggle (`internal/forwarder/proxy/handlers.go:130-134`), alpha-last winner resolved by `metadata.name` ASC in `internal/forwarder/bipcache/cache.go:66-80,97-100`; `Resolve` returns nil (no mint) when the winner's `ForwardIdentityJWT` is false. So `jwt_present` at the backend directly reflects which BIP won.
- **UnauthorizedTeam is controllable:** `internal/contentservice/authz.go:191` returns 403 when `!intersectsAny(userTeams, envRow.AuthorizedTeams)`. `intersectsAny:200-209` is true only on a shared element. `envRow.AuthorizedTeams` projects from Postgres `environments.authorized_teams` (`db/migrations/000004_cs_projection.up.sql:30`). `ek_` short-circuits the gate (`authz.go:175`), which is why the happy-path content tests pass — they use `ek`. A `pk_` against an env whose `authorized_teams` lists a team the SSO user is NOT in → empty intersection → 403. We never touch the user's LiteLLM membership.
- **ContentNotFound gate order:** the §15.5 matrix (`phase5_invariants_test.go:226-307`) orders gates: 400 missing/invalid → 401 expired → **403 unauthorized_team** → 403 wrong_environment → **403 unauthorized_content (name NOT in context)** → 404 environment_not_found → **404 content_not_found (LAST)**. So a name that IS in `context.plugins` (passes unauthorized_content) but has no backing `plugins` projection row / cache → 404 `content_not_found`.

---

## File Structure

**Modified:**
- `Makefile` — `_e2e-run` env block (`ACH_E2E_PLUGIN_FILTER ?= 1` default + add to `E2E_RUN_ENV`).
- `test/e2e/phase4_invariants_test.go` — replace the `t.Skip` stub `testPhase4SC3JwtMintAndBipAlphaLast` (`:270-272`) with a real capture-based body.
- `test/e2e/phase5_invariants_test.go` — drop `skipReason` on `UnauthorizedTeam` (`:266`) + `ContentNotFound` (`:305`); add per-case setup hooks.
- `test/e2e/phase5_helpers_test.go` (or a new `phase4_sc3_helpers_test.go`) — small helpers if needed (env-seed, mcp-echo capture read for SC3).

**Possibly added (Task 2/3 fixture route — pick per the open checks):**
- `test/e2e/cluster/05-environment/env-team-denied.yaml` and/or `env-dangling-content.yaml` — dedicated synced Environment fixtures (only if the psqlExec-mutation route proves racy). If added, register in `scripts/cluster.sh` apply set + `verify_all`, and update `references/repo-layout.md` synced-fixture list (per CLAUDE.md doc-hygiene rule).

**Docs (same-commit, per CLAUDE.md hygiene table):**
- `CLAUDE.md` "Test phases" / E2E notes — update the SKIP-count expectation if documented.
- This plan — check boxes as executed.

---

### Task 0: Confirm baseline + the suspected-already-passing case

- [ ] **Step 0.1** — Kept-cluster baseline. `make e2e-full`, capture the `-v` output. Confirm the exact 30 SKIP set and that all four target sub-cases skip for the stated reasons. Save the SKIP list for the after/before delta.
- [ ] **Step 0.2** — Verify `testSC4PostgresPepperOutsideDB` (`phase1_invariants_test.go:126`) is **already PASS** (not skip): the synced cluster ships `sts/ach-postgres` in `ach-system` (Bitnami, `scripts/cluster.sh:186`) and `findPostgresPod:207-223` probes `app.kubernetes.io/name=postgresql`. Grep the run output for this test. If it SKIPs (label mismatch), file as a tiny follow-up (fix the label selector) — do NOT bundle into this plan.

---

### Task 1: Un-skip `TestPluginFilter` (Bucket 2) — LOW risk

**Files:** `Makefile`

- [ ] **Step 1.1** — Add the default near `Makefile:741-745`:
  ```make
  ACH_E2E_PLUGIN_FILTER ?= 1
  ```
- [ ] **Step 1.2** — Append to `E2E_RUN_ENV` (`Makefile:760-762`):
  ```make
  ACH_E2E_PLUGIN_FILTER=$(ACH_E2E_PLUGIN_FILTER)
  ```
- [ ] **Step 1.3** — Validate live FIRST (it has never run in CI):
  `make e2e-focus RUN='TestPluginFilter'` against the kept cluster. Expect PASS (~1-2 min: nginx rollout + Plugin Synced + cache assert).
- [ ] **Step 1.4** — If green, the flag flip is the whole change. If the nginx fixture or `kubectl exec` path fails in the devtools network context, diagnose with `make logs-operator` before declaring done. Do NOT mark complete on a skip.

**Done when:** `TestPluginFilter` PASSes inside a full `make e2e-full` run (not just focus).

---

### Task 2: Un-skip `ContentNotFound` (Bucket 3) — LOW-MED risk

**Files:** `test/e2e/phase5_invariants_test.go`

Goal: a request whose plugin name passes the `unauthorized_content` gate (name IS in `context.plugins`) but misses content resolution → 404 `content_not_found`.

- [ ] **Step 2.0 (open check — do first):** Confirm content-service does NOT refuse to serve before the content-lookup gate when the environment is not `Available=True`. Read `internal/contentservice/authz.go` + the handler that orders the gates; confirm `content_not_found` is reachable independent of the env's `Available` condition. This decides the fixture route below.
- [ ] **Step 2.1 — Primary route (in-test psqlExec, no new fixture):** In the `ContentNotFound` case, before the request: append a ghost name to the **servable** `env-valid` projection row's context and ensure no backing row:
  ```sql
  UPDATE environments
     SET context_plugins = array_append(context_plugins, 'ghost-content')
   WHERE name='env-valid' AND NOT ('ghost-content' = ANY(context_plugins));
  DELETE FROM plugins WHERE name='ghost-content';
  ```
  via `psqlExec`. `t.Cleanup` reverts:
  ```sql
  UPDATE environments
     SET context_plugins = array_remove(context_plugins, 'ghost-content')
   WHERE name='env-valid';
  ```
  Then request `GET /content/plugin/ghost-content` with `x-ach-environment: env-valid` + `pk` (or `ek`). Assert 404 + `error.code == "content_not_found"` + ULID `request_id`.
  - **Cache note:** CS envcache TTL is 60s and the direct `UPDATE` emits no `NOTIFY` (only the operator's `with_tx_notify` does). Mirror `StaleCacheExpired` (`phase5_invariants_test.go:520`): `time.Sleep(65 * time.Second)` after the UPDATE so the loader rebuilds from the patched row. Bounded, deterministic.
  - **Reconcile-clobber note:** controller-runtime resync is effectively idle (no SyncPeriod-driven re-project absent a CRD change), so the patched array survives the test window. If Step 0.2 / a stray reconcile proves otherwise, fall to Step 2.2.
- [ ] **Step 2.2 — Fallback route (dedicated synced fixture):** add `test/e2e/cluster/05-environment/env-dangling-content.yaml` — an Environment whose `context.plugins` includes a name with no Plugin CRD, register it in `scripts/cluster.sh` apply + `verify_all` (it will NOT be `Available`, so `verify_all` must not gate it on Available), update `references/repo-layout.md`. Heavier; only if Step 2.1 is racy.
- [ ] **Step 2.3** — Remove `skipReason` from the `ContentNotFound` case (`phase5_invariants_test.go:305`) and wire the chosen setup. Keep the `skipReason`-driven `t.Skipf` mechanism (`:312-314`) for the other cases.

**Done when:** `ContentNotFound` PASSes in `make e2e-full`.

---

### Task 3: Un-skip `UnauthorizedTeam` (Bucket 3) — MED risk

**Files:** `test/e2e/phase5_invariants_test.go` (+ optional fixture)

Goal: a `pk_` request against an environment whose `authorized_teams` lists a team the SSO test user is NOT a member of → empty intersection → 403 `unauthorized_team`. The user's LiteLLM membership is never touched.

- [ ] **Step 3.0 (open check):** Determine what teams the SSO-provisioned e2e user actually has (read `mustAcquirePk` path + how the test user maps to LiteLLM teams). Pick a sentinel team name guaranteed absent (e.g. `ach-e2e-sentinel-unauthorized`). Confirm `env-valid`'s real `authorized_teams` (so the happy path keeps passing — note happy-path content tests use `ek_`, which skips the gate per `authz.go:175`).
- [ ] **Step 3.1 — Primary route (dedicated env, recommended over mutating env-valid):** add a synced Environment fixture `env-team-denied`:
  ```yaml
  # test/e2e/cluster/05-environment/env-team-denied.yaml
  apiVersion: ach.ackstorm.ai/v1alpha1
  kind: Environment
  metadata: { name: env-team-denied, namespace: ach-system }
  spec:
    authorizedTeams: ["ach-e2e-sentinel-unauthorized"]
    context:
      plugins: ["plugin-valid"]
      prompts: ["prompt-valid"]
      artifacts: ["artifact-valid"]
  ```
  Register in `scripts/cluster.sh` apply set + `verify_all`; update `references/repo-layout.md`. The case requests `GET /content/plugin/plugin-valid` with `x-ach-environment: env-team-denied` + `pk` → 403 `unauthorized_team`.
  - This is cleaner than mutating `env-valid.authorized_teams` (which the operator WOULD revert on the next reconcile, since the CRD is the SoT and a spec-less drift gets re-projected). A purpose-built CRD is stable.
- [ ] **Step 3.2 — Alternative (in-test, faster to prototype):** `psqlExec` `UPDATE environments SET authorized_teams = ARRAY['ach-e2e-sentinel-unauthorized'] WHERE name='env-valid'` + 65s settle + `t.Cleanup` restore. Use ONLY for a quick local spike; the reconcile-revert risk makes the dedicated fixture the shippable form.
- [ ] **Step 3.3** — Remove `skipReason` from the `UnauthorizedTeam` case (`phase5_invariants_test.go:266`); point its request at the denied env. Update the stale code comment that claims LiteLLM-removal is required.

**Done when:** `UnauthorizedTeam` PASSes in `make e2e-full` and the happy-path content cases stay green.

---

### Task 4: Un-skip `SC3_JwtMintAndBipAlphaLast` (Bucket 3) — MED risk

**Files:** `test/e2e/phase4_invariants_test.go` (replace the stub), optional `phase4_sc3_helpers_test.go`

Goal: two BIPs targeting the same MCPServer; the forwarder mints (or does not) a JWT for the **alphabetically-last** BIP `metadata.name`; the mcp-echo backend's `/__capture/last` proves which won via `jwt_present` / `jwt_claims`. Renaming flips the winner.

- [ ] **Step 4.0 (open check):** Reuse the existing harness. Read `TestPhase4JWTValidate` (`phase4_jwt_validate_test.go`) + `TestPhase4BIPClosedLoop` (`phase4_bip_loop_test.go`) for: how mcp-echo is deployed (`testMocks.mcpEcho.enabled`, `requireJwt=false`), how a BIP CR is applied, and how `/__capture/last` is read. Confirm `mcp-echo` runs with `requireJwt=false` so the `forwardIdentityJWT=false` winner case (no token) does not 401 before reaching capture.
- [ ] **Step 4.1** — Replace the `t.Skip` body (`phase4_invariants_test.go:270-272`) with:
  1. Apply two BIPs, both `spec.target: {kind: MCPServer, name: <synced-mcp>}`, names chosen so the tiebreak is unambiguous — e.g. `bip-aaa` (`forwardIdentityJWT: false`) and `bip-zzz` (`forwardIdentityJWT: true`). `t.Cleanup` deletes both.
  2. Wait for the forwarder `bipcache` to observe both (the cache LISTENs on `ach_bips_changed` + 5-min refresh; either wait on the BIP CR `Ready`/projection or poll the capture a few times — bounded, per CLAUDE.md no-naked-loop rule).
  3. `POST` through the forwarder `/mcp/<name>` route with a valid `pk_`/`ek_`.
  4. Read `/__capture/last`; assert `jwt_present == true` (alpha-last = `bip-zzz`, mint ON) and `jwt_claims.aud == "mcp:<name>"`, `jwt_claims.sub == "<namespace>/<ownerEmail>"`.
- [ ] **Step 4.2** — Flip the winner: rename so the alpha-last BIP is the `forwardIdentityJWT: false` one (e.g. delete `bip-zzz`, apply `bip-zzz` with `false` and `bip-aaa` with `true`, OR add `bip-zzz2`), re-request, assert `jwt_present == false`. This is the actual "alpha-last" lock-in.
- [ ] **Step 4.3** — Update the function doc comment + drop the stale #50 reference. Cross-check `references/troubleshooting.md` / `docs/developer-guide/jwt-forwarder.md` for any claim that SC3 is deferred; fix in the same commit (doc-hygiene).

**Done when:** `SC3_JwtMintAndBipAlphaLast` PASSes in `make e2e-full`, exercising BOTH winner orientations.

---

### Task 5 (NON-GOAL at e2e; coverage gap CLOSED at integration layer)

The live-cluster e2e `InFlightReadSurvivesRename` stays a deliberate non-goal (deterministic mid-stream `rename(2)` + throttled-reader orchestration is flaky through the gateway). BUT the real coverage gap — the prior integration test proved only the *kernel* inode primitive on a bare `os.Open` FD, never ACH's serve path — is now closed by a **deterministic** integration test.

- [x] **Step 5.1 — DONE (this branch, separate commit):** Added `TestPipeline_InFlightReadSurvivesRename_ServePath` in `internal/contentservice/pipeline_test.go` (`//go:build integration`). It drives the real serve path — `pipeline()` early-open (gate 8) → simulated Operator atomic `rename(2)` over the cache path → `stream()` copy from the held FD — and asserts the ORIGINAL bytes (0xAA) are served, not the swapped-in NEW bytes (0xBB). Zero production seam, no goroutine/timing race. Verified PASS via `make test-integration` (testcontainers Postgres). A regression that re-opens the file by path inside `stream()` fails this test deterministically.
- [ ] **Step 5.2 (parallel-agent owns `test/e2e/`):** Tighten the e2e skip message at `phase5_invariants_test.go:543-551` to state it is a deliberate non-goal and that the inode-pin invariant is now covered by `TestPipeline_InFlightReadSurvivesRename_ServePath` (serve-path) + `TestPipeline_InFlightReadSurvivesRename` (kernel primitive + cross-request). No e2e un-skip. Message-only edit.

---

## Verification (final gate)

- [ ] `make e2e-full` green: 0 FAIL, and SKIP count drops by 3 (PluginFilter + ContentNotFound + UnauthorizedTeam + SC3 minus any that were sub-test rollups) versus the Task 0 baseline. Expected remaining skips: Phase 6 / Phase 7 suites + `InFlightReadSurvivesRename` (+ their sub-cases).
- [ ] `make qa-lint-changed` + `make test-unit` clean.
- [ ] Diff the Task 0 SKIP list against the final run; every removed skip is now a named PASS, none flipped to FAIL.
- [ ] Per CLAUDE.md: do not push without confirming E2E green (touches `test/e2e/`, possibly `deploy/helm` + `scripts/cluster.sh` if fixture route chosen).
- [ ] Docs updated in the SAME commit (synced-fixture list in `references/repo-layout.md` if fixtures added; SKIP-count note if documented anywhere).

## Risk register

| Risk | Mitigation |
|------|------------|
| ContentNotFound psqlExec mutation reverted by operator reconcile mid-test | Resync is idle absent CRD change; 65s settle window; fall to dedicated fixture (Step 2.2) if observed |
| UnauthorizedTeam env-valid mutation reverted | Use a dedicated `env-team-denied` CRD (Step 3.1), not env-valid mutation |
| SC3 `forwardIdentityJWT=false` winner 401s before reaching capture | Ensure mcp-echo `requireJwt=false` (Step 4.0) |
| SC3 bipcache hasn't observed both BIPs yet | Bounded wait on projection/Ready or bounded capture poll (no naked loop) |
| New synced fixtures break `verify_all` (env not Available) | `verify_all` must gate these on existence/projection, not `Available=True` |
| Flag flip exposes a latent PluginFilter bug never run in CI | Validate via `e2e-focus` first (Step 1.3) before bundling into full run |

# CLAUDE.md — ach

Surgical **navigation hub** for AI agents — a smart index, not a textbook. Read
the section for your task, then follow the MANDATORY Reading Table into the
deeper docs. Reading the MANDATORY entry before touching the corresponding code
is non-negotiable.

> **Lean on purpose** (loaded every conversation). A fresh session starts with
> `docs/explanation/concepts.md` + `docs/explanation/architecture.md` (the
> system in two pages), then this file. Per-service contracts live in
> `references/services/`; workflow detail in `references/` (`repo-layout.md`,
> `release-pipeline.md`, `makefile.md`, `troubleshooting.md`,
> `litellm-permission-model.md`, `known-gaps.md`).

## Documentation hygiene — update docs IN THE SAME COMMIT

When a change alters behavior/contracts/workflows that `CLAUDE.md`,
`references/`, or `docs/` describe, update the affected doc in the SAME commit —
no "docs follow-up PR later". Drift is a bug; fix a stale claim in the change
that revealed it.

**Every change — whatever it touches — check whether it affects the agent
guidance users run on**: the OpenCode bootstrap skill
`internal/platformapi/opencode/skill/SKILL.md` (`<provider>-ach`: install
`ach-cli`, login, `environment list`, hydrate a named or the default Environment — the ONLY
guidance that names the ACH URL) and the repo-root plugin marketplace
(`.claude-plugin/marketplace.json`, Codex `.agents/plugins/marketplace.json`):
`plugins/ach-cli` (everyday CLI), `plugins/ach-gitops` (writing CRs),
`plugins/ach-install` (the chart). The plugins never hardcode a URL — the
ach-cli profile or `ACH_URL` holds it. If a change affects them, update them in
the same commit and bump that plugin's `version` in BOTH
`plugins/<name>/.claude-plugin/plugin.json` and `plugins/<name>/plugin.json`
(Claude Code only updates an installed plugin when its version changes); a
stale line there is a broken instruction for every user. Validate with
`claude plugin validate .`.

| Change | Update |
|--------|--------|
| CRD field / condition / default | `docs/api-reference/` (`make gen-crd-ref-docs`) + `examples/` |
| New/renamed `make` target or default behavior | table here + `references/makefile.md` |
| New `wait-*` / blessed pattern / polling rule | "Waiting for state" table |
| CI gate / govulncheck ack / SPDX rule | "Publication" + `references/security/...` |
| Release pipeline (`release.yml`, goreleaser, bump) | `references/release-pipeline.md` |
| Repo layout / synced-fixture set | `references/repo-layout.md` |
| **Service/domain** failure mode | `references/troubleshooting.md` |
| `ach-cli` command, flag or output change | `docs/reference/cli.md` (`make gen-cli-ref-docs`), `plugins/ach-cli/skills/ach-cli/` (+ version bump), the console guide `ui/src/routes/HowToCli.tsx`, and, for install/login/`environment list`/hydrate, `internal/platformapi/opencode/skill/SKILL.md`; verify each command against `ach-cli <cmd> --help`; re-check both before EVERY `make release-cut` (`references/release-pipeline.md`) |
| CRD field / condition / reconcile behavior an admin writes against | `plugins/ach-gitops/skills/ach-gitops/` (+ version bump) |
| Helm value / install prerequisite / upgrade step | `plugins/ach-install/skills/ach-install/` (+ version bump) |
| **Generic/workflow** failure mode | "Common failure modes" here |
| New MANDATORY-read file for a workflow | MANDATORY Reading Table |
| Service contract (endpoint, status code, header, env var, reconcile behavior) | `references/services/<service>.md`; a shift in the overall picture also `docs/explanation/architecture.md` |
| Anything a user or operator can see (behavior, output, chart, install requirement) | `CHANGELOG.md` `[Unreleased]` entry in the same PR; tests, CI and agent-only docs need none. A correction to something an earlier entry told users is itself an entry |
| Breaking change (removes/renames a command, flag, API field, Helm value or CRD field, changes a default) | `### Breaking` entry first in `[Unreleased]`, with the operator's step; the release bumps the major version (`references/release-pipeline.md` "Versioning") |

## Quick context

ACH — Agent Capability Hub. Multi-service Kubernetes control plane for
declarative agent configuration management: operator + platform API + forwarder
+ content service + CLI. The long-running services ship as a **single Go binary**
(`ach`) with cobra subcommands selected at process start; the user-facing CLI
ships as a **separate `ach-cli` binary** (login/logout/whoami/token/profile/environment/
keys/admin; hydrate/status/uninstall/fetch live under `environment`; plus the serverless
local package manager `local repo`/`local plugin`/`local skill`) that drops the
k8s.io/* + controller-runtime deps. Both
share `internal/cli/*`. Go (controller-runtime, k8s.io/* per `go.mod`).

**One install topology** (the chart fails the render on a `profile: identity` value).
The e2e cluster runs one release: `ach.e2e.local`.

A React console lives in `ui/` (imported from `alitellm-auth`), built by
`make ui-build` into `internal/platformapi/console/dist` (`go:embed`) and
served at `/` by platform-api.

Release plumbing + CI scaffolding grafted from
[ackstorm/alitellm-operator](https://github.com/ackstorm/alitellm-operator)
(Apache-2.0; see `NOTICE` + `references/upstream-sync.md`) — non-code surfaces
only. All Go code, CRDs, and Helm values are original ackstorm material.

**Editions — open core.** Core is Apache-2.0 (`LICENSE`); Enterprise code lives
under `ee/` and `ui/src/ee/` (+ `cmd/ach/ee.go`) under the ACKstorm Enterprise
License (`ee/LICENSE`, `ui/src/ee/LICENSE`; DRAFT pending legal review), per-file SPDX
`LicenseRef-ACKstorm-EE`. **Rule: core never imports `ee/`; a new Enterprise
feature goes in `ee/` and registers through `internal/edition`.**

- **One image, one console**: `ghcr.io/ackstorm/ach` always links the Enterprise
  code (`cmd/ach/ee.go` → `ee/register`) and embeds the single console (`ui/` →
  `internal/platformapi/console/dist`). Helm `enterprise.enabled` → `ACH_ENTERPRISE`
  turns the features on; enabling them anywhere needs a signed ACKstorm agreement.
- **Seam**: `internal/edition` — `RegisterCommand`/`RegisterPlatformAPI`. Disabled, the
  Enterprise `/platform/observability/*` routes answer 403 `enterprise_only`,
  `ach classifier` exits with "ACH Enterprise feature: set Helm enterprise.enabled=true",
  and `CheckEnv` makes platform-api refuse to start with `ACH_OBSERVABILITY_CLICKHOUSE_URL` /
  `ACH_OBSERVABILITY_THANOS_URL` / `ACH_CLASSIFIER_TAXONOMY_FILE` set. Bootstrap carries `enterprise: bool`; the console
  marks Enterprise pages "Enterprise only".
- **Chart**: Enterprise values (`classifier.*`, the whole `platformApi.observability` block: ClickHouse URL, secret, project ids, `thanosUrl`) are
  kept but render nothing while `enterprise.enabled` is false (no render failure), so
  enabling Enterprise is that one value.
- **Isolation gate**: `make qa-edition` (`scripts/edition-check.sh`) — only `cmd/ach`
  imports `ee/`, `ach-cli` links none, core UI imports `ui/src/ee` only via `@ee`, the
  two `ee` licence copies are identical. CI lint job.

| Enterprise | Where |
|------------|-------|
| Conversation **classification** | `ach classifier` (`ee/classifier/`, `ee/cmd/`), `/platform/observability/classification`, and every classification result shown in the console (the Enterprise Overview's Work classification panel) |
| **Observability** (every `/platform/observability/*` route) | `ee/observability/evidence/` (traces, sessions, context, workloads, classification, cost, latency, performance readers), `/platform/observability/{context,workloads,cost,latency,performance}`, console Analytics (Cost, Performance, Reliability, Runtime, Skills & context, Workloads) and the Enterprise Overview (`ui/src/ee/routes/Overview.tsx`: the core Overview plus generation evidence) |
| **Traces** | `/platform/observability/{traces[/{trace_id}],sessions[/{session_id}]}` (`ee/platformapi/obsapi`), console Traces (sessions with an execution waterfall, or calls) |
| **Evals** | not built; Enterprise |

**Overview stays core**: console `/` without Enterprise is the core Overview
(`ui/src/routes/Overview.tsx`), read from LiteLLM alone through
`/platform/console/{stats,latency}`. Core serves no `/platform/observability/*`
route; `internal/observability/evidence` keeps only the ClickHouse client, the
scope and the base Reader the Enterprise readers wrap, and
`internal/platformapi/obsapi` only the shared guard, window and error mapping.

## Architecture

```
┌──────────────┐ reconcile ┌─────────────────────────────┐  project    ┌────────────┐
│     CRDs     │──────────▶│       ach operator Pod      │────rows────▶│  Postgres  │
│ (Environmen…)│           │ ┌─────────────┐ ┌─────────┐ │  + NOTIFY   │  (SoT for  │
└──────────────┘           │ │  operator   │ │ content │ │             │ ACH state) │
                           │ │ (reconcile) │ │ service │◀┼──READ ROWS──│            │
                           │ └─────────────┘ └────┬────┘ │             └─────┬──────┘
                           └─────────┬────────────┼──────┘                   │
                                     │            │                          │
                                     ▼            ▼ /content/{prompt,…}      │
                          ┌────────────────────┐  ┌───────────────────┐      │
                          │ ach platform-api   │  │ ach forwarder     │      │
                          │ (REST + OAuth AS + │◀▶│ (JWT trust path,  │      │
                          │  /platform/hydrate)│  │  BIP+Env caches)  │      │
                          └─────────┬──────────┘  └─────────┬─────────┘      │
                                    │                       │                │
                                    └──── READ ROWS + LISTEN ach_*_changed ──┘
```
**Source of truth**: the operator writes Postgres
(13 projection tables incl. `key_grants`, `environments`, `plugins`, `skills`,
`backend_identity_policies`, `external_refs`, `marketplace_plugins`,
`marketplaces`, `skill_marketplaces`, `skill_marketplace_skills`); platform-api, forwarder,
and content-service READ from Postgres and LISTEN on the `ach_*_changed` channels
emitted by `with_tx_notify`. CRDs are not the read path for any
non-operator service. The platform-api UI writes `origin='ui'` draft Environments; the operator takes a draft over when the matching CR is applied (GitOps wins) — `references/services/platform-api.md`. The forwarder's only remaining k8s read is the
`ach-jwt-signing-keys` Secret informer; the platform-api's only k8s
touchpoints are the Dex leg of the OAuth AS and TokenReview for
`POST /platform/keys/exchange` (a cluster-wide `system:auth-delegator`
ClusterRoleBinding; outside a cluster the route is not mounted).

Content-service runs **by default** as a **sidecar in the operator Pod**
(co-located because the artifact PVC is RWO) — no `ach-content-service`
Deployment. Set `contentService.standalone=true` + an RWX `operator.cache`
(accessMode `ReadWriteMany` + a storageClassName) to split it into its own
N-replica `ach-content-service` Deployment for HA; the operator stays the
sole cache writer, content-service mounts it readOnly.
operator, platform-api, and forwarder are independent Deployments, each running
the same `ach` image with `args: ["<mode>"]`. The `ach gateway` Deployment is an
**optional** dumb edge reverse proxy fronting platform-api/content-service/
forwarder behind one `ach-gateway` Service; the public Ingress targets it
directly. It is a logic-free packaging convenience — disable it with
`gateway.enabled=false` and front the services with per-service Ingress instead.
In dev/e2e
the nginx `ach-local-gateway` is reduced to a shim adding `/dex` + `/metrics/<svc>`
in front of `ach-gateway` (preserving the single `ach.e2e.local:8080` origin). Owned
CRDs (`ach.ackstorm.ai/v1alpha1`): `Environment`, `Plugin`, `PluginMarketplace`,
`Skill`, `SkillMarketplace`, `Prompt`, `Artifact`, `LiteLLMConnection`,
`BackendIdentityPolicy`, `KeyGrant` (`api/` is authoritative —
NO `EnvKey`/`Team`/`ContentRef`/`AgentDefinition`/`AgentSession` kinds exist;
`ek_`/`pk_` keys and teams are platform-api/DB objects). Agents run under the separate
`ach-agent` project (github.com/ackstorm/ach-agent), which uses ACH's public API only:
`POST /platform/hydrate` and `GET /v2/model/info` with an `ek_` (or an exchanged ACH JWT, `POST /platform/keys/exchange`) in `x-ach-key`, plus the
model and MCP routes.

The architecture is **5 logic modes** (operator, platform-api, forwarder,
content-service, migrate); `gateway` is an **optional, logic-free packaging
convenience**, not a co-equal mode.

| Service mode | Subcommand | Owns |
|--------------|------------|------|
| operator        | `ach operator`        | Reconciles the ACH CRDs; sole writer of LiteLLM access groups, shell-team attachments (`assigned_team_ids`) and tag budgets; hourly orphan-key reaper. Contract → `references/services/operator.md` |
| platform-api    | `ach platform-api`    | REST API: keys (`pk_`/`ek_` lifecycle, budgets, limits), the OAuth 2.1 AS over Dex (the only login), hydrate, admin, UI objects, the console and its session, the OpenCode endpoints, Enterprise observability routes. Contract → `references/services/platform-api.md`; key exchange → `references/services/key-exchange.md` |
| forwarder       | `ach forwarder`       | Data path for `/v1`, `/gemini`, `/mcp/<n>`, `/a2a/<n>`, `/.well-known`: resolves ACH credentials, forwards the caller's own LiteLLM key, stamps budget tags, strips client tags, refuses per-request LiteLLM controls, mints per-target BIP JWTs; no catch-all. Contract → `references/services/forwarder.md` |
| content-service | `ach content-service` | Artifact streaming via `sendfile(2)`; `x-ach-key` takes `pk_`/`ek_` OR an OAuth JWT (hydrate from an OAuth profile) — verified against the same `ach-jwt-signing-keys` seed, mounted as files (`ACH_JWT_SECRET_DIR`, required, sidecar AND standalone; re-read every 30 s like platform-api's signer, so a key rotation needs no restart) |
| gateway         | `ach gateway`         | **Optional** edge reverse proxy — single-origin front for the HTTP surfaces (no auth, no /metrics, no /dex) plus `/` → platform-api (the console SPA); no LiteLLM path outside the ACH families is reachable through ACH — LiteLLM's `/ui`, `/key/*`, `/health` need LiteLLM's own host; disable via `gateway.enabled=false`, use per-service Ingress instead. It needs no database. |
| migrate         | `ach migrate`         | Postgres schema migrations |
| classifier      | `ach classifier`      | **Enterprise** async job (chart `classifier.enabled`, needs `enterprise.enabled`): labels idle Langfuse sessions with one taxonomy category. Detail → `docs/how-to/admins/enable-enterprise.md` |

Per-user key ceiling and login team enrolment → `references/services/platform-api.md`;
key exchange and `KeyGrant` → `references/services/key-exchange.md`.

User CLI = separate `ach-cli` binary (NOT in the service image), sharing `internal/cli/*`:
login/profiles, `environment` (list, search, describe, clone, hydrate, status, uninstall,
fetch), `launch`, `keys`, `admin`, `upgrade`, and the serverless local package manager
`local repo|plugin|skill`. Command surface, conventions and the server contracts it
depends on → `internal/cli/AGENTS.md`.

Critical paths:
- CRD apply → reconciler → state mutation (k8s + Postgres) → status condition
- `ach-cli login` → platform-api OAuth AS (loopback code+PKCE, or RFC 8628 device grant from any browser) → Dex → `provisionUser` (LiteLLM; default/user/SSO-group teams) → `/token`: 1h JWT + refresh, one `purpose='oauth'` `pk_` row per user
- `ach-cli environment hydrate` → platform-api `/platform/hydrate` → content-service sidecar → workspace
- Environment reconcile → resolve refs against LiteLLM → `POST /v1/access_group`; `Available=True` = `ExecutionResourcesResolved` + `AccessGroupSynced`
- Environment reconcile → deny-all shell team `ach-env-<name>` (sentinels: `models=["no-default-models"]` — LiteLLM's own value, which it filters out of `/v1/models`; an invented sentinel leaked into every member's catalog as a phantom model (§5), `agents=["00000000-0000-0000-0000-000000000000"]`) → joined into the access group's `assigned_team_ids` alongside `spec.authorizedTeams`; `ach-cli keys create` mints the `ek_` into that team, which is the only reliable ceiling on a key. A `pk_` is capped symmetrically by a per-user deny-all shell `ach-user-<email>`: platform-api provisions the shell + sets it as the key's `team_id` (+ matching key `duration`) at the first OAuth token issue; the **operator is the sole writer of `assigned_team_ids`** and, on every Environment reconcile, attaches the shell of each entitled member (live `GET /team/info` membership, `user_id == email`) — so one `pk_` reaches the union of the user's entitlements, fail-closed until the next reconcile for a brand-new shell
- BackendIdentityPolicy → operator RBAC → forwarder cache → per-target JWT mint → upstream
- `POST /platform/keys/{id}/suspend`/`/resume` → ACH-only state flip + resolver cache `DEL` (the backing LiteLLM key is never touched); `expired`/`invalid` are derived at read/use time from the clock and the owner-access check, never persisted

On-disk tree + the **synced-fixtures vs examples** distinction →
`references/repo-layout.md`. (Agents confuse the `test/e2e/cluster/` synced
fixtures the e2e suite asserts against with the curated `examples/` — they are
independent collections.)

## MANDATORY Reading Table

**DO NOT guess. DO NOT skip. Read the doc FIRST.**

| Working on...                          | MUST read first                          |
|----------------------------------------|------------------------------------------|
| Fresh session / whole-system comprehension ("understand the project") | `docs/explanation/concepts.md` + `docs/explanation/architecture.md`, then this file |
| Changing platform-api (keys, OAuth AS, console, OpenCode endpoints, admin, observability routes) | `references/services/platform-api.md` |
| Changing the forwarder (any route, header, body check) | `references/services/forwarder.md` |
| Changing the operator (reconcilers, LiteLLM writes, budgets, content fetch) | `references/services/operator.md` |
| Key exchange / `KeyGrant` | `references/services/key-exchange.md` |
| Proposing to fix a limitation, or reporting one as a bug | `references/known-gaps.md` (accepted trade-offs) |
| Any `make` command / command organization | `references/makefile.md` (command list + 3-context model) |
| Repo layout / synced fixtures / examples | `references/repo-layout.md` + `verify_all` in `scripts/cluster.sh` |
| Adding/auditing a CRD kind (any archetype) | `references/adding-a-cr-kind.md` (kind-lifecycle checklist + archetype matrix) |
| Release tooling / goreleaser / docs site | `references/release-pipeline.md` + `.goreleaser.yml` + `release.yml` |
| Debugging a service/domain failure     | `references/troubleshooting.md`          |
| LiteLLM teams / access groups / key scoping | `references/litellm-permission-model.md` (measured semantics — do NOT re-derive) |
| New/changed SYNCED CR fixtures         | `test/e2e/cluster/{04-objects,05-environment}/` + `references/repo-layout.md` |
| Curated examples / `ach-cli login` + `environment hydrate` demo | `examples/README.md` |
| E2E tests (kind cluster + Helm)        | `test/e2e/README.md`                     |
| CI workflows (ci, docs, release, ...)  | `.github/workflows/*.yml` (authoritative); CI matrix below |
| Helm chart values + defaults           | `deploy/helm/ach/values.yaml` (per-mode toggles) |
| API reference rendering                | `docs/Makefile` + `docs/.crd-ref-docs.yaml` |
| What was grafted from alitellm + how   | `references/upstream-sync.md`            |
| Local testing, SSO login & Gateway     | `references/local-testing-gateway.md`    |
| OLM packaging                          | NOT supported — explicit scope decision (no OperatorHub) |
| Writing/forking the JWT-validating MCP fixture | `test/e2e/mcp-echo/README.md` + `docs/how-to/backend-builders/write-an-mcp-backend.md` |
| Changing forwarder JWT mint, JWKS, or `/mcp` / `/a2a` routing | `docs/explanation/trust-path.md` (trust-path contract incl. LiteLLM `extra_headers` opt-in + §1.4 `X-Forwarded-Host` / RFC 9728 discovery) |
| Classifier: taxonomy, prompt, model, ClickHouse reads | `docs/how-to/admins/enable-enterprise.md` |
| Changing the OpenCode auth plugin | Never here: change `github.com/ackstorm/opencode-oidc-provider`, tag it, then bump `genai.opencodePluginSpec` in `deploy/helm/ach/values.yaml` |

## CI gating

| Event | lint | unit | envtest | security |
|-------|------|------|---------|----------|
| pull_request → main | ✓ | ✓ | ✓ | ✓ |

`ci.yml` is **PR-only** — `pull_request → main` is the single trigger, no
`push:` trigger. Release commits (`chore(release): v*`) → `release.yml`.
**Soak, leak, and fuzz-long run NOWHERE in CI**: their `make` targets are
**local-only, on demand**. `e2e.yml` runs `make e2e-full` on PRs touching the
surfaces below as a **non-required** measurement of hosted runners. Until that
decision lands, run `make e2e-full` locally before merging any change touching
`internal/controller|platformapi|forwarder|contentservice/`, `api/v1alpha1/`,
`deploy/helm/ach/`, or `test/e2e/` — see "E2E debug loop" (presentation-only
changes are exempt, see there). A failing `e2e.yml` does not block a merge. `main` is protected, admins included: every commit
arrives through a PR with `ci-ok` green and the branch up to date, and no
approval is required. `ci.yml` always runs and `ci-ok` is the one required
check; `scripts/ci-changes.sh` skips the test jobs when a PR only touches docs
or version lines. `release.yml` runs no tests for that reason.

`govulncheck.yml` is **cron-only** (Mondays + `workflow_dispatch`) — it has NO
`pull_request` trigger. `ci.yml`'s security job already runs `make qa-security`,
which runs the same `scripts/govulncheck-gate.sh`, on every PR; carrying both
triggers ran the identical whole-module analysis twice per PR. The cron earns
its keep by catching advisories that a vuln-DB refresh surfaces against an
unchanged `main` — which is exactly how the go1.26.5 → 1.26.6 bump was found.

## Toolchain — host has NO Go (always Docker)

The host has no Go toolchain on PATH. **Every `make` target auto-routes — the
host needs only docker.** Toolchain targets (`test-*`, `qa-*`,
`build-server`/`-cli`/`-e2e`/`-all`, `cluster-*`, `e2e-run`) wrap into the
`ach-devtools` container via the `container_target` macro; host+docker targets
(`build-image*`) and `kubectl`-only targets (`wait-*`, `logs-*`) run
on the host. Never prefix a wrapped `make` target with `./scripts/dev.sh`.
**Exception — the generator targets are NOT wrapped**: `gen-code`,
`gen-manifests`, `gen-crd-ref-docs`, `gen-cli-ref-docs`, `helm-sync`,
`helm-sync-check` call their Go tools directly, so standalone they need `./scripts/dev.sh make
helm-sync` (a bare `make helm-sync` on the host fails with `go: executable
file not found`). See `references/makefile.md` for the 3-context model.
`test/e2e/mcp-echo` is a nested module: root `./...` sweeps (vet, lint,
govulncheck, tidy) do not see it; `qa-lint` covers it
explicitly.

```bash
make build-all                   # build both binaries (auto-routes to devtools)
make ui-build                    # React console → internal/platformapi/console/dist (before build-server when / must serve it; build-image does it itself)
make shell                       # interactive shell in the devtools container
./scripts/dev.sh go build ./...  # raw go, when no make target fits
```

`scripts/dev.sh` mounts the repo + docker socket, preserves host UID:GID, and
shares **one Go build and module cache per machine** (Go's defaults,
`~/.cache/go-build` and `~/go/pkg/mod`), so a new worktree starts warm. Only
per-workspace state lives under `.gocache/` (`HOME`, `GOPATH`, kubeconfig). CI uses a pre-baked GHCR image keyed by
`sha256(Dockerfile.devtools)[:12]` (local-build fallback on miss); **the local
tag is keyed on that same hash**, so editing `Dockerfile.devtools` misses the
cache and rebuilds on the next call (dev.sh builds only when the tagged image is
ABSENT). Old hash-tagged images linger; `make clean-docker` reclaims them. Tool versions
pinned in `Dockerfile.devtools` + `go.mod`.

The container sets **`GOFLAGS=-mod=readonly`** (the Go ≥1.16 default, pinned so
a stray environment cannot loosen it). Under `-mod=mod` any module-graph-walking
command silently rewrote `go.sum` — `go list -m all` alone appended ~279
pruned-graph `/go.mod` hashes that `go mod tidy` does not record, arming the
CI `go mod tidy` drift gate from an ordinary dev command with no signal.
If a go command now fails on a missing `go.sum` entry, the fix is an explicit
`go mod tidy`, never loosening the flag.

**Disk.** Go trims the build cache itself; `./scripts/dev.sh go clean -cache
-modcache` empties both shared caches. `make cluster-up` and `make e2e-run` refuse
to start with under 5 GB free on the workspace disk (`make disk-check`). `make
clean-cache` drops `./.gocache`; `make clean` also drops
`bin/`/`dist/`/`testbin/`/coverage. For **docker** disk, `make clean-docker` reclaims
build cache + dangling images — **safe with a kind cluster up** (never touches
running containers, tagged images, or volumes); it is NOT in the `clean`
umbrella and deliberately avoids `docker system prune` / `image prune -a`.

## Test phases

`references/makefile.md` is the authoritative command list. Common phases:

| Phase | Command | When |
|-------|---------|------|
| `make test-unit`        | pure-logic, ~10s warm | every iteration |
| `make qa-lint-changed`  | golangci-lint scoped to touched pkgs | every iteration |
| `make qa-lint`          | golangci-lint full sweep | before commit; also runs in CI |
| `make test-envtest`     | controller-runtime envtest (race), ~7m | before commit on controller changes |
| `make test-envtest-fast`| envtest without -race, ~3m | dev inner loop |
| `make e2e-full`         | kind + Helm + e2e binary build + stdlib testing, ~6m | final gate before commit |
| `make e2e-focus`        | `RUN='TestPhase4Promotion/SC11a'` (stdlib) | dev loop on one sub-test |
| `make qa-security`      | govulncheck + fuzz-short, ≤6m (gosec via qa-lint) | in-container; **rarely — CI owns it, see below** |
| `make test-ui`          | console `tsc` + vitest (`ui/`), ~1m warm | every iteration touching `ui/` |
| `make test-scripts`     | self-check of `scripts/ci-changes.sh` + `scripts/changelog.sh` | after touching either script |
| `make qa-edition`       | only `cmd/ach` imports `ee/`; `ach-cli` links none; core UI reaches `ui/src/ee` only via `@ee` | after touching `ee/`, `ui/src/ee`, or imports across the edition line |

- Umbrella: `test-full` = `test-unit` + `test-envtest`. Inner loop:
  `make test-unit-pkg PKG=...`, `make test-envtest-pkg PKG=... [FOCUS=TestX]`.
- `scripts/govulncheck-gate.sh` is called from exactly two places:
  `make qa-security` (which `ci.yml`'s security job runs on every PR) and the
  Monday `govulncheck.yml` cron. Skip
  `make qa-security` in the inner loop, because `govulncheck ./...` analyses the WHOLE
  module every time (reachability is a whole-program property, so it can never
  be scoped to the diff) and it cannot be incremental.

## Waiting for state — use blessed make targets

Naked polling loops (`until ...; do sleep N; done`) are **banned**: when the
target disappears the predicate is unreachable and the agent hangs. Use a
`wait-*` target (each uses: `kubectl wait`, `kubectl rollout status`,
`timeout N docker logs -f <cid> | grep -m1`, or `docker wait <cid>`).

| Need | Target |
|------|--------|
| CR condition Ready | `make wait-cr-ready KIND=... NAME=... NS=...` |
| Operator / Platform API / Forwarder Ready | `make wait-operator` / `wait-platform-api` / `wait-forwarder` |
| Content Service container Ready (sidecar in operator Pod) | `make wait-content-service` |
| ach-gateway Deployment Ready | `make wait-gateway` |
| All ach Deployments Ready | `make wait-ach` (covers `ach-gateway`; also `ach-local-gateway` shim when present — a dev/test add-on) |
| Postgres / Redis(Valkey) / Dex | `make wait-postgres` / `wait-redis` / `wait-dex` |
| Container exit + PASS/FAIL marker | `make wait-container NAME=<c>` (`TIMEOUT=<s>`, default 600) |
| Full cluster hydration | `make cluster-up` (synchronous; do not poll after) |
| Reconcile infra/fixtures on a running cluster | `make cluster-sync` (rebuilds + rolls ach pods) |

All listed `wait-*` exist (plus `wait-litellm`/`wait-mcp-echo`/`wait-mocks` for
test backends). Default `WAIT_TIMEOUT=300s`. If none cover a new wait need,
**add a new `wait-*` target** — targets are the contract, not ad-hoc loops.

## Publication

Remote: `git@github.com:ackstorm/ach.git`. `ci-ok` on the PR is the only gate:
lint (`make qa-lint`, which also covers the nested `test/e2e/mcp-echo` module),
`make qa-edition`, chart, generated-code and `go mod tidy` drift, SPDX headers
(`gen-code` runs `scripts/fix-spdx.sh`), Helm render, dashboards, unit, console,
envtest and security. There is no local git hook. **CI does not scan for
secrets**: run `gitleaks git --log-opts="HEAD --not --remotes" --redact
--no-banner` before pushing (allowlist `.gitleaks.toml`). A secret pushed to a
branch is already public.

## After every release — clean up (once confirmed OK)

A release is confirmed OK when `gh release list -L 1` shows the new version
as `Latest` AND `gh run list --branch main -L 3` shows its `Release` run
`success`. Only then, always:

1. `git worktree remove` + `git branch -D` the merged feature worktrees;
   delete the merged remote branch (`git push origin --delete <branch>`).
2. `make clean-docker` (safe with kind up).
3. `git pull --ff-only` on `main` to pick up the merged release commit.

Never touch another session's worktree or cache while it runs a gate
(`pgrep -af <worktree-name>`), and never `uv cache clean`: long-running MCP
servers (ach-memory, muster) run from it. A release that failed is NOT
cleaned up — the warm caches are what the retry needs.

## Common failure modes (generic / workflow)

Service-specific debugging (content-service 404, forwarder JWT 401,
SourceReachable rate-limit, AccessGroupSynced, hydrate-golden diff, mcp-echo,
ConflictWithUIRow (dormant — the Environment upsert takes over UI rows),
stale image roll) → **`references/troubleshooting.md`**. The
ones below are the cross-cutting workflow traps:

### ❌ Prefixing a `make` target with `./scripts/dev.sh`
`./scripts/dev.sh make test-unit` works but the prefix is redundant —
`make test-unit` ✅ auto-routes into devtools via `container_target`. If docker
is down you get a clear preflight error, not `command not found: go`. The prefix
still works (`ACH_IN_DEVTOOLS` guard prevents nesting).

### ❌ Naked polling loop
```bash
until docker logs $(docker ps -q -f ancestor=mock) | grep -q PASS; do sleep 10; done
```
✅ Bounded wait (or a `wait-*` target):
`timeout 600 docker logs -f $cid 2>&1 | grep -m1 -E "PASS|FAIL" || { echo FAIL >&2; exit 1; }`
WHY: when the container exits and is removed, `docker ps -q` is empty,
`docker logs` errors forever, and the loop never exits.

### ❌ Judging an e2e run by a wrapper's exit code
`timeout 1500 make e2e-run` that expires, or `make e2e-run | tail`, says nothing
about the suite. ✅ Only `>>> E2E RESULT: PASS` as the last line is green. Leave
the run unbounded or bound it well above the ~10 min: a loaded box can spend
17 min on the cold build alone. WHY: a kill mid-run stops the recipe, so the
later test groups never run.

### ❌ Invalid Postgres / Redis URLs in dev hydration
`ach operator --postgres-url postgres://localhost/ach` → panic. ✅ Always pass
full DSNs: `postgres://ach:ach@postgres.ach.svc:5432/ach?sslmode=disable`. WHY:
pgx parses `postgres://` strictly; a missing user/port/scheme panics on startup,
looking like a network issue.

### ❌ Running a service mode without its subcommand
`./bin/ach` prints help and exits 0 — the operator never starts. ✅ Each
long-running mode needs its subcommand (`operator`, `platform-api`, `forwarder`,
`content-service`, `migrate`). WHY: a Deployment omitting `args: ["<mode>"]`
CrashLoopBackOffs / silently restarts.

### ❌ Kubectl from host against the kind cluster
`kubectl get pods` → context not found. ✅ Go through devtools:
`./scripts/dev.sh kubectl get pods`. WHY: the kind kubeconfig lives at
`/workspace/.gocache/kube/config` — inside the container.

### ❌ Editing files via relative paths when cwd is the wrong repo
Relative-path writes silently hit a sibling repo (`ach-old/`,
`alitellm-operator/`) if cwd is wrong — "succeeding" while leaving this repo
unchanged. ✅ Use absolute paths; verify with `pwd && git remote -v` (expect
`ackstorm/ach`).

## Repository-specific patterns

- **Single-binary cobra layout**: each long-running mode is a subcommand under
  `cmd/ach/cmd/<mode>.go` wiring its `internal/<service>/` impl. New modes go
  here, NEVER as a second `cmd/<x>/main.go` tree.
- **Reconciler shape**: `internal/controller/<kind>_controller.go` follows
  `Reconcile(ctx, req) (Result, error)` + `meta.SetStatusCondition`.
  Side-effecting I/O lives in dedicated `internal/` packages for
  unit-testability — the reconciler owns the k8s state machine; service packages
  own the I/O.
- **Per-mode Helm Deployments**: operator, platform-api, forwarder are
  independent Deployments sharing one image; content-service is the **second
  container in the operator Pod** (RWO artifacts PVC forces co-location). Toggle
  topology via `deploy/helm/ach/values.yaml` `*.enabled` flags. Each Deployment
  carries `args: ["<mode>"]`.



- **Operator semantics** (Environment two-axis status, the Skill kind, the BIP/Environment
  read path, marketplace parsing, `spec.<git>.path` narrowing) → `references/services/operator.md`.
- **SPDX-only license headers**: every `*.go` (outside `vendor/`,
  `zz_generated*.go`, `mock_*.go`) starts with
  `// SPDX-License-Identifier: Apache-2.0` (`LicenseRef-ACKstorm-EE` under `ee/` and
  `cmd/ach/ee.go`; the CI drift gate enforces;
  `hack/boilerplate.go.txt` feeds controller-gen via `make gen-code`). Run
  `make fix-spdx` to auto-prepend the header to any file missing it (also runs
  automatically at the end of `make gen-code`).
- **govulncheck ack-list**: stdlib HIGH advisories awaiting upstream Go fixes
  live in `references/security/govulncheck-acknowledged.md`; the gate enforces a
  1:1 match (drift either way blocks push).
- **Upstream-sync ledger**: `references/upstream-sync.md` records every file
  grafted from `alitellm-operator` + adaptations. New grafts MUST add a row.

## E2E debug loop

`make e2e-full` is the full-suite final gate (~10 min). It **keeps the cluster
up** after the run — pass OR fail — so a red run can be diagnosed live; reclaim
with `make cluster-down`. **`e2e.yml`
is not a required check**, so the local run stays the gate. Run `make e2e-full` on the host before merging any
change to the controller/services/CRDs/Helm/e2e surfaces.
Iterate with the kept-cluster loop (full diagnosis recipe in
`test/e2e/README.md`):

```bash
make e2e-full                                 # cluster-up + e2e-tagged binaries + e2e-run, cluster KEPT
make logs-operator                            # diagnose live
make e2e-focus RUN="TestPhase4Promotion/SC11a" # focused subtest
make cluster-sync                             # after a code edit: rebuild image + roll ach pods
make cluster-down && make e2e-full            # clean-room start; cluster kept after
```

Never push a change touching `internal/controller|platformapi|forwarder|
contentservice/`, `api/v1alpha1/`, `deploy/helm/ach/`, or `test/e2e/` without
confirming E2E green. **Exempt — presentation-only changes** (HTML/CSS
templates, copy text, `ui/`): no behavior or contract moves, so the package's
unit tests + lint + a rendered-page check are the gate, not a ~15 min e2e.

Kept-cluster tests see prior runs' history (each e2e process mints its own
`demo-ek`; spend rows accumulate). Assert deltas from a baseline captured
BEFORE sending traffic, and aggregate every row sharing an alias — first-match
returns a previous run's key and never grows.

Merging a worktree branch: from a `git worktree add` worktree, `cd <primary> &&
git merge --ff-only <branch>` works; a session entered via `EnterWorktree` must
`ExitWorktree(keep)` first (the isolation hook refuses `git -C`/`cd … && git`).

## External references

Project docs may lag — verify current APIs with Context7 / DeepWiki / WebSearch:
- **controller-runtime / kubebuilder / client-go / cobra**: signatures pinned
  to `go.mod`.
- **Dex SSO**: WebFetch `https://dexidp.io/docs/` (OIDC connector/discovery).
- **goreleaser v2**: https://goreleaser.com — watch the `dockers` → `dockers_v2`
  migration (deferred; configs validate today).

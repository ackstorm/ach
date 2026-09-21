# CLAUDE.md — ach

Surgical **navigation hub** for AI agents — a smart index, not a textbook. Read
the section for your task, then follow the MANDATORY Reading Table into the
deeper docs. Reading the MANDATORY entry before touching the corresponding code
is non-negotiable.

> **Lean on purpose** (loaded every conversation). Deep narrative in
> `references/`: `understanding.md` (**whole-system mental model — read FIRST
> in a fresh session instead of re-exploring the repo**), `repo-layout.md`,
> `release-pipeline.md`, `makefile.md`, `troubleshooting.md` (service-specific
> debugging), `litellm-permission-model.md`.

## Documentation hygiene — update docs IN THE SAME COMMIT

When a change alters behavior/contracts/workflows that `CLAUDE.md`,
`references/`, or `docs/` describe, update the affected doc in the SAME commit —
no "docs follow-up PR later". Drift is a bug; fix a stale claim in the change
that revealed it.

| Change | Update |
|--------|--------|
| CRD field / condition / default | `docs/api-reference/` (`make gen-crd-ref-docs`) + `examples/` |
| New/renamed `make` target or default behavior | table here + `references/makefile.md` |
| New `wait-*` / blessed pattern / polling rule | "Waiting for state" table |
| Pre-push gate / govulncheck ack / SPDX rule | "Publication" + `references/security/...` |
| Release pipeline (`release.yml`, goreleaser, bump) | `references/release-pipeline.md` |
| Repo layout / synced-fixture set | `references/repo-layout.md` |
| **Service/domain** failure mode | `references/troubleshooting.md` |
| **Generic/workflow** failure mode | "Common failure modes" here |
| New MANDATORY-read file for a workflow | MANDATORY Reading Table |
| Architecture/contract shift that invalidates the whole-system brief | `references/understanding.md` |

## Quick context

ACH — Agent Capability Hub. Multi-service Kubernetes control plane for
declarative agent configuration management: operator + platform API + forwarder
+ content service + CLI. The long-running services ship as a **single Go binary**
(`ach`) with cobra subcommands selected at process start; the user-facing CLI
ships as a **separate `ach-cli` binary** (login/logout/whoami/config/env/
keys/admin/runtime; hydrate/status/uninstall live under `env`; plus the serverless
local package manager `repo`/`plugin`/`skill`) that drops the
k8s.io/* + controller-runtime deps. Both
share `internal/cli/*`. Go (controller-runtime, k8s.io/* per `go.mod`).

**Two install profiles from the one chart + image** (`profile:` in values →
`ACH_PROFILE` on the forwarder): `full` (everything above — governance) and
`identity` (platform-api + forwarder + gateway + migrate only: OAuth-for-humans
and the declared credential headers in front of ONE LiteLLM host, no CRDs, no
operator, no Environments/BIP — `/mcp`/`/a2a` forward the resolved identity
as-is, LiteLLM comes from `ACH_LITELLM_BASE_URL`, the forwarder mints
`ach-jwt-signing-keys` itself). Releases coexist one per namespace; CRDs come
from the full one. The e2e cluster runs both: `ach.e2e.local` (full) and
`api.e2e.local` (identity, release `ach-identity`).

Release plumbing + CI scaffolding grafted from
[ackstorm/alitellm-operator](https://github.com/ackstorm/alitellm-operator)
(Apache-2.0; see `NOTICE` + `references/upstream-sync.md`) — non-code surfaces
only. All Go code, CRDs, and Helm values are original ackstorm material.

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
**Source of truth (Phase D, #34)**: the operator writes Postgres
(13 projection tables incl. `environments`, `plugins`, `skills`,
`backend_identity_policies`, `external_refs`, `marketplace_plugins`,
`marketplaces`, `skill_marketplaces`, `skill_marketplace_skills`,
`achagents` (operator-written read model for the gateway `/agents` route set +
the future UI agent list; no UI write path)); platform-api, forwarder,
and content-service READ from Postgres and LISTEN on the `ach_*_changed` channels
emitted by `with_tx_notify`. CRDs are no longer the read path for any
non-operator service. **GitOps-wins UI write path (G2)**: the platform-api UI
Objects API (`/platform/objects`, Environment only in v1) also writes
`origin='ui'` draft rows; the operator is always authoritative and TAKES OVER a
matching `ui` row on CR apply (`origin` 'ui'→'cr', `locked=TRUE`), while the UI
is fenced from operator-owned rows (`403 immutable_via_ui`). Round-trip: draft
in UI → `GET …/yaml` export → commit + `kubectl apply` → operator takeover. The forwarder's only remaining k8s read is the
`ach-jwt-signing-keys` Secret informer; the platform-api's only remaining k8s
touchpoint is the Dex leg of the OAuth AS.

Content-service runs **by default** as a **sidecar in the operator Pod**
(co-located because the artifact PVC is RWO) — no `ach-content-service`
Deployment. Set `contentService.standalone=true` + an RWX `operator.cache`
(accessMode `ReadWriteMany` + a storageClassName) to split it into its own
N-replica `ach-content-service` Deployment for HA (G16); the operator stays the
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
`BackendIdentityPolicy`, `AgentProfile`, `ACHAgent` (`api/` is authoritative —
NO `EnvKey`/`Team`/`ContentRef`/`AgentDefinition`/`AgentSession` kinds exist;
`ek_`/`pk_` keys and teams are platform-api/DB objects). **`AgentProfile` (reusable infra + defaults) + `ACHAgent`
(an agent instance)** render into the single `agent-config-v1` config the
`ach-agent` harness self-boots from: the `ACHAgentReconciler` writes a
`config.json` ConfigMap + a single-replica Deployment (probes, inbound
channel-auth secrets injected as `ACH_SECRET_*` env vars via `secretKeyRef` —
never file-mounted, keeping secret material off the pod filesystem and the
harness contract simple (NOT an isolation boundary: same-uid reads
`/proc/<pid>/environ` either way) — salted
config-hash roll; optional profile spec.podTemplate raw overlay
strategic-merged over the pod template — pass-through, selector label +
config-hash re-pinned) — the harness **self-hydrates**
against ACH at boot (no init container, no CLI), so operator status derives from
probe-backed `pod.status` only.
`placement` (`standalone` default | `distributed`; `ACHAgent.spec.placement ??
AgentProfile.spec.achagent.placement`, agent wins; operator-only, never in config.json)
picks the pod topology: standalone is the single `agent` container rendering,
unchanged; distributed renders `channels`/`harness`/`engine` containers (`args: [--role,
<name>]`, same image, `command` never set) in the same single-replica `Recreate`
Deployment — channels+harness get the operator env verbatim, engine gets only the
`engine.forwardEnv`-selected entries (secretKeyRef preserved, never `ACH_*`); `config.json`
mounts into harness only; data = `<base>/state`→harness, `<base>/home`→engine,
`<base>/home/workspace`→harness (subPath `home/workspace` — the workspace KEEPS its standalone
location so a placement flip on a populated PVC preserves path + directory; the engine reaches
it through `home`, the harness never sees the rest of engine home; never a `workspace` subPath)
via PVC subPath (or one emptyDir when not persistent, `<base>=/tmp/ach-agent`); IPC dirs
`/run/ach-agent/{transfer (H+E), channels (H rw, C ro), engine (E rw, H ro)}`; private
`/tmp` per container; probes are httpGet `/readyz`+`/healthz` on fixed role ports
(`rolePorts` in `achagent_workload.go`: channels 8080, harness 8090, engine 8081 — the
profile/agent `health.port` is ignored in this mode; never exec/socket probes);
`enableServiceLinks: false`; Service targetPort 8080 (channels); profile `resources` per
container (pod total 3×). Placement is a config-hash input. Requires ach-agent
`v0.16.5`+ (HTTP role-port probes since v0.16.3; `home/workspace` layout since v0.16.5 —
v0.16.3/v0.16.4 expect `base/workspace` and are NOT aligned). BOTH placements pin pod
uid/gid/fsGroup 10001 (image uid): without fsGroup a fresh root-owned cloud PVC (EBS)
was unwritable on a persistent standalone pod (ach-agent finding 2026-09-15; kind's
local-path dirs are 0777 and never show it). A default-layout PVC survives a
standalone↔distributed flip in place (same PV, same `home/workspace`; e2e
`pvc_placement_transition`); explicit custom layouts and PVCs already populated under an
older distributed `base/workspace` are NOT relocated. The profile's `spec.achagent` block (image/ach/model/engine/limits/health/cost/placement) holds
the agent-overridable defaults; an ACHAgent sets the same fields flat on its
spec (inline `AgentDefaults`) and resolution is a uniform per-field deep merge
(`agentrender.Resolve{Image,Model,Engine,Limits,Health,Cost,Placement}` + `ResolveAchBaseURL`):
a set agent field wins, an omitted one inherits the profile's. Slices/maps/
nested blocks (`engine.forwardEnv`, `model.params`, `model.thinking`,
`engine.pi`, `cost`) are atomic — present on the agent ⇒ replace as a whole. Everything
else on the profile is profile-only infrastructure an agent cannot override:
`imagePullSecrets`, `resources`, `extraEnv`, `nodeSelector`, `tolerations`,
`persistence`, `networkPolicy`, `terminationGracePeriodSeconds`, `podTemplate`.
`ach.baseUrl` resolves `ACHAgent.spec.ach ?? AgentProfile.spec.achagent.ach ??
operator ACH_BASE_URL` (empty everywhere ⇒ Render blocks the agent); `health`
is resolved ONCE via `agentrender.ResolveHealth` so the config health block,
Service targetPort, and container probes never drift (standalone; distributed uses the
fixed role ports above).

The architecture is **5 logic modes** (operator, platform-api, forwarder,
content-service, migrate); `gateway` is an **optional, logic-free packaging
convenience**, not a co-equal mode.

| Service mode | Subcommand | Owns |
|--------------|------------|------|
| operator        | `ach operator`        | Reconciles ACH CRDs; hourly LiteLLM orphan-key reaper scoped to keys stamped `metadata.ach_issuer == ACH_BASE_URL` (required) — never another release's or foreign keys |
| platform-api    | `ach platform-api`    | REST + `pk_`/`ek_` lifecycle (an `ek_` is minted ONLY into its Environment's deny-all shell team `ach-env-<env>` — no models, no object_permission, no access-group binding; a `pk_` is minted into the caller's per-user deny-all shell team `ach-user-<email>` (provisioned idempotently at mint) with a matching key expiry — grants attach via the operator, not platform-api; see `references/litellm-permission-model.md`; `POST /platform/keys` (ek_ create) + `DELETE /platform/keys/{id}` (ek_ revoke; also caller-scoped pk self-revoke, owner==caller, NOT admin-gated, `?force=true` overrides the active-key 409 guard); combined read `GET /platform/keys` + `GET /platform/admin/keys`) + admin object inventory (read) + UI Objects API (write, Environment only — `/platform/objects`, G2) + admin runtime catalog read (`GET /platform/admin/runtime/{models,mcp-servers,a2a-agents,teams,catalog}`) + **OAuth 2.1 AS — the only login** (`/platform/oauth/{register,authorize,as-callback,device_authorization,device,token}`: DCR, code+PKCE S256 via Dex, RFC 8628 device grant for headless hosts (`ach-cli login --no-browser`), refresh rotation **re-validated at the IdP** — `offline_access` at Dex, every ACH refresh replays Dex's refresh token, a refusal (user disabled at Google/Azure…) ends the session and revokes the oauth `pk_`, Dex unreachable is a 503; no `pk_` is minted at login; issues 1h Ed25519 JWTs signed with the mounted `ach-jwt-signing-keys` seed (`ACH_JWT_SECRET_DIR`); each user's JWT resolves to one `personal_keys` row with `purpose='oauth'`, re-minted at `/token` when LiteLLM no longer lists its key (one `GET /key/list` per issue); RFC 8414/9728 documents are served by the forwarder) + **BIP-declared consent** (an MCP `BackendIdentityPolicy` may name one `consentBroker` + `consentAudience`; ACH probes the backend through the forwarder, performs one broker metadata/DCR/PKCE hop only when the outcome is `auth_required`, then re-probes before issuing the code; no broker code is redeemed) |
| forwarder       | `ach forwarder`       | JWT trust path, `/v1`/`/gemini`/`mcp`/`a2a` rewrite; anonymous `/.well-known/jwks.json` + **ACH-composed** RFC 8414 (`/.well-known/oauth-authorization-server`) + RFC 9728 (`/.well-known/oauth-protected-resource[/v1|/gemini|/mcp/<n>|/a2a/<n>]`) documents (LiteLLM's PRM is no longer relayed); Authn reads the **declared** credential slots (`forwarder.headers` → `ACH_CREDENTIAL_HEADERS`, in order; defaults `x-ach-key`, `x-api-key`): `mode: resolve` → pk_/ek_ resolved, header removed, the caller's own LiteLLM key forwarded as `x-litellm-api-key`; `mode: passthrough` → the backend's own key forwarded as it came + mirrored to `x-litellm-api-key`, no ACH identity; every other header passes as it came. With no declared slot present, `Authorization: Bearer` is resolved only when it is ACH's own OAuth token (JWS-shaped; a JWS that does not verify is 401 + challenge so MCP clients re-auth) — anything else there (LiteLLM's UI bearer, a raw key) is not ours: forwarded untouched with no ACH identity (no precheck, no BIP JWT, no env tag), LiteLLM authenticates it. Nothing presented at all → 401 + `WWW-Authenticate: Bearer resource_metadata=…` on the owned families, while the catch-all `/*` forwards it anonymously (LiteLLM decides); OAuth JWTs have no consent scopes and there is no static scope gate; BIP JWTs are minted for pk_/OAuth callers only — ek_ gets no JWT and no Authorization upstream |
| content-service | `ach content-service` | Artifact streaming via `sendfile(2)`; `x-ach-key` takes `pk_`/`ek_` OR an OAuth JWT (hydrate from an OAuth profile) — verified against the same `ach-jwt-signing-keys` seed, mounted as files (`ACH_JWT_SECRET_DIR`, required, sidecar AND standalone) |
| gateway         | `ach gateway`         | **Optional** edge reverse proxy — single-origin front for the HTTP surfaces (no auth, no /metrics, no /dex) plus a `/` catch-all to the forwarder so every other LiteLLM path (`/ui`, `/key/*`, `/health`…) is reachable; disable via `gateway.enabled=false`, use per-service Ingress instead. Also reads the `achagents` projection (**`ACH_DB_URL` required — refuses to start without it**) and serves `/agents/{ns}/{service}/…` to per-agent Services (`{service}` = the Service name, e.g. `achagent-gh`; the tail after it is forwarded verbatim — webhook, a2a, whatever the harness serves) — **only agents that opt in via `spec.expose.gateway=true` are in the route set** (`exposed` projection column); still no auth/no header rewrite; the `ach-agent` harness verifies HMAC on its webhook route |
| migrate         | `ach migrate`         | Postgres schema migrations |

User CLI = separate `ach-cli` binary (NOT in the service image): `login`/
`logout`/`whoami`/`config`/`env`/`keys`/`admin`/`runtime` (workspace verbs
`hydrate`/`status`/`save`/`uninstall` live under `env`, e.g. `ach-cli env hydrate`).
`env save` writes a committed `ach.yaml` (env names + targets) so a teammate's
bare `ach-cli env hydrate` reproduces the workspace. Bare `ach-cli env hydrate`
(no `<name>`, no `ACH_ENVIRONMENT`) reads that `ach.yaml` and hydrates each
listed Environment best-effort (exit ≠0 if any fails).
Plus the **serverless local package manager** — `repo` (register a GitHub/git
marketplace or direct plugin/skill source), `plugin` and `skill`
(`install`/`uninstall`/`update`/`list` a `name@repo` into per-tool adapter dirs
via `--target`, no Environment/CRD ceremony). `env` is the governed remote
object; `repo`/`plugin`/`skill` are the local-first quick path.

Critical paths:
- CRD apply → reconciler → state mutation (k8s + Postgres) → status condition
- `ach-cli login` → platform-api OAuth AS (loopback code+PKCE, or RFC 8628 device grant from any browser) → Dex → `provisionUser` (LiteLLM) → `/token`: 1h JWT + refresh, one `purpose='oauth'` `pk_` row per user
- `ach-cli env hydrate` → platform-api `/platform/hydrate` → content-service sidecar → workspace
- Environment reconcile → resolve refs against LiteLLM → `POST /v1/access_group`; `Available=True` = `ExecutionResourcesResolved` + `AccessGroupSynced`
- Environment reconcile → deny-all shell team `ach-env-<name>` (sentinels: `models=["__deny_all__"]`, `agents=["00000000-0000-0000-0000-000000000000"]`) → joined into the access group's `assigned_team_ids` alongside `spec.authorizedTeams`; `ach-cli keys create` mints the `ek_` into that team, which is the only reliable ceiling on a key. A `pk_` is capped symmetrically by a per-user deny-all shell `ach-user-<email>`: platform-api provisions the shell + sets it as the key's `team_id` (+ matching key `duration`) at the first OAuth token issue; the **operator is the sole writer of `assigned_team_ids`** and, on every Environment reconcile, attaches the shell of each entitled member (live `GET /team/info` membership, `user_id == email`) — so one `pk_` reaches the union of the user's entitlements, fail-closed until the next reconcile for a brand-new shell
- BackendIdentityPolicy → operator RBAC → forwarder cache → per-target JWT mint → upstream
- Webhook inbound: GitHub → Ingress → gateway `/agents/{ns}/{service}/…` → (prefix stripped, tail forwarded verbatim) → `{service}.{ns}.svc:8080/…` → harness (HMAC-verify + dedup + session_key on its webhook route). `{service}` is the agent's Service name (`achagent-{name}`); the gateway only allowlists via the `achagents` projection and forwards — the tail (`/channels/{ch}/events`, a2a, …) is the harness's contract, not the gateway's. **Reachability is opt-in per agent via `spec.expose` (both default false):** `expose.service` creates the ClusterIP Service (in-cluster reachability for a2a peers / your own ingress); `expose.gateway` (requires `service`) adds the agent to the gateway route set (webhook OR a2a route the same way) and publishes `ACHAgent.status.gatewayURL` (full URL when `ACH_PUBLIC_BASE_URL` — or, as a fallback, `ACH_BASE_URL` — is set on the operator, else the path-only form). Omit `expose` for a fully private agent (no Service, no route, no URL).

On-disk tree + the **synced-fixtures vs examples** distinction →
`references/repo-layout.md`. (Agents confuse the `test/e2e/cluster/` synced
fixtures the e2e suite asserts against with the curated `examples/` — they are
independent collections.)

## MANDATORY Reading Table

**DO NOT guess. DO NOT skip. Read the doc FIRST.**

| Working on...                          | MUST read first                          |
|----------------------------------------|------------------------------------------|
| Fresh session / whole-system comprehension ("understand the project") | `references/understanding.md` (complete mental model — replaces re-exploring the repo) |
| Any `make` command / command organization | `references/makefile.md` (command list + 3-context model) |
| Repo layout / synced fixtures / examples | `references/repo-layout.md` + `verify_all` in `scripts/cluster.sh` |
| Adding/auditing a CRD kind (any archetype) | `references/adding-a-cr-kind.md` (kind-lifecycle checklist + archetype matrix) |
| Release tooling / goreleaser / docs site | `references/release-pipeline.md` + `.goreleaser.yml` + `release.yml` |
| Debugging a service/domain failure     | `references/troubleshooting.md`          |
| LiteLLM teams / access groups / key scoping | `references/litellm-permission-model.md` (measured semantics — do NOT re-derive) |
| New/changed SYNCED CR fixtures         | `test/e2e/cluster/{04-objects,05-environment}/` + `references/repo-layout.md` |
| Curated examples / `ach-cli login` + `env hydrate` demo | `examples/README.md` |
| E2E tests (kind cluster + Helm)        | `test/e2e/README.md`                     |
| CI workflows (ci, docs, release, ...)  | `.github/workflows/*.yml` (authoritative); CI matrix below |
| Pre-push gate logic                    | `scripts/pre-push-check.sh`              |
| Helm chart values + defaults           | `deploy/helm/ach/values.yaml` (per-mode toggles) |
| API reference rendering                | `docs/Makefile` + `docs/.crd-ref-docs.yaml` |
| What was grafted from alitellm + how   | `references/upstream-sync.md`            |
| Local testing, SSO login & Gateway     | `references/local-testing-gateway.md`    |
| OLM packaging                          | NOT supported — explicit scope decision (no OperatorHub) |
| Writing/forking the JWT-validating MCP fixture | `test/e2e/mcp-echo/README.md` + `docs/runbooks/writing-an-mcp-backend.md` |
| Changing forwarder JWT mint, JWKS, or `/mcp` / `/a2a` routing | `docs/developer-guide/jwt-forwarder.md` (trust-path contract incl. LiteLLM `extra_headers` opt-in + §1.4 `X-Forwarded-Host` / RFC 9728 discovery) |
| Changing what `AgentProfile`+`ACHAgent` render into (`agent-config-v1`) | `../ach-agent/docs/schemas/operator-contract.md` — prose half, pins contract rev **v3** (was `CONTRACT_v3.md` until 2026-07-27) — plus `agent-config-v1.schema.json` beside it, authoritative for field names/types/defaults. **Neither repo may change it unilaterally:** ach-agent regenerates (`make schema`), then re-vendor `internal/agentrender/testdata/agent-config-v1.schema.json` in the SAME change — `TestSchema_NoDrift` enforces it. Published: `https://ackstorm.github.io/ach-agent/stable/schemas/agent-config-v1.schema.json` |

## CI gating

| Event | lint | unit | envtest | security |
|-------|------|------|---------|----------|
| pull_request → main | ✓ | ✓ | ✓ | ✓ |

`ci.yml` is **PR-only** — `pull_request → main` is the single trigger, no
`push:` trigger. Release commits (`chore(release): v*`) → `release.yml`.
**E2E, soak, leak, and fuzz-long run NOWHERE in CI** — `nightly.yml` was deleted
2026-07-17 (unused). Their `make` targets still work; they are now **local-only,
on demand**. Run `make e2e-full` locally before merging any change touching
`internal/controller|platformapi|forwarder|contentservice/`, `api/v1alpha1/`,
`deploy/helm/ach/`, or `test/e2e/` — see "E2E debug loop". There is **no
automated backstop on `main`**: nothing catches an e2e regression except a
human running the suite. Docs-only PRs
(paths-ignore `**/*.md`, `docs/**`, `references/**`, `FIX*.txt`, `LICENSE`,
`NOTICE`, `CODEOWNERS`, `.gitignore`) skip `ci.yml`. **⚠ PR-only is a real gate
only if branch protection on `main` is enabled** (needs a paid plan / public
repo); until then direct pushes to `main` are unguarded.

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
(`build-image*`, the gates) and `kubectl`-only targets (`wait-*`, `logs-*`) run
on the host. Never prefix a wrapped `make` target with `./scripts/dev.sh`.
**Exception — the generator targets are NOT wrapped**: `gen-code`,
`gen-manifests`, `gen-crd-ref-docs`, `helm-sync`, `helm-sync-check` call
controller-gen directly, so standalone they need `./scripts/dev.sh make
helm-sync` (a bare `make helm-sync` on the host fails with `go: executable
file not found`). See `references/makefile.md` for the 3-context model.

```bash
make build-all                   # build both binaries (auto-routes to devtools)
make shell                       # interactive shell in the devtools container
./scripts/dev.sh go build ./...  # raw go, when no make target fits
```

`scripts/dev.sh` also bind-mounts a sibling `../ach-agent` checkout read-only at
`/ach-agent` when one exists — `TestSchema_NoDrift` reads the frozen
`agent-config-v1` schema at `../../../ach-agent/...`, and without the mount that
path is absent in the container, so the test skipped on the ReadFile error and
the contract guard silently compared NOTHING (it had never once run). No sibling
checkout ⇒ it still skips, by design.

`scripts/dev.sh` also bind-mounts a sibling `../ach-agent` checkout read-only at
`/ach-agent` when one exists — `TestSchema_NoDrift` reads the frozen
`agent-config-v1` schema at `../../../ach-agent/...`, and without the mount that
path is absent in the container, so the test skipped on the ReadFile error and
the contract guard silently compared NOTHING (it had never once run). No sibling
checkout ⇒ it still skips, by design.

`scripts/dev.sh` mounts the repo + docker socket, preserves host UID:GID, and
persists Go caches under `.gocache/` (per-workspace, so **each git worktree gets
its own**). CI uses a pre-baked GHCR image keyed by
`sha256(Dockerfile.devtools)[:12]` (local-build fallback on miss); **the local
tag is keyed on that same hash**, so editing `Dockerfile.devtools` misses the
cache and rebuilds on the next call. (It was a fixed `:latest` until
2026-07-31, and dev.sh only builds when the image is ABSENT — so a Dockerfile
edit silently changed nothing locally while CI ran the new image. Old
hash-tagged images linger; `make clean-docker` reclaims them.) Tool versions
pinned in `Dockerfile.devtools` + `go.mod`.

The container sets **`GOFLAGS=-mod=readonly`** (the Go ≥1.16 default, pinned so
a stray environment cannot loosen it). Under `-mod=mod` any module-graph-walking
command silently rewrote `go.sum` — `go list -m all` alone appended ~279
pruned-graph `/go.mod` hashes that `go mod tidy` does not record, arming the
pre-push `go mod tidy` drift gate from an ordinary dev command with no signal.
If a go command now fails on a missing `go.sum` entry, the fix is an explicit
`go mod tidy`, never loosening the flag.

**Keep the environment clean — `make clean-cache` after each feature.** Go marks
its module cache read-only (`0444`/`0555`), so a plain `rm -rf .gocache` (or
`git worktree remove`) fails with `Permission denied` — the files are owned by
**you, not root**; you just can't unlink from a non-writable dir. `make
clean-cache` (host-only: `chmod -R u+w` then `rm -rf ./.gocache`) clears it
safely. Recommended once a feature/worktree is done so stale per-worktree caches
don't pile up; re-created on next `scripts/dev.sh` use. (`make clean` is the
broader umbrella — also drops `bin/`/`dist/`/`testbin/`/coverage on top of the
cache.) For **docker** disk (not the Go cache), `make clean-docker` reclaims
build cache + dangling images — **safe with a kind cluster up** (never touches
running containers, tagged images, or volumes); it is NOT in the `clean`
umbrella and deliberately avoids `docker system prune` / `image prune -a`.

## Test phases

`references/makefile.md` is the authoritative command list. Common phases:

| Phase | Command | When |
|-------|---------|------|
| `make test-unit`        | pure-logic, ~10s warm | every iteration |
| `make qa-lint-changed`  | golangci-lint scoped to touched pkgs | every iteration |
| `make qa-lint`          | golangci-lint full sweep | before commit; also runs inside the pre-push gate |
| `make test-envtest`     | controller-runtime envtest (race), ~7m | before commit on controller changes |
| `make test-envtest-fast`| envtest without -race, ~3m | dev inner loop |
| `make e2e-full`         | kind + Helm + e2e binary build + stdlib testing, ~6m | final gate before commit |
| `make e2e-focus`        | `RUN='TestPhase4Promotion/SC11a'` (stdlib) | dev loop on one sub-test |
| `make qa-security`      | govulncheck + fuzz-short, ≤6m (gosec via qa-lint) | in-container; **rarely — CI owns it, see below** |
| `make pre-push`         | gitleaks + trufflehog + 18 gates | host-only; before push |

- Umbrellas: `test-full` = `test-unit` + `test-envtest`; `verify` =
  `qa-fuzz-short` + `pre-push` (NOT `qa-security` — pre-push gate 13 already
  runs the same `govulncheck-gate.sh`, so calling both ran the identical
  whole-module analysis twice); `make hooks` installs `.git/hooks/pre-push ->
  scripts/pre-push-check.sh` (and removes any stale pre-commit hook from a prior
  install). Inner loop: `make test-unit-pkg PKG=...`,
  `make test-envtest-pkg PKG=... [FOCUS=TestX]`.
- `pre-push` is **host-only** — never via `./scripts/dev.sh`. Don't run it by
  hand; the installed hook fires the same script (exception: after a
  `--no-verify` push).
- The fast pre-commit gate was retired — lint + unit now run inside the
  pre-push gate and in CI.
- **Don't run `qa-security` by hand as routine.** `ci.yml`'s security job runs
  it on every PR, and `pre-push` gate 13 runs the same
  `scripts/govulncheck-gate.sh` before every push — so a local run is a third
  copy of an analysis that is already covered twice. `govulncheck ./...`
  analyses the WHOLE module every time (reachability is a whole-program
  property, so it can never be scoped to the diff) and it cannot be
  incremental. Run it locally only when you are actively chasing a specific
  advisory or bumping the toolchain.

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

## Publication — the pre-push gate is non-negotiable

Remote: `git@github.com:ackstorm/ach.git`. A single hook stage gates publication
before a push leaves the host:

- The fast pre-commit gate was retired — lint + unit now run inside the pre-push
  gate and in CI; no separate commit-time gate remains.
- `pre-push` (full): **18-gate** publication check. lint + unit live INSIDE the
  18 (gates 16+17), so the full lint + unit sweep always fires before a push.

The 18 hard gates (failure blocks push): gitleaks + trufflehog
(`origin/main..HEAD`; allowlist `.gitleaks.toml`) · large files >2 MB ·
sensitive patterns (`.env`, `*.pem`, `*.key`, kubeconfig) · LICENSE + README ·
origin-remote match · govulncheck ack-list 1:1 (`scripts/govulncheck-gate.sh`,
list at `references/security/govulncheck-acknowledged.md`) · `go mod tidy` drift
· per-file SPDX header · full golangci-lint · `make test-unit` · chart mirror
drift (`make helm-sync-check` — `crd-sources/` vs `config/crd/bases` #44). Fix
the root cause — never `--no-verify` (it skips ONLY the local hook; CI reruns the
gates).

## Common failure modes (generic / workflow)

Service-specific debugging (content-service 404, forwarder JWT 401,
SourceReachable rate-limit, AccessGroupSynced, hydrate-golden diff, mcp-echo,
ConflictWithUIRow (dormant/reserved — no UI write path in v1alpha1),
stale image roll) → **`references/troubleshooting.md`**. The
seven below are the cross-cutting workflow traps:

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

### ❌ Pushing without the gate
`git push --no-verify` bypasses the local hook ONLY (CI still runs it). ✅ Let
the installed hook gate (`make hooks`), or `make pre-push` then push. WHY:
pushed secrets / license-header drift / govulncheck regressions cannot be
un-true'd from public history. The 18-gate script is the contract.

### ❌ Kubectl from host against the kind cluster
`kubectl get pods` → context not found. ✅ Go through devtools:
`./scripts/dev.sh kubectl get pods`. WHY: the kind kubeconfig lives at
`/workspace/.gocache/kube/config` — inside the container.

### ❌ Editing files via relative paths when cwd is the wrong repo
Relative-path writes silently hit a sibling repo (`ach-old/`,
`alitellm-operator/`) if cwd is wrong — "succeeding" while leaving this repo
unchanged. ✅ Use absolute paths; verify with `pwd && git remote -v` (expect
`ackstorm/ach`).

### ❌ Editor save vs `ach-cli env hydrate` runtime-config — user edit silently lost
`ach-cli env hydrate` reads the adapter runtime-config file (`.claude/settings.json`,
`.gemini/settings.json`, `.codex/config.toml`, `.opencode/opencode.json`),
deep-merges ACH's keys, and atomic-renames the result back. The `<achDir>/lock`
flock excludes other ach-cli processes — NOT other tools. A concurrent editor
save (auto-format on file change, manual write) between hydrate's read and
hydrate's rename overwrites the merge with the user's pre-merge edit; on the
NEXT hydrate ACH re-merges its keys back in, so the engine self-heals — but the
user's edit made during the hydrate window is silently lost.

✅ Avoid saving the runtime-config files while `ach-cli env hydrate` is running. If
you need to edit the config concurrently, run hydrate to completion first
(`echo $?` == 0), THEN edit. There's no telemetry for the race; the user-visible
symptom is "my edit reverted." Documented as a known v1 trade-off (security
2.4 — accept-disposition); a future mtime-recheck would close it.

## Repository-specific patterns

- **ACHAgent placement**: `agentrender.ResolvePlacement` (agent ?? profile ?? `standalone`); the distributed
  matrix lives in `distributedContainers`/`distributedVolumes` (`achagent_workload.go`) and
  is asserted by `TestBuildDeployment_Distributed*`. The e2e stage 06 ships THREE shapes:
  `e2e-agent` (standalone, ephemeral) + `e2e-agent-dist` (`spec.placement: distributed`) +
  `e2e-agent-pvc` (standalone on the persistent `e2e-profile-pvc`), image `v0.16.5` pinned by
  digest, model `demo-model`. Two evidence tracks: `scripts/cluster.sh` gates the
  rendered shape (`WorkloadApplied`, uid/fsGroup 10001 on all three, harness
  `home/workspace` mount, no `workspace` subPath); `test/e2e/agent_runtime_ready_test.go`
  mints a real `ek_`, swaps it into `e2e-agent-ek`, and requires `WorkloadReady=True` on all
  three + the distributed isolation matrix + a PVC write as uid 10001 + the placement
  transition (populate standalone PVC → flip to distributed → same PV, same
  `home/workspace` contents, harness confined to it → flip back). Pods can hydrate because the e2e origin is
  `http://ach.e2e.local:8080` on BOTH sides (devtools `--add-host` → 127.0.0.1; CoreDNS
  rewrite → `ach-local-gateway:8080`) — never `localhost:8080`, which a pod resolves to
  itself.

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
- **Environment two-axis status**: `ExecutionResourcesResolved`
  (Plugin/Prompt/Artifact/**Skill** closed-set; `context.skills` is
  content-gated like plugins) + `AccessGroupSynced` (LiteLLM: names →
  IDs each reconcile, then `POST /v1/access_group`) — plus the per-Environment deny-all shell team (`ShellTeamFailed` when it cannot be provisioned/repaired). Composite `Available=True`
  rolls both up — that's what `ach-cli env hydrate` / the demo gate on.
  `spec.runtime.guardrails` (LiteLLM guardrail names) **requires a LiteLLM
  Enterprise licence** — team-scoped guardrails are premium-gated, so on an
  unlicensed proxy a non-empty list 403s at attach (the Environment never goes
  Available) AND on every request from a key in that team. Empty is exempt;
  global `default_on` guardrails run ungated and need no ACH config at all.
  It resolves against LiteLLM
  like the other runtime names; an unresolved one fails `AccessGroupSynced`,
  blocking new `ek_` mints but **not** existing keys, hydrate, or forwarded
  traffic. Guardrail coverage is **EK-only**. `ek_` keys live in the
  Environment's shell team and inherit its guardrails; `pk_` keys live in
  `ach-user-<email>` and reach the Environment through the access group,
  which carries no guardrail field, so **human CLI traffic to the same
  models is unguarded**. This is a known v1 limitation, not a bug.
- **Skill content kind**: a `Skill` CR (agentskills.io `SKILL.md` directory)
  mirrors **Plugin** end-to-end (fetch → `SKILL.md` Stage-2 validation gate →
  `skill/<name>.tar.gz` → `skills` projection → content-service
  `/content/skill/{name}` gzip). On hydrate it rides the plugin-mirrored stage
  root: extract to `<tmp>/skill/<name>`, nest under a synthetic `skills/<name>/`,
  then the EXISTING claudecode `skills/**/* → .claude/skills/**/*` rule projects
  it via `route.Project` (`projectSkills`). `SkillMarketplace` is a follow-up.
- **BIP + Environment forwarder read-path (Postgres-as-SoT, #34)**: operator
  projects BIPs/Environments → tables, emitting `NOTIFY ach_*_changed` from the
  same tx via `with_tx_notify`. The forwarder's `internal/forwarder/bipcache` +
  `internal/forwarder/envstore` each run a `db.Listener` + 5-min periodic
  refresh (LISTEN/NOTIFY is at-most-once on session loss). JWT mint reads the
  in-memory cache — no per-request Postgres/k8s hit.
- **SPDX-only license headers**: every `*.go` (outside `vendor/`,
  `zz_generated*.go`, `mock_*.go`) starts with
  `// SPDX-License-Identifier: Apache-2.0` (pre-push gate enforces;
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
with `make cluster-down`. **CI
does NOT run e2e at all** — the `e2e` job was removed from `ci.yml`, so e2e is a
**local-only** gate now. Run `make e2e-full` on the host before merging any
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
confirming E2E green.

## External references

Project docs may lag — verify current APIs with Context7 / DeepWiki / WebSearch:
- **controller-runtime / kubebuilder / client-go / cobra**: signatures pinned
  to `go.mod`.
- **Dex SSO**: WebFetch `https://dexidp.io/docs/` (OIDC connector/discovery).
- **goreleaser v2**: https://goreleaser.com — watch the `dockers` → `dockers_v2`
  migration (deferred; configs validate today).
- **Claude Code plugin / marketplace schemas**: JSON Schemas at schemastore.org
  (narrative: code.claude.com/docs/en/plugin-marketplaces). The parser
  (`internal/controller/ach/marketplace_parse.go`) follows the real schema with
  one drift ack: `url`-Kind entries carry an optional `path` (→ `git-subdir`).
  Plugin manifests (`.claude-plugin/plugin.json`) are **optional** per the
  schema; the Stage-2 gate (`verifyPluginContents`, `marketplace_manifest.go`)
  accepts a plugin that has the manifest OR ≥1 convention component
  (`commands/`/`agents/`/`skills/`/`hooks/`/`output-styles/`/`themes/`/
  `monitors/`, or root `SKILL.md`/`.mcp.json`/`.lsp.json`). Only a tar with
  none of these fails `UpstreamInvalid`.
- **SkillMarketplace discovery is convention-based, NOT index-based**: unlike
  `PluginMarketplace` (which parses `.claude-plugin/marketplace.json`), a
  `SkillMarketplace` fetches the repo as one tar.gz and tree-walks it
  (`skillmarketplace_discover.go` `discoverSkillsInTree`) for every directory
  one level under the skills-root (`spec.<git>.path`) whose
  `<dir>/SKILL.md` frontmatter `name` == `<dir>` (agentskills.io ships no index).
  For git sources an **unset `path` defaults to `skills`** (controller-side, in
  `skillMarketplaceSubPath`) — the dominant `anthropics/skills`-style monorepo
  layout; skill dirs at the **repo root** opt out with `path: "."`. The default
  is NOT a `+kubebuilder:default` on the shared `GitHubSource.Path` (that would
  wrongly fetch-narrow the OBJECT kinds Plugin/Artifact/Prompt/Skill).
  A `SkillMarketplace` is a **discovery** kind (like `PluginMarketplace`), NOT a
  narrow-at-fetch object: it strips `spec.<git>.path` before fetch (whole-repo
  tar, via `withoutGitPath`) and uses `path` only as the POST-FETCH skills-root
  tree-walk hint (`discoverSkillsInTree` + `sliceSkillSubtree` per skill). Each
  discovered skill is sliced into its own `skill-marketplace/<mkt>/<name>.tar.gz`
  and folded into the admin SKILLS inventory as `<skill>@<marketplace>` (mirrors
  the plugin merge).
- **`spec.<git>.path` narrows at FETCH time for OBJECT kinds (F1)**: the shared
  per-provider fetchers honor `spec.path` — a **directory** narrows to that
  subtree's contents (git on-disk via `git.tarSubtree`; legacy REST via
  `sources.NarrowArchiveSubtree`), a single **file** returns its raw bytes
  (Prompt, Artifact `scope=object`). Applies to the served/hydrated objects:
  `Plugin`, `Skill`, `Artifact`, `Prompt`. The fetcher infers file-vs-dir from
  the path shape (Artifact `scope` is orthogonal — it only drives cache-file
  naming; a CR's `scope` should match its path target). DISCOVERY kinds
  (`PluginMarketplace`, `SkillMarketplace`) opt OUT via `withoutGitPath` and
  walk the whole repo. Symlinked / traversal / missing paths →
  `UpstreamInvalid`.

# ACH — whole-system understanding (agent onboarding brief)

> **Purpose of this file:** the complete mental model of ACH so a fresh AI-agent
> session does NOT need to re-explore the repo. Read this first, then the
> per-surface docs it links. Snapshot as of **v0.6.15, 2026-07-19** — verify
> claims against code when a section looks stale (documentation-hygiene rule:
> update this file in the SAME commit that invalidates it).
>
> Complements (does not duplicate): `CLAUDE.md` (navigation hub),
> `references/repo-layout.md`, `references/makefile.md`,
> `references/troubleshooting.md` (25 service failure modes),
> `references/release-pipeline.md`, `docs/developer-guide/jwt-forwarder.md`,
> `docs/ach-project-spec.md` (CTO-level review brief), `spec/*.md` (frozen
> normative specs).

---

## 1. What ACH is, and why

**ACH = Agent Capability Hub.** One line: *IAM + package manager + fleet
manager for AI agents, GitOps-native.* ~146k lines of Go.

Business problem: AI usage inside companies explodes with zero governance —
developers paste API keys into local configs, teams wire their own agents,
nobody can answer "who can use which model, with which data, at what cost."
ACH answers with GitOps: capabilities are declared as Kubernetes resources,
reviewed in PRs, reconciled by an operator, audited centrally.

Two consumption surfaces:

1. **Developer workspaces** — `ach-cli login` + `ach-cli env hydrate` →
   local AI tools (Claude Code, Codex, Gemini CLI, OpenCode, Pi) configured
   with exactly the models/keys/skills the team is authorized for.
2. **Running agents** — an `ACHAgent` CR deploys a long-running agent
   (webhook/cron/queue/a2a-driven) that self-hydrates against ACH at boot and
   is reachable through a governed gateway.

Design scale envelope: single-cluster, single-org — hundreds of developers,
tens-to-hundreds of Environments/agents, content objects in tens of MB.

## 2. Why built ON TOP of a governance stack (boundary-layer design)

ACH is **not an execution engine**. Hub spec §1: "the product, key, content,
hydrator, identity-bridging, and forwarding boundary for AI runtimes." Each
underlying layer keeps its job:

- **LiteLLM** = execution authorization + budget + attribution backend.
  Source of truth (spec §17) for models, MCP servers, A2A agents, Teams,
  users, virtual keys, budgets. ACH never mirrors LiteLLM state as product
  truth; reaches it **exclusively via REST** (never via LiteLLM CRDs).
  Budget model: the operator creates a LiteLLM **tag** `<environment>` per
  Environment; `ek_` traffic carries the tag → user + Team + Environment
  budgets stack (hard 429). `pk_` traffic carries **no** tag — human traffic
  is capped by user/Team budgets only. "pk_ for humans, ek_ for workloads"
  is the contract; deliberately never server-enforced (§8.6).
- **Kubernetes** = ingress path for object creation (GitOps) + reconcile
  substrate. NOT the read path: since issue #34, **Postgres is the
  request-time source of truth**; only the operator holds K8s RBAC.
- **Dex/OIDC** = identity. SSO login mints `pk_`, capped into the caller's
  per-user deny-all shell team `ach-user-<email>` (platform-api provisions it
  idempotently at mint, matching key `duration`) — NOT teamless; the operator
  (sole writer of `assigned_team_ids`) attaches every entitled Environment's
  access group onto that shell each reconcile, so one `pk_` covers the union
  of the user's entitlements (locked residual: pre-change PKs stay
  `team_id=NULL`/fail-open until revoked by hand). `owner_email` stored
  verbatim (no normalization — canonicalization is Dex's job). Admin = SSO
  e-mail allowlist file (`/etc/ach/admins/admins.txt`), read at process
  start, no hot reload.
- **MCP / A2A servers** = runtime backends. ACH bridges SSO identity into
  them via 120-second Ed25519 JWTs, opt-in per target via
  `BackendIdentityPolicy`.
- **ToolHive** — NOT integrated. Never appears in the spec; grafted
  scaffolding removed 2026-07-15 (zero Go consumers, issue #59). Only
  residue: stale pins in `test/e2e/CHART_PINS.md`. MCP governance rides
  LiteLLM's MCP gateway instead.
- **Rate limiting / DoS** — explicitly delegated to LiteLLM + ingress/WAF
  (spec §18.1). ACH does not duplicate throttling.

## 3. Architecture — 5 logic modes + optional gateway, one binary

`ach` binary, cobra subcommand per mode (`cmd/ach/cmd/*.go`), each an
independent Helm Deployment sharing one image (`args: ["<mode>"]`).
Separate **`ach-cli`** binary drops all k8s deps (enforced by a
`go list -deps` gate). Both share `internal/cli/*`; both funnel every error
through `exit.DispatchAndRender` (sole `os.Exit` callers).

| Mode | Role | Key wiring |
|---|---|---|
| operator | Sole K8s watcher. Reconciles the 9 live CRD kinds → projects Postgres rows via `WithTxNotify` (row write + NOTIFY in one tx). Sole LiteLLM access-group/tag writer. Mints `ach-jwt-signing-keys` (mint-once, survives uninstall), bootstraps `LiteLLMConnection/default` pre-`mgr.Start` (Helm can't — REST-mapper limitation). Runnables: LiteLLM snapshot (5m), orphan cleanup, 5-min resync, `ach_refresh` LISTEN. | `ACH_DB_URL`, pepper, `ACH_NAMESPACE` (ach-system), `ACH_CACHE_ROOT` (/var/cache/ach), size caps, orphan knobs |
| platform-api | NO k8s client. Chi REST: Dex SSO + device-code CLI login, `pk_`/`ek_` lifecycle, `/platform/hydrate`, admin (inventory/refresh/keys/runtime-catalog), UI Objects API (Environment-only v1, GitOps-wins). | `ACH_BASE_URL`, DB, pepper, DEK, LiteLLM base+master, Dex 4-var set, Redis, `POD_NAMESPACE` |
| forwarder | Runtime data path `/v1 /v2 /gemini /mcp /a2a`. Key resolve (Redis 60s → Postgres), MCP/A2A precheck, header strip+rewrite, per-target JWT mint, JWKS. Only k8s touchpoint: the JWT Secret informer (field-selector-scoped). LiteLLM endpoint+key resolved from the `LiteLLMConnection/default` projection at boot (60s retry). | dual port: traffic :8080, health :8081 |
| content-service | Default: **sidecar in operator Pod** (RWO PVC forces co-location; `contentService.standalone=true` + RWX for HA, G16). 8-gate authz pipeline → `sendfile(2)` streaming, inode-pinned via early `os.Open`. Range/conditional headers ignored — always full 200, `Cache-Control: no-store`. | :8082, Redis envcache (`ach_environments_changed`) |
| migrate | golang-migrate one-shot init Job. 18 migrations in `db/migrations/`. | `ACH_MIGRATIONS_PATH` (/db/migrations) |
| gateway (optional) | Logic-free edge proxy, single origin: `/platform`→PA, `/content`→CS, `/v1 /v2 /gemini /mcp /a2a /.well-known`→FWD. No auth, no /metrics, no /dex. Plus `/agents/{ns}/{service}/…` from the `achagents` projection — only `expose.gateway=true` agents in route set; prefix stripped, tail verbatim; HMAC verified by the harness, not the gateway. **Requires `ACH_DB_URL`.** | disable via `gateway.enabled=false` |

**Universal read-path pattern:** `atomic.Pointer` in-memory cache +
`LISTEN ach_*_changed` + 5-min periodic refresh (`db.RunRefreshLoop`) —
LISTEN is at-most-once on session loss. Instances: forwarder `bipcache` +
`envstore`, content-service `envcache`, gateway `agentstore`. Consequence:
revocation/change propagation on the JWT trust path bounded ~5 min (gap #11);
LiteLLM-side key revocation is per-call, unaffected.

Dev/e2e routing: nginx `ach-local-gateway` is an **e2e-only shim** (single
`localhost:8080` origin; adds `/dex` + `/metrics/<svc>` kludges, falls through
to the real `ach-gateway`). See `references/local-testing-gateway.md`.

## 4. Resource model — 11 CRD kinds, 4 archetypes

`ach.ackstorm.ai/v1alpha1`, all namespaced. Archetype doctrine + 11-surface
parity checklist: `references/adding-a-cr-kind.md`.

- **object** (fetch → Stage-2 validate → cache tar.gz → serve → hydrate;
  `spec.<git>.path` narrows AT FETCH, F1 — dir→subtree, file→raw bytes):
  `Skill`, `Prompt`, `Artifact` (+ gated `Plugin`). 6 source types:
  github/gitlab/bitbucket (git protocol ONLY — ls-remote + shallow clone;
  no REST rate limits; token via `http.extraHeader`, never URL; gitlab uses
  Basic `oauth2:<token>`), s3 (ETag), gcs (generation), http
  (conditional-GET). Uniform cache format: everything `.tar.gz` (single-file
  Prompt/Artifact wrapped as 1-entry tar; migration needs a generation bump —
  `NotModified` shortcut skips rewrite otherwise).
- **discovery** (whole-repo fetch via `withoutGitPath`, tree-walk, slice per
  item): `SkillMarketplace` — agentskills.io convention, NO index; a skill =
  top-level dir whose `SKILL.md` frontmatter name == dir basename; unset git
  path defaults to `skills` (controller-side), `path: "."` opts out. Gated
  `PluginMarketplace` parses `.claude-plugin/marketplace.json`
  (manifest-less plugins accepted — any convention component suffices).
- **governance**: `Environment` — THE product boundary:
  `runtime{models,mcpServers,a2aAgents,guardrails}` + `context{prompts,plugins,artifacts,skills}`
  + `authorizedTeams` (≥1, CEL). Status: `ExecutionResourcesResolved`
  (LiteLLM name resolution + content-gating: `last_successful_refresh IS NULL`
  on a referenced skill/plugin row blocks it — prevents Available=True
  false-green) ∧ `AccessGroupSynced` (names→IDs each reconcile →
  `POST /v1/access_group`; the same condition also covers the per-Environment
  deny-all shell team, reason `ShellTeamFailed` when it cannot be created or
  repaired) → composite `Available`. Strict name deny-patterns
  (S2): no `/ \ ? # % whitespace ctrl DEL`; models get the looser pattern
  (provider-prefixed names allowed). `notice` (post-hydrate advisory) +
  `description` (catalog). `BackendIdentityPolicy` — target
  {MCPServer|A2AAgent, DNS-1123 name} + REQUIRED `forwardIdentityJWT` (no
  default, CEL). Duplicates allowed: forwarder sorts matching CRs by name ASC,
  **LAST wins**, no status churn (operator stays dumb).
- **config singleton**: `LiteLLMConnection` — name CEL-forced `default`;
  endpoint + master-key SecretRef. Operator probes, `EnsureDefaultTeam`
  (idempotent — LiteLLM assigns UUID team_id).
- **agent fleet**: `AgentProfile` (infra template: resources,
  extraEnv (ACH_* CEL-forbidden), persistence PVC (Retain/Delete), egress-only
  default-deny `networkPolicy` opt-in (DNS rule + declared peers), raw
  `podTemplate` strategic-merge overlay — pass-through by design, selector +
  config-hash re-pinned after merge) + `ACHAgent` (instance: `identity.secretRef`
  ek_ → `ACH_TOKEN` env via secretKeyRef — never file-mounted, explicitly NOT
  an isolation boundary (same-uid reads /proc/pid/environ); channels
  webhook (gitlab/github/generic auth, botUsername loop-guard, triggerUsers
  allowlist) / cron / queue / a2a; session spec (none/auto/custom + compact/
  rotate overflow); memory hindsight (bank NEVER templated from payload —
  cross-tenant risk) / codemem; harness MCP servers repoCheckout (harness-
  hosted, ek-injected) / local (stdio passthrough, ACH_*/ek stripped from env
  fwd) / remote (headers = ${env:NAME} refs; co-resident same-uid CAN read —
  front via ACH if unacceptable); `expose.service` + `expose.gateway` both
  default false, gateway requires service). Operator renders `agent-config-v1`
  ConfigMap + single-replica Deployment (`internal/agentrender`, JSON tags
  schema-locked); salted config-hash roll; harness **self-hydrates** at boot —
  no init container; status from probes. Profile defaults under `spec.achagent`
  (image, ach, model, engine, limits, health, cost) deep-merge per-field with the
  agent's flat overrides (set agent field wins; `engine.forwardEnv`/`model.params`/
  `model.thinking`/`engine.pi`/`cost` atomic). Profile-only infra (not overridable):
  imagePullSecrets, resources, extraEnv, nodeSelector, tolerations, persistence,
  networkPolicy, terminationGracePeriodSeconds, podTemplate. `ach.baseUrl` = agent ?? profile
  ?? operator ACH_BASE_URL (empty blocks); `health` resolved ONCE
  (`ResolveHealth`) for config + Service targetPort + probes. `status.gatewayURL` host from
  `ACH_PUBLIC_BASE_URL` ?? `ACH_BASE_URL`, else path-only.

Not CRDs (common confusion): pk_/ek_ (platform-api/DB objects), teams
(LiteLLM + runtime catalog), gateway route set (`achagents` projection).

**Plugins gated OFF**: compile-time `featuregate.PluginsEnabled = false` —
a Go const so the compiler dead-code-eliminates and no env var re-enables.
Six gate sites: operator reconciler wiring, content-service `/content/plugin`
route, admin inventory listers, environment reconciler (`context.plugins`
SKIPPED, not failed), localpkg discover lenses, ach-cli command tree
(`local plugin` unregistered). Skill/SkillMarketplace are the live twins.
Flip + `make helm-sync` re-enables everything. NOT a bug to "fix".

## 5. Database — Postgres as source of truth

18 migrations → 18 tables. Full detail per migration in git; summary:

- **Hub operational** (platform-api-owned, NOT re-derivable from CRDs):
  `personal_keys` (PK `pkid_*`, `credential_hash` UNIQUE, sliding
  `expires_at`, `litellm_user_id`, `litellm_token` partial-unique,
  `litellm_key_material_enc`), `environment_keys` (PK `ekid_*`, same trio,
  no expiry), `external_refs` (PK kind,name), `marketplace_plugins`
  (PK marketplace,name).
- **Operator projections** (re-derivable): `environments` (text[] axes +
  3 jsonb condition cols + notice/description), `plugins`, `prompts`,
  `artifacts`, `skills`, `litellm_connections`,
  `backend_identity_policies`, `marketplaces`, `skill_marketplaces`,
  `skill_marketplace_skills`, `runtime_catalog_entries` (LiteLLM registry
  read-model: model/mcp_server/a2a_agent/team, active/missing),
  `achagents` (gateway route set; `exposed` bool; hard DELETE, no drain).
- **Cross-cutting columns**: `origin ('cr'|'ui')` + `locked` (GitOps-wins:
  operator UPSERTs guarded `ON CONFLICT … WHERE existing.origin='cr'`;
  CR apply TAKES OVER a ui row 'ui'→'cr' locked=TRUE; UI fenced with
  `403 immutable_via_ui`); `deletion_timestamp` soft-delete drain markers +
  partial indexes; `force_refresh_requested_at`; `upstream_rev`
  (SHA/ETag/generation/ETag|Last-Modified).
- **NOTIFY**: `internal/db.WithTxNotify` — `pg_notify` inside the same tx as
  the row write (closes visibility race). 13 `ach_*_changed` channels + the
  `ach_refresh` admin-refresh signal channel.
- Auth-critical queries: `PkCheckAndExtend` (atomic CTE `FOR UPDATE`,
  7-day sliding window, 5-min debounce, **90-day hard cap** from created_at),
  `EkResolve` (debounced last_used). `(nil,nil)` = revoked/expired/unknown,
  deliberately indistinguishable (→ 401 `expired_or_revoked`).
- The pk_ slide is **mirrored onto LiteLLM**: `PkCheckAndExtend` returns
  `Extended`, and `keystore.NewLiteLLMPkExtendHook` fires a background
  `POST /key/update` (best-effort, off the request path) so the LiteLLM key's
  expiry tracks the ACH row's. Without the mirror the LiteLLM key dies at
  mint+7d while ACH still honours the pk_ for up to 90 days — LLM traffic 401s
  at LiteLLM against a key `ach-cli` reports as active. `db.PkSlidingWindow` is
  the one canonical window value (mint, slide, and mirror all read it).
- **Nothing ever writes `status='expired'`** — expiry lives only in the auth
  predicate. Any read path reporting liveness (e.g. `db.ListKeys`) must DERIVE
  it from `expires_at` + the 90d cap, or it will report dead keys as active.

## 6. Identity + security model

- **Bearers**: 48 bytes crypto/rand → 64 base64url chars (384-bit). Key-ids
  `pkid_`/`ekid_` (ULID) — a deliberately distinct namespace (pasting a
  bearer where an id belongs → `400 invalid_argument`).
- **At rest**: no plaintext anywhere. HMAC-SHA-256 `credential_hash` with
  pepper held OUTSIDE Postgres (`ACH_CREDENTIAL_HASH_PEPPER`; placeholder
  refused, fail-fast). LiteLLM virtual-key material sealed AES-256-GCM under
  `ACH_KEY_ENCRYPTION_KEY` (base64 of exactly 32 bytes; G3, migration
  000014). Both Secrets provisioned out-of-band, MUST stay stable across
  upgrades. `keys`/`credhash`/`keycrypt` packages carry NO logger imports;
  plaintext emitted at exactly 2 sites (SSO callback, POST /platform/keys),
  CI grep-gated. Authn middleware `Header.Del`s the bearer post-resolve.
- **Asymmetric revocation** (load-bearing, opposite by design):
  - `pk_` **DB-first** — both CS + forwarder run check-and-extend per
    request, so the Postgres flip is the barrier.
  - `ek_` **LiteLLM-first** — runtime rides LiteLLM and ek CS-auth has no
    per-request re-check, so the LiteLLM ack is the barrier. Out-of-band
    deleted LiteLLM key → 404 treated as idempotent success.
  - Redis keystore TTL 60s, NON-configurable = bounded acceptance window.
- **JWT trust path** (`/mcp`, `/a2a`; authoritative:
  `docs/developer-guide/jwt-forwarder.md`): BIP opts in → forwarder mints
  EdDSA `{iss=ACH_BASE_URL, sub=<bare owner-email> (hard-cut from the old
  ns/email form — do NOT split on '/'), email (additive, omitted when
  empty), groups (additive, LiteLLM team ALIASES — never UUIDs — omitted
  when empty; pk_=caller's teams ∩ union of authorizedTeams across active
  Envs whose runtime.mcpServers/a2aAgents contains <name>, ek_=own Env's
  authorizedTeams; ach-env-*/ach-user-* shell teams stripped), aud=mcp:<name>
  |a2a:<name>, iat, exp=iat+120}`. **No nbf, no jti.** Staleness: exp(120s) +
  60s teams-cache = ~180s worst case for a team-membership change to reach a
  backend — same window the pk_ precheck already runs on, not new exposure.
  JWKS `Cache-Control: max-age=3600` forces ≥24h rotation overlap. Fail-closed
  default: no BIP → Authorization stripped, nothing attached (no smuggling).
  LiteLLM MCP gateway needs `extra_headers: ["authorization"]` +
  `allow_all_keys: true` on the server registration or the JWT never reaches
  the backend (top failure mode — surfaces as `tools=[]`).
- **LiteLLM auth (post-migration 000011, TESTING-PHASE)**: forwarder
  authenticates with the **caller's own** virtual key — bare
  `x-litellm-api-key` on `/v1`+`/v2`+`/a2a`, `Bearer`-prefixed on `/mcp`, bare
  `x-goog-api-key` on `/gemini` (native passthrough reads only that). Master
  key OUT of the data path (survives only for teams precheck + admin ops).
  `x-litellm-key-id` delegation no longer sent; pre-000011 keys have NULL
  material → 401, re-mint. Director collapses `/mcp/<n>/…` to the bare
  `/mcp/<n>` (LiteLLM admin-only route guard). The old master-key
  impersonation doc (`docs/developer-guide/litellm-custom-auth.md`) describes
  the superseded path.
- **Header rewrite** (every route): strip ALL `Authorization`,
  `x-litellm-*`, `x-ach-*`, hop-by-hop → write auth headers fresh. `ek_`
  runtime traffic gets the `<environment>` tag injected; `pk_` untagged.
- **Precheck** (mcp/a2a only, before JWT): ek → name ∈ bound Environment's
  runtime list (O(1) cache, terminating fail-closed); pk → LiteLLM team
  intersect (unreachable → `503 litellm_unreachable`). `/v1`+`/v2`+`/gemini` skip
  (model authz delegated to LiteLLM). `/v2` behaves exactly as `/v1`: bare
  `x-litellm-api-key`, no precheck, and no JWT.
- **Supply chain**: git protocol allow-list pinned
  (`protocol.allow=never`, https allow, file top-level-only —
  CVE-2022-39253-class defense); CR-02 validators reject URL/argv
  metacharacters in CR git fields; `os.MkdirTemp` (symlink-race fix);
  30s inner ls-remote timeout + `WaitDelay`.
- **Safe tar extraction** (CLI `internal/cli/extract` + operator
  `internal/contentkit`, same rules F3): reject absolute, `..`, hardlink,
  device, FIFO, socket, sparse, pax-path injection, unknown typeflags;
  symlink in-tree-only behind `--allow-symlinks`; modes `& 0755` with
  setuid/setgid/sticky/group+world-write stripped; streaming (never
  buffered), per-kind bomb caps fired BEFORE body write
  (`ACH_MAX_EXTRACTED_{PLUGIN=200,ARTIFACT=500,SKILL=50}_MIB`,
  `ACH_MAX_ARCHIVE_ENTRIES=65536`).
- **Orphan cleanup** (`internal/orphan`): joins on LiteLLM
  `metadata.ach_key_id` (the `pkid_/ekid_` namespace) — past incident: a join
  on the opaque token namespace mis-classified everything and revoked a
  fleet. Guards: ownership gate, B1 empty-active-set skip, B2 circuit
  breaker (`ACH_ORPHAN_CLEANUP_MAX_REVOKE`, default 10), B3 dry-run
  (`MustEnvBool` — typo fails startup), 10-min age floor. Known limit: only
  users with an ACTIVE ACH row are checked.
- **Audit** (spec §18.2): structured JSON, `audit=true`, closed outcome
  enum, `actor=<ns>/<email>`, key.id only — never plaintext/hash. Runtime
  forwarding NOT audited (LiteLLM is that source). Metrics (spec §18.5):
  per-service process-local registries, typed label enums, no per-request
  labels; `ach_litellm_unreachable_total{caller}` cross-service;
  `forwarder_jwt_signed/suppressed_total{reason}` = BIP visibility.

## 7. Platform API surfaces

Middleware order: RequestID (always server-minted) → RecoverPanic →
AccessLog (never logs x-ach-key) → ContentTypeJSON → Authn.

- SSO: `/platform/auth/login|callback` (OIDC+PKCE, `__Host-ach_sso` cookie;
  hardened iff `ACH_BASE_URL` is https) + device-code CLI flow
  `/platform/auth/cli/{init,token}` (Redis session, GETDEL one-shot).
  First login: `provisionUser` → LiteLLM UserNew + `default` Team add, NO
  max_budget. Missing default team → `500 default_team_missing`, fail-loud,
  self-heals via operator team bootstrap.
- Keys: `POST /platform/keys` (ek create, §8.2 8-step: env exists + not
  terminating → team intersect → verify-or-create LiteLLM user → gate on
  `AccessGroupSynced=True` else `503 not_ready` → KeyGenerate into the
  Environment's shell team (`team_id`) + tag, no access-group binding →
  INSERT w/ LiteLLM compensation on failure → plaintext once),
  `GET /platform/keys`, `DELETE /platform/keys/{id}` (prefix-dispatched;
  caller-scoped pk self-revoke NOT admin-gated, `?force=true` overrides
  active-key 409).
- Hydrate: `POST /platform/hydrate` — pk requires body `environment` +
  team-intersect (`400 missing_environment` / `403 unauthorized_team`); ek
  optional/must-match (`403 wrong_environment`). Response
  `schemaVersion:"v1alpha1"`, runtime+context ALWAYS present (`[]` never
  null), no plaintext ever. Terminating envs still hydrate.
- Admin (SSO allowlist, ek → `401 invalid_key_type`, non-listed →
  `403 not_admin` BEFORE validation): keys revoke/list, users revoke-keys,
  `POST /platform/admin/refresh` (PA's only "write" — force_refresh marker +
  `ach_refresh` NOTIFY; the operator's refreshsignal listener maps it to a
  GenericEvent), object inventory (SYNC column: Available/Degraded |
  fresh/STALE/never | `fresh*` false-green for name-only prompts/artifacts |
  projected), runtime catalog (`/platform/admin/runtime/{models,mcp-servers,a2a-agents,teams,guardrails,catalog}`).
- UI Objects API (G2, Environment-only v1): `origin='ui'` DRAFT rows, YAML
  export → kubectl apply → operator takeover. `ACH_DISABLE_UI_WRITES=true`
  kills the write path.

## 8. Hydrate engine (`internal/cli/hydrate`) — 14-step commit

Under advisory `flock` on `.ach/<env>/lock` (FailFast default, `--wait`,
`--lock-timeout`): sweep tmp → read state (schemaVersion "3"; mismatch exit 5
unless `--force`; env mismatch exit 4) → prune vs disk → `POST /platform/hydrate`
(strict decode) → scope diff → unconditional content GET with xxh3
short-circuit → safe extract (plugins/skills to EPHEMERAL `.ach/<env>/tmp` —
**no persistent plugin cache, by design**; prompts/artifacts to
`.ach/<env>/<kind>`) → adapter render → optional `--sync` inverse-merge
(deepest-first, drift-wins preserve, deep-key/composite-marker subtraction,
`os.Remove` only) → **atomic state write LAST**
(tmp+fsync(fd)+rename+fsync(parent) — SIGKILL-safe at any step; e2e-verified
via `-tags=e2e` kill seam, prod build gets a no-op stub) → gitignore
marker-block (covers credential-bearing files) → cleanup.

- **Dual-hash drift** (xxh3, non-crypto): `hash` (on-disk) + `sourceHash`
  (pre-transform upstream) → 4-outcome matrix: no-op /
  upstream-only-overwrite / local-edit-preserve (exit 2) / conflict-preserve
  (exit 2); `--force` takes upstream.
- **Merge strategies**: `replace` / `deep` (JSON/TOML, incoming leaves win,
  arrays wholesale, contributed `keys[]` tracked for surgical inverse-merge)
  / `composite` (marker-bounded markdown). MergeDeep files written 0600
  (credential-bearing), others 0644.
- **Adapters** (closed set, init-registered, case-fold+alias lookup):
  claudecode (canonical pass-through → `.claude/` + root `.mcp.json` +
  CLAUDE.md), codex (TOML `.codex/config.toml`, `tools:`→`allowed_tools:`),
  gemini (`.gemini/settings.json`), opencode (`.opencode/opencode.json`),
  pimono (`.pi/`). Under `--global` each adapter's paths resolve through its
  own config-dir env var (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`,
  `GEMINI_CLI_HOME`, `PI_CODING_AGENT_DIR`, `XDG_CONFIG_HOME`) — see
  `internal/cli/CLAUDE.md` "Global-scope root resolution". Credential
  injected ONLY via context (`WithCredential`) — adapters can't read env for
  credentials (the `--global` root resolution above is path scope, not an
  adapter, and reads only the user's own config-dir vars).
  Runtime config wires MCP + A2A as http entries with
  `x-ach-key` + `x-ach-environment` headers. **Models are never projected**
  (server-side access-group is the mechanism).
- State namespaced per env BOTH scopes (`.ach/<env>/` project,
  `$HOME/.ach/<env>/` global); per-platform `state-<platform>.json`;
  legacy flat state auto-migrated. Runtime mirrors
  (`runtime-<platform>/{mcp,a2a,model}.json`) credential-free.
- `ach.yaml` (`env save`) = committed, hub-agnostic env-name manifest; bare
  `env hydrate` reads it, best-effort per env. `env uninstall` reuses Sync.
- Exit codes (closed matrix, `internal/cli/exit`): 0 ok · 1 general ·
  2 drift · 3 auth · 4 env-mismatch · 5 schema-mismatch · 6 network/503 ·
  7 collision-refuse (auto-claim differing bytes) · 8 config file.
  `MapServerError` is the single HTTP→code chokepoint.
- **In-flight change (2026-07-19, uncommitted)**: flip runtime projection ON
  by default (`--no-runtime` opt-out, `--include-runtime` deprecated alias),
  models become an informative summary line. Plan:
  `docs/plans/2026-07-19-hydrate-runtime-on-by-default-plan.md`.

## 9. CLI command tree (ach-cli)

`login` (device-code SSO) · `logout` · `whoami [--verify]` (pk→env list,
ek→hydrate probe) · `config add/list/show/use/remove/rename/rm-ek`
(multi-profile `~/.config/ach/config.yaml`, 0600/0700, https-only unless
`--insecure`/`ACH_INSECURE` — G19) · `env list/describe/hydrate/status/save/uninstall`
· `keys create/list/revoke/prune` (create auto-saves ek into profile unless
`--no-save`; prune keeps newest pk, never force-revokes active) ·
`admin keys/users/refresh/list` (exit 3 on not_admin) ·
`runtime models/mcp/a2a/teams/catalog` · `content fetch` ·
`local repo add/list/remove/update` + `local skill install/uninstall/update/outdated/list`
(`local plugin` gated off).

Credential precedence: synthetic (`ACH_BASE_URL`+`ACH_API_KEY`) → `--api-key`
→ `--env-key label` → `ACH_API_KEY` → `ACH_ENV_KEY` → profile pk. Profile:
`--profile` → `ACH_PROFILE` → `default:` → sole entry.

**Synthetic mode**: `ACH_BASE_URL` + credential = headless/no-disk-config;
half-set (URL alone) = hard error; login/logout/config/`--profile`/`--env-key`
refused; profile label `(env)`. `ACH_PLATFORM_URL` is the login-prompt
prefill — a DIFFERENT var, does not trigger synthetic.

**Local package manager** (serverless, no hub): `repo add github:owner/repo`
/ `git:url` → ls-remote + tarball → 4-lens capability detect
(skill-marketplace roots `["", "skills"]`; plugin lenses gated). Install by
`name@repo` (LastIndex '@'), same `route.Project` engine as hydrate, with
project-root containment (only `.mcp.json` allowed at root). State
`~/.config/ach/local/{repos.json, credentials.json (0600), installed.json}`.

## 10. Machinery (build, test, release)

- **Host has NO Go** — everything via `ach-devtools` container
  (`container_target` macro; 3-context model → `references/makefile.md`).
  Per-worktree `.gocache` (read-only modcache → `make clean-cache`).
- Test ladder: `test-unit` ~10s → `test-envtest[-fast]` 3-7m → `e2e-full`
  kind+Helm ~6-10m, **cluster KEPT after** (debug loop:
  `logs-*`, `e2e-focus RUN=…`, `cluster-sync` w/ `image.rebuildId` roll).
  **CI runs NO e2e** — local-only gate before merging controller/service/
  CRD/Helm/e2e changes. CI = PR-only (lint+unit+envtest+security); no branch
  protection yet → direct pushes unguarded.
- e2e: stdlib testing (Ginkgo REJECTED — user memory), synced fixtures
  `test/e2e/cluster/{00..06}` gated healthy by `verify_all`; tests ASSERT
  against synced state, only mutate throwaways. Golden
  `examples/hydrate.json` byte-diffed (host-normalized). mcp-echo = stdlib
  JWT-verifying reference backend.
- Pre-push: 18 hard gates (installed hook; never `--no-verify`).
- Release: commit-msg-driven `chore(release): vX.Y.Z` push → release.yml →
  tests → bot manifest bump → goreleaser (multi-arch `ach` + distroless
  `ach-cli` images, CycloneDX SBOM, cosign keyless) → OCI Helm chart →
  **tag LAST** (no orphan tags). `make release-cut VERSION=…`.
- Helm: only supported install path (kustomize deleted 2026-07-17;
  `config/` = build/test scaffolding; `config/default` RBAC broken for
  ACHAgent — known gap #9).

## 11. Known gaps / accepted trade-offs (from `docs/ach-project-spec.md` §4)

1. e2e not a CI gate (local discipline). 2. No branch protection on main.
3. Hydrate vs editor-save race (edit in hydrate window silently lost;
self-heals next run; mtime-recheck would close). 4. Environment authoring
admin-only (no ACL model yet). 5. UI write path Environment-only.
6. CS HA requires RWX. 7. Plugins carried dormant (insurance w/ premiums).
8. LISTEN/NOTIFY at-most-once (5-min bound). 9. `config/default` advertised
in release notes but RBAC can't reconcile ACHAgent. 10. Postgres DR
undefined (pk/ek + catalog rows NOT re-derivable). 11. ~5-min revocation
bound on the BIP/Env JWT path.

## 12. Spec-vs-code drift (code wins; spec cadence pending)

- JWT `sub` = **bare email** now (spec §9.1 says `<ns>/<email>`); additive
  `email` claim; **no nbf** (spec mandates nbf).
- BIP duplicate tiebreak: name-ASC **LAST** wins, NO `DuplicateTarget`
  status (spec said lowest-wins + status).
- LiteLLM data-path auth: per-user key material (000011/000014) supersedes
  the master-key impersonation protocol in
  `docs/developer-guide/litellm-custom-auth.md` (kept as history).
- `NameConflict` reason deleted (v0.2.5+): marketplace-scoped
  `name@marketplace` refs make cross-marketplace conflicts impossible;
  intra-marketplace dups soft-skip (`DuplicateName` in message).
- `authSecretRef` optional on git sources (spec §10.1 says required);
  `transport: rest` escape hatch removed — git-only.
- ROADMAP.md progress table stale (stops at Phase 3 "not started"; all 7
  phases shipped long ago) — treat git history + CHANGELOG as truth.

## 13. Memory anchors (session memories exist for these)

Prod env placeholder MCP+A2A unblock; hydrate has NO plugin disk cache
(never re-add); never hand-delete ACH-owned LiteLLM users (cascades pk_);
env-list stale deletion_timestamp bug (PR #113); pre-push trufflehog
worktree false-positive; LiteLLM UserNew leak fix (auto_create_key:false +
UserID:email).

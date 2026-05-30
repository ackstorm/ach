# ACH — Agent Capability Hub

## What This Is

ACH (Agent Capability Hub) is the product, key, content, hydrator, identity-bridging, and forwarding boundary for AI runtimes. Hub-side it is a Kubernetes-native platform (Operator + Platform API + Forwarder + Content Service) that defines `Environment`s as curated bundles of LiteLLM-registered execution capabilities (models, MCP servers, A2A agents) plus ACH-served content (prompts, plugins, artifacts), with two key types — `pk_` (Personal Key, SSO-bound) and `ek_` (Environment Key, workload-bound). CLI-side it is the `ach-cli` binary (split from the service-mode `ach` binary in phase 6 follow-up) that hydrates a chosen Environment into the local filesystem of one of four supported AI agent platforms (Claude Code, OpenAI Codex CLI, Google Gemini CLI, OpenCode), translating a runtime-agnostic JSON manifest into the platform's native runtime configuration and unpacking trusted content.

## Core Value

A user runs `ach-cli hydrate --environment <env>` and gets a working AI agent configured against an ACH-curated set of models, MCP servers, A2A agents, prompts, plugins, and artifacts — with cost attributed to the right budget surface, content served from a cached PVC the operator controls, and a single `x-ach-key` header authenticating every call. If everything else fails, this end-to-end path must work for both `pk_` and `ek_` against all four shipped platform adapters.

## Requirements

### Validated

(None yet — v1alpha1 has no deployed installs; ship to validate.)

### Active

- [ ] **Hub: API group + 6 CRDs** — `Environment`, `Plugin`, `PluginMarketplace`, `Artifact`, `Prompt`, `BackendIdentityPolicy` under `ach.ackstorm.ai/v1alpha1`, with CEL admission validation. (Hub spec §2)
- [ ] **Hub: Two-key model** — `pk_` (Personal Key, sliding-window 7-day expiry, accepted everywhere) and `ek_` (Environment Key, bound to one Environment, no expiry, revocation-only). `pk_` on runtime is permanent first-class; no server-side toggle to forbid it. (Hub spec §3, §7, §8)
- [ ] **Hub: Single ACH-specific HTTP header `x-ach-key`** plus `x-ach-environment` (Content Service only). No other ACH-specific auth headers. (Hub spec §3, §5)
- [ ] **Hub: ACH Operator** — controller-runtime-based reconciler. Owns Environment status, LiteLLM access group + budget tag lifecycle (sole writer), PluginMarketplace materialization, external-ref refresh, plugin-too-large enforcement, finalizer-driven deletion drain (§6.5), orphan LiteLLM key cleanup, BackendIdentityPolicy duplicate-target status. (Hub spec §5.1, §6.2, §6.3, §6.5, §10, §12.4, §18.4, §9.3)
- [ ] **Hub: Platform API** — Dex SSO, `pk_`/`ek_` lifecycle, hydrate (§15.1), Environment listing, admin endpoints, `force-refresh` annotation patch (only non-Operator write surface to ACH CRDs). (Hub spec §5.1, §15.4, §15.5, §18)
- [ ] **Hub: Forwarder** — runtime forwarding for `/v1`, `/gemini`, `/mcp`, `/a2a`. Strips client `Authorization` + `x-litellm-*` + `x-ach-*` on every route; writes `x-litellm-api-key` + `x-litellm-key-id`; signs and attaches ACH JWT (EdDSA) only on MCP/A2A AND only when a matching `BackendIdentityPolicy` opts in (fail-closed). Owns `/.well-known/jwks.json`. Reads from in-process informer cache + Redis(≤60s) + Postgres. (Hub spec §5.1, §9.1, §9.2, §9.3)
- [ ] **Hub: Content Service** — single read endpoint `GET /content/{kind}/{name}`, streams via `sendfile(2)` from the shared RWO PVC. Authorizes per-request: §7.1 check-and-extend on `pk_`, Redis→Postgres on `ek_`, Team-membership intersection for `pk_`, scope resolution for artifacts (§13). Co-located with the Operator in a single-replica `Recreate` Pod. (Hub spec §5.1, §15.6)
- [ ] **Hub: External-reference cache** — fetch from `github`, `gitlab`, `bitbucket`, `s3`, `gcs`, `http`. Atomic `rename(2)` publication on a `ReadWriteOnce` PVC (§10.3). Required `refresh.maxStaleness` on every CRD. Plugin `.tar.gz` hard size cap via `ACH_PLUGIN_MAX_SIZE_MIB` (default 50; Operator refuses to start at `0`/negative/non-numeric). (Hub spec §10, §11, §12, §13, §14)
- [ ] **Hub: PluginMarketplace** — Claude Code marketplace schema verbatim. Two-stage refresh (file → per-plugin best-effort → DELETE sweep). RE2 regex include/exclude filters anchored at start, case-sensitive. Cross-marketplace name conflicts resolved alphabetically. `npm` source skipped with `Synced=False, reason=UnsupportedPluginSource`. (Hub spec §12)
- [ ] **Hub: ACH JWT trust path** — backend-pull JWKS at `/.well-known/jwks.json` with `Cache-Control: public, max-age=3600`. Ed25519 OKP keys with `kid`. Rotation: ≥24h overlap exceeding backend cache TTL. Manual rotation in v1alpha1, single Secret `ach-jwt-signing-keys` RBAC-scoped to Forwarder SA. (Hub spec §9.1, §9.2)
- [ ] **Hub: Asymmetric revocation** — `pk_` is **DB-first** (Postgres status flip is the load-bearing barrier), `ek_` is **LiteLLM-first** (LiteLLM revocation is the load-bearing barrier). (Hub spec §7.1, §8.5, AC13)
- [ ] **Hub: Audit + Metrics** — structured JSON audit events (§18.2) with stable `outcome` enum. Prometheus `/metrics` per component with normative label-value enums (§18.5). `key.id` in audit uses `pkid_`/`ekid_` prefixes, never plaintext. (Hub spec §18.2, §18.5)
- [ ] **Hub: ACH DB schema** — Postgres tables for `personal_keys`, `environment_keys`, `external_refs`, `marketplace_plugins`. HMAC-SHA-256 credential hash with server-side pepper held outside Postgres. `key_id` PRIMARY KEY with `pkid_`/`ekid_` prefixes (distinct from plaintext bearer prefixes). (Hub spec §16, §16.1)
- [ ] **Hub: LiteLLM decoupling** — Hub never reads any `litellm.ackstorm.ai` CRD at runtime. Talks to LiteLLM exclusively via REST. The `default` Team is a deployer concern; missing → `500 default_team_missing`. (Hub spec §17)
- [ ] **Hub: Multi-tenancy** — namespace-scoped deployments. JWT `sub = <namespace>/<owner-email>`. Distinct deployments MUST configure distinct `ACH_BASE_URL` unless intentionally sharing JWKS. (Hub spec §18.3)
- [ ] **Hub: Admin allowlist** — ConfigMap-mounted file (default `/etc/ach/admins/admins.txt`), one SSO email per line. Read at process start; ConfigMap edits require Platform API restart. `ek_` never accepted on admin endpoints. (Hub spec §18, AC24)
- [ ] **CLI: Multi-deployment registry** — `~/.config/ach/config.yaml` with named deployment entries (`url`, `pk`, optional `ek` map). Mode `0600` enforced. `default:` selector + `--deployment <name>` per-invocation override. (CLI spec §3, §15.4 of Hub)
- [ ] **CLI: Synthetic deployment mode** — `ACH_BASE_URL` + `ACH_API_KEY` (or `--api-key`) bypasses the config file. Designed for K8s InitContainer use. `ach-cli login`/`config`/`logout`/`env-keys create --save-as` exit 1 in this mode. (CLI spec §3.3, §2.3)
- [ ] **CLI: Authentication commands** — `ach-cli login` (Dex SSO mints `pk_`, written to `deployments.<name>.pk`), `ach-cli logout`, `ach-cli whoami [--verify]` (asymmetric verification: `pk_` → `GET /platform/environments?limit=1`, `ek_` → `POST /platform/hydrate {}`), `ach-cli config {list,show,use,remove,rename}`. (CLI spec §5.1–§5.4)
- [ ] **CLI: Environment & key commands** — `ach-cli env list`, `ach-cli env describe <name>` (two-call: list + hydrate), `ach-cli env-keys {create,list,revoke}`. `--save-as <label>` writes plaintext to `deployments.<active>.ek.<label>`. `key_id` arguments expect `ekid_` prefix. (CLI spec §5.5, §5.6)
- [ ] **CLI: Hydrate engine** — POST `/platform/hydrate`, scope-aware diff (`runtime` vs `context`), three-state per-resource state machine, dual-hash drift detection (`hash` for on-disk, `sourceHash` for upstream pre-transformation), `--include-runtime` / `--only-runtime` / `--sync` / `--force` / `--dry-run` flags. Default writes context only. Workspace vs global scope (`-g`). Lock + atomic state write. (CLI spec §5.7, §6, §8)
- [ ] **CLI: Safe tar extraction** — reject absolute paths, `..` segments, symlinks (default; `--allow-symlinks` opt-in for in-tree targets), hardlinks, devices, FIFOs, sockets, pax-extended-header path injections. Mode mask `0755`, strip setuid/setgid/sticky. Decompression-bomb caps (`ACH_MAX_EXTRACTED_PLUGIN_MIB` default 200, `ACH_MAX_EXTRACTED_ARTIFACT_MIB` default 500, `ACH_MAX_ARCHIVE_ENTRIES` default 65536). Atomic temp-then-`rename(2)`. (CLI spec §6.4)
- [ ] **CLI: Four platform adapters** — `claude-code` (pass-through), `codex`, `gemini-cli`, `opencode`. Each declares `detection: [...]`, `aliases: [...]`, runtime-config file(s) with merge strategy (`deep` for JSON/TOML, `composite` for markdown). Plugin transformation imperative in each adapter (no DSL); v1alpha1 ships four. Platform autodetection on cwd when `--platform` omitted. (CLI spec §7)
- [ ] **CLI: State schema v2** — `<ach-dir>/state.json` with per-file `{ target, hash, sourceHash, merge?, keys? }` entries. `merge` + `keys[]` enable precise inverse-merge on `--sync` for user-co-owned files (e.g. `.claude/.mcp.json`). Atomic write via tmp → `fsync(fd)` → `rename(2)` → `fsync(parent_dir)`. (CLI spec §8)
- [ ] **CLI: Admin commands** — `ach-cli admin keys revoke <pkid_|ekid_>`, `ach-cli admin users revoke-keys <email>`, `ach-cli admin refresh <kind> <name>`. `403 not_admin` → CLI exit 3. (CLI spec §5.10)
- [ ] **CLI: pk_ runtime warning** — emitted to stderr on every `ach-cli hydrate` invoked with a `pk_`, suppressed by `--no-warnings`. Surfaces the §8.6 budget-attribution asymmetry (`pk_` → user/Team budgets, `ek_` → Environment tag budget). (CLI spec §6.6, Hub spec §15.3)
- [ ] **Distribution** — OCI container `ghcr.io/ackstorm/ach:<version>`, standalone binaries for `linux-amd64`/`linux-arm64`/`darwin-amd64`/`darwin-arm64`/`windows-amd64`, Homebrew tap. Hub deployed via Helm chart. (CLI spec §2)

### Out of Scope

- **Service-to-service identity / on-behalf-of delegation / shared-mailbox identity** — `ek_` is bearer; per-workload identity via dedicated SSO principal. (Hub spec §4)
- **Server-side toggle to forbid `pk_` on runtime routes** — permanent design decision, not deferred. (Hub spec §8.6 v8 changelog)
- **HA Operator + Content Service** — v1alpha1 single-replica `Recreate`. Tracked in §20 backlog. (Hub spec §5.1, §20)
- **Auto-revoke `ek_` on creator's Team-membership removal** — manual offboarding runbook in v1alpha1. (Hub spec §8.1, §20)
- **HTTP `Range` / Conditional GET on `/content/`** — full body, ignore `Range`/`If-None-Match`. (Hub spec §15.6, §20)
- **mTLS / workload-identity at the application layer** — deployment concern (NetworkPolicy + service mesh). (Hub spec §5.1)
- **Soft-mode budget enforcement** — only hard enforcement surfaced; soft-mode configured directly in LiteLLM. (Hub spec §6.3)
- **Cryptographic signature verification / sandboxing of plugin code / SLSA / sigstore / malware scanning** — content is "trusted administrative". (Hub spec §10.2)
- **Default user-level budget on first SSO** — ACH writes no `max_budget`; deployer configures in LiteLLM. (Hub spec §6.3, §8.6)
- **`custom` CLI platform adapter** — four shipped adapters cover immediate consumers. (CLI spec §7.6, §13)
- **Declarative transformation DSL for plugin adapters** — imperative in each adapter for v1alpha1. (CLI spec §13)
- **Template rendering on artifacts** — Hub serves opaque bytes / `.tar.gz`. (CLI spec §13)
- **OS keyring integration** — credentials in `~/.config/ach/config.yaml` protected only by file mode in v1alpha1. (CLI spec §13)
- **`ach hook emit`** — hooks not in Hub v1alpha1. (CLI spec §13)
- **Offline `ach status`** — every server-bearing subcommand requires connectivity. (CLI spec §13)
- **Dual-key acceptance window for the Forwarder↔LiteLLM shared key** — single-key rotation, planned maintenance window. (Hub spec §9, §20)
- **JWT `jti` / replay-window restatement** — accepted as part of the v1alpha1 threat model. (Hub spec §9.1, §20)
- **HTTP escape hatch (no `ACH_BASE_URL` non-HTTPS)** — components refuse to start. (Hub spec §9.1)

## Context

- **Source of truth.** Two specs live in this repo at `ach_hub_spec_v20260515_FINALv4.md` (Hub document version `v20260515_FINALv10`) and `ach_cli_spec_v20260515_FINALv4.md` (CLI document version `v20260515_FINALv6`). Edits MUST go to the local files; the Hub spec has been through 10 design-review iterations on 2026-05-15 alone (v1 → v10). The companion canonical copy lives at `../ach-spec/`; periodically diff to pick up minor revisions.
- **LiteLLM coupling.** ACH is a *boundary* in front of LiteLLM. LiteLLM owns model registrations, MCP/A2A registrations, virtual keys, Teams, access groups, budgets — ACH is a thin layer that bundles those into Environments and adds identity, content delivery, and identity-bridging via JWTs.
- **LiteLLM Operator coordination.** Sister project at `litellm.ackstorm.ai/v1alpha1`. ACH NEVER reads its CRDs at runtime. Coordinated revisions live in both specs (e.g. budget field renames `limitUsd→limit`, `period`).
- **Predecessor work.** `litellm-autoconfig` (Python daemon at `/home/jcm/Projects/mcp/litellm-autoconfig`) informs the LiteLLM Operator's provider matrix, filter regex, and MCP discovery; ACH inherits the latter's API surface assumptions.
- **CLI inspiration.** The `ach` CLI explicitly adopts patterns from `enulus/openpackage` (`opkg`) — `--platform` flag, `detection`/`aliases` per-platform fields, `--global` boolean (vs `--scope` enum), state schema with per-file `hash`+`sourceHash`+`keys[]`+`merge`, "auto-claim" terminology, and the override hierarchy for the platforms table. The `$pipeline`/`$map` DSL is NOT adopted — v1alpha1 transformations are imperative in each adapter (option A).
- **Plugin canonical format.** Plugins are received from the Hub as `.tar.gz` archives in **Claude Code plugin format** (Hub §11). The Hub never produces per-platform variants; per-target adaptation (Codex, Gemini CLI, OpenCode) is the **CLI's responsibility**.
- **No deployed installs.** v1alpha1 has no deployed installs anywhere, so migration concerns do not apply. Schema bumps (e.g. CLI state schemaVersion 1→2, the `key_id` `pkid_`/`ekid_` prefix introduction) are clean breaks without compatibility shims.
- **Greenfield codebase.** This repo currently contains the two specs and a `.claude/repo_cost.json`. No code yet.

## Constraints

- **Tech stack — Hub**: Go (controller-runtime for Operator; standard `net/http` + chi/echo for Platform API and Forwarder; `pgx` for Postgres; `go-redis` for Redis; `crypto/ed25519` for JWT signing). Python is explicitly NOT used Hub-side despite the predecessor `litellm-autoconfig` being Python — ACH wants idiomatic K8s tooling.
- **Tech stack — CLI**: Go. Cobra for command parsing. `xxhash` for content hashing (CLI spec §8.2 `xxh3:` prefix). YAML round-trip preserving comments. Single-binary distribution; cross-compile for 5 host platforms.
- **Container runtime**: Operator + Content Service co-located in **one Pod** with two containers sharing a `ReadWriteOnce` PVC (default mount `/var/cache/ach`). `strategy: Recreate`, single replica. Platform API and Forwarder are stateless `Deployment`s with ≥1 replica.
- **External dependencies**: Postgres (connection-pool sized per replica), Redis (≤60s TTL ceiling on every key-resolution entry), LiteLLM (REST API, exclusively), Dex (OIDC), Kubernetes (informers + RBAC scoped to deployment namespace).
- **Security**: HMAC-SHA-256 with server-side pepper for credential hashes; pepper held outside Postgres. Plaintext key values MUST NOT be persisted anywhere (DB, Redis, logs, audit, metrics, traces). HTTPS-only via deployment-configured `ACH_BASE_URL` — no HTTP escape hatch.
- **Performance targets** (not yet quantified): Forwarder must absorb high-RPS MCP/A2A workloads with EdDSA signing. Content Service streams via `sendfile(2)` and never buffers a full body. Plugin archive hard cap default 50 MiB.
- **Wire format compatibility**: Claude Code plugin format on the wire from Hub. Marketplace schema verbatim from Claude Code. Manifest `schemaVersion` strict-match (no semver tolerance).

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Scope: Hub + CLI as one coordinated project | Specs are interlocked; budget/key/wire-format invariants only hold when both sides ship in lockstep | — Pending |
| Language: Go for both | Idiomatic K8s tooling on Hub side; cross-compiled CLI; one toolchain across the project. Two ship binaries post-phase-6: `ach` (5 service modes) + `ach-cli` (8 user subcommands), sharing `internal/cli/*` + `internal/cli/exit.Dispatch`. | — Pending |
| Granularity: Standard (5–8 phases) | Maps cleanly onto component boundaries (Operator / Platform API / Forwarder / Content Service / CLI core / Adapters) | — Pending |
| Mode: YOLO | Auto-approve; specs are exhaustive, decision overhead at every gate adds little signal | — Pending |
| Project structure: Horizontal Layers | ACH is infrastructure; component contracts are the right unit of completion | — Pending |
| Skip per-phase research agent | The two specs ARE the research output (~4100 lines, 10 Hub iterations); re-researching the domain just rediscovers what's specified | — Pending |
| Plan-check + Verifier ON | 27 Hub ACs + 26 CLI ACs are the contract — gates pay for themselves | — Pending |
| Quality model profile (Opus) | Cross-component invariants (revocation order, JWT trust path, drain semantics) reward deeper reasoning | — Pending |
| `pk_` on runtime is **permanent first-class** | Mental model is the contract: `pk_` represents a user, `ek_` represents a workload — no future toggle to forbid `pk_` on runtime routes | ✓ Good (per [[feedback_ach_pk_runtime_first_class]]) |
| Plugin canonical wire format = Claude Code | Hub never produces per-platform variants; CLI owns adaptation | ✓ Good (per Hub §11 v4 changelog) |
| `key_id` prefixes `pkid_`/`ekid_` distinct from plaintext `pk_`/`ek_` | Audit forensics; admin-flow ergonomics; static analysis | ✓ Good (per Hub §16 v8 changelog) |
| ACH spec source of truth is local `.md`, not gist | Round-trip latency, edit conflicts | ✓ Good (per [[feedback_spec_source_of_truth]]) |
| `HTTPS-only` invariant relaxed for upstream `HTTPSource` | Hermetic e2e fixture-server (in-cluster nginx) requires `http://` during Phase 02.1; production deployments still use `https://` by convention | Lifted Phase 02.1 (commit 45b7558) |
| SCM `authSecretRef` made optional | Anonymous public-repo fetches needed for SC1 fixture (`octocat/Hello-World @ master`); S3/GCS retain mandatory auth | Lifted Phase 02.1 (commit 94f24b5) |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd:complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-05-18 after Phase 02 (external-refs-marketplace-operator-reconciliation) — 9/9 plans landed; Operator now reconciles 6 source-type fetchers, three-stage marketplace refresh with anchored RE2 + cross-marketplace name conflict, plugin size cap, snapshot-driven `ExecutionResourcesResolved`, and the orphan-key cleanup Runnable. Code review identified 3 Blocker + 9 Warning findings, all fixed in atomic commits before phase close. Verification status: `human_needed` (5 SCs verified in code; 6 items persisted to `02-HUMAN-UAT.md` for live-cluster confirmation, including the Phase-1 manifest gap that blocks SC#1–4 end-to-end). Requirements OP-03, OP-06, OP-07, OP-08, OP-09, OP-13, OP-15 are code-satisfied pending UAT.*

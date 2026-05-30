# ACH — v1alpha1 Requirements

This file enumerates the v1alpha1 acceptance criteria derived from `ach_hub_spec_v20260515_FINALv4.md` (Hub document version `v20260515_FINALv10`, 27 ACs) and `ach_cli_spec_v20260515_FINALv4.md` (CLI document version `v20260515_FINALv6`, 26 ACs). Each requirement is testable. Phase mapping in the Traceability section is filled by `/gsd:plan-phase`.

REQ-IDs follow `<COMPONENT>-<NUMBER>` where `<COMPONENT>` is one of:
- `CRD` — CRD shapes, admission validation, condition reasons
- `KEY` — `pk_`/`ek_` lifecycle, sliding window, revocation
- `OP` — ACH Operator (reconcile, finalizers, cache, marketplace, status)
- `API` — Platform API (Dex, hydrate, env-keys, env, admin endpoints)
- `FWD` — Forwarder (header rewrite, JWT, JWKS, BackendIdentityPolicy)
- `CS` — Content Service (auth, sendfile, scope resolution, errors)
- `DB` — Postgres schema, credential-hash storage
- `OBS` — Audit + Metrics
- `MULTI` — Multi-tenancy / namespace scoping
- `CLI` — `ach` binary commands and behavior
- `ADAPT` — Platform adapters (claude-code, codex, gemini-cli, opencode)
- `STATE` — CLI local state schema + lock + atomic write
- `SAFE` — CLI safe extraction policy
- `DIST` — Distribution (OCI, binaries, Helm)

---

## v1 Requirements

### CRD (CRD shapes + admission)

- [x] **CRD-01**: All ACH objects under `ach.ackstorm.ai/v1alpha1` API group with these first-class kinds: `Environment`, `Plugin`, `PluginMarketplace`, `Artifact`, `Prompt`, `BackendIdentityPolicy`. (Hub §2)
- [x] **CRD-02**: `Environment.spec` has separate `runtime` (models, mcpServers, a2aAgents) and `context` (prompts, plugins, artifacts) blocks; both blocks always present in hydrate response even when one is empty `[]`. (Hub §6, §15.2)
- [x] **CRD-03**: All admission rules enforced via `x-kubernetes-validations` (CEL) on the OpenAPIv3Schema; v1alpha1 deploys NO `ValidatingAdmissionWebhook`. CEL covers required fields (`refresh.maxStaleness`, `Artifact.spec.scope`, plugin size-cap presence), cross-field constraints (`refresh.interval ≤ refresh.maxStaleness`, source-type subobject matches `spec.type`), and per-kind path/key validation. (Hub §2)
- [x] **CRD-04**: External-reference CRDs (`Plugin`, `Prompt`, `Artifact`, `PluginMarketplace`) declare `refresh.maxStaleness` (REQUIRED) and use branch/tag refs only (no immutable commit refs). Marketplace plugin-entry sources permit optional `sha`. (Hub AC10, §10, §10.1)
- [x] **CRD-05**: `Artifact.spec.scope ∈ {object, directory}` (REQUIRED). For `type: http`, only `scope: object` is permitted; CRD admission rejects `directory`. (Hub §13)
- [x] **CRD-06**: `Environment` carries finalizer `environments.ach.ackstorm.ai/finalizer`; external-reference CRDs carry `<kindPlural>.ach.ackstorm.ai/finalizer`. Finalizers removed only after drain/cleanup completes. (Hub §6.5, §10.3)
- [x] **CRD-07**: Condition reasons for each kind are drawn from the closed sets in Hub §6.6 (`Available/Cascade`, `ContentReady/{StaleCacheExpired,ContentMissing}`, `ExecutionResourcesResolved/{ResourceUnresolved,TeamMissing}`, `AccessGroupSynced/{LiteLLMUnreachable,SyncFailed}`, `SourceReachable/{Unreachable,StaleCacheExpired,PluginTooLarge}`, `Synced/{NameConflict,UpstreamInvalid,InvalidConfig,UnsupportedPluginSource,DuplicateTarget}`). `True` means satisfied; `reason` is set only when `False`.
- [x] **CRD-08**: `BackendIdentityPolicy.spec` requires `target.kind ∈ {MCPServer, A2AAgent}` (CEL-enforced enum), `target.name` (DNS-1123 subdomain), and `forwardIdentityJWT` (REQUIRED, no default). (Hub §9.3)

### KEY (pk_ / ek_ lifecycle)

- [ ] **KEY-01**: Two key types only: `pk_` (Personal Key, SSO-bound, sliding-window 7-day expiry, accepted everywhere) and `ek_` (Environment Key, bound to one Environment, no expiry, revocation-only). No third login-only key type. (Hub §3, §7, §8, AC4)
- [ ] **KEY-02**: `x-ach-key` is the single authenticated header on all surfaces; `x-ach-environment` is additionally accepted on Content Service only. No other ACH-specific auth header. (Hub AC2, AC5)
- [ ] **KEY-03**: Each `ach-cli login` mints a NEW `pk_` and returns plaintext exactly once. Multiple active `pk_` per user are allowed. (Hub AC1, §7)
- [ ] **KEY-04**: `pk_` sliding-window check-and-extend implemented as a single atomic SQL CTE that returns 0 rows on revoked/expired (→ `401 expired_or_revoked`) or 1 row otherwise; `expires_at` extended to `now() + 7 days` only when `last_used_at < now() - interval '5 minutes'` at the statement snapshot. (Hub §7.1, AC17)
- [ ] **KEY-05**: `ek_` is bound to exactly one Environment; bound at creation, not re-checked at every request; never live-re-authorized against Team membership. (Hub AC7, §8.1)
- [ ] **KEY-06**: `ek_` `last_used_at` UPDATE is debounced identically (`< now() - interval '5 minutes'`); the UPDATE does NOT participate in the auth decision (resolution lookup of `credential_hash` and `status` is authoritative). (Hub AC26, §8.1)
- [ ] **KEY-07**: `pk_` revocation is **DB-first** (Postgres `status='revoked'` flip → LiteLLM revoke → Redis invalidate). Postgres flip is the load-bearing barrier for both Forwarder and Content Service. (Hub AC13, §7.1)
- [ ] **KEY-08**: `ek_` revocation is **LiteLLM-first** (LiteLLM virtual-key revoke → DB flip → Redis invalidate). LiteLLM is the load-bearing barrier for runtime; DB flip + Redis ≤60s TTL bound the Content Service window. (Hub AC13, §8.5)
- [ ] **KEY-09**: `pk_` is permanent first-class on runtime forwarding routes. `pk_` traffic carries no Environment tag and is governed by user/Team budgets only; `ek_` traffic carries the Environment tag and stacks user + Team + Environment-tag budgets. (Hub §7.3, §8.6)
- [ ] **KEY-10**: ACH Platform API never sets a default `max_budget` on first-SSO LiteLLM user creation. (Hub §6.3, §8.6)
- [ ] **KEY-11**: `ek_` creation runs the Hub §8.2 8-step flow including: verify Environment exists with nil `deletionTimestamp` (→ `404 environment_not_found` if terminating), verify `AccessGroupSynced=True` via informer cache (→ `503 not_ready` otherwise), idempotent verify-or-create LiteLLM user, and create-then-insert ordering with cleanup on DB-insert failure. (Hub §8.2, §8.3)

### OP (ACH Operator)

- [x] **OP-01**: ACH Operator is the SOLE writer of LiteLLM access groups and budget tags for every `Environment` (create/update/delete). Platform API, Forwarder, Content Service never mutate these. (Hub §5.1, §6.2, AC6)
- [x] **OP-02**: `Environment` deletion drain executes in the order: K8s `deletionTimestamp` set → delete LiteLLM access group `<environment>` → delete LiteLLM tag `<environment>` → drain `ek_` rows (loop until no active rows) → remove finalizer. Access-group deletion is the runtime barrier. (Hub AC14, §6.5)
- [ ] **OP-03**: External-reference refresh per §10.3 sequence: fetch upstream → materialize served form into `.tmp/<random>` → `fsync` staging file → `rename(2)` to final path → UPDATE `external_refs`/`marketplace_plugins` row. Step 4 is the load-bearing barrier; crash between 4 and 5 is benign. (Hub §10.3)
- [ ] **OP-04**: `rename(2)` failure leaves the previously cached file intact and surfaces `SourceReachable=False, reason=Unreachable` with errno in `status.message`. Successive failures crossing `maxStaleness` flip to `reason=StaleCacheExpired` and Content Service returns `503 stale_cache_expired`. (Hub AC23)
- [x] **OP-05**: Operator + Content Service Pod is single-replica `strategy: Recreate` sharing an RWO PVC. In-flight Content Service read against an old inode finishes successfully even when Operator publishes a new revision mid-request; no torn-byte response observable. (Hub AC22)
- [ ] **OP-06**: `PluginMarketplace` refresh has three stages: Stage-1 marketplace-file fetch+parse (failure aborts before any UPSERT/DELETE), Stage-2 per-plugin best-effort UPSERT (single-plugin failure does NOT roll back others; record in `status.message`), Stage-3 final DELETE sweep of vanished names. (Hub §12.4)
- [ ] **OP-07**: Marketplace include/exclude filters use Go RE2 anchored at start (Operator prepends `^`), case-sensitive. Empty `include`/`exclude` ≡ absent. Pattern compile failure → `Synced=False, reason=InvalidConfig`. `include` matching zero upstream → `UpstreamInvalid`; `exclude` matching zero is a silent no-op. (Hub §12)
- [ ] **OP-08**: Cross-marketplace plugin name conflicts resolved by alphabetical priority on `PluginMarketplace.metadata.name` (Unicode code-point, case-sensitive). Losing marketplaces report `Synced=False, reason=NameConflict`; the winner serves. A `Plugin` CRD with `metadata.name = X` always beats marketplace-sourced `X`. (Hub §12.3)
- [ ] **OP-09**: Plugin `.tar.gz` size cap configured via `ACH_PLUGIN_MAX_SIZE_MIB` env var on Operator Pod (default 50). Operator refuses to start if value is `0`, negative, or non-numeric. Cap changes require Operator restart. Oversized plugins flip `SourceReachable=False, reason=PluginTooLarge`; Content Service returns `503 plugin_too_large` with streamed body (never buffered). (Hub AC21, AC25, §11)
- [x] **OP-10**: Cache layout under PVC mount root (`/var/cache/ach` default): `prompt/<name>`, `plugin/<name>.tar.gz`, `marketplace/<marketplace-name>/plugin/<plugin-name>.tar.gz`, `artifact/<name>` (object) or `artifact/<name>.tar.gz` (directory), `.tmp/` staging. Same filesystem so `rename(2)` is atomic. (Hub §10.3)
- [x] **OP-11**: Cache reconstruction after PVC loss: Operator MUST reset `external_refs.last_successful_refresh` and `marketplace_plugins.last_successful_refresh` to NULL before re-running refresh; affected Environments surface `ContentReady=False, reason=StaleCacheExpired` during warm-up. (Hub §10.3)
- [x] **OP-12**: External-reference CRD finalizer cleanup: on deletion, the Operator removes the cached file (or, for `PluginMarketplace`, the entire `marketplace/<name>/` subtree) BEFORE removing the finalizer. (Hub §10.3)
- [x] **OP-13**: `ExecutionResourcesResolved` determined by querying LiteLLM REST for registered model/MCP/A2A name set on each reconcile, intersecting with `Environment.spec.runtime.*`. Environments requeue every 5 minutes in addition to event-driven reconciliation. Unresolved names written to `status.unresolvedRuntime`. (Hub §6.4)
- [ ] **OP-14**: `BackendIdentityPolicy` duplicate-target status: Operator groups CRs by `(spec.target.kind, spec.target.name)`, flips every non-winner (every CR except alphabetically lowest `metadata.name`) to `Synced=False, reason=DuplicateTarget`. Status is for operator visibility only — Forwarder applies the alphabetical rule independently. (Hub §9.3)
- [x] **OP-15**: Orphan LiteLLM key cleanup runs on the configurable interval `ACH_ORPHAN_CLEANUP_INTERVAL` (default 1h, minimum 5m, refuses to start below). Orphan = LiteLLM key absent from active ACH DB rows AND ≥10min old AND owning `user_id` is ACH-managed. Aborted on LiteLLM-unreachable, retried next interval. Each revocation emits an audit event. (Hub §18.4)
- [ ] **OP-16**: ACH Operator adds itself as the sole writer to `BackendIdentityPolicy.status`. Forwarder reads `BackendIdentityPolicy.spec` only via informer (no `status` reads at request time, decoupling runtime authority from status-write latency). (Hub §9.3)

### API (Platform API)

- [ ] **API-01**: All Platform API endpoints under `/platform/` prefix. Hydrate is `POST /platform/hydrate`; ek-keys under `/platform/env-keys/`; admin under `/platform/admin/`; environments under `/platform/environments`. No `/v1/hydrate` legacy path. (Hub §5.1)
- [ ] **API-02**: Dex SSO login flow creates a LiteLLM user on first SSO and adds them to the `default` Team. If `default` is missing in LiteLLM → `500 default_team_missing` with audit event `outcome=default_team_missing`. ACH does NOT create `default` lazily. (Hub §5.1, §17, AC1)
- [ ] **API-03**: `POST /platform/hydrate` accepts both `pk_` and `ek_`. For `pk_`: body `environment` REQUIRED (missing → `400 missing_environment`). For `ek_`: body `environment` OPTIONAL; if present must match the bound Environment (mismatch → `403 wrong_environment`). Authorization: `pk_` requires `authorizedTeams[]` ∩ caller's LiteLLM Teams ≠ ∅; `ek_` requires active+bound. Failure codes: `400 invalid_key_format`, `401 expired_or_revoked`, `403 unauthorized_team`, `403 wrong_environment`, `404 environment_not_found`, `503 litellm_unreachable`. Terminating Environments still serve hydrate. (Hub §15.1)
- [ ] **API-04**: Hydrate response carries `schemaVersion: "v1alpha1"` strictly; both `runtime` and `context` blocks always present (with `[]` when empty). Carries `id` + `downloadUrl` per content item; never carries `pk_`/`ek_` plaintext. (Hub §15.2, AC1, §16.1)
- [ ] **API-05**: `POST /platform/env-keys` runs the §8.2 flow. Response includes `key_id` (`ekid_…`), `plaintext` (returned exactly once), `environment`, `name`, `owner_email`, `created_at`. Failure codes including `404 environment_not_found` and `503 not_ready`. (Hub §15.5, §8.2)
- [ ] **API-06**: `GET /platform/env-keys` returns only the caller's own rows for non-admin; admin callers may filter by `owner_email`. Non-admin passing `owner_email` → `400 invalid_argument`. (Hub §15.5, §8.5)
- [ ] **API-07**: `DELETE /platform/env-keys/{key_id}` validates `ekid_` prefix BEFORE lookup (mismatch → `400 invalid_argument`). Runs §8.5 flow synchronously, returns `204 No Content` only after LiteLLM acknowledges. Owner check → `403 not_key_owner`. (Hub §15.5)
- [ ] **API-08**: `GET /platform/environments` returns rows whose `spec.authorizedTeams[]` intersects the caller's LiteLLM Teams; admin callers see all rows in the namespace. Includes `conditions[]` verbatim from §6.6. (Hub §15.5)
- [ ] **API-09**: Admin endpoints (`/platform/admin/*`): `403 not_admin` check runs BEFORE any other validation. `POST /platform/admin/keys/revoke` accepts `pkid_` or `ekid_` (else `400 invalid_argument`); applies §7.1 or §8.5 path accordingly. `POST /platform/admin/users/{email}/revoke-keys` URL-decodes email then matches verbatim. `POST /platform/admin/refresh` patches `ach.ackstorm.ai/force-refresh: <RFC3339>` annotation on the target CR (only non-Operator write surface to ACH CRDs); returns `202 Accepted`. (Hub §15.5, §5.2)
- [ ] **API-10**: Admin authorization gated by SSO email matching the operator-configured allowlist (ConfigMap-mounted file at `/etc/ach/admins/admins.txt`; one email per line; comments and blank lines ignored). Read at process start; ConfigMap edits require Platform API restart. `ek_` never accepted on admin endpoints (`401 invalid_key_type`). (Hub AC18, AC24, §18)
- [ ] **API-11**: Management endpoints accept `pk_` only (`401 invalid_key_type` on `ek_`). Hydrate (§15.1) and Content Service (§15.6) are platform-use endpoints — they share the §15.5 envelope/timestamp/pagination conventions but accept both key types. (Hub §15.5)
- [ ] **API-12**: All endpoints respond with `application/json` UTF-8. Error envelope: `{ "error": { "code": "<outcome>", "message": "..." }, "request_id": "req_..." }`. Timestamps RFC 3339 UTC. List endpoints accept `?limit=<int, default 100, max 500>` and `?cursor=<opaque>`; responses carry `next_cursor` (null on last page). (Hub §15.5)

### FWD (Forwarder)

- [ ] **FWD-01**: Forwarder owns `/v1`, `/gemini`, `/mcp`, `/a2a` and serves `/.well-known/jwks.json`. Workload prefixes never overlap with Platform API or Content Service prefixes — ingress dispatch by prefix alone. (Hub §5.1)
- [ ] **FWD-02**: Per-request flow: read `x-ach-key` (malformed → `400 invalid_key_format` with no log of the raw value), resolve via Redis (≤60s TTL ceiling) → Postgres on miss; reject revoked/expired/unknown with `401 expired_or_revoked`. (Hub §5.1, AC15)
- [ ] **FWD-03**: §5.1 step 4 pre-check on `/mcp/<name>` and `/a2a/<name>`: `ek_` checks bound Environment's `spec.runtime.{mcpServers,a2aAgents}[]` (O(1) informer lookup); `pk_` queries LiteLLM Team memberships and verifies access to `<name>`. Failure → `403 unauthorized_resource`; LiteLLM-unreachable on `pk_` path → `503 litellm_unreachable`. No JWT signed and no LiteLLM forward on either failure. (Hub AC19, AC20, §5.1)
- [ ] **FWD-04**: Header rewrite: STRIP every client `Authorization`, every `x-litellm-*`, every `x-ach-*` (including `x-ach-key` and `x-ach-environment`), and every hop-by-hop header (`Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Proxy-Authenticate`, `Proxy-Authorization`) on EVERY route. Then write `x-litellm-api-key: <shared key>` and `x-litellm-key-id: <virtual key id>`. The shared key without `x-litellm-key-id` MUST be rejected by LiteLLM (deployment requirement on the LiteLLM Operator's custom-auth plugin). (Hub AC15, §5.1, §9)
- [ ] **FWD-05**: `Authorization: Bearer <ACH JWT>` written ONLY on `/mcp/<name>` or `/a2a/<name>` AND ONLY when a `BackendIdentityPolicy` exists in the namespace with `spec.target.kind` matching the route, `spec.target.name == <name>`, and `spec.forwardIdentityJWT: true`. Duplicate CRs resolved by alphabetical priority on `metadata.name`. No matching CR or matching CR with `false` → request forwarded with NO `Authorization` header. (Hub AC27, §9.1, §9.3)
- [ ] **FWD-06**: For `ek_` traffic, attach Environment attribution tag via LiteLLM's tag mechanism (`metadata.tags` for `/v1`, route-specific equivalents for others). `pk_` carries no tag. (Hub §5.1, §6.3)
- [ ] **FWD-07**: ACH JWT signed with EdDSA (Ed25519). Header `{ alg: EdDSA, kid, typ: JWT }`. Claims: `iss = $ACH_BASE_URL`, `sub = <namespace>/<owner-email>`, `aud = mcp:<name>` or `a2a:<name>` (literal string, NOT URI), `iat`/`nbf`/`exp = iat + 120s`. No `jti`. (Hub §9.1)
- [ ] **FWD-08**: JWKS at `$ACH_BASE_URL/.well-known/jwks.json` returns `application/jwk-set+json` with `Cache-Control: public, max-age=3600`. Each JWK: `kty: OKP`, `crv: Ed25519`, REQUIRED `kid`/`x`, RECOMMENDED `use: sig` and `alg: EdDSA`. No other RFC 7517 fields. Anonymous access; backends fetch backend-pull. (Hub AC16, §9.1)
- [ ] **FWD-09**: JWT signing-key rotation: ≥24h overlap (MUST exceed backend JWKS cache TTL). Forwarder owns both signing and JWKS; signing material in single Kubernetes Secret `ach-jwt-signing-keys` RBAC-scoped to Forwarder ServiceAccount only. Manual rotation in v1alpha1 (publish new key alongside prior, rolling-restart, after overlap remove prior key, rolling-restart again). (Hub AC16, §9.2)
- [ ] **FWD-10**: Forwarder `ACH_BASE_URL` MUST begin with `https://`; component refuses to start otherwise. No HTTP escape hatch. (Hub §9.1)
- [ ] **FWD-11**: Access log records request only AFTER `x-ach-key` redaction; raw `pk_`/`ek_` plaintext MUST NOT appear in any access-log field, including raw-header dumps. (Hub §5.1)

### CS (Content Service)

- [ ] **CS-01**: Single read endpoint `GET /content/{kind}/{name}` with `{kind} ∈ {prompt, plugin, artifact}` (singular, lowercase). Other values → `404 content_not_found`. Other HTTP methods → `405 Method Not Allowed`. `HEAD` MAY be supported but not contractual. (Hub §15.6)
- [ ] **CS-02**: Authorization: every `pk_` request runs §7.1 check-and-extend FIRST (zero rows → `401 expired_or_revoked`); every `ek_` request resolves Redis → Postgres-on-miss and rejects `status != 'active'` with `401 expired_or_revoked`. The §8.1 `last_used_at` debounce UPDATE applies to Content Service `ek_` requests. (Hub §5.1)
- [ ] **CS-03**: Environment context resolution: `pk_` REQUIRES `x-ach-environment` (missing → `400 missing_environment`); `pk_` Team memberships MUST intersect `Environment.spec.authorizedTeams[]` (else `403 unauthorized_team`); LiteLLM-unreachable on cache miss → `503 litellm_unreachable`. `ek_` makes `x-ach-environment` OPTIONAL (mismatch → `403 wrong_environment`). Team-membership cache uses ≤60s Redis TTL. (Hub §5.1, §15.6)
- [ ] **CS-04**: After environment resolution, content authorization runs as a **fixed two-step gate**: (1) resolve `<name>` to a backing `Plugin`/`Prompt`/`Artifact` CRD in the namespace, or (for `plugin`) a `marketplace_plugins` row per §12.3 priority — no backing resource anywhere → `404 content_not_found`; (2) verify `<name>` appears in `Environment.spec.context.<kindPlural>[]` — not present → `403 unauthorized_content`. Order is load-bearing: resource-exists-but-not-granted is `403`, not `404`. (Hub §5.1)
- [ ] **CS-05**: Plugin name resolution per §12.3: `Plugin` CRD with `metadata.name = X` wins; otherwise `marketplace_plugins` row whose `marketplace_name` sorts alphabetically lowest wins. Resolved on every request from Postgres. (Hub §10.3, §12.3)
- [ ] **CS-06**: Response `Content-Type` per kind: prompt = `spec.contentType` else upstream-derived else `application/octet-stream`; plugin = `application/gzip`; artifact `scope: object` = upstream-derived else `application/octet-stream`; artifact `scope: directory` = `application/gzip`. `Content-Length` set; `Cache-Control: no-store`; identity transfer encoding (no chunked, no compression at CS tier). Body streamed via `sendfile(2)` or equivalent — never buffered. (Hub §15.6, AC21)
- [ ] **CS-07**: Artifact `spec.scope` resolved from §5.2 informer cache: `artifact/<name>` for `object`, `artifact/<name>.tar.gz` for `directory`. URL carries no `.tar.gz` suffix; `Content-Type` is the client's signal. (Hub §15.6)
- [ ] **CS-08**: Conditional/partial requests in v1alpha1: `Range`, `If-None-Match`, `If-Modified-Since`, `If-Match`, `If-Unmodified-Since` IGNORED — full body, status `200`, never `206 Partial Content`. (Hub §15.6)
- [ ] **CS-09**: Content access during Environment drain continues until the Environment is fully removed from K8s (then `404 content_not_found`). Access-group deletion is the runtime barrier; content is static read-only data. (Hub §6.5)
- [ ] **CS-10**: Staleness check reads `external_refs.last_successful_refresh` + `external_refs.max_staleness` (or `marketplace_plugins` equivalents) from Postgres at request time. `now - lastSuccessfulRefresh > maxStaleness` → `503 stale_cache_expired`. (Hub §10)
- [ ] **CS-11**: Error envelope per §15.5; outcomes per §15.6 table including `invalid_key_format`, `missing_environment`, `expired_or_revoked`, `unauthorized_team`, `unauthorized_content`, `wrong_environment`, `environment_not_found`, `content_not_found`, `stale_cache_expired`, `plugin_too_large`, `litellm_unreachable`. (Hub §15.6)

### DB (Postgres schema + credential storage)

- [x] **DB-01**: Postgres tables present: `personal_keys`, `environment_keys`, `external_refs`, `marketplace_plugins` per Hub §16 column lists. PRIMARY KEYS: `personal_keys.key_id`, `environment_keys.key_id`, `external_refs(kind, name)`, `marketplace_plugins(marketplace_name, name)`. UNIQUE constraint on both `credential_hash` columns. (Hub §16)
- [x] **DB-02**: `key_id` columns carry prefix `pkid_` (personal) and `ekid_` (environment), distinct from plaintext bearer prefixes `pk_`/`ek_`. Server-generated, immutable, opaque. Linter-friendly. (Hub §16, "Key ID prefix convention")
- [x] **DB-03**: Credential storage: HMAC-SHA-256 with server-side pepper held outside Postgres (Kubernetes Secret, KMS reference, or equivalent). Plaintext-to-hash comparison constant-time. Plaintext returned to caller exactly once, never recoverable thereafter. (Hub §16.1) — schema contract surface landed in 01-03 (column shape, UNIQUE NOT NULL); HMAC hash computation lands in 01-04 (internal/credhash); first row-writer lands in Phase 3.
- [x] **DB-04**: Plaintext key values MUST NOT appear in Postgres, Redis, application logs, audit logs, metrics labels, trace spans, or error responses. (Hub §16.1) — schema-level enforcement landed in 01-03 (zero columns capable of holding raw bearer values); log/audit non-leak invariant continues in Phase 3+.
- [x] **DB-05**: `owner_email` stored verbatim from Dex SSO claim (no normalization, no case folding, no `+tag` stripping, no dot collapsing). The namespace prefix used in JWT `sub` and audit `actor` is composed at emission time, never persisted. (Hub §16, §8.4) — column shape landed in 01-03 (`owner_email text NOT NULL`, no normalization column, no `actor`/namespace-prefixed column); SSO write path lands in Phase 3.
- [x] **DB-06**: `external_refs.storage_location` and `marketplace_plugins.storage_location` hold a filesystem path under the deployment's cache root. Operator writes the column in the same transaction that updates `last_successful_refresh` and `next_refresh_at`, AFTER the atomic `rename(2)` publication. (Hub §16, §10.3) — column shape landed in 01-03 (`storage_location text NOT NULL`, `last_successful_refresh`/`next_refresh_at` siblings); transactional write semantics land in Phase 2 (OP-03, OP-06).

### OBS (Audit + Metrics)

- [ ] **OBS-01**: Structured JSON audit events emitted to the configured log sink for: `pk_`/`ek_` create/revoke, Environment create/update/delete, hydrate, content download, admin operations (force-refresh, orphan-cleanup, admin key revocation). Sliding-window pk_ extension is NOT its own event. Runtime forwarding NOT audited (LiteLLM is the runtime audit source). (Hub §18.2)
- [ ] **OBS-02**: Audit event schema: REQUIRED `timestamp`, `actor`, `action`, `outcome`, `request_id`. `actor = <namespace>/<sso-email>`. `outcome ∈ §18.2 stable enum`; consumers MUST tolerate unknown codes. `key.id` always uses `pkid_`/`ekid_` prefix; plaintext and credential hash MUST NEVER appear. `target.kind`/`target.name` REQUIRED on resource-scoped events. (Hub §18.2)
- [ ] **OBS-03**: Each component (Platform API, Forwarder, Content Service, Operator) exposes Prometheus `/metrics` with the §18.5 metric set. Names, types, label keys, and label-value enums are NORMATIVE; new values added additively within an API version. (Hub §18.5)
- [ ] **OBS-04**: Forwarder metrics: `forwarder_requests_total{route, key_type, outcome}` (outcome shares §18.2 enum), `forwarder_request_duration_seconds{route, key_type, status_class}`, `forwarder_jwt_signed_total{kind}`, `forwarder_jwt_suppressed_total{kind, reason ∈ {no_policy, policy_false}}`. (Hub §18.5)
- [ ] **OBS-05**: Cross-component `litellm_unreachable_total{caller ∈ {forwarder, content_service, platform_api, operator}}` counter. Body / HTTP status / audit `outcome` are all `litellm_unreachable` on every surface (no asymmetry). (Hub §18.5, §15.6 v9 changelog)
- [ ] **OBS-06**: Content Service metrics: `content_service_requests_total{kind, outcome}`, `content_service_request_duration_seconds{kind}`, `content_service_bytes_served_total{kind}`. Cardinality discipline: no per-request labels (no `request_id`, no `owner_email`) on metrics. (Hub §18.5)

### MULTI (Multi-tenancy)

- [x] **MULTI-01**: ACH deployment is namespace-scoped: Platform API, Forwarder, Content Service, Operator, Postgres schema, Redis cache, content cache scoped to one K8s namespace; never shared across namespaces. All bare resource references in `Environment` resolve within the Environment's namespace. (Hub §18.3)
- [x] **MULTI-02**: ServiceAccount RBAC: each component carries `get/list/watch` on its required `ach.ackstorm.ai` kinds (per §5.2 table). NO write verbs outside the Operator, with one exception: Platform API has `patch` on `plugins`/`prompts`/`artifacts`/`pluginmarketplaces` scoped to deployment namespace, used exclusively for the `ach.ackstorm.ai/force-refresh` annotation (§15.5). (Hub §5.2)
- [x] **MULTI-03**: Each component blocks readiness probe until informer caches complete initial list-and-watch sync. Stays ready through transient API-server outages. (Hub §5.2)
- [x] **MULTI-04**: JWT `sub` includes namespace prefix (`<namespace>/<owner-email>`); same email in two ACH deployments is two distinct ACH principals. Distinct deployments MUST configure distinct `ACH_BASE_URL` (= `iss`) unless intentionally sharing JWKS + signing-key Secret. (Hub §18.3, §9.1)

### CLI (ach binary commands)

- [ ] **CLI-01**: `ach-cli login` mints a `pk_` against a chosen deployment (prompts for name + URL), returns plaintext exactly once, writes `deployments.<name>.pk` in `~/.config/ach/config.yaml`. Sets `default:` if absent. Exits 1 in synthetic mode. Repeated login overwrites prior `pk:` (prior key remains active server-side). (CLI AC1, §5.1)
- [ ] **CLI-02**: `~/.config/ach/config.yaml` created with `0600`; parent dir `0700`. CLI warns on read of more-permissive mode; normalizes on write. CLI refuses to write or read entries with non-HTTPS `url`. (CLI AC2, AC18, §3.2)
- [ ] **CLI-03**: Every authenticated request carries `x-ach-key`. `pk_` Content Service requests additionally carry `x-ach-environment`. Hydrate uses JSON body field `environment`. (CLI AC3)
- [ ] **CLI-04**: `pk_` plaintext only echoed at one-time return of `ach-cli login`; `ek_` plaintext only at one-time return of `ach-cli env-keys create`. Verbose-mode HTTP logs redact `x-ach-key` to `<prefix>_***`. (CLI AC4, §11.1)
- [ ] **CLI-05**: `ach-cli hydrate` with a `pk_` emits the §6.6 stderr warning before any write; suppressed by `--no-warnings`. (CLI AC5, §6.6)
- [ ] **CLI-06**: `ach-cli hydrate` with `pk_` REQUIRES `--environment`; with `ek_` it is OPTIONAL. `pk_` invocation without `--environment` exits 1 (or relays server's `400 missing_environment`). (CLI AC6)
- [ ] **CLI-07**: Synthetic deployment mode activates when BOTH `ACH_BASE_URL` set AND credential resolves from `ACH_API_KEY`/`--api-key`. In this mode: NO config file read; `ach-cli login`/`config`/`logout`/`env-keys create --save-as` exit 1; `--deployment`/`ACH_DEPLOYMENT` rejected with exit 1; state files record `"deployment": "(env)"`. Half-set synthetic config (`ACH_BASE_URL` without credential) → exit 1. (CLI AC14, §3.3)
- [ ] **CLI-08**: Multi-deployment registry mode: deployment resolution via `--deployment` → `ACH_DEPLOYMENT` → `default:` → sole entry. Zero registered deployments + no synthetic vars → exit 1 recommending `ach-cli login`. (CLI AC15)
- [ ] **CLI-09** *(DEVIATED Phase 6 D-07: spec §5.6 `--save-as` removed; `ek_` always-persists; `--no-save` opts out — see "Phase 6 Deviations" below)*: Credential sources `--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY` are mutually exclusive — presenting more than one → exit 1. `--env-key`/`ACH_ENV_KEY` resolve against `deployments.<active>.ek.<label>` and are not available in synthetic mode. (CLI AC16, §6.1)
- [ ] **CLI-10**: `ach-cli env-keys list` shows only the calling user's own rows for non-admin `pk_` (admin sees all + may filter `owner_email`). `ach-cli admin keys revoke` and `ach-cli admin users revoke-keys` exit 3 on `403 not_admin`. (CLI AC17, §5.6, §5.10)
- [ ] **CLI-11**: `ach-cli whoami --verify` uses asymmetric verification: `pk_` → `GET /platform/environments?limit=1`; `ek_` → `POST /platform/hydrate {}` with `Accept-Encoding: gzip` and discarded body. Exit 0 on `200`, exit 3 on `401`, exit 6 on network failure. (CLI AC21, §5.3)
- [ ] **CLI-12**: `ach-cli env describe <name>` issues `GET /platform/environments` paginated until row found, then `POST /platform/hydrate` for `runtime`+`context`. `403 unauthorized_team` on the second call rendered as `Runtime: (unavailable)` / `Context: (unavailable)`; command still exits 0. `--metadata-only` skips the second call. (CLI AC22, §5.5)
- [ ] **CLI-13**: `ach-cli env-keys revoke <key-id>` accepts `ekid_…` only (plaintext rejected with `400 invalid_argument`). Interactive confirmation; `--yes` bypasses. `ach-cli admin keys revoke` accepts both `pkid_…` and `ekid_…`. (CLI §5.6, §5.10)

### ADAPT (Platform adapters)

- [ ] **ADAPT-01**: Four platform adapters compiled into the CLI binary: `claude-code`, `codex`, `gemini-cli`, `opencode`. Each declares `detection: [...]`, `aliases: [...]` (case-insensitive), runtime-config file(s) with merge strategy, plugin output root. Aliases resolve to canonical IDs before adapter dispatch. (CLI AC24, §7.2)
- [ ] **ADAPT-02**: Platform autodetection: when `--platform` omitted AND `ACH_PLATFORM` unset, scan cwd (workspace) or `$HOME` (global) for each platform's `detection` patterns. Zero matches → exit 1 with prompt. One match → use it (print `Detected platform: <id>` to stderr). Multi-match → exit 1 listing matches. (CLI AC24, §7.5)
- [x] **ADAPT-03**: When runtime is in scope, adapter renders `manifest.runtime` into platform-native config: points at `baseUrl` for model traffic, registers each MCP/A2A endpoint at `baseUrl + endpoint`, configures `x-ach-key: <credential>` attachment via platform-native auth mechanism. NO other ACH-issued secret in runtime config. (CLI AC7)
- [x] **ADAPT-04**: Plugin canonical wire format from Hub is Claude Code plugin format. `claude-code` adapter: pass-through extraction; merges plugin `.mcp.json` into `.claude/.mcp.json` via `merge: deep` recording contributed `mcpServers.<id>` keys. `codex`/`gemini-cli`/`opencode` adapters: distribute pieces preserving Claude layout, MCP merge into platform-specific runtime-config (TOML/JSON), agent frontmatter rewriting per platform schema (model/tools/permissions). Each contributed top-level merged-file key recorded in `state.adapter.files[*].keys[]`. (CLI AC25, §7.4)
- [x] **ADAPT-05**: Merge strategies: `deep` (default for JSON/TOML — recursive object merge, leaf wins, arrays replace wholesale, contributed top-level keys recorded), `composite` (markdown — wraps in `<!-- ach:begin -->` / `<!-- ach:end -->`), `replace` (full overwrite, never default but adapter-allowed for end-to-end ACH-owned files). Adapter writes use temp+rename. (CLI AC26, §7.1)
- [x] **ADAPT-06**: Adapter scope rule: `claude-code` adapter touches only `.claude/`; `codex` only `.codex/`; etc. Prompts and artifacts (platform-agnostic) live under `<ach-dir>/prompts/` and `<ach-dir>/artifacts/` — written DIRECTLY by the hydrator core in §6.4, not the adapter. Multi-platform hydration in the same workspace shares prompts/artifacts. (CLI AC23, §6.5)
- [x] **ADAPT-07**: Adapter components a platform cannot meaningfully translate (e.g. `hooks/` for Codex) silently dropped from output; adapter records dropped names in stderr warning at end of hydration (does not change exit code). (CLI §7.4)

### STATE (CLI local state + lock + atomic write)

- [ ] **STATE-01**: State file at `<ach-dir>/state.json`. `<ach-dir> = <workspace>/.ach/` (workspace) or `~/.ach/<environment>/` (global). Includes lock, staging, prompts, artifacts as siblings. Multiple state files coexist per machine; not version-controlled (`.ach/` in `.gitignore`). (CLI §8.1)
- [x] **STATE-02**: State schema v2: per-file entries `{ target, hash, sourceHash, merge?, keys? }`. `hash = xxhash3` of bytes written; `sourceHash = xxhash3` of upstream input pre-transformation. For pass-through resources `hash == sourceHash`. `merge`+`keys[]` REQUIRED for adapter-written shared files; absent for ACH-owned files. CLI rejects `schemaVersion != "2"` with exit 5 unless `--force`. No migration in v1alpha1. (CLI AC9, §8.2)
- [ ] **STATE-03**: Same-`<ach-dir>` different-Environment guard: hydrate aborts with exit 4 unless `--force` is set (workspace scope only; in global scope `~/.ach/<environment>/` namespacing makes this case impossible). Guard keys on `environment`, not `deployment`. (CLI AC12, §8.3)
- [x] **STATE-04**: Drift detection (per §8.4 truth table): compares `state.hash` (last-written), `state.sourceHash` (last-upstream), on-disk hash, freshly-staged source hash. Four outcomes: no-op, upstream-only change (overwrite, no warning), local-only edit (preserve, exit 2), real conflict (preserve, exit 2). `--force` overwrites in any non-no-op case. Out-of-scope state slices NOT consulted. Tracked file missing on disk → silently pruned. (CLI AC11, §8.4)
- [x] **STATE-05**: `--sync` per-file deletion deepest-first; on-disk-hash mismatch preserves the file with stderr warning (drift wins; user edits sacred). For entries with `merge` + `keys[]`: inverse-merge (remove only listed keys via deep-merge inverse, OR replace `<!-- ach:begin -->...<!-- ach:end -->` for composite). Empty-dir pruning via `rmdir(2)` honoring `ENOTEMPTY` silently — CLI NEVER recursively deletes a directory. (CLI AC9, §8.5)
- [ ] **STATE-06**: Lock at `<ach-dir>/lock` via advisory `flock(LOCK_EX)` (POSIX) / `LockFileEx` (Windows). Released on process exit (even SIGKILL). Held from start of `ach-cli hydrate` through exit. Contention defaults to fail-fast (exit 1); `--wait` blocks; `--lock-timeout <duration>` caps the wait. (CLI AC20, §6.7)
- [x] **STATE-07**: Atomic state write: marshal → write `state.json.tmp` → `fsync(fd)` → `rename(2) state.json.tmp → state.json` → `fsync(parent_dir)`. State written LAST in the commit sequence. Crash anywhere before final rename leaves prior state intact; `state.json.tmp` swept on next hydrate start. (CLI AC20, §8.7)
- [x] **STATE-08**: Hydrate commit sequence (§6.7 14 steps) end-to-end: lock → sweep tmp → read state → reconcile vs disk (silent prune of missing-but-recorded files) → POST hydrate → diff → GET content → safe extract → hash + classify → adapter run → optional `--sync` → atomic state write → cleanup tmp → exit. (CLI AC20, §6.7)
- [ ] **STATE-09**: Manifest schema-version mismatch: CLI verifies `schemaVersion == "v1alpha1"` strictly before interpreting any other field; unknown versions abort with exit 5 and no files written. Both `runtime` and `context` MUST be present at top level (else schema violation, exit 5). (CLI AC13, §6.2)
- [x] **STATE-10**: Diff iteration scoped by `--include-runtime`/`--only-runtime` table. Default = context only; `--include-runtime` = both; `--only-runtime` = runtime only. Out-of-scope halves are not iterated and their state slices remain untouched. `--force` is also scope-bounded. (CLI AC10, §6.3, §6.5)
- [x] **STATE-11**: Hydrate fetch is unconditional even when `state` claims an upstream is unchanged: every `GET <downloadUrl>` runs, the disk write is short-circuited only when freshly-downloaded sha256 matches on-disk sha256. `--only-runtime` skips the GETs entirely. (CLI AC10, §6.3, §6.4)

### SAFE (Safe extraction)

- [ ] **SAFE-01**: Reject archive entries with absolute paths, `..` segments, or paths normalized outside the extraction root. Reject symlinks by default (`--allow-symlinks` opts in for in-tree-only resolved targets). Reject hardlinks, device files, FIFOs, sockets, pax-extended-header path injections — unconditionally. (CLI AC19, §6.4)
- [ ] **SAFE-02**: File modes masked to `mode & 0755`; setuid (`04000`), setgid (`02000`), sticky (`01000`), group-write (`0020`), world-write (`0002`) unconditionally stripped. Directory modes forced to `0755`. CLI never `chown`/`chmod` to archive-recorded values. Mtime/atime not preserved. (CLI AC19, §6.4)
- [ ] **SAFE-03**: Decompression-bomb caps: `ACH_MAX_EXTRACTED_PLUGIN_MIB` default 200 MiB, `ACH_MAX_EXTRACTED_ARTIFACT_MIB` default 500 MiB, `ACH_MAX_ARCHIVE_ENTRIES` default 65536. Exceeding any limit aborts THAT resource before writing the offending entry; partial output discarded with the rest of the staging dir; other resources continue. (CLI AC19, §6.4)
- [x] **SAFE-04**: Auto-claim collision policy at final-rename: `none` → write; `owned-by-current` (recorded in `state.*.files[]`) → overwrite (subject to drift); `exists-unowned` → three-tier lazy content compare (eager → lazy `resolveOutputContent()` → lazy source-file read). Identical bytes → auto-claim into state on commit. Different bytes → exit 7 with refusal, unless `--force`. (CLI §6.4)
- [ ] **SAFE-05**: Per-resource atomic publication: each resource extracted under `<ach-dir>/tmp/<rand>/<resource>/` then `rename(2)` into final location. Crash mid-extraction leaves no half-extracted resource visible; `<ach-dir>/tmp/` swept on every hydrate start. (CLI §6.4)
- [ ] **SAFE-06**: CLI streams archives through gzip decompression; MUST NOT buffer a full plugin or artifact archive in memory. Honors `Content-Length` when present, aborts partial stream with clear error. (CLI AC8, §6.4)

### DIST (Distribution)

- [ ] **DIST-01**: OCI container `ghcr.io/ackstorm/ach:<version>` with `ach` as default entrypoint; ships with NO preset credentials/config (injected at runtime). (CLI §2.1)
- [ ] **DIST-02**: Standalone `ach` binaries built for `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, `windows-amd64`. Distributed via GitHub Releases + Homebrew tap `ackstorm/tap/ach`. Container and standalone share the same codebase. (CLI §2.2)
- [ ] **DIST-03**: K8s InitContainer pattern documented and tested: `ach-cli hydrate --platform=$(ACH_PLATFORM) --include-runtime --output=/workspace` with `ACH_BASE_URL` + `ACH_API_KEY` from Secret, `ACH_PLATFORM` from env, workspace volume shared with main agent container. Runs in synthetic mode without local config file. (CLI §2.3)
- [ ] **DIST-04**: Hub deployed via Helm chart wiring: ACH Operator + Content Service single-replica `Recreate` Pod with RWO PVC; Platform API Deployment ≥1 replica; Forwarder Deployment ≥1 replica with `ach-jwt-signing-keys` Secret RBAC-scoped to its ServiceAccount; admin allowlist mounted from ConfigMap; Postgres + Redis as deployment-layer concerns. (Hub §5.1, §5.2, §18, §9.2)

---

## v2 Requirements

(Items not selected for v1alpha1 but expected of platform users in v2.)

- The §20 v1beta1 backlog items from the Hub spec (HA Operator + Content Service, auto-revoke `ek_` on Team-membership removal, HTTP `Range` + Conditional GET on `/content/`, dual-key acceptance window for the LiteLLM shared key, JWT `jti` + replay-window restatement, JWT signing throughput target, bearer-`ek_` identity guidance).
- The §13 v1beta1 backlog items from the CLI spec (custom adapter, user-overrideable platform table, declarative transformation DSL, template rendering, OS keyring integration, `ach hook emit`, offline `ach status`, deployment discovery, `ach-cli env-keys rotate`, workforce SSO multiplexing, resumable downloads, conditional content GET, sandboxed in-tree symlink resolution, dedicated `whoami` and `environments/{name}` endpoints).

## Out of Scope

(See PROJECT.md "Requirements › Out of Scope" — covers permanent design boundaries: no on-behalf-of identity, no server-side `pk_`-on-runtime forbid toggle, no soft-mode budget, no signature verification / sandboxing / SLSA / sigstore on plugin content, no default user-level budget on first SSO, no HTTP escape hatch, no `${VAR}` interpolation in CLI config file.)

---
## Traceability

Phase mapping populated by roadmap on 2026-05-15. Plan column filled by `/gsd:plan-phase`.

**Coverage:** 126 of 126 v1alpha1 REQ-IDs mapped to exactly one phase. No orphans, no duplicates.

| REQ-ID | Phase | Plan |
|--------|-------|------|
| CRD-01 | Phase 1 | TBD |
| CRD-02 | Phase 1 | TBD |
| CRD-03 | Phase 1 | 01-02 (CEL discriminator + interval≤maxStaleness rules) + 01-11 (CEL admission test asserts both rules on three fixtures) |
| CRD-04 | Phase 1 | 01-02 (maxStaleness REQUIRED marker + CEL has-check) + 01-11 (CEL admission test asserts via plugin_missing_maxstaleness fixture) |
| CRD-05 | Phase 1 | 01-02 (Artifact http→object CEL rule) + 01-11 (CEL admission test asserts via artifact_http_with_directory_scope fixture) |
| CRD-06 | Phase 1 | 01-02 (finalizer name declared in CRD) + 01-05 (finalizer add/remove in six reconcilers) + 01-11 (six per-kind envtest finalizer add+remove tests) |
| CRD-07 | Phase 1 | 01-02 (condition Type enum in CRD status schema) + 01-05 (writeStatus helpers emit closed-set reasons) |
| CRD-08 | Phase 1 | 01-02 (forwardIdentityJWT REQUIRED marker + resource-root CEL has-check) + 01-11 (CEL admission test asserts via backendidentitypolicy_missing_forwardidentityjwt fixture) |
| KEY-01 | Phase 3 | TBD |
| KEY-02 | Phase 3 | TBD |
| KEY-03 | Phase 3 | TBD |
| KEY-04 | Phase 3 | TBD |
| KEY-05 | Phase 3 | TBD |
| KEY-06 | Phase 3 | TBD |
| KEY-07 | Phase 3 | TBD |
| KEY-08 | Phase 3 | TBD |
| KEY-09 | Phase 3 | TBD |
| KEY-10 | Phase 3 | TBD |
| KEY-11 | Phase 3 | TBD |
| OP-01 | Phase 1 | TBD |
| OP-02 | Phase 1 | 01-05 (EnvironmentReconciler §6.5 drain via litellm.Client interface) + 01-11 (envtest finalizer test asserts litellmCounter >= 2 — DeleteAccessGroup + DeleteTag both fired) |
| OP-03 | Phase 2 | TBD |
| OP-04 | Phase 1 | TBD |
| OP-05 | Phase 1 | 01-08 (Pod spec: strategy:Recreate + single replica + RWO PVC) + 01-11 (e2e SC#2 subtest asserts ready=true ready=true + PVC Bound) |
| OP-06 | Phase 2 | TBD |
| OP-07 | Phase 2 | TBD |
| OP-08 | Phase 2 | TBD |
| OP-09 | Phase 2 | TBD |
| OP-10 | Phase 1 | 01-07 (internal/cachefs.EnsureLayout) + 01-11 (envtest cache-root seeding + cleanup assertion across four external-ref reconcilers) |
| OP-11 | Phase 1 | 01-07 (internal/cachefs layout creation; refresh-reset is Phase 2) |
| OP-12 | Phase 1 | 01-05 (external-ref reconcilers §10.3 os.Remove/RemoveAll before RemoveFinalizer) + 01-11 (four envtest tests seed cached file/subtree and assert removal before finalizer drops) |
| OP-13 | Phase 2 | TBD |
| OP-14 | Phase 4 | TBD |
| OP-15 | Phase 2 | TBD |
| OP-16 | Phase 4 | TBD |
| API-01 | Phase 3 | TBD |
| API-02 | Phase 3 | TBD |
| API-03 | Phase 3 | TBD |
| API-04 | Phase 3 | TBD |
| API-05 | Phase 3 | TBD |
| API-06 | Phase 3 | TBD |
| API-07 | Phase 3 | TBD |
| API-08 | Phase 3 | TBD |
| API-09 | Phase 3 | TBD |
| API-10 | Phase 3 | TBD |
| API-11 | Phase 3 | TBD |
| API-12 | Phase 3 | TBD |
| FWD-01 | Phase 4 | TBD |
| FWD-02 | Phase 4 | TBD |
| FWD-03 | Phase 4 | TBD |
| FWD-04 | Phase 4 | TBD |
| FWD-05 | Phase 4 | TBD |
| FWD-06 | Phase 4 | TBD |
| FWD-07 | Phase 4 | TBD |
| FWD-08 | Phase 4 | TBD |
| FWD-09 | Phase 4 | TBD |
| FWD-10 | Phase 4 | TBD |
| FWD-11 | Phase 4 | TBD |
| CS-01 | Phase 5 | TBD |
| CS-02 | Phase 5 | TBD |
| CS-03 | Phase 5 | TBD |
| CS-04 | Phase 5 | TBD |
| CS-05 | Phase 5 | TBD |
| CS-06 | Phase 5 | TBD |
| CS-07 | Phase 5 | TBD |
| CS-08 | Phase 5 | TBD |
| CS-09 | Phase 5 | TBD |
| CS-10 | Phase 5 | TBD |
| CS-11 | Phase 5 | TBD |
| DB-01 | Phase 1 | 01-03 |
| DB-02 | Phase 1 | 01-03 |
| DB-03 | Phase 1 | 01-03 (schema), 01-04 (hash), Phase 3 (write path) |
| DB-04 | Phase 1 | 01-03 (schema-level non-presence) |
| DB-05 | Phase 1 | 01-03 (column shape), Phase 3 (write path) |
| DB-06 | Phase 1 | 01-03 (column shape), Phase 2 (transactional writes) |
| OBS-01 | Phase 3 | TBD |
| OBS-02 | Phase 3 | TBD |
| OBS-03 | Phase 5 | TBD |
| OBS-04 | Phase 5 | TBD |
| OBS-05 | Phase 5 | TBD |
| OBS-06 | Phase 5 | TBD |
| MULTI-01 | Phase 1 | TBD |
| MULTI-02 | Phase 1 | TBD |
| MULTI-03 | Phase 1 | 01-06 (manager.WaitForCacheSync in cmd/operator/main.go) + 01-11 (envtest setupAndRun MULTI-03 gate before m.Run; e2e suite waits on Deployment rollout status) |
| MULTI-04 | Phase 1 | TBD |
| CLI-01 | Phase 6 | TBD |
| CLI-02 | Phase 6 | TBD |
| CLI-03 | Phase 6 | TBD |
| CLI-04 | Phase 6 | TBD |
| CLI-05 | Phase 6 | TBD |
| CLI-06 | Phase 6 | TBD |
| CLI-07 | Phase 6 | TBD |
| CLI-08 | Phase 6 | TBD |
| CLI-09 | Phase 6 | TBD |
| CLI-10 | Phase 6 | TBD |
| CLI-11 | Phase 6 | TBD |
| CLI-12 | Phase 6 | TBD |
| CLI-13 | Phase 6 | TBD |
| ADAPT-01 | Phase 7 | TBD |
| ADAPT-02 | Phase 7 | TBD |
| ADAPT-03 | Phase 7 | TBD |
| ADAPT-04 | Phase 7 | TBD |
| ADAPT-05 | Phase 7 | TBD |
| ADAPT-06 | Phase 7 | TBD |
| ADAPT-07 | Phase 7 | TBD |
| STATE-01 | Phase 7 | TBD |
| STATE-02 | Phase 7 | TBD |
| STATE-03 | Phase 7 | TBD |
| STATE-04 | Phase 7 | TBD |
| STATE-05 | Phase 7 | TBD |
| STATE-06 | Phase 7 | TBD |
| STATE-07 | Phase 7 | TBD |
| STATE-08 | Phase 7 | TBD |
| STATE-09 | Phase 7 | TBD |
| STATE-10 | Phase 7 | TBD |
| STATE-11 | Phase 7 | TBD |
| SAFE-01 | Phase 7 | TBD |
| SAFE-02 | Phase 7 | TBD |
| SAFE-03 | Phase 7 | TBD |
| SAFE-04 | Phase 7 | TBD |
| SAFE-05 | Phase 7 | TBD |
| SAFE-06 | Phase 7 | TBD |
| DIST-01 | Phase 7.1 | TBD |
| DIST-02 | Phase 7.1 | TBD |
| DIST-03 | Phase 7.1 | TBD |
| DIST-04 | Phase 7.1 | TBD |

---

## Phase 6 Deviations

Intentional divergences from `spec/ach_cli_spec_v20260515_FINALv4.md` taken during Phase 6 execution. Each row points at the originating decision in `06-CONTEXT.md` and the implementation plan that landed it.

| REQ                        | Status   | Decision | Plan(s) | Notes                                                                                                                                                                                                                                                                                                                                                                                  |
| -------------------------- | -------- | -------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CLI-09 (AC4 wire shape)    | DEVIATED | D-07     | 06-05   | spec §5.6 `--save-as` flag REMOVED. `ach-cli env-keys create` ALWAYS persists the returned `ek_` plaintext to `deployments.<active>.ek.<server-name>` in the active deployment of `~/.config/ach/config.yaml`. `--no-save` is the explicit opt-out (CI / vault-piping workflows). Wire-format binary-compat: flag REMOVED, new flag ADDED, default behavior CHANGES. See spec changelog 2026-05. |

---
*Last updated: 2026-05-28 — Phase 6 Deviations section added (D-07).*

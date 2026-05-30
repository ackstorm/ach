# Phase 3: Hub Identity & Platform API - Context

**Gathered:** 2026-05-19
**Status:** Ready for planning
**Mode:** `/gsd:discuss-phase 3 --auto` (single-pass autonomous resolution; all gray areas auto-selected, recommended option chosen per question)

<domain>
## Phase Boundary

Phase 3 turns the Phase 1 `cmd/platform-api/` stub (currently a `/healthz`-only long-running binary) into the user-facing identity and control plane for ACH. It implements 25 REQ-IDs across three families:

- **KEY-01..KEY-11** — `pk_` and `ek_` lifecycle:
  - Two key types only (`pk_` SSO-bound sliding-window 7-day, `ek_` workload-bound revocation-only); `x-ach-key` as the single auth header.
  - Each `ach login` mints a NEW `pk_` plaintext returned exactly once (multiple active `pk_` per user allowed).
  - §7.1 atomic single-CTE check-and-extend (`expires_at += 7d` only when `last_used_at < now() - 5m`; zero rows → `401 expired_or_revoked`).
  - `ek_` bound to exactly one Environment at creation; `last_used_at` debounced identically but does NOT participate in the auth decision.
  - **Asymmetric revocation**: `pk_` is DB-first (Postgres flip is the visible barrier); `ek_` is LiteLLM-first (LiteLLM is the runtime barrier; DB flip + Redis TTL bound the Content Service window).
  - `pk_` is permanent first-class on runtime forwarding routes (per [[feedback_ach_pk_runtime_first_class]] — no toggle, ever).
  - ACH never sets a default `max_budget` on first-SSO LiteLLM user creation.
  - `ek_` creation runs the Hub §8.2 8-step flow (verify Environment non-terminating → wait `AccessGroupSynced=True` via informer → idempotent verify-or-create LiteLLM user → create-then-insert with cleanup on DB-insert failure).

- **API-01..API-12** — Platform API endpoint surface under `/platform/`:
  - `POST /platform/hydrate` (accepts both `pk_` and `ek_` per §15.1; response carries `schemaVersion: "v1alpha1"` with both `runtime` + `context` blocks always present, `[]` when empty; never carries plaintext).
  - `POST/GET/DELETE /platform/env-keys` (§8.2 / §8.5 flows; `204 No Content` only after LiteLLM acks).
  - `GET /platform/environments` (informer-backed; filters by `authorizedTeams[]` ∩ caller Teams; admin sees all; carries `conditions[]` verbatim from §6.6).
  - `POST /platform/admin/{keys/revoke, users/{email}/revoke-keys, refresh}` — `403 not_admin` check runs BEFORE any other validation; admin endpoints reject `ek_` with `401 invalid_key_type`.
  - Admin allowlist read at process start from `/etc/ach/admins/admins.txt` (ConfigMap-mounted, one email per line; comments + blanks ignored; restart required to update).
  - `POST /platform/admin/refresh` patches `ach.ackstorm.ai/force-refresh: <RFC3339>` annotation on the target CR — Platform API's only write surface to ACH CRDs (MULTI-02 carve-out granted in Phase 1's RBAC).
  - Error envelope: `{ "error": { "code": "<outcome>", "message": "..." }, "request_id": "req_..." }`; list endpoints accept `?limit=<int>&cursor=<opaque>`.

- **OBS-01, OBS-02** — structured JSON audit events:
  - Reuse the Phase 2 `internal/audit/handler.go` slog handler (D-17 of Phase 2 — `audit:true` top-level field; JSON; stdout sink).
  - Events for: `pk_`/`ek_` create+revoke, Environment create/update/delete, hydrate, admin operations. Sliding-window `pk_` extension is NOT its own event; runtime forwarding is NOT audited (LiteLLM owns that channel).
  - Required fields: `timestamp`, `actor=<namespace>/<sso-email>`, `action`, `outcome ∈ §18.2 enum`, `request_id`, `key.id` in `pkid_`/`ekid_` form (NEVER plaintext, NEVER `credential_hash`).
  - `target.kind`/`target.name` required on resource-scoped events.

Phase 3 explicitly **excludes**:
- The Forwarder's runtime header rewrite, JWKS publication, ACH JWT signing, `BackendIdentityPolicy.status` writes (Phase 4 — FWD-01..FWD-11, OP-14, OP-16).
- Content Service streaming + `sendfile(2)` + scope-aware authorization at the content surface (Phase 5 — CS-01..CS-11). Phase 3 produces the DB rows + Redis cache that Phase 5 will consume; Phase 3 owns the SQL helper (`PkCheckAndExtend`) and Redis schema.
- Prometheus `/metrics` endpoints + ServiceMonitor manifests (Phase 5 — OBS-03..OBS-06). Phase 3 emits AUDIT events; counter hooks are stubbed inline but the `/metrics` wiring is Phase 5.
- CLI work (Phase 6 — CLI-01..CLI-13).
- Any CRD schema changes — Phase 1 owns the v1alpha1 schema verbatim.
- Any LiteLLM Operator coordination — ACH talks to LiteLLM REST exclusively, never reads `litellm.ackstorm.ai/*` CRDs.

</domain>

<decisions>
## Implementation Decisions

### HTTP server + middleware stack

- **D-01:** **`net/http` + `go-chi/chi/v5` as router.** Lightweight, idiomatic, stdlib-compatible `Handler`/`Middleware` signatures (chi middleware is `func(http.Handler) http.Handler`), no global state, no codegen, no struct-tag DSL. Echo and Gin were considered; chi wins on (a) zero-magic routing, (b) widest K8s-controller-ecosystem familiarity (cert-manager / controller-runtime examples / kubebuilder community), (c) friendly to the Phase 1 stub idiom (`http.ServeMux` already in `cmd/platform-api/main.go:42` migrates cleanly to `chi.Mux`). Dependency added in Phase 3.
- **D-02:** **Middleware chain (outer → inner):** `RequestID` (generates `req_<ulid>` early, stores in `context.Context`, sets `X-Request-Id` response header) → `RecoverPanic` (slog.Error + audit `outcome=internal_error`) → `AccessLog` (method, path, status, latency_ms; NEVER raw `x-ach-key`; redacts to `<prefix>_***` per FWD-11 analog in API surface) → `ContentTypeJSON` (sets `application/json; charset=utf-8` on every response, error or success) → `Authn` (extracts `x-ach-key` from header, resolves via the D-07 cache helper; injects `KeyContext{key_id, key_type, owner_email, environment?}` into ctx) → routed handler. `Authn` is per-route, not global — `/healthz`, `/livez`, `/readyz`, and the Dex callback `/auth/sso/callback` must bypass it.
- **D-03:** **`http.Server` config** retains Phase 1's `ReadHeaderTimeout: 5s` (gosec G112); adds `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s`, `MaxHeaderBytes: 1 MiB`. Graceful shutdown via `http.Server.Shutdown(ctx, 10s)` — same Phase 1 idiom carried forward.

### Dex SSO flow

- **D-04:** **Stateless OIDC + PKCE Authorization Code flow.** Use `github.com/coreos/go-oidc/v3` (provider discovery, ID-token validation, claim extraction) + `golang.org/x/oauth2` (already an indirect dep per `go.mod`; promote to direct). The flow:
  1. `GET /platform/auth/login` — generate `state` (16B random base64url) + PKCE `code_verifier` (32B), set both in a SHORT-LIVED `__Host-ach_sso` HttpOnly+Secure+SameSite=Strict cookie (TTL 10min), redirect to Dex authorize URL with `code_challenge=S256`.
  2. `GET /platform/auth/sso/callback?code=...&state=...` — verify state matches cookie, exchange code for ID token using `code_verifier`, validate ID-token signature against Dex JWKS, extract `email` claim verbatim (Hub §16 DB-05 — no normalization), idempotent verify-or-create LiteLLM user (D-25 `UserNew` + `UserInfoByEmail`), add to `default` Team (mandatory; missing → `500 default_team_missing` with audit `outcome=default_team_missing`), mint `pk_` (D-25 `KeyGenerate`), INSERT row, return JSON `{"key_id":"pkid_...","plaintext":"pk_...","owner_email":"..."}` exactly once. No server-side session is established after step 2 — the response IS the only output and the cookie is cleared.
- **D-05:** **No browser-facing UI.** Both endpoints serve JSON only; the redirect from step 1 is a `302` straight to Dex. CLI consumers (`ach login` in Phase 6) drive the flow via the local OAuth helper pattern (open browser → loopback callback → POST resulting code/state to ACH's `/platform/auth/sso/callback`). v1alpha1 supports that pattern only; a hosted login web page is deferred to v1beta1.
- **D-06:** **Dex configuration via env vars:** `ACH_DEX_ISSUER_URL` (required; e.g. `https://dex.ach-system.svc:5556/dex`), `ACH_DEX_CLIENT_ID` (required), `ACH_DEX_CLIENT_SECRET` (required; sourced from `Secret`-mounted env via `envFrom`), `ACH_DEX_REDIRECT_URL` (required; e.g. `$ACH_BASE_URL/platform/auth/sso/callback`). Process refuses to start if any of these are unset/empty. Dex's JWKS is discovered via the OIDC `.well-known/openid-configuration` endpoint at process start; cached in-process; refreshed on signature validation failures.

### Key resolution + Redis cache

- **D-07:** **Redis cache schema:** key = `ach:key:<hex_credential_hash>` (full 64-char hex; never the bearer plaintext); value = JSON `{key_id, key_type, owner_email, status, expires_at?, environment?, litellm_token?}`; TTL = 60s (hard ceiling per Hub §5.1 / FWD-02 / KEY-04). On miss, single-flight lookup against Postgres (`golang.org/x/sync/singleflight`), populate Redis, return. On hit, return without DB I/O. Invalidation on revoke: explicit `DEL ach:key:<hex>` immediately after the DB+LiteLLM ordered writes complete (Redis is the last barrier in both revocation flows per KEY-07, KEY-08).
- **D-08:** **`internal/keystore/`** new package owns this concern. Exposes: `Resolver` interface with `Resolve(ctx, plaintextKey) (*KeyInfo, error)`. Concrete `redisCachedResolver` wraps a `Resolver` (the Postgres-backed `dbResolver`) and shorts on cache hit. Both `pk_` and `ek_` resolution paths share the same Resolver; `KeyInfo.key_type` discriminates downstream. Reusing this package is what makes Phase 4 (Forwarder) and Phase 5 (Content Service) cheap — they import `internal/keystore` and the cache wiring lives in one place.
- **D-09:** **`go-redis/redis/v9`** as client library (current major; matches the predecessor `litellm-autoconfig` pattern). Connection pool sized per Platform API replica via `ACH_REDIS_*` env vars (`ACH_REDIS_ADDR`, `ACH_REDIS_PASSWORD?`, `ACH_REDIS_TLS?=false`, `ACH_REDIS_DB?=0`). Phase 3 introduces Redis as an ACH dependency for the first time; the existing `docker-compose.yml` already has a `redis` service from Phase 1's scaffolding.

### §7.1 atomic check-and-extend SQL

- **D-10:** **Single-statement UPDATE with WHERE-snapshot + RETURNING.** Form:
  ```sql
  UPDATE personal_keys SET
      last_used_at = CASE
          WHEN last_used_at IS NULL OR last_used_at < now() - interval '5 minutes' THEN now()
          ELSE last_used_at
      END,
      expires_at = CASE
          WHEN last_used_at IS NULL OR last_used_at < now() - interval '5 minutes' THEN now() + interval '7 days'
          ELSE expires_at
      END
  WHERE credential_hash = $1
    AND status = 'active'
    AND expires_at > now()
  RETURNING key_id, owner_email, expires_at
  ```
  Zero rows returned ⇒ revoked, expired, or unknown ⇒ `401 expired_or_revoked` (per KEY-04; the three causes are indistinguishable by design — no information leak). One row returned ⇒ valid; `last_used_at` debounce embedded in the same statement (no second roundtrip). This statement IS the `pk_` resolution path on every Hub component (Phase 3 Platform API, Phase 4 Forwarder, Phase 5 Content Service); ship as `internal/db.PkCheckAndExtend(ctx, pool, credentialHashHex) (PkKeyInfo, error)`.
- **D-11:** **`ek_` resolution helper** is simpler — no check-and-extend, no sliding window. Form:
  ```sql
  UPDATE environment_keys SET
      last_used_at = CASE
          WHEN last_used_at IS NULL OR last_used_at < now() - interval '5 minutes' THEN now()
          ELSE last_used_at
      END
  WHERE credential_hash = $1
    AND status = 'active'
  RETURNING key_id, environment, owner_email, name, litellm_token
  ```
  Per KEY-06 the `last_used_at` UPDATE does NOT participate in the auth decision (the row-returned predicate `status = 'active'` is authoritative). Ship as `internal/db.EkResolve(ctx, pool, credentialHashHex) (EkKeyInfo, error)`.

### §8.2 ek_ create transaction shape

- **D-12:** **LiteLLM `KeyGenerate` runs OUTSIDE the DB transaction; INSERT runs INSIDE.** Sequence per Hub §8.2 8-step:
  1. Parse + authenticate caller (must be `pk_`; `ek_` here → `401 invalid_key_type`).
  2. Read Environment from informer cache (D-20). Missing or `deletionTimestamp != nil` → `404 environment_not_found`.
  3. Read `AccessGroupSynced` condition from Environment status (informer). Not `True` → `503 not_ready` (caller may retry).
  4. Verify caller has `authorizedTeams[]` ∩ LiteLLM Teams ≠ ∅ (cached Team-membership lookup, ≤60s TTL — same cache the Forwarder uses in Phase 4). Empty → `403 unauthorized_team`.
  5. Idempotent verify-or-create LiteLLM user: `UserInfoByEmail(owner_email)`; if absent, `UserNew(email, team_ids=["default"])`. NEVER sets `max_budget` (KEY-10).
  6. `KeyGenerate(user_id, environment, name, access_groups=["<environment>"])` → receives LiteLLM-internal `key`, `token_id` (LiteLLM's opaque hex). Returned `key` is hashed by Platform API code (HMAC-SHA-256 with pepper, per `internal/credhash`).
  7. INSERT row in `environment_keys` (key_id=`ekid_<ulid>`, credential_hash, environment, owner_email, name, status='active', litellm_user_id=user_id, litellm_token=token_id) — inside `BEGIN/COMMIT`. On INSERT failure (unique violation on credential_hash retried 1× with new ulid; other failures), call `LiteLLM.RevokeKey(token_id)` to clean up. Surfacing `500` on irrecoverable failure is acceptable; the LiteLLM-side compensation MUST run regardless.
  8. Return JSON `{key_id, plaintext, environment, name, owner_email, created_at}` once. Emit audit `action=ek.create`, `outcome=created` (or `outcome=db_insert_failed`/`outcome=litellm_unreachable`/etc on failure paths).
- **D-13:** **Generate the bearer plaintext SERVER-SIDE on ACH** (NOT relayed from LiteLLM's `KeyGenerate` response). The `ek_<base32-no-pad-26-chars>` value is created by ACH using `crypto/rand` BEFORE calling LiteLLM; ACH hashes it (HMAC-SHA-256 + pepper, hex) for `credential_hash`. The LiteLLM-side `KeyGenerate` call sets LiteLLM's `key` to ACH's plaintext via the `key` request-body field (LiteLLM supports caller-supplied keys per its `/key/generate` API). This keeps ACH the sole authority over the bearer namespace (`pk_*`/`ek_*` prefix invariant) and lets ACH return the plaintext from D-12 step 6 without an extra round-trip parse. `pk_` plaintext is generated identically: `pk_<base32-no-pad-26-chars>`.

### Asymmetric revocation

- **D-14:** **Two distinct handlers**, one per key type, never one branching helper. `revokePersonalKey(ctx, key_id)` runs:
  1. `BEGIN` → `UPDATE personal_keys SET status='revoked', revoked_at=now() WHERE key_id=$1 AND status='active' RETURNING credential_hash, litellm_token` → `COMMIT`. Zero rows → `404` (already revoked or unknown).
  2. `litellm.RevokeKey(litellm_token)`. Best-effort: on LiteLLM-unreachable, log `outcome=litellm_unreachable`, emit `litellm_unreachable_total{caller="platform_api"}` counter increment (Phase 5 surfaces the metric), proceed — Postgres flip IS the visible barrier per Hub §7.1; LiteLLM is the runtime barrier and stays revoked-with-retry via the orphan-cleanup loop on the next tick.
  3. `redis.DEL("ach:key:" + credential_hash)`. Best-effort; Redis ≤60s TTL caps the worst case.
  4. Audit `action=pk.revoke`, `outcome=revoked|litellm_unreachable`, `key.id=pkid_...`.
- **D-15:** **`revokeEnvironmentKey(ctx, key_id)`** inverts steps 1 and 2:
  1. Read row to capture `credential_hash` + `litellm_token`. No status flip yet.
  2. `litellm.RevokeKey(litellm_token)`. On LiteLLM-unreachable → return `503 litellm_unreachable` to caller; abort. LiteLLM is the load-bearing barrier per KEY-08; the DB row stays active so a retry retries cleanly.
  3. `UPDATE environment_keys SET status='revoked', revoked_at=now() WHERE key_id=$1` (post-LiteLLM-ack DB flip).
  4. `redis.DEL(...)`.
  5. Audit `action=ek.revoke`, `outcome=revoked`. `204 No Content` to caller only after the LiteLLM ack (per API-07).

### Hydrate endpoint shape

- **D-16:** **`POST /platform/hydrate` body schema (request)** — strict JSON: `{"environment": "<name>"}` (string, optional for `ek_`, required for `pk_`). Unknown fields rejected by `json.Decoder.DisallowUnknownFields()` → `400 invalid_argument`. Missing required field for `pk_` → `400 missing_environment`.
- **D-17:** **Response shape** built from informer cache + computed `downloadUrl`s:
  ```json
  {
    "schemaVersion": "v1alpha1",
    "environment": "<name>",
    "runtime": {"models":[...],"mcpServers":[...],"a2aAgents":[...]},
    "context": {"prompts":[...],"plugins":[...],"artifacts":[...]}
  }
  ```
  Each `runtime[*]` carries `id` + `endpoint` (LiteLLM-side mapping for the resource); each `context[*]` carries `name`, `id`, `downloadUrl: $ACH_BASE_URL/content/<kind>/<name>`. Both blocks always present, `[]` when empty (per API-04). Plaintext never appears anywhere in the response. Terminating Environments STILL serve hydrate (per API-03 v9 — drain semantics are Phase 5 / CS-09 territory).

### Audit event surface

- **D-18:** **`internal/audit/events.go`** new file defines exported constants for every Phase 3 action + outcome — single source of truth for downstream phases.
  - Actions: `ActionSSOLogin = "platform.sso.login"`, `ActionEkCreate = "platform.ek.create"`, `ActionEkRevoke = "platform.ek.revoke"`, `ActionPkRevoke = "platform.pk.revoke"`, `ActionHydrate = "platform.hydrate"`, `ActionAdminRefresh = "platform.admin.refresh"`, `ActionAdminKeysRevoke = "platform.admin.keys.revoke"`, `ActionAdminUsersRevokeKeys = "platform.admin.users.revoke_keys"`, `ActionEnvironmentLifecycle = "platform.environment.lifecycle"` (Phase 3 does NOT itself write Environment CRs — this constant is reserved for the Operator's emission point and intentionally namespaced under `platform.` for grep ergonomics).
  - Outcomes (extend Phase 2's `internal/audit` enum): `OutcomeCreated, OutcomeRevoked, OutcomeUnauthorizedTeam, OutcomeWrongEnvironment, OutcomeMissingEnvironment, OutcomeEnvironmentNotFound, OutcomeNotReady, OutcomeDefaultTeamMissing, OutcomeInvalidKeyType, OutcomeNotAdmin, OutcomeNotKeyOwner, OutcomeInvalidKeyFormat, OutcomeExpiredOrRevoked, OutcomeLitellmUnreachable, OutcomeDbInsertFailed, OutcomeInternalError` — verbatim values from Hub §18.2.
- **D-19:** **`EmitAudit(ctx, audit.Event{...})` helper** in `internal/audit/`. Always invoked from the request handler (NOT middleware) so the handler has full control over `target`/`key.id` fields. The middleware-injected `req_<ulid>` and `actor=<namespace>/<sso-email>` are read from ctx; the audit logger does NOT itself derive `key.id` from the credential — the handler is the only place that knows the resolved `key_id` (with `pkid_`/`ekid_` prefix) safely. Plaintext bearer values are ALREADY out of ctx by the time the handler runs (Authn middleware D-02 discards plaintext after resolution).

### Informer-backed Environment read model

- **D-20:** **Platform API runs a controller-runtime `manager.Manager` without controllers and without leader election.** Just the cache and the informer set. Configured via `ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{ LeaderElection: false, HealthProbeBindAddress: ":8082", MetricsBindAddress: "0", Cache: cache.Options{Namespaces: ...}})` (namespace-scoped per MULTI-01). Informers registered for all six ACH kinds + `corev1.Secret` (Dex client secret rotation; mirrored from Phase 2 D-11). `mgr.WaitForCacheSync()` runs at startup and gates `/readyz` (MULTI-03 carry-forward).
- **D-21:** **Reader helpers in `internal/platformapi/store/`** wrap the typed informer-backed client (`mgr.GetClient()` returns a cached client). One helper per Environment-related read: `GetEnvironment(ctx, name)`, `ListAuthorizedEnvironments(ctx, callerTeams)`, `EnvironmentAccessGroupSynced(ctx, name)`, `EnvironmentTerminating(ctx, name)`. All sub-millisecond after warmup; no API-server round trips.

### Admin allowlist + ConfigMap mount

- **D-22:** **Allowlist parsing happens once at process start.** Path is `ACH_ADMIN_ALLOWLIST_PATH` env var (default `/etc/ach/admins/admins.txt`). Format: one email per line; blank lines ignored; lines beginning with `#` ignored; whitespace trimmed; case-sensitive verbatim comparison (mirrors `owner_email` storage discipline per Hub §16 DB-05). Result is an in-memory `map[string]struct{}`; lookup is O(1). The file is NOT re-read; per Hub AC18 / AC24, ConfigMap edits require Platform API restart (deployer concern). The K8s ConfigMap watch could re-trigger a reload but is deliberately out of scope.
- **D-23:** **Missing/empty file behavior — start succeeds with empty allowlist.** Per the spec "ConfigMap-mounted file" (AC18), an absent allowlist is a deployer choice (zero admins) and ACH MUST NOT refuse to start. Log a single `WARN` at startup if the allowlist resolves to zero admins. `403 not_admin` then rejects every admin call uniformly — there is no escape hatch and no implicit-root behavior.

### Code structure

- **D-24:** **`internal/platformapi/` as the home package**, with one file per endpoint group plus shared helpers:
  - `internal/platformapi/server.go` — `New(deps Deps) http.Handler` returns the `chi.Mux` with the middleware chain (D-02) applied.
  - `internal/platformapi/auth/sso.go` — `LoginHandler`, `CallbackHandler` (D-04).
  - `internal/platformapi/envkeys/handler.go` — `CreateHandler`, `ListHandler`, `RevokeHandler`, `GetHandler`.
  - `internal/platformapi/environments/handler.go` — `ListHandler`, `GetHandler`.
  - `internal/platformapi/hydrate/handler.go` — `HydrateHandler`.
  - `internal/platformapi/admin/handler.go` — `RevokeKeyHandler`, `RevokeUserKeysHandler`, `ForceRefreshHandler`.
  - `internal/platformapi/store/` — informer-backed reader helpers (D-21).
  - `internal/platformapi/render/json.go` — error envelope helpers (per API-12).
  - `cmd/platform-api/main.go` — wires `manager.Manager`, `pgxpool`, `go-redis` client, `litellm.RESTClient`, `audit.Logger`, `oidc.Provider`, allowlist loader, then `mgr.Add(serverRunnable{})` to run the HTTP server alongside the informer cache.

### LiteLLM client extensions

- **D-25:** **New methods on `litellm.Client` interface** (`internal/litellm/client.go`), added during Phase 3:
  - `UserNew(ctx, req UserNewRequest) (*UserInfo, error)` — `POST /user/new` with `{user_email, user_id?, teams=[default]}`; mirrors sister's idioms.
  - `UserInfoByEmail(ctx, email) (*UserInfo, error)` — `GET /user/info?user_email=<email>`. Returns `nil, ErrNotFound` when LiteLLM returns 404 (per `errors.go` convention).
  - `TeamMemberAdd(ctx, teamID, userID, role) error` — `POST /team/member_add`. Idempotent client-side (read membership first); LiteLLM treats duplicate add as 4xx, caller swallows.
  - `KeyGenerate(ctx, req KeyGenerateRequest) (*KeyGenerateResponse, error)` — `POST /key/generate` with `{user_id, key="<ACH-generated>", key_alias?, models?, max_budget=null, tags?, access_groups?}`; response carries `token` (LiteLLM-internal opaque hex; stored in `personal_keys.litellm_token`/`environment_keys.litellm_token` per Phase 02.2 D-01).
  - Existing `RevokeKey(ctx, keyID)` is reused for revocation paths.
  - `NoopClient` gains stub implementations returning canned `&UserInfo{...}` / `&KeyGenerateResponse{...}` values so unit tests can run without a real LiteLLM. The compile-time `var _ Client = (*NoopClient)(nil)` canary catches drift.

### Force-refresh annotation patch

- **D-26:** **`POST /platform/admin/refresh`** uses `client.Client.Patch(ctx, obj, client.MergeFrom(orig))` — a JSON merge-patch via the typed controller-runtime client (NOT a strategic merge patch; CRDs are unstructured to that helper). The annotation `ach.ackstorm.ai/force-refresh: <RFC3339>` is set; `Patch` returns the updated object. The Phase 2 reconcile loop (D-07 of Phase 2 CONTEXT) reads + clears the annotation on the next reconcile. Returns `202 Accepted` immediately after the Patch ack (the actual refresh is async). Body: `{"kind":"plugin|prompt|artifact|pluginmarketplace","name":"<name>"}`. Unknown kind → `400 invalid_argument`. Resource not found → `404`. Patch failure (RBAC, conflict) → `500` with audit `outcome=internal_error`.

### Claude's Discretion

- **`request_id` ULID source** — `github.com/oklog/ulid/v2` (lock-free, time-ordered, base32-encoded — naturally readable). The `req_` prefix is added at emission time.
- **`crypto/rand` for bearer plaintext** — stdlib only; the `base32.StdEncoding.WithPadding(base32.NoPadding)` encoder is the right shape for `pk_<26-chars>` / `ek_<26-chars>` (26 chars ≈ 130 bits of entropy from 16 random bytes).
- **`pgx` row-scan idioms** carried over from Phase 1 / Phase 2 (`pgx.RowToStructByPos` / `pgx.CollectOneRow`). The DB write path stays inside the `internal/db/` package; handlers call typed helpers, never craft SQL inline.
- **OIDC discovery cache** for Dex JWKS: 24h TTL with on-signature-failure refresh (standard go-oidc pattern via `oidc.NewRemoteKeySet`).
- **Test infrastructure** — Ginkgo + Gomega + envtest from Phase 1 continues; `httptest.Server` for Dex-mock and LiteLLM-mock paths; testcontainers-go for Postgres + Redis. The `cmd/platform-api/` binary gets a smoke test in `test/e2e/` (Phase 1 patterns) gated on `ACH_PLATFORM_API_BASE_URL`.
- **Counter hooks** (`platform_api_requests_total{route,key_type,outcome}`, `platform_api_request_duration_seconds`, `litellm_unreachable_total{caller="platform_api"}`) are added inline in the middleware + handlers — the full Prometheus emitter and `/metrics` endpoint wiring is Phase 5.
- **Migration sequence** — no new migration. Phase 02.2 already added `litellm_token` columns (000003). Phase 3 writes into `personal_keys.litellm_user_id`/`litellm_token` and `environment_keys.litellm_user_id`/`litellm_token` for the first time; this closes the documented Phase 02.2 D-02 prerequisite.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### ACH Hub Spec (source of truth)

- `ach_hub_spec_v20260515_FINALv4.md` §3 — `x-ach-key` single-header invariant; `x-ach-environment` for Content Service only (Phase 3 emits the header conventions Phase 4/5 will enforce).
- `ach_hub_spec_v20260515_FINALv4.md` §5.1 — Per-component RBAC table + Platform API's `patch` carve-out (MULTI-02); resolver short-circuit ordering.
- `ach_hub_spec_v20260515_FINALv4.md` §5.2 — Informer-cache discipline; readiness gate on cache sync; secret informer.
- `ach_hub_spec_v20260515_FINALv4.md` §6.3 — Budget model; `pk_` no Environment tag; `ek_` Environment tag; ACH never sets `max_budget` (KEY-10).
- `ach_hub_spec_v20260515_FINALv4.md` §6.4 — Environment requeue + `ExecutionResourcesResolved` (read-only by Platform API).
- `ach_hub_spec_v20260515_FINALv4.md` §6.5 — Environment drain order (read-only by Platform API; Phase 3 reads `deletionTimestamp` and `AccessGroupSynced` via informer).
- `ach_hub_spec_v20260515_FINALv4.md` §6.6 — Condition reasons closed set (`conditions[]` carried verbatim in environment listing per API-08).
- `ach_hub_spec_v20260515_FINALv4.md` §7 — `pk_` lifecycle, sliding window, 7-day expiry.
- `ach_hub_spec_v20260515_FINALv4.md` §7.1 — `pk_` atomic check-and-extend SQL CTE shape (D-10 implements verbatim).
- `ach_hub_spec_v20260515_FINALv4.md` §7.3 — `pk_` runtime budget asymmetry (read-only context for Phase 3).
- `ach_hub_spec_v20260515_FINALv4.md` §8 — `ek_` lifecycle.
- `ach_hub_spec_v20260515_FINALv4.md` §8.1 — `ek_` binding semantics; `last_used_at` debounce; NOT live-reauthorized.
- `ach_hub_spec_v20260515_FINALv4.md` §8.2 — 8-step `ek_` create flow (D-12 implements verbatim).
- `ach_hub_spec_v20260515_FINALv4.md` §8.3 — `ek_` create error matrix (`404 environment_not_found`, `503 not_ready`, ...).
- `ach_hub_spec_v20260515_FINALv4.md` §8.5 — `ek_` revoke flow (D-15 LiteLLM-first sequencing).
- `ach_hub_spec_v20260515_FINALv4.md` §8.6 — Budget attribution asymmetry; `pk_` runtime is permanent first-class.
- `ach_hub_spec_v20260515_FINALv4.md` §15.1 — `POST /platform/hydrate` request/response contract (D-16, D-17 implement verbatim).
- `ach_hub_spec_v20260515_FINALv4.md` §15.2 — Hydrate `schemaVersion: "v1alpha1"`; runtime + context always present.
- `ach_hub_spec_v20260515_FINALv4.md` §15.5 — Platform API envelope, list pagination, env-keys endpoints, admin endpoints, force-refresh annotation contract.
- `ach_hub_spec_v20260515_FINALv4.md` §15.6 — Content Service contract (read-only by Phase 3 — informs `downloadUrl` construction in hydrate response).
- `ach_hub_spec_v20260515_FINALv4.md` §16 — DB schema; `pkid_`/`ekid_` prefix invariant.
- `ach_hub_spec_v20260515_FINALv4.md` §16.1 — Credential storage; HMAC-SHA-256 with pepper; plaintext never persisted; constant-time compare.
- `ach_hub_spec_v20260515_FINALv4.md` §17 — LiteLLM decoupling; ACH talks REST only; `default` Team is a deployer concern.
- `ach_hub_spec_v20260515_FINALv4.md` §18 — Admin allowlist; ConfigMap mount; restart-to-update; `ek_` never on admin endpoints.
- `ach_hub_spec_v20260515_FINALv4.md` §18.2 — Audit event schema, stable `outcome` enum (D-18 wires constants verbatim).
- `ach_hub_spec_v20260515_FINALv4.md` §18.3 — Multi-tenancy; namespace prefix in `actor`; deployment isolation.
- `ach_hub_spec_v20260515_FINALv4.md` §18.4 — Orphan-cleanup audit shape (Phase 2 owns the emission; Phase 3 audit handler is the same `slog.Handler` instance).

### Planning Artifacts

- `.planning/PROJECT.md` — Hub-side stack (Go, chi/echo, `pgx`, `go-redis`, `crypto/ed25519`); LiteLLM coupling; `pk_` permanent first-class; HTTPS-only Platform API ingress.
- `.planning/REQUIREMENTS.md` — Phase 3 maps to: KEY-01..11, API-01..12, OBS-01..02 (25 REQ-IDs).
- `.planning/ROADMAP.md` — Phase 3 entry: goal, depends-on (Phase 1 + Phase 2), six SCs covering SSO/hydrate/env-keys/revocation/admin/audit.
- `.planning/STATE.md` — Position pre-Phase-3 (Phase 02.2 complete; engineer-pending live UAT for OP-13 closure).
- `.planning/phases/01-foundation-crds-db-schema-operator-skeleton-multi-tenancy/01-CONTEXT.md` — Phase 1 carry-forward: kubebuilder v4 layout, `internal/db` pgxpool wrapper, `internal/credhash` HMAC, `cmd/platform-api/main.go` stub, RBAC scaffolding incl. MULTI-02 carve-out.
- `.planning/phases/02-external-refs-marketplace-operator-reconciliation/02-CONTEXT.md` — Phase 2 carry-forward: `internal/litellm.RESTClient` (real implementation), `internal/audit/handler.go` (slog with `audit:true`), force-refresh annotation contract (D-07 of Phase 2), counter-hook pattern.
- `.planning/phases/02.2-phase-02-cleanup-gap-g1-fix-litellm-real-uat-path-invariant-/02.2-CONTEXT.md` — Phase 02.2 carry-forward: `litellm_token` column added to `personal_keys`/`environment_keys` (D-01 of Phase 02.2); Phase 3 is the documented write path (D-02 of Phase 02.2).

### Sister Project (lift / convention source)

- `../ach_litellm/internal/litellm/` — Provider matrix, request shapes for `UserNew` / `KeyGenerate` / `TeamMemberAdd` (Phase 3 D-25 mirrors the naming conventions established in Phase 2's lift).
- `../ach_litellm/cmd/` per-binary layouts — useful reference if Phase 3 evolves `cmd/platform-api/` past a single `main.go`.

### External Libraries (new dependencies for Phase 3)

- `github.com/go-chi/chi/v5` — HTTP router + middleware idioms (D-01).
- `github.com/coreos/go-oidc/v3` — OIDC discovery, ID-token validation (D-04).
- `golang.org/x/oauth2` — OAuth2 PKCE flow (promote from indirect to direct).
- `github.com/redis/go-redis/v9` — Redis client (D-09).
- `golang.org/x/sync/singleflight` — In-process dedup for cache-miss DB lookups (D-07).
- `github.com/oklog/ulid/v2` — `req_<ulid>` request IDs (Claude's discretion).
- (existing) `sigs.k8s.io/controller-runtime` — informer-only manager (D-20).
- (existing) `github.com/jackc/pgx/v5` — Postgres pool (Phase 1 carry-forward).

### Predecessor / Memory References

- [[reference_ach_litellm_sister_project]] — Lift target for LiteLLM client extensions.
- [[reference_litellm_autoconfig_predecessor]] — Python daemon predecessor; informs LiteLLM API shape understanding.
- [[feedback_ach_pk_runtime_first_class]] — `pk_` on runtime is permanent first-class; NO server-side toggle to forbid it (carry-forward; not relitigated).
- [[feedback_spec_source_of_truth]] — Local `.md` spec is canonical (`ach_hub_spec_v20260515_FINALv4.md`); never the gist.
- [[feedback_litellm_operator_no_redaction_filter]] — Discipline over scrubbing; audit logger reuses Phase 2's no-redaction handler.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets (from Phases 1, 2, 02.2)

- **`cmd/platform-api/main.go`** (Phase 1, ~75 LoC stub) — currently a `/healthz` long-running process. Phase 3 grows this to wire up: `manager.Manager` (D-20), `pgxpool.Pool` (Phase 1 DB), `go-redis.Client` (new D-09), `litellm.RESTClient` (Phase 2 lift), `audit.Logger` (Phase 2 D-17), OIDC provider (D-04), allowlist (D-22), then `mgr.Add(serverRunnable{addr, handler})` and `mgr.Start(signalContext)`.
- **`internal/db/db.go`** (Phase 1) — `pgxpool.New()` wrapper; Phase 3 adds `PkCheckAndExtend` (D-10), `EkResolve` (D-11), `InsertPersonalKey`, `InsertEnvironmentKey`, `RevokePersonalKey`, `RevokeEnvironmentKey`, `GetPersonalKey`, `GetEnvironmentKey`, `ListEnvironmentKeysByOwner` helpers.
- **`internal/db/active_keys.go`** (Phase 2 → Phase 02.2) — `ListActiveACHKeyIDs` is the orphan-loop primitive; the existing doc comment correctly notes "Phase 3 will add a litellm_key_id column … and ListActiveACHKeyIDs will be replaced by a more precise helper". Phase 3 adds `ListActiveACHKeyTokens` (returns `litellm_token` values where non-null + status='active') as that more-precise helper. The orphan loop in `internal/orphan/` is swapped to call the new helper.
- **`internal/credhash/credhash.go`** (Phase 1 — `Hash`, `Equal`, constant-time compare, pepper-required guarantee `ErrEmptyPepper`) — Phase 3 uses it on every key INSERT and on every resolution path before the DB lookup.
- **`internal/credhash/pepperenv/`** (Phase 1) — `LoadPepper()` from env; Platform API uses identical idiom (already process-start required per D-09 of Phase 1 CONTEXT).
- **`internal/litellm/`** (Phase 2 lift, Phase 02.2 G1 fix) — `RESTClient` with `DeleteAccessGroup`, `DeleteTag`, `ListModels`, `ListMCPServers`, `ListA2AAgents`, `ListUserKeys`, `RevokeKey`, `Team*` methods. Phase 3 ADDS `UserNew`, `UserInfoByEmail`, `TeamMemberAdd`, `KeyGenerate` (D-25). `NoopClient` and the compile-time interface canary catch any drift.
- **`internal/audit/handler.go`** (Phase 2) — `NewLogger(io.Writer)` returns `*slog.Logger` with `audit:true`. Phase 3 reuses verbatim; adds `internal/audit/events.go` with action/outcome constants (D-18) and an `EmitAudit(ctx, Event)` helper (D-19) on top.
- **`internal/config/`** (Phase 1) — `EnvOr` + small validation helpers; Phase 3 adds parsing for `ACH_DEX_*`, `ACH_REDIS_*`, `ACH_ADMIN_ALLOWLIST_PATH`, `ACH_PLATFORM_API_BIND_ADDRESS`.
- **`db/migrations/000001_init.up.sql`, `000002_phase2.up.sql`, `000003_litellm_token.up.sql`** — All four target tables already exist with the right shapes; Phase 3 does NOT add a fourth migration.
- **`config/rbac/platformapi_role.yaml`** (Phase 1) — already grants `get/list/watch` on all six ACH CRDs and `patch` on four external-ref kinds (MULTI-02 carve-out). Phase 3 amends to add `get/list/watch` on `secrets` in the deployment namespace (Dex client secret rotation observation; mirrors Phase 2 D-11). RBAC stays namespace-scoped (`Role` + `RoleBinding`, never `ClusterRole` — MULTI-01).
- **`docker-compose.yml`** — Phase 1 `postgres` + `redis` services already in place; Phase 02.2 added `litellm` + `litellm-db` under `profiles: [litellm]`. Phase 3 adds a `dex` service (also profile-gated — `profiles: [dex]`) for the local integration-test path. The compose-merge convention from Phase 02.2 (root compose + profile gates; no separate spike yaml) is preserved.

### Established Patterns (carry-forward)

- **Kubebuilder v4 + multigroup layout** mirroring `../ach_litellm/`.
- **Containerized toolchain**: every `go`/`kubebuilder`/`make` invocation goes through `./scripts/dev.sh`. Plans' `<action>` and `<verify>` blocks are read as "run via `./scripts/dev.sh`" by executor agents.
- **Logging**: `log/slog` JSON in prod / text in dev for application logs; audit via the Phase 2 `audit.NewLogger`. Both write to stdout; Kubernetes log collection picks up both; downstream filter on `audit:true` predicate.
- **Tests**: Ginkgo + Gomega + envtest for informer-backed tests; testcontainers-go for Postgres + Redis integration; `httptest.Server` for Dex + LiteLLM client unit tests; the existing `test/e2e/` patterns extend with a Phase 3 invariants suite.
- **Config plumbing**: `os.Getenv` + `internal/config.MustEnv` / `MustEnvDurationAtLeast` (Phase 2 added the latter for `ACH_ORPHAN_CLEANUP_INTERVAL`). New Phase 3 knobs: `ACH_BASE_URL` (required; HTTPS-only refuse-to-start), `ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET`, `ACH_DEX_REDIRECT_URL`, `ACH_REDIS_ADDR`, `ACH_REDIS_PASSWORD`, `ACH_REDIS_TLS`, `ACH_REDIS_DB`, `ACH_ADMIN_ALLOWLIST_PATH`, `ACH_PLATFORM_API_BIND_ADDRESS`. `ACH_LITELLM_*` (Phase 2) and `ACH_CREDENTIAL_HASH_PEPPER` (Phase 1) re-used as-is.
- **Counter-hook stubs inline; full Prometheus emitter Phase 5** — same convention Phase 2 established.

### Integration Points (forward + lateral)

- **Phase 1 → Phase 3:** RBAC carve-out (MULTI-02) already grants `patch`; pgxpool already wired; credhash already shipped; platform-api stub binary already builds.
- **Phase 2 → Phase 3:** Audit logger reused; `litellm_user_id` column written for the first time; force-refresh annotation now patched by Platform API (Phase 2 reconciler reads + clears it); `AccessGroupSynced` condition that Phase 2 Environment reconcile sets is what Phase 3 §8.2 step 3 waits on.
- **Phase 02.2 → Phase 3:** `litellm_token` column written for the first time (closes Phase 02.2 D-02 prerequisite — orphan loop becomes precise from the next tick onward).
- **Phase 3 → Phase 4:** Forwarder consumes `internal/keystore` (D-08) and the `PkCheckAndExtend`/`EkResolve` SQL helpers (D-10, D-11); revocation flows of Phase 3 must complete before Phase 4's resolver tests can prove revocation propagation.
- **Phase 3 → Phase 5:** Content Service consumes `internal/keystore` for both `pk_` and `ek_` resolution; Hydrate's `downloadUrl` construction defines the URL shape Phase 5's Content Service binds (`/content/<kind>/<name>`).
- **Phase 3 → Phase 6:** CLI `ach login`, `ach env-keys *`, `ach env *`, `ach admin *` commands all bind to Phase 3 endpoint shapes — request/response schemas are the contract.

</code_context>

<specifics>
## Specific Ideas

- **`pk_` and `ek_` plaintext format frozen:** `pk_<base32-no-pad-26-chars>` / `ek_<base32-no-pad-26-chars>` — 26 base32 chars from 16 random bytes via `crypto/rand` is ~130 bits of entropy. The `_` after `pk`/`ek` is the literal prefix delimiter (matches Hub spec consistently); the suffix uses RFC 4648 base32 (unpadded). LiteLLM's `KeyGenerate` accepts caller-supplied `key` per the API; ACH owns the namespace.
- **`pkid_` / `ekid_`** (the OPAQUE key_id, DISTINCT from the bearer plaintext) follows the same generator: `pkid_<ulid>` / `ekid_<ulid>` — base32-encoded ULID. ULID provides time-ordered insertion (helpful for DB index locality + audit log readability). 26 chars after the prefix; the `CHECK (key_id LIKE 'pkid_%')` constraint from `000001_init.up.sql` accepts this verbatim.
- **`request_id` format:** `req_<ulid>` — same ULID source; chosen for natural sort order + grep-friendliness in JSON audit/log streams.
- **`actor` composition:** `<namespace>/<sso-email>` per Hub §18.3 — namespace from `POD_NAMESPACE` env var (downward API), email from the resolved key's `owner_email`. Composed at emission time, NEVER persisted (per DB-05 invariant from Phase 1).
- **Allowlist file format mirrors `/etc/hosts` conventions** — blank lines, `#` comments, whitespace-trim, case-sensitive verbatim. Single recognizable shape across platform-API binaries; the deployer's ConfigMap example in the Phase 7 Helm chart will document this verbatim.
- **Audit-handler reuse is the load-bearing detail** — Phase 2 left a usable `audit.NewLogger(io.Writer) *slog.Logger`. Phase 3 does NOT re-write the handler; it adds the action/outcome constants and the `EmitAudit(ctx, Event)` helper as a thin wrapper. Anything richer (file/PVC routing, structured query API, replay) is v1beta1.
- **Dex `default` Team is the deployer's responsibility**, not ACH's. Hub §17 / API-02 are explicit: missing → `500 default_team_missing` with audit `outcome=default_team_missing`. ACH does NOT lazily create the Team; that's a fail-loud signal that the LiteLLM deployment is misconfigured. Researcher/planner: do NOT add lazy-create logic.
- **`POST /platform/auth/sso/callback` is the ONLY endpoint that returns plaintext other than `POST /platform/env-keys`** — both follow the "plaintext exactly once" invariant; no other code path emits plaintext anywhere. Static-analysis hook (or a grep gate in CI): scan handler output for `pk_*`/`ek_*` patterns and fail the build if found outside these two handlers.

</specifics>

<deferred>
## Deferred Ideas

Discussion stayed within Phase 3 scope. Items intentionally out of Phase 3 (already mapped to later phases, out of v1alpha1, or deferred for ergonomic reasons):

- **Hosted login web page** — v1alpha1 has only JSON endpoints. The CLI (Phase 6) drives Dex login via a local browser + loopback. A first-party hosted login UI is v1beta1.
- **Cookie-backed user session for repeat calls** — D-04 is deliberately stateless (no session). Sessions would require a server-side store (Redis) just for SSO, which adds a second cache surface without clear v1alpha1 benefit. Revisit if the CLI grows multi-step interactive flows.
- **K8s ConfigMap watch for admin allowlist hot-reload** — explicitly excluded by Hub AC18 / AC24 (restart required). Adding a watch would require restart-equivalent semantics anyway (config drift between replicas without rolling restart). Defer.
- **Per-action `outcome` enum extension beyond §18.2** — D-18 ships only what §18.2 catalogs. New outcomes added in later phases (Phase 5 metric outcomes, Phase 4 forwarder outcomes) extend the enum additively per Hub §18.5.
- **Prometheus `/metrics` endpoint** — Phase 5 (OBS-03..06). Phase 3 only emits counter hooks.
- **`ach_personal_key_extend_total` audit event** — per OBS-01 ("Sliding-window pk_ extension is NOT its own event"). Permanently out of scope, not just deferred.
- **JWT signing + JWKS publication** — Phase 4 (FWD-07..09).
- **`BackendIdentityPolicy.status` writes** — Phase 4 (OP-14, OP-16).
- **`/v1/hydrate` legacy path** — explicitly forbidden by API-01.
- **`max_budget` setting on first SSO** — explicitly forbidden by KEY-10 / §6.3. Permanently out.
- **`Range`/`If-None-Match` on content endpoints** — explicitly forbidden by CS-08; Phase 5 will refuse.
- **HA Platform API (multi-replica leader election)** — Platform API IS stateless (Deployment ≥1 replica per Hub §5.1), so this is more "horizontal scale-out" than HA. The informer-only `manager.Manager` (D-20) is compatible with multi-replica deployments out of the box; just don't enable leader election. Defer formal multi-replica testing to a later milestone unless someone reports a specific issue.
- **OS keyring integration for the bearer plaintext returned by `POST /platform/env-keys`** — server returns plaintext exactly once; the caller's storage is the caller's choice. CLI Phase 6 will write to `~/.config/ach/config.yaml` mode `0600` per CLI-02. v1alpha1 does not provide OS keyring helpers.
- **Audit log to a dedicated file / PVC** — same deferred reasoning as Phase 2 D-17.
- **`SIGHUP` allowlist reload** — out of scope; deployer restarts the Pod.
- **Rate limiting on `POST /platform/auth/login`** — Hub spec is silent; Dex itself rate-limits the upstream IdP. Defer to deployment-layer NetworkPolicy / Service mesh.
- **`whoami` dedicated endpoint** — CLI uses `GET /platform/environments?limit=1` for `pk_` and `POST /platform/hydrate {}` for `ek_` (asymmetric verification per CLI-11). v1beta1 backlog per CLI spec §13.
- **Dual-key acceptance window for the Forwarder↔LiteLLM shared key** — Hub §9 + §20 v1beta1 backlog. Phase 3 does NOT touch the Forwarder shared key.

### Engineer-pending verification debt from Phase 02.2

- **`scripts/uat-g1.sh` against live LiteLLM v1.83.10** — Phase 02.2 plan complete; engineer must run `docker-compose --profile litellm up -d && ./scripts/uat-g1.sh` once locally and confirm `uat-g1: PASS — revocation audit event observed`. This closes OP-13's audit-event verifiability gap end-to-end. NOT a Phase 3 task; tracked in `.planning/STATE.md` and `.planning/phases/02.2-*/02.2-VERIFICATION.md`. Phase 3 plan-phase should not block on it, but the result feeds the orphan-cleanup audit shape Phase 3 reuses verbatim.

</deferred>

---

*Phase: 3-hub-identity-platform-api*
*Context gathered: 2026-05-19 (auto mode — single-pass autonomous resolution)*

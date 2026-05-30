# Phase 4: Hub Forwarder & JWT Trust Path - Context

**Gathered:** 2026-05-26
**Status:** Ready for planning
**Mode:** `/gsd:discuss-phase 4` (interactive — 4 single-question turns, 4 areas)

<domain>
## Phase Boundary

Phase 4 turns the Phase 1 `cmd/ach/cmd/forwarder.go` stub (currently `/healthz`-only on `:8081`) into the runtime forwarding boundary for ACH. It implements 13 REQ-IDs across two families:

- **FWD-01..FWD-11** — runtime forwarding on `/v1`, `/gemini`, `/mcp/{name}`, `/a2a/{name}`:
  - Resolve `x-ach-key` via `internal/keystore` (Redis→Postgres single-flight, reusing Phase 3 D-08 + D-10 `PkCheckAndExtend` + D-11 `EkResolve`).
  - §5.1 step-4 pre-check on `/mcp/{name}` + `/a2a/{name}`:
    - `ek_`: name MUST appear in bound `Environment.spec.runtime.mcpServers[]` or `.a2aAgents[]` (informer cache lookup of Environment CR — local, no LiteLLM round-trip).
    - `pk_`: caller's LiteLLM Teams MUST intersect non-empty with the union of `Environment.spec.authorizedTeams[]` across Environments whose `spec.runtime.{mcpServers|a2aAgents}` contain `name`. LiteLLM Teams come from `keystore.TeamsResolver` (Redis cache; LiteLLM `UserInfoByEmail` fallback on miss).
    - Fail modes: `403 unauthorized_resource` (ek_ name not in bound Env), `403 unauthorized_team` (pk_ empty intersection), `503 litellm_unreachable` (LiteLLM down — fail-closed per SC#2).
  - Header strip+rewrite contract (§5.1) on every route — single-pass through `httputil.ReverseProxy.Director`.
  - Single upstream model: forwarder forwards to `ACH_LITELLM_BASE_URL` for ALL routes. ACH does not know real MCP/A2A backend URLs — LiteLLM owns second-hop dispatch and returns its own `404` on unknown `{name}`. (Locked via discussion Q1.)
  - ACH JWT signing (EdDSA / Ed25519, 120s exp, no `jti`) — minted ONLY on `/mcp` + `/a2a` AND only when a matching `BackendIdentityPolicy` resolves with `forwardIdentityJWT: true`.
  - `BackendIdentityPolicy` lookup at request time: forwarder-side controller-runtime `IndexField` on `(spec.target.kind, spec.target.name)`; matches sorted by `metadata.name` ASC; **alphabetically-LAST** wins (`Items[len-1]`); `forwardIdentityJWT` honored from that winner. Zero matches → no JWT attached, request forwarded with stripped `Authorization`. (Locked via TODO.md §6 + Q3.)
  - JWKS publication at `/.well-known/jwks.json` (anonymous, `Cache-Control: public, max-age=3600`, `application/jwk-set+json`) — publishes BOTH `current` + `next` JWKs whenever both slots present in the `ach-jwt-signing-keys` Secret.
  - Manual rotation procedure: regenerate `next` → wait ≥24h (exceeds backend JWKS cache TTL of 3600s by ≥6.6×) → promote `next`→`current`. Document as a runbook in this phase.
  - Forwarder refuses to start when `ACH_BASE_URL` doesn't begin with `https://` (per SC#5), when `ach-jwt-signing-keys` Secret missing, or when neither slot has a valid Ed25519 seed.

- **OP-14, OP-16** — Operator status authority on `BackendIdentityPolicy`:
  - Per Hub §5.1 + spec, the Operator is the **sole writer** of `BackendIdentityPolicy.status`. The Forwarder reads `spec` only via informer (no `status` reads at request time), so runtime authority is decoupled from status-write latency.
  - **NO `Synced=DuplicateTarget` reconciler in Phase 4** (or any future phase) per TODO.md §6 design decision (2026-05-26). Multiple BIPs targeting the same `(target.kind, target.name)` coexist without status churn; the Forwarder resolves the alphabetical-LAST winner at READ time. The Phase 1 BIP-reconciler stub at `internal/controller/ach/backendidentitypolicy_controller.go` already does nothing on duplicates — its doc comment ("Phase 4 may need to invalidate Forwarder cache… Synced=DuplicateTarget is Phase 4's owner; CRD-07 doesn't admit an 'Initializing' reason for this kind") is **stale and must be scrubbed** in this phase.

Phase 4 explicitly **excludes**:

- Content Service streaming, `sendfile(2)`, scope-aware authorization at the `/content/` surface (Phase 5 — CS-01..CS-11). Phase 4's `keystore.TeamsResolver` (D-17) IS the same primitive Phase 5 will reuse for `pk_` Team-intersection on `/content/` requests.
- Prometheus `/metrics` endpoints + ServiceMonitor manifests (Phase 5 — OBS-03..OBS-06). Phase 4 emits counter-hook stubs inline (same Phase 3 convention).
- CLI work (Phase 6).
- Any audit events on the runtime forwarding path. Per OBS-01: "runtime forwarding is NOT audited; LiteLLM owns that channel." Phase 4 emits metrics only.
- BIP `Synced=DuplicateTarget` status writes — **permanently removed**, not deferred (TODO.md §6).
- Server-side `pk_` runtime-forbid toggle — permanent design decision (`[[feedback_ach_pk_runtime_first_class]]`).
- JWT verification on the forwarder side. Backends pull JWKS and verify themselves; the forwarder only signs.
- Dual-key acceptance window for the Forwarder↔LiteLLM shared key (`ACH_LITELLM_SHARED_KEY`) — Hub §9 + §20 v1beta1 backlog. Single-key rotation requires a planned maintenance window.
- JWT `jti` claim or replay-window restatement — accepted as part of v1alpha1 threat model (Hub §9.1, §20).
- HTTP escape hatch — refuse-to-start on non-HTTPS `ACH_BASE_URL` (Hub §9.1).
- HA forwarder (multi-replica with shared state) — Forwarder IS stateless and multi-replica-friendly out of the box; formal multi-replica testing deferred unless a specific issue surfaces.
- Automated rotation primitives (cron + double-flip) — v1beta1; v1alpha1 is `kubectl patch` + wait.

</domain>

<decisions>
## Implementation Decisions

### HTTP server + middleware stack

- **D-01:** **chi.Mux carry-forward from Phase 3 D-01.** Same router, same idioms. New `internal/forwarder/server.go` builds the mux; `cmd/ach/cmd/forwarder.go`'s `RunE` wires it. No new HTTP-framework dependency.
- **D-02:** **Middleware chain (outer → inner)** mirrors Phase 3 D-02 with route-specific bypass:
  `RequestID` (generates `req_<ulid>` via `oklog/ulid/v2`, sets `X-Request-Id` response header) → `RecoverPanic` (slog.Error; emits `forwarder_requests_total{outcome="internal_error"}`) → `AccessLog` (method, path, status, latency_ms; redacts `x-ach-key` to `<prefix>_***` per FWD-11) → `Authn` (per-route — bypass `/.well-known/jwks.json`, `/healthz`, `/livez`, `/readyz`). The success path is **pass-through** for body + headers — no `ContentTypeJSON` middleware on the proxy paths (LiteLLM sets its own content type); JSON envelope is applied ONLY in ACH-originated error handlers.
- **D-03:** **Bind topology — two ports.** Traffic (forwarding + JWKS): `ACH_FORWARDER_BIND_ADDRESS` (default `:8080`). Health probes (`/healthz`, `/livez`, `/readyz`): `FORWARDER_HEALTH_BIND_ADDRESS` (default `:8081` — Phase 1 stub default carried forward). Two `http.Server` instances; the traffic mux includes `/.well-known/jwks.json`. Container readiness probe targets `:8081/readyz`. `/readyz` gates on `mgr.WaitForCacheSync` + `current` slot of the JWT Secret loaded.
- **D-04:** **`http.Server` config** retains Phase 3 D-03 timeouts: `ReadHeaderTimeout: 5s` (gosec G112), `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s`, `MaxHeaderBytes: 1 MiB`. Graceful shutdown via `http.Server.Shutdown(ctx, 10s)`. NOTE: For streaming endpoints (`/v1`/`/gemini` chat completions can be SSE), `WriteTimeout: 30s` is too tight — override to `0` (no timeout) on the proxy handler using a custom-wrapped `http.ResponseWriter` if needed; finalize during planning by measuring against the real LiteLLM SSE behavior. Pre-decision: ship `0` on `WriteTimeout` for the traffic listener and rely on upstream cancellation propagation via `Request.Context()`.

### Single-upstream reverse proxy

- **D-05:** **`httputil.ReverseProxy` with custom `Director` + `ModifyResponse`.** One instance per process; routes `/v1`, `/gemini`, `/mcp/{name}`, `/a2a/{name}` all share it. `Director`:
  1. Set `req.URL.Scheme` + `req.URL.Host` from parsed `ACH_LITELLM_BASE_URL`.
  2. Preserve `req.URL.Path` verbatim (`/v1/chat/completions` → `LITELLM/v1/chat/completions`, `/mcp/<name>/foo` → `LITELLM/mcp/<name>/foo`).
  3. Apply header strip+rewrite (D-06, D-07).
  4. For `/mcp/{name}` + `/a2a/{name}` paths where the BIP resolver matched a winner with `forwardIdentityJWT: true`, attach `Authorization: Bearer <ACH-JWT>` (D-13). Otherwise no `Authorization` header at all.
  5. Stamp `X-Forwarded-For` per `httputil.ReverseProxy` default behavior (preserve client IP for audit upstream).

  `ModifyResponse` is a no-op pass-through (LiteLLM's status code, headers, body all flow back verbatim).

- **D-06:** **Header strip list (blacklist mode).** Forwarder strips:
  - Client `Authorization` (regardless of scheme).
  - All `x-litellm-*` headers (any case).
  - All `x-ach-*` headers (any case) — including `x-ach-key`, `x-ach-environment`, any future `x-ach-*` we add.
  - Hop-by-hop headers per RFC 7230 §6.1 + `Connection`-named: `Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`. Plus every header named in the incoming `Connection: ...` token list.
  - Strip pass MUST be case-insensitive (`net/http.Header` is canonical-case map; iterate keys with `strings.HasPrefix(strings.ToLower(k), ...)` for the prefix matches).
  - All OTHER headers (e.g. `User-Agent`, `Accept`, `Content-Type`, `Content-Length`, `Accept-Encoding`, `X-Forwarded-*`) pass through unchanged.

- **D-07:** **Header write list (after strip).** Forwarder writes:
  - `x-litellm-api-key: <ACH_LITELLM_SHARED_KEY>` — sourced from env, identical to the Phase 2 `internal/litellm` REST client's existing config (reuse same env var name; no new knob).
  - `x-litellm-key-id: <litellm_token>` — sourced from `KeyInfo.litellm_token` (Phase 02.2 column; populated for both `pk_` and `ek_` via Phase 3 SSO/`KeyGenerate` flows).
  - On `/mcp/{name}` + `/a2a/{name}` when BIP opts in: `Authorization: Bearer <ACH-JWT>` (D-13).
  - No other ACH-specific headers written. Specifically, NO `x-ach-*` headers re-injected to the upstream; LiteLLM never sees ACH metadata at the HTTP layer.

### Forwarder informer manager

- **D-08:** **controller-runtime `manager.Manager` without controllers, without leader election.** Mirrors Phase 3 D-20 verbatim:
  ```go
  ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
      LeaderElection:         false,
      HealthProbeBindAddress: ":0", // forwarder runs its own health mux
      MetricsBindAddress:     "0",
      Cache: cache.Options{
          DefaultNamespaces: map[string]cache.Config{ns: {}},
      },
  })
  ```
  Informers registered for: `BackendIdentityPolicy`, `Environment`, `corev1.Secret` (filtered by name `ach-jwt-signing-keys`). `mgr.WaitForCacheSync()` gates `/readyz`. `mgr.GetFieldIndexer().IndexField(ctx, &BackendIdentityPolicy{}, "spec.target", ...)` registers the BIP target index (D-09).

- **D-09:** **BIP request-time lookup via `IndexField`.** Indexer key format: `"<kind>/<name>"` (e.g. `"MCPServer/github-mcp"`). Request path:
  ```go
  var list achv1alpha1.BackendIdentityPolicyList
  if err := mgr.GetClient().List(ctx, &list,
      client.MatchingFields{"spec.target": kind + "/" + name},
      client.InNamespace(ns)); err != nil { ... }
  sort.SliceStable(list.Items, func(i, j int) bool {
      return list.Items[i].Name < list.Items[j].Name
  })
  if len(list.Items) == 0 {
      return nil // no JWT
  }
  winner := list.Items[len(list.Items)-1] // alpha-LAST
  if !winner.Spec.ForwardIdentityJWT {
      return nil // explicit opt-out
  }
  return &winner
  ```
  Race-free (`IndexField` is the controller-runtime idiom), O(log N) lookup, sub-ms after cache sync.

### Ed25519 signing key Secret + JWT minting

- **D-10:** **Secret `ach-jwt-signing-keys` layout — `current` + `next` slots.** Data keys (Secret values are base64-encoded by K8s; ACH stores raw bytes in the value):
  - `current.kid` — string, e.g. `ach-jwt-<ulid>`.
  - `current.seed` — 32 raw bytes of Ed25519 seed (`crypto/ed25519.NewKeyFromSeed(seed)` reconstructs the full private key).
  - `next.kid` — string (optional; empty when no rotation pending).
  - `next.seed` — 32 raw bytes (optional).
  Forwarder loads at process start AND watches the Secret via informer; reload is atomic (swap the in-memory `signer` struct via `atomic.Pointer`). Forwarder refuses to start if `current.kid` empty, `current.seed` not exactly 32 bytes, or Secret missing.

- **D-11:** **`github.com/golang-jwt/jwt/v5` for SIGNING.** Use `jwt.SigningMethodEdDSA` with `ed25519.PrivateKey` (64-byte form = seed||public; constructed via `ed25519.NewKeyFromSeed(currentSeed)`). Inject `kid` via `token.Header["kid"] = currentKid` before `token.SignedString(privKey)`. New direct dependency (added by Phase 4); no transitive conflicts expected (jwt/v5 has zero non-stdlib deps).

- **D-12:** **JWKS endpoint: hand-rolled JSON marshal — no library.** Handler logic:
  ```go
  type jwk struct {
      Kty string `json:"kty"`
      Crv string `json:"crv"`
      Use string `json:"use,omitempty"`
      Alg string `json:"alg,omitempty"`
      Kid string `json:"kid"`
      X   string `json:"x"` // base64url(pub-32B), no padding
  }
  type jwks struct {
      Keys []jwk `json:"keys"`
  }
  ```
  Publishes BOTH `current` + `next` slots when both populated; only `current` when `next` empty. `use:"sig"` + `alg:"EdDSA"` always set. Response: `Content-Type: application/jwk-set+json`, `Cache-Control: public, max-age=3600`. Anonymous access (Authn middleware bypass per D-02).

- **D-13:** **JWT claims (locked via spec §9.1):**
  - Header: `{"alg":"EdDSA","typ":"JWT","kid":"<current.kid>"}` — always sign with `current`, never `next` (next exists for rotation OVERLAP — backends verify against both via JWKS).
  - Claims:
    - `iss` = `$ACH_BASE_URL` (verbatim — already validated https-only at startup).
    - `sub` = `<namespace>/<owner-email>` — namespace from `POD_NAMESPACE` env (downward API; Phase 3 D-22 idiom), `owner_email` from resolved `KeyInfo`.
    - `aud` = `mcp:<name>` on `/mcp/<name>`; `a2a:<name>` on `/a2a/<name>`.
    - `iat` = `time.Now().Unix()`.
    - `exp` = `iat + 120` (Hub §9.1; 120-second skew window).
    - **NO `jti`** (Hub §9.1, §20 — accepted v1alpha1 threat model).

- **D-14:** **Manual rotation runbook (documented in this phase, not coded):**
  1. Generate new keypair: `seed := make([]byte, 32); crypto/rand.Read(seed); pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)`. Pick `kid := "ach-jwt-" + ulid.New()`.
  2. `kubectl patch secret ach-jwt-signing-keys --type=json -p='[{"op":"replace","path":"/data/next.kid","value":"<b64-kid>"},{"op":"replace","path":"/data/next.seed","value":"<b64-seed>"}]'`.
  3. Forwarder reloads on Secret update via informer; JWKS endpoint now publishes both `current` + `next`.
  4. Wait ≥24h (configurable for tests; production = 24h × N_DEPLOYMENTS conservative; the JWKS `max-age=3600` means worst-case backend cache is 1h, so 24h is ≥24× the cache TTL).
  5. Promote: `kubectl patch` to set `current = next-values`, then clear `next` (or leave it for the next rotation).
  6. Forwarder reloads; JWTs now signed with the new `current.kid`. JWKS continues to publish whichever slots are populated.

  Document this verbatim in `docs/runbooks/jwt-key-rotation.md` (new file, lifted-and-adapted from Hub §9.2 + this runbook).

### §5.1 step-4 pre-check + Teams cache

- **D-15:** **Pre-check logic — `ek_` path:**
  - Resolve `KeyInfo` via `keystore` (single Redis→Postgres lookup, FWD-02 cache budget honored).
  - `ek_` is bound to exactly one Environment: read `KeyInfo.environment` → `informerCache.Get(name)` → check `spec.runtime.mcpServers[]` (on `/mcp/<X>`) or `spec.runtime.a2aAgents[]` (on `/a2a/<X>`) contains `<X>`.
  - Not present → `403 unauthorized_resource`, no JWT, no LiteLLM forward.
  - Environment terminating (`DeletionTimestamp != nil`) → `404 environment_not_found` (or `503 environment_draining` — finalize during planning by reading §6.5 drain semantics; pre-decision: `403 unauthorized_resource` to keep the error surface narrow).

- **D-16:** **Pre-check logic — `pk_` path:**
  - Resolve `KeyInfo` via `keystore` (single Redis→Postgres lookup via `PkCheckAndExtend`).
  - Resolve caller's LiteLLM Teams via `keystore.TeamsResolver` (D-17).
  - List Environments via informer where `spec.runtime.{mcpServers|a2aAgents}` contains `<X>` AND `spec.authorizedTeams[]` intersects caller's Teams.
  - Empty result → `403 unauthorized_team`, no JWT, no LiteLLM forward.
  - LiteLLM unreachable during Teams resolution → `503 litellm_unreachable`, fail-closed.

  **Planner clarification needed:** Spec §5.1 step-4 for `pk_` on `/mcp/<X>` is "intersect caller's Teams with the union of Environment.spec.authorizedTeams across Environments hosting `<X>`" — that's my reading of SC#2 ("a pk_ to the same endpoint queries LiteLLM Team memberships and grants/denies accordingly"). The hub spec §5.1 + §9.3 should be re-read by the planner to confirm. If a single specific Environment is implied (e.g. via an `x-ach-environment` header on `pk_` /mcp), this changes the lookup.

- **D-17:** **`internal/keystore.TeamsResolver` — Redis-shared, separate keyspace.** New type mirroring D-08 `KeyResolver`:
  ```go
  type TeamsResolver interface {
      Resolve(ctx context.Context, ownerEmail string) ([]string, error) // returns team_id list
  }
  type redisCachedTeamsResolver struct {
      base   TeamsResolver // wraps the LiteLLM-backed resolver
      rdb    *redis.Client
      sf     *singleflight.Group
  }
  ```
  - Key: `ach:teams:<owner_email>` (Phase 3 D-07 uses `ach:key:<credential_hash>`; this is a parallel keyspace).
  - Value: JSON `[]string` of LiteLLM team IDs.
  - TTL: 60s (matches Hub §5.1 cache budget).
  - Miss path: single-flight (`golang.org/x/sync/singleflight`) → `litellm.UserInfoByEmail(owner_email)` → `UserInfo.Teams` → populate Redis → return.
  - LiteLLM unreachable → propagate error; caller returns `503 litellm_unreachable`.
  - Reuses Phase 3 D-09 `go-redis/redis/v9` client (no second connection pool).
  - Phase 5 Content Service reuses this resolver verbatim for its own `pk_` Team-intersection check (CS-04).

  **Planner note:** Decide cache-key shape between `owner_email` vs `litellm_user_id` during planning. `owner_email` is what `UserInfoByEmail` accepts as input (saves a lookup) and matches `KeyInfo.owner_email`. `litellm_user_id` is more stable if LiteLLM later allows email change. Pre-decision: `owner_email` for v1alpha1 consistency.

### Counter hooks (Phase 3 carry-forward — full /metrics in Phase 5)

- **D-18:** **Counter-hook stubs inline** in middleware + handlers:
  - `forwarder_requests_total{route, key_type, outcome}` — route ∈ `/v1, /gemini, /mcp, /a2a`; key_type ∈ `pk, ek, none`; outcome ∈ `forwarded, unauthorized_resource, unauthorized_team, expired_or_revoked, litellm_unreachable, internal_error`.
  - `forwarder_jwt_signed_total{kind}` — kind ∈ `MCPServer, A2AAgent`.
  - `forwarder_jwt_suppressed_total{kind, reason}` — reason ∈ `no_policy, policy_opt_out, signing_failure`.
  - `litellm_unreachable_total{caller="forwarder"}` — single counter spanning all callers per Hub §18.5 normative label-value enum.
  Phase 5 owns the `prometheus.NewCounter*` registration and `/metrics` endpoint wiring (OBS-03..06).

### Operator-side BIP reconciler scope

- **D-19:** **NO DuplicateTarget reconciler in Phase 4 or any future phase.** Per TODO.md §6 (2026-05-26 design decision; ref `[[feedback_bip_no_shadow_logic.md]]`):
  - Operator stays dumb on BIP duplicates.
  - No `Synced=DuplicateTarget` status reason emitted.
  - No shadow flip.
  - Multiple BIPs targeting same `(kind, name)` coexist.
  - Operators flip precedence by renaming CRs (e.g. `zz-` suffix → alpha-LAST winner switches).
  - The Phase 1 BIP-reconciler stub at `internal/controller/ach/backendidentitypolicy_controller.go` ALREADY does nothing on duplicates. Phase 4 only **scrubs the stale doc comment** (lines 17–25 + lines 80–82, which forecast Phase-4 DuplicateTarget logic). Reconciler body unchanged.

### Code structure

- **D-20:** **`internal/forwarder/` package layout:**
  - `internal/forwarder/server.go` — `New(deps Deps) http.Handler` returns the `chi.Mux` with middleware chain (D-02) + routes (proxy on /v1, /gemini, /mcp/{name}, /a2a/{name}; JWKS on /.well-known/jwks.json).
  - `internal/forwarder/proxy/proxy.go` — `httputil.ReverseProxy` + `Director` (D-05).
  - `internal/forwarder/headers/strip.go` — strip+rewrite logic (D-06, D-07) with table-driven tests.
  - `internal/forwarder/jwt/signer.go` — `Signer` interface, `Ed25519Signer`, `signer.Sign(ctx, claims)` returns compact JWS; constructed from the Secret loader.
  - `internal/forwarder/jwt/secret.go` — `ach-jwt-signing-keys` loader; informer-watched; atomic `Pointer[signer]` swap on update.
  - `internal/forwarder/jwt/jwks.go` — JWKS HTTP handler (D-12).
  - `internal/forwarder/bip/index.go` — `IndexField` registration + lookup helper (D-09).
  - `internal/forwarder/precheck/check.go` — `ek_` + `pk_` pre-check helpers (D-15, D-16).
  - `internal/keystore/teamsresolver.go` — new `TeamsResolver` + `redisCachedTeamsResolver` (D-17).
  - `cmd/ach/cmd/forwarder.go` — grow from Phase 1 stub: wire `manager.Manager`, `pgxpool`, `go-redis`, `litellm.RESTClient`, `keystore.KeyResolver` + `keystore.TeamsResolver`, JWT Secret loader, then `mgr.Add(serverRunnable{trafficAddr, healthAddr, handler})` and `mgr.Start(signalContext)`.

### Error envelope

- **D-21:** **JSON envelope on ACH-originated errors** matching Phase 3 D-19:
  ```json
  {"error":{"code":"<outcome>","message":"..."},"request_id":"req_..."}
  ```
  Used on: 401 (`expired_or_revoked`, `invalid_key_type`), 403 (`unauthorized_resource`, `unauthorized_team`), 503 (`litellm_unreachable`), 500 (`internal_error`), 426 (`https_required` if forwarder ever sees non-https — though the listener IS https-terminated upstream so this is theoretical).
  **Pass-through on LiteLLM responses** — forwarder never inspects or rewrites the upstream body; whatever LiteLLM returns (success or error) flows back verbatim (subject to D-05 ModifyResponse no-op).

### RBAC

- **D-22:** **New `config/rbac/forwarder_role.yaml` Role + RoleBinding** (mirrors Phase 3 `platformapi_role.yaml`):
  - `get/list/watch` on `backendidentitypolicies.ach.ackstorm.ai` + `environments.ach.ackstorm.ai`.
  - `get/list/watch` on `secrets` with `resourceNames: ["ach-jwt-signing-keys"]` (least-privilege; SC#4 requires "only the Forwarder ServiceAccount can read").
  - Namespace-scoped (`Role` + `RoleBinding`, never `ClusterRole` per MULTI-01).
  - NO write verbs on any ACH CRD (per Hub §5.1: Operator is sole writer, Platform API has the §15.5 force-refresh patch carve-out; Forwarder is read-only).

### Tests

- **D-23:** **Test plan (per Phase 1 + Phase 3 patterns):**
  - **Unit:** header strip+rewrite (table-driven, ~30 cases covering case-insensitive matches, multi-value Connection tokens, hop-by-hop edge cases), JWT mint (canonical fixtures verifying header `kid`, claims, `EdDSA` sig), JWKS marshal (snapshot test with deterministic key fixtures), BIP alpha-LAST resolution (table-driven with 0/1/2/3/N policies).
  - **Envtest:** BIP `IndexField` registration + lookup; Secret informer reload triggering signer atomic swap; Environment informer cache hit for `ek_` pre-check.
  - **Integration:** `testcontainers-go` Postgres + Redis; `keystore.TeamsResolver` end-to-end against a `httptest.Server` mocking LiteLLM `/user/info`.
  - **Proxy integration:** `httptest.Server` mocking LiteLLM upstream; verify header strip+rewrite via `Director`; verify body pass-through; verify SSE (chunked) preserves streaming.
  - **E2E:** existing `test/e2e/` Ginkgo suite extended with Phase 4 invariants — `kind` cluster + real Helm chart + real Dex + LiteLLM + Postgres + Valkey + a mock MCP backend; exercises SCs #1–#5 end-to-end. Driven by `make e2e-full` per CLAUDE.md "E2E debug loop".

### Claude's Discretion

- **`ACH_LITELLM_BASE_URL` config** — new required env var (forwarder refuse-to-start if missing or doesn't begin with `http://` or `https://`; do NOT enforce `https://` on this one — LiteLLM may legitimately be reached via cluster-local HTTP). Parsed via `url.Parse`; stored as `*url.URL`.
- **`ACH_LITELLM_SHARED_KEY` reuse** — Phase 2 `internal/litellm` REST client uses this env var. Forwarder reads the SAME env to populate `x-litellm-api-key`. No new knob.
- **`POD_NAMESPACE` env via downward API** — Helm chart Deployment template MUST inject:
  ```yaml
  env:
    - name: POD_NAMESPACE
      valueFrom: { fieldRef: { fieldPath: metadata.namespace } }
  ```
  Phase 3 already uses this idiom; Phase 4 carries forward.
- **JWT signing — atomic pointer swap on Secret reload** — `atomic.Pointer[Signer]` avoids RWMutex on the request hot path. `Pointer.Load()` is sub-ns.
- **JWKS endpoint anonymous** — no Authn middleware in front; Helm `NetworkPolicy` should allow ingress from configured backend namespaces (deployer concern; we document but don't enforce).
- **kid format:** `ach-jwt-<26-char-base32-ulid>` — readable, grep-friendly, time-ordered, ≤41 chars total. Matches Phase 3 D-22 ULID idiom for `req_*` / `pkid_*` / `ekid_*`.
- **Counter-hook package** — `internal/forwarder/metrics/counters.go` declares the no-op stubs as `func incRequests(route, keyType, outcome string)` etc. Phase 5 replaces the stub bodies with real `prometheus.CounterVec.WithLabelValues(...).Inc()` calls.
- **Per-route handler split:** chi routes register 4 separate `http.HandlerFunc` wrappers (one per route family). Each wrapper does: (a) parse `{name}` (only on /mcp /a2a), (b) call `precheck.CheckEk`/`CheckPk` based on key_type, (c) call `bip.ResolveWinner` (only on /mcp /a2a), (d) attach JWT if winner present + opted in, (e) hand off to the shared `httputil.ReverseProxy`.
- **Health probe semantics:**
  - `/livez` — always 200 (process alive).
  - `/readyz` — 200 iff `mgr.GetCache().WaitForCacheSync(ctx)` returned true AND `secretLoader.Loaded()` returned true (current slot ≥1 valid Ed25519 seed). 503 otherwise.
  - `/healthz` — alias for `/livez` (legacy Phase 1 stub idiom).
- **No CORS on /.well-known/jwks.json** — backends fetch server-to-server; browsers don't need it. If a future v1beta1 lets browsers verify, revisit.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### ACH Hub Spec (source of truth)

- `ach_hub_spec_v20260515_FINALv4.md` §5.1 — Per-component RBAC table + Forwarder per-route flow (header strip+rewrite contract, step-4 pre-check, resolver short-circuit ordering, cache budget ≤60s).
- `ach_hub_spec_v20260515_FINALv4.md` §5.2 — Informer-cache discipline; readiness gate on cache sync; secret informer.
- `ach_hub_spec_v20260515_FINALv4.md` §6.5 — Environment drain order (read-only by Forwarder; informs `ek_` pre-check on terminating Environment).
- `ach_hub_spec_v20260515_FINALv4.md` §7.1 — `pk_` atomic check-and-extend SQL CTE (Phase 3 D-10 implements; Phase 4 calls).
- `ach_hub_spec_v20260515_FINALv4.md` §8.1 — `ek_` binding semantics; `last_used_at` debounce; NOT live-reauthorized (Phase 3 D-11 implements; Phase 4 calls).
- `ach_hub_spec_v20260515_FINALv4.md` §8.6 — Budget attribution asymmetry; `pk_` runtime is permanent first-class (no toggle).
- `ach_hub_spec_v20260515_FINALv4.md` §9 — Forwarder↔LiteLLM shared key; single-key rotation (dual-key window is §20 v1beta1).
- `ach_hub_spec_v20260515_FINALv4.md` §9.1 — JWT shape (EdDSA, kid, typ:JWT), claims (iss/sub/aud/exp=iat+120s, no jti), key-rotation overlap.
- `ach_hub_spec_v20260515_FINALv4.md` §9.2 — JWKS publication (path, Cache-Control, JWK shape, OKP/Ed25519 only, optional use:sig + alg:EdDSA, single Secret RBAC-scoped to Forwarder SA, manual rotation publish-overlap-revoke).
- `ach_hub_spec_v20260515_FINALv4.md` §9.3 — `BackendIdentityPolicy` semantics; `forwardIdentityJWT` opt-in; target shape `{kind, name}`. **Note:** stale language about `Synced=DuplicateTarget` to be scrubbed per TODO.md §6 cleanup.
- `ach_hub_spec_v20260515_FINALv4.md` §17 — LiteLLM decoupling; ACH talks REST only; never reads `litellm.ackstorm.ai/*` CRDs at runtime.
- `ach_hub_spec_v20260515_FINALv4.md` §18.3 — Multi-tenancy; namespace prefix in `actor`/`sub` claim; deployment isolation.
- `ach_hub_spec_v20260515_FINALv4.md` §18.5 — Forwarder metric labels (normative enum); `litellm_unreachable_total{caller}` single counter spanning callers.
- `ach_hub_spec_v20260515_FINALv4.md` §20 — Backlog (dual-key window, jti, HA, automated rotation — all OUT of Phase 4).
- `ach_cli_spec_v20260515_FINALv4.md` §6.6 — CLI emits `pk_` runtime warning; informs why Forwarder needs no warning-injection of its own.

### Planning Artifacts

- `.planning/PROJECT.md` — Hub-side stack (Go, chi, pgx, go-redis, crypto/ed25519); LiteLLM coupling; `pk_` permanent first-class; HTTPS-only ingress; namespace-scoped deployments.
- `.planning/REQUIREMENTS.md` — Phase 4 maps to FWD-01..FWD-11 + OP-14 + OP-16 (13 REQ-IDs).
- `.planning/ROADMAP.md` Phase 4 entry — Goal, depends-on (Phase 1 + Phase 3), 5 SCs covering header rewrite/JWT signing/BIP/JWKS/HTTPS-refuse-to-start. **Note:** SC#3 contains stale "losers report DuplicateTarget" text; planner must reconcile against TODO.md §6 (operator emits no such status).
- `.planning/STATE.md` — Position pre-Phase-4 (Phase 3 complete, engineer-pending UAT-Phase3).
- `.planning/phases/01-foundation-crds-db-schema-operator-skeleton-multi-tenancy/01-CONTEXT.md` — Phase 1 carry-forward: kubebuilder v4 layout, `internal/db` pgxpool wrapper, `internal/credhash` HMAC, RBAC scaffolding, MULTI-01..04, `cmd/ach/cmd/forwarder.go` Phase 1 stub.
- `.planning/phases/03-hub-identity-platform-api/03-CONTEXT.md` — Phase 3 carry-forward: chi router (D-01), middleware chain (D-02), keystore (D-08), `PkCheckAndExtend` (D-10), `EkResolve` (D-11), manager.Manager idiom (D-20), LiteLLM client extensions including `UserInfoByEmail` returning `UserInfo.Teams[]` (D-25), audit handler reuse, counter-hook stub pattern.
- `TODO.md` §6 — **Canonical BIP design decision (2026-05-26):** No Operator DuplicateTarget reconciler, alphabetically-LAST winner resolved by Forwarder at read time, no `Synced` status churn, no shadow flip. Operators rename CRs (`zz-` suffix) to flip precedence. Memory ref `[[feedback_bip_no_shadow_logic.md]]`.

### Sister Project + Predecessor

- `../ach-old/cmd/forwarder/main.go` — Domain-port source per TODO.md §2 order step 9 ("`cmd/ach/cmd/forwarder.go` — MCP/A2A forwarding"). Lift the forwarding logic; adapt to single-binary cobra `RunE`; skip the ach-old BIP DuplicateTarget reconciler per TODO §6.
- `../ach-old/internal/forwarder/` — Domain logic to lift (header strip, JWT signing, JWKS handler, BIP indexer, pre-check). **MUST verify each lift against TODO §6** — any DuplicateTarget code is dropped.
- `../ach_litellm/internal/litellm/` — Sister project for REST client idioms (Phase 2 + Phase 3 lift target; Phase 4 doesn't extend the LiteLLM client beyond `UserInfoByEmail` already shipped Phase 3).

### External Libraries (new dependencies for Phase 4)

- `github.com/golang-jwt/jwt/v5` — Ed25519 JWT signing (D-11). New direct dep.
- (existing) `crypto/ed25519` — stdlib; key generation + raw sign helpers if jwt/v5 needs supplementing.
- (existing) `crypto/rand` — Ed25519 seed generation for manual rotation.
- (existing) `github.com/redis/go-redis/v9` — TeamsResolver cache (D-17); reuses Phase 3 D-09 client.
- (existing) `golang.org/x/sync/singleflight` — TeamsResolver miss dedup; reuses Phase 3 D-07 idiom.
- (existing) `github.com/go-chi/chi/v5` — Router carry-forward (D-01).
- (existing) `sigs.k8s.io/controller-runtime` — Forwarder informer-only manager + IndexField (D-08, D-09).
- (existing) `github.com/oklog/ulid/v2` — `req_<ulid>` and `ach-jwt-<ulid>` ID generation.

### Predecessor / Memory References

- `[[reference_ach_litellm_sister_project]]` — Sister-project layout reference.
- `[[reference_litellm_autoconfig_predecessor]]` — Python daemon predecessor; informs LiteLLM API shape.
- `[[feedback_ach_pk_runtime_first_class]]` — `pk_` on runtime is permanent first-class; NO server-side toggle. Phase 4 forwarder MUST accept `pk_` on all runtime routes uniformly with `ek_`.
- `[[feedback_bip_no_shadow_logic.md]]` — BIP duplicates: no Operator-side logic, no Synced status churn, alpha-LAST winner at read time.
- `[[feedback_spec_source_of_truth]]` — Local `.md` spec is canonical (`ach_hub_spec_v20260515_FINALv4.md`); never the gist.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets (from Phases 1, 2, 02.2, 3)

- **`cmd/ach/cmd/forwarder.go`** (Phase 1, ~70 LoC stub) — currently `/healthz` long-running process on `:8081`. Phase 4 grows this to wire up: `manager.Manager` (D-08), `pgxpool.Pool` (Phase 1 DB), `go-redis.Client` (Phase 3 D-09 client), `litellm.RESTClient` (Phase 2 lift), `keystore.KeyResolver` + new `keystore.TeamsResolver` (Phase 3 D-08 + Phase 4 D-17), JWT Secret loader (D-10), then `mgr.Add(serverRunnable{trafficAddr, healthAddr, handler})` and `mgr.Start(signalContext)`.
- **`internal/keystore/`** (Phase 3) — `KeyResolver` interface, `redisCachedResolver`, `dbResolver` already shipped. Phase 4 ADDS `TeamsResolver` + `redisCachedTeamsResolver` mirroring the same pattern (D-17). The compile-time `var _ Resolver = (*xxxResolver)(nil)` canary catches drift.
- **`internal/db/`** (Phases 1–3) — `PkCheckAndExtend` (Phase 3 D-10), `EkResolve` (Phase 3 D-11), `pgxpool.Pool` wrapper. Phase 4 calls them via `keystore`; no new SQL helpers needed.
- **`internal/credhash/`** (Phase 1) — `Hash`, `Equal`, constant-time compare, `ErrEmptyPepper`. Phase 4 uses on every incoming `x-ach-key` before the DB/Redis lookup (Phase 3 D-07 idiom).
- **`internal/litellm/`** (Phase 2 + Phase 3) — `RESTClient` + `Client` interface. `UserInfoByEmail` already returns `UserInfo{Teams: []string}` (verified — see `internal/litellm/types.go:28` + `internal/litellm/client.go:104`). Phase 4 calls this for `TeamsResolver` miss path; no new client methods needed.
- **`internal/audit/handler.go`** (Phase 2) — `audit.NewLogger`. **NOT used by Forwarder runtime path** (OBS-01). Phase 4 emits no audit events on `/v1`/`/gemini`/`/mcp`/`/a2a` proxy traffic.
- **`internal/config/`** (Phases 1–3) — `EnvOr`, `MustEnv`, `MustEnvDurationAtLeast`. Phase 4 adds parsing for `ACH_LITELLM_BASE_URL` (required, http/https accepted), reuses `ACH_LITELLM_SHARED_KEY` (Phase 2), `ACH_REDIS_*` (Phase 3), `ACH_POSTGRES_*` (Phase 1), `ACH_BASE_URL` (Phase 3 https-only refuse-to-start), `ACH_FORWARDER_BIND_ADDRESS` (default `:8080`), `FORWARDER_HEALTH_BIND_ADDRESS` (default `:8081` — Phase 1 stub default), `POD_NAMESPACE` (downward API).
- **`internal/controller/ach/backendidentitypolicy_controller.go`** (Phase 1 stub) — finalizer add/remove only; no status writes. **Phase 4 does NOT extend the reconciler body.** Phase 4 ONLY scrubs the stale doc comment (lines 17–25 + 80–82) forecasting Phase-4 DuplicateTarget logic — TODO.md §6 supersedes.
- **`api/ach/v1alpha1/`** — `BackendIdentityPolicy` + `Environment` types already shipped Phase 1. No CRD changes in Phase 4 (per Phase 3 D-19 invariant — Phase 1 owns v1alpha1 schema verbatim).
- **`config/rbac/`** — Phase 1 + Phase 3 RBAC files in place. Phase 4 ADDS `config/rbac/forwarder_role.yaml` (D-22) — namespace-scoped Role + RoleBinding for the Forwarder ServiceAccount.
- **`deploy/helm/ach/templates/`** — Per TODO.md §3 ("Multi-component Helm templates"), `forwarder-deployment.yaml` is one of the new templates needed. Phase 4 SHOULD ship this template; planner decides whether to include in Phase 4 scope or surface as a Helm-only follow-up. Pre-decision: ship in Phase 4 so the Helm chart can install the Forwarder end-to-end for the SC verification.

### Established Patterns (carry-forward)

- **Single-binary cobra layout** — `cmd/ach/cmd/forwarder.go` grows in place; NO new `cmd/<x>/main.go` tree.
- **Containerized toolchain** — every `go`/`make`/`controller-gen`/`golangci-lint` invocation goes through `./scripts/dev.sh` (CLAUDE.md "Toolchain — host has NO Go").
- **`make` targets shelling to `go`** — prefixed `./scripts/dev.sh` per CLAUDE.md; targets that call `kubectl`/`docker`/`helm`/`kind` run on host.
- **Logging** — `log/slog` JSON in prod / text in dev; `x-ach-key` redacted to `<prefix>_***` in access logs per FWD-11.
- **Tests** — Ginkgo + Gomega + envtest for informer-backed tests; testcontainers-go for Postgres + Redis; `httptest.Server` for LiteLLM upstream + LiteLLM REST client tests; `test/e2e/` Ginkgo suite extended.
- **Wait targets** — Per CLAUDE.md "Waiting for state", any wait in E2E uses blessed `make wait-*` (e.g. `make wait-forwarder`); naked `until ...; do sleep N; done` is banned.
- **Counter hooks inline; full Prometheus emitter Phase 5** — Phase 3 D-Discretion pattern reused.
- **Pre-push gate** — `make pre-push` (17-gate) MUST pass before any push; SPDX header on every new `*.go`; `make lint` + `make unit` are gates 16+17.

### Integration Points (forward + lateral)

- **Phase 1 → Phase 4:** RBAC scaffolding (forwarder Role added by Phase 4); BIP CRD + finalizer in place; Phase 1 forwarder stub binary already builds and is referenced by Helm chart Deployment readiness probe.
- **Phase 3 → Phase 4:** `internal/keystore.KeyResolver` ready; `PkCheckAndExtend` + `EkResolve` ready; chi router idiom established; `manager.Manager` informer pattern established; `litellm.UserInfoByEmail` returns `Teams[]`; `KeyInfo.litellm_token` populated for both `pk_` and `ek_` paths (Phase 02.2 column write closed by Phase 3 SSO+`KeyGenerate` flows).
- **Phase 02.2 → Phase 4:** `litellm_token` column populated → `x-litellm-key-id` header (D-07) sources from it. Without Phase 02.2's column, this header would have to come from a runtime LiteLLM lookup on every request.
- **Phase 4 → Phase 5:** Content Service consumes `internal/keystore.KeyResolver` + new `internal/keystore.TeamsResolver` for its own `pk_`/`ek_` resolution + Team-intersection check. Phase 5's SC-04 explicitly mirrors Phase 4's `pk_` pre-check pattern.
- **Phase 4 → Phase 6:** CLI `ach login` (Phase 6) drives Dex SSO via Phase 3 endpoints; CLI runtime usage (`ach hydrate`) calls Forwarder runtime routes. Forwarder behavior is the contract.
- **Phase 4 → Phase 7:** CLI hydrate engine's `runtime` block (models/MCP/A2A entries) references Forwarder upstream URLs; CLI plugins MAY register MCP servers that backend-pull JWKS from the Forwarder's `/.well-known/jwks.json`.

</code_context>

<specifics>
## Specific Ideas

- **`kid` format: `ach-jwt-<26-char-base32-ulid>`** — readable, grep-friendly, time-ordered, ≤41 chars total. Matches Phase 3 D-22 ULID idiom. Backends log `kid` on every JWT verification; this format keeps the log greppable.
- **Ed25519 key generation snippet (for rotation runbook):**
  ```go
  seed := make([]byte, 32)
  if _, err := crypto/rand.Read(seed); err != nil { panic(err) }
  kid := "ach-jwt-" + ulid.Make().String()
  // store seed (raw bytes) and kid (string) in Secret.next.{seed,kid}
  ```
- **`x-ach-key` redaction in access logs** — `<prefix>_***` shape (e.g. `pk_***`, `ek_***`). Phase 3 D-02 established the convention; same regex in Phase 4 access log middleware.
- **No body inspection on proxy path** — `httputil.ReverseProxy` streams `Body` verbatim. Forwarder MUST NOT call `io.ReadAll(req.Body)` (would break SSE on /v1/chat/completions + bloat memory). Confirm with planner that `Director` does not touch `Body`.
- **`POD_NAMESPACE` downward API env var** — Phase 3 already uses; Phase 4 Helm template adds the same `valueFrom: fieldRef.fieldPath: metadata.namespace` block to the Forwarder Deployment.
- **JWKS `Cache-Control: public, max-age=3600`** — verbatim per Hub §9.2. The 1-hour cache TTL is what makes ≥24h rotation overlap necessary (backends can hold a stale JWKS view for up to an hour after rotation step 5).
- **SSE / streaming pass-through** — `/v1/chat/completions` with `stream: true` returns `text/event-stream`. `httputil.ReverseProxy` handles this natively (no buffering by default). Forwarder traffic listener MUST NOT set a `WriteTimeout` (D-04 ships `0`) or it will cut the stream at 30s. Use context cancellation via `Request.Context()` for graceful client-disconnect propagation.
- **`X-Forwarded-For` preservation** — `httputil.ReverseProxy` defaults to appending the client IP. Keep default; LiteLLM may use this for its own audit / rate-limiting; ACH's audit channel is separate (LiteLLM-owned per OBS-01).
- **Helm chart `forwarder-deployment.yaml`** — must mount the `ach-jwt-signing-keys` Secret as `subPath` files OR as `envFrom` (NOT as a single env var — Ed25519 seed is 32 binary bytes; env-var encoding would force base64 indirection). Pre-decision: mount as `secret` volume at `/etc/ach/jwt/`, files `current.kid`, `current.seed`, `next.kid` (optional), `next.seed` (optional). Loader reads files; informer watches the Secret for change events; on event, re-read files + atomic-swap signer.

</specifics>

<deferred>
## Deferred Ideas

Discussion stayed within Phase 4 scope. Items intentionally out (already mapped to later phases, out of v1alpha1, or permanently dropped):

- **Operator-side `Synced=DuplicateTarget` reconciler** — **PERMANENTLY DROPPED** per TODO.md §6 + `[[feedback_bip_no_shadow_logic.md]]`. Not deferred; not coming back.
- **JWT `jti` claim + replay-window** — Hub §9.1 + §20 v1beta1 backlog; accepted v1alpha1 threat model.
- **Dual-key acceptance window for `ACH_LITELLM_SHARED_KEY`** — Hub §9 + §20 v1beta1 backlog; v1alpha1 uses single-key rotation with planned maintenance window.
- **Automated JWT key rotation (cron + double-flip)** — v1beta1; v1alpha1 is `kubectl patch` + wait.
- **JWT sign-result cache (sign-once-reuse for hot users)** — premature; 120s exp + hot path measured during E2E. Defer until measured need.
- **HA Forwarder (multi-replica with shared state)** — Forwarder IS stateless; multi-replica works out of the box. Formal multi-replica testing deferred unless a specific issue surfaces.
- **`/v1/hydrate` legacy path on Forwarder** — explicitly forbidden by API-01; hydrate is Platform API (Phase 3 owns it).
- **Per-MCP/A2A backend URL registry in ACH** — superseded by single-upstream model (D-05, Q1 confirmation). LiteLLM owns second-hop dispatch.
- **`Range` / `If-None-Match` / `If-Modified-Since` on `/content/`** — Phase 5 (CS-08); Phase 4 doesn't touch `/content/`.
- **Audit events on runtime forwarding** — explicitly forbidden by OBS-01 ("LiteLLM owns that channel"). Permanent.
- **FWD-06 MCP/A2A request-body tag injection** — v1alpha1 scope is `/v1` + `/gemini` ONLY (LiteLLM chat-completion JSON envelope honors `metadata.tags`). Hub spec §6.3 says "route-specific equivalents for others" without naming the MCP/A2A slot. MCP request bodies are JSON-RPC protocol-framed (no `metadata` field); A2A bodies follow the A2A protocol shape (also no `metadata.tags` equivalent). Header-based equivalents (e.g. `x-ach-environment-tag`) would require LiteLLM-side support that is not in v1alpha1 scope. Deferred to v1beta1 when a) the LiteLLM MCP/A2A adapter exposes a tag slot, OR b) the Hub spec finalizes the route-specific equivalent. Until then, `ek_` traffic on `/mcp/{name}` and `/a2a/{name}` is forwarded WITHOUT an Environment attribution tag; LiteLLM's MCP/A2A audit channels (if present) own that attribution gap.
- **`pk_` on runtime — server-side forbid toggle** — `[[feedback_ach_pk_runtime_first_class]]`; permanent.
- **`max_budget` default on first SSO** — Phase 3 OUT per KEY-10; permanent.
- **CORS on `/.well-known/jwks.json`** — backends fetch server-to-server; revisit only if a v1beta1 browser-side verifier surfaces.
- **Forwarder TLS termination** — deployment-layer concern (Ingress + cert-manager). Forwarder serves HTTP; cluster ingress terminates TLS. `ACH_BASE_URL` https-only refuse-to-start refers to the **externally-facing URL**, not the in-process listener.
- **mTLS to LiteLLM** — deployment-layer (NetworkPolicy + service mesh). Forwarder ↔ LiteLLM is in-cluster HTTP plus shared key.
- **Rate limiting at the Forwarder** — deployment-layer (Ingress controller or service mesh). Hub spec is silent.
- **Hot reload of `ACH_LITELLM_BASE_URL` / `ACH_LITELLM_SHARED_KEY`** — env-driven; restart-on-change. K8s rolling deploy handles it.
- **CLI `ach hydrate` injection of Forwarder URLs into runtime block** — Phase 7 (CLI hydrate engine consumes Phase 3 hydrate response). Phase 4 just publishes `$ACH_BASE_URL` as the iss claim; the URL composition is Phase 3 + Phase 5's job (Phase 5's `downloadUrl`).

### Spec-revision flags for planner

- **ROADMAP Phase 4 SC#3** — reads "Two policies for the same target resolve via alphabetical `metadata.name`; losers report `DuplicateTarget`." The second clause is dead per TODO.md §6. **Action:** during planning, either patch the ROADMAP entry (single-line scrub) OR add a verification-time amendment to the Phase 4 VERIFICATION report explaining the divergence. Pre-decision: patch the ROADMAP in this phase (it's a 1-line correction) and mention in commit body.
- **Hub spec §9.3** — same scrub per TODO.md §6 cleanup task. Touch when planner cross-references during research.
- **Phase 1 BIP-reconciler doc comment** — `internal/controller/ach/backendidentitypolicy_controller.go` lines 17–25 + 80–82 forecast Phase-4 DuplicateTarget logic that won't ship. Scrub during this phase (single PR with the rest of Phase 4 work).
- **Operator-side BIP work in `../ach-old/internal/controller/`** — TODO.md §2 order step 5 says "port reconcilers". When porting the BIP reconciler, **SKIP the DuplicateTarget logic** per TODO.md §6.

### Engineer-pending verification debt from prior phases

- **`scripts/uat-g1.sh` against live LiteLLM v1.83.10** — Phase 02.2 carry-forward.
- **`scripts/uat-phase3.sh` against live kind+Helm stack** — Phase 3 carry-forward. NOT a Phase 4 blocker, but Phase 4's E2E suite extends the same harness.

</deferred>

---

*Phase: 4-hub-forwarder-jwt-trust-path*
*Context gathered: 2026-05-26*

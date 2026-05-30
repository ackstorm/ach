# Phase 3: Hub Identity & Platform API — Pattern Map

**Mapped:** 2026-05-19
**Files analyzed:** 28 (8 new internal subpackages + 1 rewrite + supporting helpers + RBAC + compose + tests)
**Analogs found:** 28 / 28 (every Phase 3 file has a concrete in-repo or sister-project analog)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `cmd/platform-api/main.go` (REWRITE) | binary entrypoint | wiring + long-running | `cmd/operator/main.go` | role-match (operator wires full manager; platform-api wires informer-only manager + chi server Runnable) |
| `internal/platformapi/server.go` (NEW) | router constructor | request-response | `cmd/operator/main.go` lines 305-360 (mgr/webhook server bootstrap) + new chi | partial (no existing chi router; sister has none) |
| `internal/platformapi/auth/sso.go` (NEW) | controller (HTTP) | request-response | `cmd/platform-api/main.go` (Phase 1 stub `mux.HandleFunc`) + new go-oidc | partial (no existing OIDC handler) |
| `internal/platformapi/envkeys/handler.go` (NEW) | controller (HTTP) | CRUD | `internal/orphan/runnable.go` (LiteLLM-first revoke order) + `internal/db/external_refs.go` (DB UPSERT idiom) | role-match |
| `internal/platformapi/environments/handler.go` (NEW) | controller (HTTP) | request-response | `internal/snapshot/snapshot.go` (informer-snapshot read pattern) | role-match |
| `internal/platformapi/hydrate/handler.go` (NEW) | controller (HTTP) | request-response | `internal/snapshot/snapshot.go` (read-cached, no-roundtrip) | role-match |
| `internal/platformapi/admin/handler.go` (NEW) | controller (HTTP) | request-response | `internal/db/external_refs.go` `UpsertExternalRef` `force_refresh_requested_at` clear (Phase 2 D-07 contract) | exact (admin/refresh writes the same annotation Phase 2's reconciler reads + clears) |
| `internal/platformapi/store/store.go` (NEW) | service (informer reader) | request-response (in-memory) | `internal/snapshot/snapshot.go` `Snapshot()` + `cmd/operator/main.go` lines 367-370 (informer pre-warm) | role-match |
| `internal/platformapi/render/json.go` (NEW) | utility | request-response | new (no existing JSON-envelope writer); shape spec'd by §15.5 | no analog (write from spec) |
| `internal/keystore/keystore.go` (NEW) | service (cache wrapper) | request-response | `internal/snapshot/snapshot.go` (atomic.Pointer publication) + new Redis | role-match (Redis is new) |
| `internal/keystore/dbresolver.go` (NEW) | service (DB-backed resolver) | request-response | `internal/db/external_refs.go` `GetExternalRef` pgx idiom | role-match |
| `internal/db/personal_keys.go` (NEW) | model (SQL helpers) | CRUD | `internal/db/external_refs.go` | exact |
| `internal/db/environment_keys.go` (NEW) | model (SQL helpers) | CRUD | `internal/db/external_refs.go` | exact |
| `internal/db/check_extend.go` (NEW, `PkCheckAndExtend`) | model (SQL helper) | atomic UPDATE+RETURNING | `internal/db/external_refs.go` `UpsertExternalRef` (single-statement DDL+RETURNING idiom) | role-match |
| `internal/db/ek_resolve.go` (NEW, `EkResolve`) | model (SQL helper) | atomic UPDATE+RETURNING | `internal/db/external_refs.go` same UPSERT idiom | role-match |
| `internal/db/active_keys.go` (MODIFY) | model (SQL helper) | read | itself (Phase 2 → Phase 02.2) | exact (adds `ListActiveACHKeyTokens` per Phase 02.2 D-02 plan) |
| `internal/audit/events.go` (NEW) | utility (constants + helper) | n/a | `internal/orphan/runnable.go` lines 39-49 (`OutcomeRevoked` etc.) | exact (lifts the enum pattern to package-level) |
| `internal/audit/emit.go` (NEW, `EmitAudit`) | utility (helper) | event-driven | `internal/orphan/runnable.go` `r.Audit.Info(...)` call sites | role-match |
| `internal/litellm/users.go` (NEW, `UserNew`/`UserInfoByEmail`/`TeamMemberAdd`) | service (REST client method group) | request-response | `internal/litellm/team.go` (`CreateTeam`/`UpdateTeam`/`DeleteTeam`) | exact |
| `internal/litellm/keygen.go` (NEW, `KeyGenerate`) | service (REST client method) | request-response | `internal/litellm/team.go` `CreateTeam` (POST + decode JSON envelope) | exact |
| `internal/litellm/types.go` (MODIFY) | model (Go types) | n/a | itself (Phase 2 carry-forward) | exact (adds `UserNewRequest`/`UserInfo`/`TeamMemberAddRequest`/`KeyGenerateRequest`/`KeyGenerateResponse`) |
| `internal/litellm/client.go` (MODIFY) | interface | n/a | itself | exact (extends `Client` interface — same `var _ Client = (*…)(nil)` canary) |
| `internal/litellm/noop.go` (MODIFY) | implementation | n/a | itself | exact (adds stubs returning canned values for new methods) |
| `internal/config/config.go` (MODIFY) | utility | n/a | itself | exact (no new helper APIs needed; reuses `MustEnvNonEmpty`/`EnvOr`/`EnvBool`) |
| `config/rbac/platformapi_role.yaml` (MODIFY) | RBAC manifest | n/a | `config/rbac/operator_role.yaml` lines 44-46 (operator's `secrets` rule) | exact |
| `docker-compose.yml` (MODIFY) | infra manifest | n/a | itself (Phase 02.2 added `profiles: [litellm]`) | exact (adds `dex` under `profiles: [dex]`) |
| `test/e2e/phase3_invariants_test.go` (NEW) | test | n/a | `test/e2e/phase2_invariants_test.go` (worktree analog; same Ginkgo shape) | role-match |
| `internal/platformapi/*/handler_test.go` (NEW × 5) | test | n/a | `internal/orphan/runnable_test.go` (fake LiteLLM + bytes.Buffer audit) | role-match |

## Pattern Assignments

---

### `cmd/platform-api/main.go` (binary entrypoint — REWRITE in place)

**Analog:** `cmd/operator/main.go` (existing; ~510 LoC, controller-runtime full manager) + Phase 1 stub `cmd/platform-api/main.go` (existing; ~75 LoC, http.ServeMux + signal handling).

**Phase 1 stub idioms to preserve verbatim** (`cmd/platform-api/main.go` lines 40-78):
```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
logger.Info("Phase 1 stub: platform-api starting", ...)

addr := config.EnvOr("PLATFORM_API_HEALTH_BIND_ADDRESS", ":8081")
srv := &http.Server{
    Addr:    addr,
    Handler: mux,
    // ReadHeaderTimeout closes the slowloris attack surface flagged by
    // gosec G112 — every Phase 1 stub server sets this …
    ReadHeaderTimeout: 5 * time.Second,
}

sig := make(chan os.Signal, 1)
signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
<-sig
logger.Info("shutdown signal received, draining")

shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := srv.Shutdown(shutdownCtx); err != nil { ... }
```

**Operator-main wiring idioms to lift** (`cmd/operator/main.go`):

Env-var fail-fast block (lines 135-191):
```go
pepper, err := pepperenv.Load()
if err != nil {
    setupLog.Error(err, "fatal: credential-hash pepper invalid (D-09 / Hub §16.1)")
    os.Exit(1)
}
dbURL, err := config.MustEnvNonEmpty("ACH_DB_URL")
if err != nil { ... os.Exit(1) }
liteLLMBaseURL, err := config.MustEnvNonEmpty("ACH_LITELLM_BASE_URL")
if err != nil { ... os.Exit(1) }
liteLLMMasterKey, err := config.MustEnvNonEmpty("ACH_LITELLM_MASTER_KEY")
if err != nil { ... os.Exit(1) }
```

Postgres pool open + deferred close (lines 201-209):
```go
dbCtx, dbCancel := context.WithCancel(context.Background())
defer dbCancel()
dbPool, err := db.Open(dbCtx, dbURL)
if err != nil { ... os.Exit(1) }
defer dbPool.Close()
setupLog.Info("Postgres pool opened", "maxConns", 10)
```

RESTClient construction (lines 257-258) — Phase 3 reuses verbatim:
```go
realLiteLLM := litellm.NewRESTClient(liteLLMBaseURL, liteLLMMasterKey,
    ctrl.Log.WithName("litellm"))
```

Audit logger construction (line 263) — Phase 3 reuses verbatim:
```go
auditLog := audit.NewLogger(os.Stdout)
```

Manager construction with namespace-scoped cache (lines 341-356) — Phase 3 SIMPLIFIES (no leader election, MetricsBindAddress = "0"):
```go
mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
    Scheme:  scheme,
    Metrics: metricsServerOptions,
    Cache: cache.Options{
        DefaultNamespaces: map[string]cache.Config{
            watchNS: {},
        },
    },
    HealthProbeBindAddress: probeAddr,
    LeaderElection:         false,   // <-- D-20 divergence: ALWAYS false for platform-api
})
```

Secret informer pre-warm (lines 367-370):
```go
if _, err := mgr.GetCache().GetInformer(context.Background(), &corev1.Secret{}); err != nil {
    setupLog.Error(err, "unable to install Secret informer pre-warm")
    os.Exit(1)
}
```

Manager.Add + signal-handler start (lines 504-507):
```go
setupLog.Info("starting manager", "watchNS", watchNS)
if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil { ... }
```

**Divergence notes:**
- NO controllers registered (`SetupWithManager` calls deleted). Six ACH CRDs are informer-cached for read-only use by `internal/platformapi/store/`. `+kubebuilder:scaffold:builder` marker stays but with no surrounding controller blocks.
- NO leader election ever (D-20). `LeaderElection: false` hardcoded.
- NEW dependencies wired BEFORE `mgr.Start`: `go-redis` client (D-09), `oidc.NewProvider` (D-04), allowlist loader (D-22), `chi.Mux` server.
- HTTP server wrapped as `manager.Runnable` and registered via `mgr.Add(serverRunnable{...})` so the chi.Mux lifecycle is tied to the manager's signal context — analog: `mgr.Add(orphanRunnable)` at lines 382-387.
- NEW env vars validated up front: `ACH_BASE_URL` (HTTPS-only refuse-to-start), `ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET`, `ACH_DEX_REDIRECT_URL`, `ACH_REDIS_ADDR`, `ACH_ADMIN_ALLOWLIST_PATH`, `ACH_PLATFORM_API_BIND_ADDRESS`. All via `config.MustEnvNonEmpty` / `config.EnvOr`.
- Phase 1 stub's `http.ServeMux` + manual `srv.ListenAndServe + srv.Shutdown` is REPLACED by the `serverRunnable` pattern so the manager's signal context drives shutdown (eliminates the duplicate signal channel).

**Tests to mirror:** `internal/orphan/runnable_test.go` (manager.Runnable lifecycle) + new envtest smoke test under `test/e2e/phase3_invariants_test.go`.

---

### `internal/platformapi/server.go` (NEW — router constructor)

**Analog:** No in-repo chi router exists yet. Closest structural analog is the `cmd/operator/main.go` webhook-server + manager-options bootstrap (lines 305-356) — same "construct deps, wire middleware, return composed Handler" shape.

**Phase 1 stub HTTP/ServeMux pattern** (`cmd/platform-api/main.go` lines 44-47) — migrate cleanly to chi:
```go
mux := http.NewServeMux()
mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```

**chi.Mux target shape** (NEW — per D-01, D-02):
```go
// Deps is the dependency bag the orchestrator (cmd/platform-api/main.go)
// constructs and hands to New().
type Deps struct {
    Pool       *pgxpool.Pool
    Redis      *redis.Client
    LiteLLM    litellm.Client
    Audit      *slog.Logger      // audit.NewLogger(os.Stdout) handle
    Pepper     []byte            // pepperenv.Load() byte slice
    Allowlist  map[string]struct{}
    OIDC       *oidc.Provider
    OAuth2Cfg  *oauth2.Config
    K8sClient  client.Client     // mgr.GetClient() — for force-refresh PATCH
    Store      *store.Store      // informer-backed environment reader
    Logger     *slog.Logger      // operational (NOT audit) logger
}

// New returns the composed chi.Mux. The mux is the manager.Runnable's
// inner handler — server.go does NOT own the *http.Server lifecycle.
func New(deps Deps) http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.RequestID)         // <- replace with custom ULID middleware (D-02)
    r.Use(middleware.Recoverer)         // -> wrap to emit audit outcome=internal_error
    r.Use(accessLog(deps.Logger))       // method/path/status/latency_ms; never x-ach-key body
    r.Use(contentTypeJSON)

    // Unauthenticated endpoints (D-02 carve-out)
    r.Get("/healthz", health)
    r.Get("/livez", health)
    r.Get("/readyz", readiness(deps.Pool, deps.Redis))
    r.Get("/platform/auth/login", auth.LoginHandler(deps))
    r.Get("/platform/auth/sso/callback", auth.CallbackHandler(deps))

    // Authenticated subtree
    r.Group(func(r chi.Router) {
        r.Use(authn(deps))               // resolves x-ach-key via keystore
        r.Post("/platform/hydrate", hydrate.HydrateHandler(deps))
        r.Route("/platform/env-keys", envkeys.Mount(deps))
        r.Route("/platform/environments", environments.Mount(deps))
        r.Route("/platform/admin", admin.Mount(deps))  // admin guard inside Mount
    })
    return r
}
```

**Middleware chain idiom — borrow request-id + access-log discipline from the redacting RoundTripper** (`internal/litellm/transport.go` lines 42-98):
```go
// transport.go logs ONLY {method, path, status, latency_ms} by default.
// chi accessLog middleware MUST mirror this discipline:
fields := []any{
    "method", req.Method,
    "path", req.URL.Path,
    "status", resp.StatusCode,
    "latency_ms", latency.Milliseconds(),
}
```

**Divergence notes:**
- chi is a NEW go.mod dependency (D-01). No prior usage in the codebase.
- `request_id` ULID via `github.com/oklog/ulid/v2` — D-19 / Claude's discretion. Pattern: `id := "req_" + ulid.Make().String()` then `r = r.WithContext(WithRequestID(r.Context(), id))`.
- `authn` middleware MUST discard plaintext from `req.Header` AFTER resolution to satisfy D-19 ("Plaintext bearer values are ALREADY out of ctx by the time the handler runs"). Implementation: `req.Header.Del("x-ach-key")` after the keystore resolve.
- `readyz` gates on `mgr.GetCache().WaitForCacheSync(ctx)` analog — actually delegated to `manager`'s `/readyz` (mgr.AddReadyzCheck pattern from `cmd/operator/main.go` line 498-501).

**Tests to mirror:** `internal/litellm/client_test.go` (httptest.Server + table-driven middleware probes).

---

### `internal/platformapi/auth/sso.go` (NEW — Dex SSO handler)

**Analog:** No prior OIDC code. The structural shape is: build URL → 302 (Login), parse code+state → exchange → mint pk_ → JSON (Callback). Closest in-repo idiom for the "build path, JSON-decode response" is `internal/litellm/team.go` `CreateTeam`:

```go
func (c *RESTClient) CreateTeam(ctx context.Context, req *NewTeamRequest) (*TeamListEntry, error) {
    raw, err := c.makeRequest(ctx, "POST", "/team/new", req)
    if err != nil {
        return nil, err
    }
    var out TeamListEntry
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("litellm: decode POST /team/new: %w", err)
    }
    return &out, nil
}
```

**Pattern (NEW — per D-04):**
- `LoginHandler`: generate 16-byte `state` and 32-byte `verifier` via `crypto/rand`; set `__Host-ach_sso` cookie HttpOnly+Secure+SameSite=Strict, 10min TTL; redirect 302 to `oauth2Cfg.AuthCodeURL(state, oauth2.AccessTypeOnline, oidc.PKCEChallengeFromVerifier(verifier))`.
- `CallbackHandler`: read cookie, verify state, `oauth2Cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))`, extract `id_token` from `token.Extra("id_token")`, `oidcProvider.Verifier(&oidc.Config{ClientID: ...}).Verify(ctx, idTokenRaw)`, extract `email` claim, run `litellm.UserInfoByEmail` → `UserNew` if absent → `TeamMemberAdd("default", userID, "user")` (idempotent — swallow LiteLLM dup-add 4xx) → mint pk_ via `KeyGenerate(key=<ACH-generated>)`, INSERT row, return JSON `{"key_id":"pkid_...","plaintext":"pk_...","owner_email":"..."}`.

**Bearer plaintext generation pattern (NEW — Claude's discretion + Hub §16):**
```go
// pk_<26 base32 chars> = 16 random bytes encoded RFC 4648 base32 no-pad.
b := make([]byte, 16)
if _, err := rand.Read(b); err != nil { ... }
suffix := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
plaintext := "pk_" + strings.ToLower(suffix)  // 16 bytes → 26 base32 chars
```

**pkid_/ekid_ key_id generator** — use `ulid.Make()` so DB index locality is preserved:
```go
keyID := "pkid_" + strings.ToLower(ulid.Make().String())  // 26 chars after prefix
```

**Audit emission on success** — mirror `internal/orphan/runnable.go` lines 200-203:
```go
deps.Audit.Info(audit.ActionSSOLogin,
    "actor", actorFromCtx(r.Context()),    // <namespace>/<email>
    "outcome", audit.OutcomeCreated,
    "key.id", keyID,
    "request_id", requestIDFromCtx(r.Context()),
)
```

**Divergence notes:**
- `coreos/go-oidc/v3` + `golang.org/x/oauth2` are NEW go.mod deps. Promote `x/oauth2` from indirect to direct.
- The "exactly-once plaintext" invariant (per Specifics block) is enforced by structure: only this handler and `envkeys.CreateHandler` write plaintext to the response. NO other handler imports `crypto/rand` or `base32.StdEncoding`.
- Default-team-missing branch (D-04 step 5) → emit `audit.OutcomeDefaultTeamMissing` then `500` via `render.Error(w, http.StatusInternalServerError, "default_team_missing", ...)`.

**Tests to mirror:** `internal/orphan/runnable_test.go` (fake LiteLLM + bytes.Buffer audit) + `httptest.Server` substituting for Dex (mock `.well-known/openid-configuration` + `/keys` + `/token` per coreos/go-oidc test fixtures).

---

### `internal/platformapi/envkeys/handler.go` (NEW — ek_ CRUD)

**Analog:** `internal/orphan/runnable.go` (LiteLLM-first ordering for revocation; lines 218-237) + `internal/db/external_refs.go` (DB UPSERT pgx idiom; lines 82-107).

**Lift the orphan-loop's "call LiteLLM, then DB-flip" sequencing for `RevokeHandler`** (D-15):
```go
// internal/orphan/runnable.go lines 218-237:
if err := r.Client.RevokeKey(ctx, k.Token); err != nil {
    r.Audit.Info("operator.orphan-cleanup",
        "target.kind", "litellm_key",
        "target.name", k.Token,
        "outcome", OutcomeRevokeFailed,
        "user_id", uid)
    // ... continue
}
r.Audit.Info("operator.orphan-cleanup",
    "target.kind", "litellm_key",
    "target.name", k.Token,
    "outcome", OutcomeRevoked,
    "user_id", uid)
```

Phase 3 `revokeEnvironmentKey` (D-15):
1. SELECT row → capture `credential_hash`+`litellm_token`. NO status flip yet.
2. `deps.LiteLLM.RevokeKey(ctx, token)`. On error → return `503 litellm_unreachable` + audit `outcome=litellm_unreachable`. DB row stays active.
3. `UPDATE environment_keys SET status='revoked', revoked_at=now() WHERE key_id=$1`.
4. `deps.Redis.Del(ctx, "ach:key:" + credential_hash)`. Best-effort.
5. Audit `action=ek.revoke`, `outcome=revoked`. Return `204 No Content`.

**§8.2 8-step CreateHandler** (D-12, D-13) — generate plaintext SERVER-SIDE, hash with `credhash.Hash(deps.Pepper, []byte(plaintext))`, call `LiteLLM.KeyGenerate(key=<plaintext>)`, INSERT inside tx, compensate on INSERT failure.

**DB INSERT idiom** — copy `internal/db/external_refs.go` `UpsertExternalRef` lines 82-107:
```go
const sql = `
    INSERT INTO environment_keys
        (key_id, credential_hash, environment, owner_email, name,
         status, litellm_user_id, litellm_token)
    VALUES ($1, $2, $3, $4, $5, 'active', $6, $7)
`
if _, err := pool.Exec(ctx, sql,
    keyID, credHash, environment, ownerEmail, name, userID, token,
); err != nil {
    if isTransientPgErr(err) {
        return err
    }
    return fmt.Errorf("db: InsertEnvironmentKey(%s): %w", keyID, err)
}
```

**LiteLLM compensation on INSERT failure** — lift the "best-effort second call" pattern from orphan loop (call `RevokeKey(token)` if INSERT fails). Implementation in `internal/db/environment_keys.go` `InsertEnvironmentKey`, and handler invokes compensation inline:
```go
genResp, err := deps.LiteLLM.KeyGenerate(ctx, req)
if err != nil { /* render 500/503 */ }
if err := db.InsertEnvironmentKey(ctx, deps.Pool, row); err != nil {
    // Compensate per D-12: revoke the LiteLLM-side key we just created.
    if cleanupErr := deps.LiteLLM.RevokeKey(context.Background(), genResp.Token); cleanupErr != nil {
        deps.Logger.Error("ek.create: compensation revoke failed",
            "token", genResp.Token, "err", cleanupErr)
    }
    deps.Audit.Info(audit.ActionEkCreate,
        "outcome", audit.OutcomeDbInsertFailed,
        ...)
    render.Error(w, 500, "db_insert_failed", ...)
    return
}
```

**Divergence notes:**
- Caller-type guard at the top of every handler: `if keyCtx.KeyType != "pk_" { render.Error(w, 401, "invalid_key_type", ...); return }`. `ek_` is rejected on the create path per D-12 step 1.
- `informer-cached Environment read` (D-12 step 2) goes through `deps.Store.GetEnvironment(ctx, name)` — Phase 3's `internal/platformapi/store/` helper. Deletion timestamp check: `if env.DeletionTimestamp != nil { 404 environment_not_found }`.
- `AccessGroupSynced=True` check (D-12 step 3) goes through `deps.Store.EnvironmentAccessGroupSynced(ctx, name)`; not `True` → `503 not_ready`.

**Tests to mirror:** `internal/orphan/runnable_test.go` (fake LiteLLM + bytes.Buffer audit) for the LiteLLM client; `httptest` for the HTTP path; testcontainers-go Postgres for the INSERT compensation.

---

### `internal/platformapi/environments/handler.go` (NEW — informer-backed read)

**Analog:** `internal/snapshot/snapshot.go` `Snapshot()` (lines 111-116) — lock-free read of an immutable snapshot:
```go
func (s *Snapshotter) Snapshot() LiteLLMSnapshot {
    if p := s.snap.Load(); p != nil {
        return *p
    }
    return LiteLLMSnapshot{}
}
```

**Phase 3 pattern (NEW — per D-21):**
```go
// ListHandler: filter by caller teams; admin sees all.
envs, err := deps.Store.ListAuthorizedEnvironments(ctx, callerTeams)
if err != nil { render.Error(w, 500, "internal_error", ...); return }
// Pagination via ?limit=&cursor= (API-09); cursor is opaque base64-encoded offset.
items := paginate(envs, limit, cursor)
render.JSON(w, 200, map[string]any{"items": items, "next_cursor": nextCursor})
```

**`store/` reader semantics** — every helper sub-millisecond after warmup. Caller-team intersection logic:
```go
func (s *Store) ListAuthorizedEnvironments(ctx context.Context, callerTeams []string) ([]EnvironmentView, error) {
    var list achv1alpha1.EnvironmentList
    if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil { ... }
    out := make([]EnvironmentView, 0, len(list.Items))
    for _, env := range list.Items {
        if hasIntersect(env.Spec.AuthorizedTeams, callerTeams) || isAdmin {
            out = append(out, toView(env))
        }
    }
    return out, nil
}
```

**Divergence notes:**
- The Snapshotter's `atomic.Pointer` indirection is NOT needed — controller-runtime's typed client (`mgr.GetClient()`) already reads from the informer cache lock-free. Phase 3 just calls `s.client.List(ctx, &list, ...)`.
- `conditions[]` is carried VERBATIM from `status.Conditions` per API-08 / §6.6. Marshaled as-is (the CRD type's `meta/v1.Condition` slice JSON-marshals correctly).

**Tests to mirror:** `internal/snapshot/snapshot_test.go` (fake-data injection + assert on read return); envtest for the live informer in `test/e2e/phase3_invariants_test.go`.

---

### `internal/platformapi/hydrate/handler.go` (NEW — §15.1 response builder)

**Analog:** `internal/platformapi/store/` (above) for reads + `internal/litellm/team.go` `ListTeamsByAlias` (lines 60-78) for "compose response from cached source" idiom.

**Pattern (NEW — per D-16, D-17):**
```go
// Strict JSON decode — DisallowUnknownFields per D-16:
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()
var req struct{ Environment string `json:"environment"` }
if err := dec.Decode(&req); err != nil {
    render.Error(w, 400, "invalid_argument", "...")
    return
}
// For pk_: req.Environment is required.
// For ek_: req.Environment is ignored; the key's bound Environment wins.

envName := req.Environment
if keyCtx.KeyType == "ek_" {
    envName = keyCtx.Environment // already resolved by authn middleware
}
env, err := deps.Store.GetEnvironment(ctx, envName)
if env == nil { render.Error(w, 404, "environment_not_found", ...); return }
// Terminating Environments STILL serve hydrate per API-03 v9.

resp := map[string]any{
    "schemaVersion": "v1alpha1",
    "environment":   envName,
    "runtime": map[string]any{
        "models":      toRuntimeModels(env.Spec.Runtime.Models),       // [] if empty
        "mcpServers":  toRuntimeMCPs(env.Spec.Runtime.MCPServers),     // [] if empty
        "a2aAgents":   toRuntimeAgents(env.Spec.Runtime.A2AAgents),    // [] if empty
    },
    "context": map[string]any{
        "prompts":   toCtxPrompts(env, deps.BaseURL),    // downloadUrl = $BASE_URL/content/prompt/<name>
        "plugins":   toCtxPlugins(env, deps.BaseURL),    // downloadUrl = $BASE_URL/content/plugin/<name>
        "artifacts": toCtxArtifacts(env, deps.BaseURL),  // downloadUrl = $BASE_URL/content/artifact/<name>
    },
}
render.JSON(w, 200, resp)
```

**Divergence notes:**
- Plaintext NEVER appears in the response (Specifics block invariant). Hydrate is read-only over Environment + cached external_refs; bearer values are not in this read path.
- `[]` (NOT `null`) for empty arrays per API-04. Pattern: `if items == nil { items = []runtimeItem{} }` before marshaling.

**Tests to mirror:** envtest with seeded Environment CRs + `httptest.NewRequest` body decoding tests.

---

### `internal/platformapi/admin/handler.go` (NEW — admin endpoints)

**Analog (force-refresh):** `internal/db/external_refs.go` `UpsertExternalRef` lines 87-95 — clears `force_refresh_requested_at` in the same UPDATE; Phase 2 D-07 contract that Phase 3's `POST /platform/admin/refresh` PATCHES the CR annotation that Phase 2's reconciler reads + clears.

**Admin guard runs FIRST** (per D-23, API-12) — before any other validation:
```go
func Mount(deps Deps) func(chi.Router) {
    return func(r chi.Router) {
        r.Use(adminOnly(deps))  // <- this runs BEFORE the route's own body parsing
        r.Post("/keys/revoke", revokeKeyHandler(deps))
        r.Post("/users/{email}/revoke-keys", revokeUserKeysHandler(deps))
        r.Post("/refresh", forceRefreshHandler(deps))
    }
}

func adminOnly(deps Deps) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            keyCtx, _ := KeyContextFromCtx(r.Context())
            if keyCtx.KeyType != "pk_" {
                render.Error(w, 401, "invalid_key_type", "admin endpoints require pk_")
                return
            }
            if _, ok := deps.Allowlist[keyCtx.OwnerEmail]; !ok {
                deps.Audit.Info(audit.ActionAdminRefresh,  // or generic admin action
                    "outcome", audit.OutcomeNotAdmin, "actor", actorFromCtx(r.Context()))
                render.Error(w, 403, "not_admin", "...")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

**Force-refresh PATCH pattern** (D-26) — controller-runtime client.MergeFrom:
```go
var body struct {
    Kind string `json:"kind"`  // plugin|prompt|artifact|pluginmarketplace
    Name string `json:"name"`
}
// ... unknown-fields rejection per D-16 idiom ...

obj := newACHObject(body.Kind)  // returns *achv1alpha1.Plugin / Prompt / Artifact / PluginMarketplace
if err := deps.K8sClient.Get(ctx, types.NamespacedName{Namespace: deps.Namespace, Name: body.Name}, obj); err != nil {
    if apierrors.IsNotFound(err) { render.Error(w, 404, "not_found", ...); return }
    render.Error(w, 500, "internal_error", ...); return
}
orig := obj.DeepCopyObject().(client.Object)
ann := obj.GetAnnotations()
if ann == nil { ann = map[string]string{} }
ann["ach.ackstorm.ai/force-refresh"] = time.Now().UTC().Format(time.RFC3339)
obj.SetAnnotations(ann)
if err := deps.K8sClient.Patch(ctx, obj, client.MergeFrom(orig)); err != nil {
    deps.Audit.Info(audit.ActionAdminRefresh,
        "outcome", audit.OutcomeInternalError, ...)
    render.Error(w, 500, "internal_error", ...); return
}
deps.Audit.Info(audit.ActionAdminRefresh,
    "actor", actorFromCtx(r.Context()),
    "outcome", audit.OutcomeCreated,  // or a dedicated "accepted" outcome
    "target.kind", body.Kind,
    "target.name", body.Name,
    "request_id", requestIDFromCtx(r.Context()),
)
render.JSON(w, 202, map[string]any{"status": "accepted"})
```

**Divergence notes:**
- The PATCH is Platform API's only K8s write surface; the RBAC carve-out is already in `config/rbac/platformapi_role.yaml` lines 25-31 (MULTI-02). NO RBAC change for this endpoint.
- Phase 2's `internal/db/external_refs.go` `UpsertExternalRef` line 95 (`force_refresh_requested_at = NULL`) is what closes the loop — Phase 3 sets the CR annotation; Phase 2 reconciler reads the annotation, runs the refresh, and the same UPSERT clears the marker.

**Tests to mirror:** `internal/controller/ach/*_controller.go` envtest patterns (existing in repo) for the Patch round-trip; `internal/orphan/runnable_test.go` for the audit-line assertion.

---

### `internal/platformapi/store/store.go` (NEW — informer reader)

**Analog:** `internal/snapshot/snapshot.go` `Snapshot()` (lines 111-116) — but without the `atomic.Pointer` because controller-runtime's client.Client already reads lock-free from the cache.

**Pattern (NEW — per D-21):**
```go
type Store struct {
    client client.Client
    ns     string
    log    logr.Logger
}

func New(c client.Client, ns string, log logr.Logger) *Store {
    return &Store{client: c, ns: ns, log: log}
}

func (s *Store) GetEnvironment(ctx context.Context, name string) (*achv1alpha1.Environment, error) {
    env := &achv1alpha1.Environment{}
    if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.ns, Name: name}, env); err != nil {
        if apierrors.IsNotFound(err) { return nil, nil }
        return nil, err
    }
    return env, nil
}

func (s *Store) EnvironmentAccessGroupSynced(ctx context.Context, name string) (bool, error) {
    env, err := s.GetEnvironment(ctx, name)
    if err != nil || env == nil { return false, err }
    for _, c := range env.Status.Conditions {
        if c.Type == "AccessGroupSynced" {
            return c.Status == metav1.ConditionTrue, nil
        }
    }
    return false, nil
}
```

**Divergence notes:**
- Caller-supplied `client.Client` MUST be a manager-derived client (`mgr.GetClient()`) so reads are cache-served, not API-server round trips. The `cmd/platform-api/main.go` rewrite wires `deps.Store = store.New(mgr.GetClient(), watchNS, log)` AFTER `mgr.GetCache().GetInformer` pre-warm — same shape as `cmd/operator/main.go` lines 367-370.

**Tests to mirror:** `internal/controller/ach/main_wiring_envtest_test.go` (existing in repo) for envtest-backed informer reads.

---

### `internal/keystore/keystore.go` (NEW — Redis-cached resolver)

**Analog:** `internal/snapshot/snapshot.go` (atomic publication pattern; lock-free reads) for the cache mental model + `internal/db/external_refs.go` `GetExternalRef` (line 115-139) for the DB-backed branch.

**Pattern (NEW — per D-07, D-08):**
```go
type KeyInfo struct {
    KeyID         string
    KeyType       string  // "pk_" or "ek_"
    OwnerEmail    string
    Status        string
    ExpiresAt     *time.Time   // pk_ only
    Environment   string       // ek_ only
    LiteLLMToken  string       // ek_ only (post Phase 3 SSO write)
}

type Resolver interface {
    Resolve(ctx context.Context, plaintext string) (*KeyInfo, error)
}

// redisCachedResolver wraps an inner Resolver (dbResolver) and shorts on cache hit.
type redisCachedResolver struct {
    inner  Resolver
    redis  *redis.Client
    pepper []byte
    sf     singleflight.Group  // per-hash dedup of DB calls
    ttl    time.Duration       // hard ceiling 60s per Hub §5.1
}

func (r *redisCachedResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
    hash, err := credhash.Hash(r.pepper, []byte(plaintext))
    if err != nil { return nil, err }
    cacheKey := "ach:key:" + hash

    // Cache hit
    raw, err := r.redis.Get(ctx, cacheKey).Bytes()
    if err == nil {
        var info KeyInfo
        if err := json.Unmarshal(raw, &info); err == nil {
            return &info, nil
        }
    }
    // Single-flight DB lookup
    v, err, _ := r.sf.Do(hash, func() (any, error) {
        return r.inner.Resolve(ctx, plaintext)
    })
    if err != nil { return nil, err }
    info := v.(*KeyInfo)
    if info != nil {
        if b, err := json.Marshal(info); err == nil {
            _ = r.redis.Set(ctx, cacheKey, b, r.ttl).Err()
        }
    }
    return info, nil
}
```

**Hashing call site** — copy `internal/credhash/credhash.go` `Hash` (lines 45-52) verbatim:
```go
func Hash(pepper, plaintext []byte) (string, error) {
    if len(pepper) == 0 { return "", ErrEmptyPepper }
    h := hmac.New(sha256.New, pepper)
    h.Write(plaintext)
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

**Divergence notes:**
- `go-redis/redis/v9` + `x/sync/singleflight` are NEW go.mod deps.
- Cache value JSON-encodes the KeyInfo struct directly. NO plaintext field; the cache key derives from the hash, never the plaintext.
- TTL fixed at 60s ceiling (Hub §5.1 / FWD-02 / KEY-04 hard ceiling). No knob to extend it.
- The `dbResolver` branches on plaintext prefix (`pk_` vs `ek_`) to dispatch to `db.PkCheckAndExtend` vs `db.EkResolve`. Both helpers do their own debounced UPDATE (D-10, D-11).

**Tests to mirror:** `internal/orphan/runnable_test.go` (fakes for both LiteLLM client + DB seam — same pattern fits Redis + DB resolver). testcontainers-go for the Redis integration smoke.

---

### `internal/db/check_extend.go` (NEW — `PkCheckAndExtend`)

**Analog:** `internal/db/external_refs.go` `UpsertExternalRef` (lines 82-107) — single-statement `INSERT … ON CONFLICT … RETURNING` analog of "atomic UPDATE with embedded debounce".

**Pattern (NEW — per D-10, Hub §7.1):**
```go
// PkCheckAndExtend is the Hub §7.1 atomic sliding-window check-and-extend.
// Zero rows returned ⇒ revoked/expired/unknown ⇒ caller returns
// 401 expired_or_revoked (three causes indistinguishable by design).
func PkCheckAndExtend(ctx context.Context, pool *pgxpool.Pool, credentialHashHex string) (*PkKeyInfo, error) {
    const sql = `
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
        RETURNING key_id, owner_email, expires_at, litellm_user_id, litellm_token
    `
    r := &PkKeyInfo{}
    err := pool.QueryRow(ctx, sql, credentialHashHex).Scan(
        &r.KeyID, &r.OwnerEmail, &r.ExpiresAt, &r.LiteLLMUserID, &r.LiteLLMToken,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, nil  // 401 path — three causes indistinguishable
        }
        if isTransientPgErr(err) {
            return nil, err
        }
        return nil, fmt.Errorf("db: PkCheckAndExtend: %w", err)
    }
    return r, nil
}
```

**Error classification idiom — copy verbatim from `external_refs.go` lines 178-195:**
```go
func isTransientPgErr(err error) bool {
    var pgErr *pgconn.PgError
    if !errors.As(err, &pgErr) { return false }
    if len(pgErr.Code) < 2 { return false }
    class := pgErr.Code[:2]
    return class == "08" || class == "57"
}
// (Phase 3 may move this to a shared file if `personal_keys.go` and `environment_keys.go` both need it; alternatively reuse from external_refs.go via private export.)
```

**Divergence notes:**
- `pgx.ErrNoRows` → `(nil, nil)` instead of an error. The caller (resolver) reads `nil` and renders `401 expired_or_revoked`.
- This is the ONLY SQL helper that mutates `last_used_at` + `expires_at` on the auth path. No corresponding helper outside this file should touch these columns.
- `personal_keys` table columns referenced: `last_used_at`, `expires_at`, `credential_hash`, `status`, `key_id`, `owner_email`, `litellm_user_id` (Phase 2 column), `litellm_token` (Phase 02.2 column).

**Tests to mirror:** `internal/db/external_refs_test.go` + `internal/db/active_keys_test.go` (testcontainers-go Postgres + table-driven cases).

---

### `internal/db/ek_resolve.go` (NEW — `EkResolve`)

**Analog:** Same `external_refs.go` UPDATE+RETURNING shape; simpler (no sliding window).

**Pattern (NEW — per D-11, Hub §8.1):**
```go
func EkResolve(ctx context.Context, pool *pgxpool.Pool, credentialHashHex string) (*EkKeyInfo, error) {
    const sql = `
        UPDATE environment_keys SET
            last_used_at = CASE
                WHEN last_used_at IS NULL OR last_used_at < now() - interval '5 minutes' THEN now()
                ELSE last_used_at
            END
        WHERE credential_hash = $1
          AND status = 'active'
        RETURNING key_id, environment, owner_email, name, litellm_user_id, litellm_token
    `
    r := &EkKeyInfo{}
    err := pool.QueryRow(ctx, sql, credentialHashHex).Scan(
        &r.KeyID, &r.Environment, &r.OwnerEmail, &r.Name, &r.LiteLLMUserID, &r.LiteLLMToken,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
        if isTransientPgErr(err) { return nil, err }
        return nil, fmt.Errorf("db: EkResolve: %w", err)
    }
    return r, nil
}
```

**Divergence notes:**
- No `expires_at` column on `environment_keys` (revocation-only per migration 000001 lines 47-49).
- `last_used_at` UPDATE does NOT participate in auth decision (KEY-06). The `status = 'active'` predicate is authoritative.

**Tests to mirror:** Same as `check_extend.go`.

---

### `internal/db/active_keys.go` (MODIFY — add `ListActiveACHKeyTokens`)

**Existing analog (the file itself):** `internal/db/active_keys.go` lines 67-99 `ListActiveACHKeyIDs`.

**Current shape to lift:**
```go
const sql = `
    SELECT DISTINCT key_id FROM (
        SELECT key_id FROM personal_keys    WHERE status = 'active'
        UNION
        SELECT key_id FROM environment_keys WHERE status = 'active'
    ) AS u
`
```

**New `ListActiveACHKeyTokens`** (per Phase 02.2 D-02; Phase 3 fulfills the prerequisite):
```go
// ListActiveACHKeyTokens is the more-precise orphan-loop helper Phase 02.2
// promised. Returns the DISTINCT union of every active personal_keys.litellm_token
// and environment_keys.litellm_token where the column is non-null.
func ListActiveACHKeyTokens(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
    const sql = `
        SELECT DISTINCT litellm_token FROM (
            SELECT litellm_token FROM personal_keys
                WHERE status = 'active' AND litellm_token IS NOT NULL
            UNION
            SELECT litellm_token FROM environment_keys
                WHERE status = 'active' AND litellm_token IS NOT NULL
        ) AS u
    `
    // ... same Query/rows.Scan loop as ListActiveACHKeyIDs ...
}
```

**Divergence notes:**
- Phase 2's `internal/orphan/runnable.go` `NewRunnable` (lines 100-111) is the consumer — the `ListKeyIDs` test seam swaps from `db.ListActiveACHKeyIDs` to `db.ListActiveACHKeyTokens` once Phase 3's INSERT path populates `litellm_token` on every new key. The orphan loop's `achKeySet[k.Token]` membership test (runnable.go line 214) BECOMES PRECISE from this swap forward.
- `ListActiveACHKeyIDs` stays in place — Phase 3 does not delete it. Phase 4+ may retire it.

**Tests to mirror:** `internal/db/active_keys_test.go` (existing).

---

### `internal/audit/events.go` (NEW — action + outcome constants)

**Analog:** `internal/orphan/runnable.go` lines 39-49 — the existing outcome enum (package-private to `orphan`):
```go
const (
    OutcomeRevoked            = "revoked"
    OutcomeLiteLLMUnreachable = "litellm_unreachable"
    OutcomeRevokeFailed       = "revoke_failed"
)
```

**Phase 3 pattern (NEW — per D-18, Hub §18.2):**
```go
// Actions (events) — every Hub §18.2 action a Platform API handler may emit.
const (
    ActionSSOLogin             = "platform.sso.login"
    ActionEkCreate             = "platform.ek.create"
    ActionEkRevoke             = "platform.ek.revoke"
    ActionPkRevoke             = "platform.pk.revoke"
    ActionHydrate              = "platform.hydrate"
    ActionAdminRefresh         = "platform.admin.refresh"
    ActionAdminKeysRevoke      = "platform.admin.keys.revoke"
    ActionAdminUsersRevokeKeys = "platform.admin.users.revoke_keys"
    ActionEnvironmentLifecycle = "platform.environment.lifecycle"  // reserved (Operator emits)
)

// Outcomes — closed enum per Hub §18.2; extends Phase 2's three orphan-loop outcomes.
// Phase 02.2 invariants:
//   - Phase 3 ADDS to the enum, never narrows it.
//   - Future phases extend ADDITIVELY per Hub §18.5.
const (
    OutcomeCreated               = "created"
    OutcomeRevoked               = "revoked"   // value matches orphan.OutcomeRevoked
    OutcomeUnauthorizedTeam      = "unauthorized_team"
    OutcomeWrongEnvironment      = "wrong_environment"
    OutcomeMissingEnvironment    = "missing_environment"
    OutcomeEnvironmentNotFound   = "environment_not_found"
    OutcomeNotReady              = "not_ready"
    OutcomeDefaultTeamMissing    = "default_team_missing"
    OutcomeInvalidKeyType        = "invalid_key_type"
    OutcomeNotAdmin              = "not_admin"
    OutcomeNotKeyOwner           = "not_key_owner"
    OutcomeInvalidKeyFormat      = "invalid_key_format"
    OutcomeExpiredOrRevoked      = "expired_or_revoked"
    OutcomeLitellmUnreachable    = "litellm_unreachable"  // value matches orphan.OutcomeLiteLLMUnreachable
    OutcomeDbInsertFailed        = "db_insert_failed"
    OutcomeInternalError         = "internal_error"
)
```

**Divergence notes:**
- Package-LEVEL constants (not orphan-private). The orphan package may eventually re-export through these — but Phase 3 does NOT modify `internal/orphan/runnable.go` outcome constants. Same string values for the two overlapping outcomes (`"revoked"`, `"litellm_unreachable"`) ensure downstream log filters still match.
- New constants live in `events.go` alongside the existing `handler.go`/`doc.go` files in the same package (matches `internal/litellm/`'s split: `client.go`, `errors.go`, `types.go`).

**Tests to mirror:** `internal/audit/handler_test.go` — extend with a constant-stability test (`TestEventConstantsAreStable`) asserting the action/outcome string values never change.

---

### `internal/audit/emit.go` (NEW — `EmitAudit` helper)

**Analog:** `internal/orphan/runnable.go` lines 200-206 (the call-site idiom Phase 3 wraps into a helper):
```go
r.Audit.Info("operator.orphan-cleanup",
    "target.kind", "tick",
    "outcome", OutcomeLiteLLMUnreachable,
    "user_id", uid)
r.Log.Info("orphan-cleanup: tick aborted on LiteLLM-unreachable",
    "user_id", uid, "err", err)
```

**Phase 3 pattern (NEW — per D-19):**
```go
// Event is the structured audit-event payload Phase 3 handlers emit.
// Composed at the handler site; never by middleware (D-19) so the handler
// retains control over the resolved key.id (with pkid_/ekid_ prefix) and
// target.kind/target.name.
type Event struct {
    Action    string  // one of the Action* constants in events.go
    Outcome   string  // one of the Outcome* constants in events.go
    Actor     string  // "<namespace>/<sso-email>" per Hub §18.3
    RequestID string  // "req_<ulid>" from middleware
    KeyID     string  // "pkid_..." or "ekid_..." — NEVER plaintext, NEVER credential_hash
    Target    *Target // optional; required on resource-scoped events
    Extra     map[string]string  // optional additional context attributes
}

type Target struct {
    Kind string  // "environment", "plugin", "litellm_key", ...
    Name string  // CR metadata.name or LiteLLM token id (for orphan-cleanup compat)
}

// EmitAudit logs a single audit event through the provided logger.
// The logger is the *slog.Logger returned by audit.NewLogger — the audit=true
// top-level attribute is already attached.
//
// Audit-safety contract (per audit/doc.go): callers MUST NOT include
// credential plaintext, credential_hash, response bodies, or raw error
// strings in e.Extra. The function does NOT scrub — it transports raw.
func EmitAudit(ctx context.Context, logger *slog.Logger, e Event) {
    attrs := []any{
        "action", e.Action,
        "outcome", e.Outcome,
        "actor", e.Actor,
        "request_id", e.RequestID,
    }
    if e.KeyID != "" {
        attrs = append(attrs, "key.id", e.KeyID)
    }
    if e.Target != nil {
        attrs = append(attrs, "target.kind", e.Target.Kind, "target.name", e.Target.Name)
    }
    for k, v := range e.Extra {
        attrs = append(attrs, k, v)
    }
    logger.Info(e.Action, attrs...)
}
```

**Divergence notes:**
- ctx parameter accepted but unused at the helper level — kept in signature for future ctx-derived attributes (e.g. trace_id) without breaking the call sites.
- `actor` is the FULL `<namespace>/<sso-email>` composition per Hub §18.3 / Specifics block. Handlers compose this from `os.Getenv("POD_NAMESPACE")` (downward API) + `keyCtx.OwnerEmail`.
- The `logger.Info(e.Action, ...)` shape preserves the orphan-loop's idiom (Action string as the slog message + Action as a top-level attribute). This double-coding is intentional — both fields are useful for downstream log filters.

**Tests to mirror:** `internal/audit/handler_test.go` — add an `EmitAudit` round-trip test (compose Event → log to bytes.Buffer → JSON-decode → assert fields).

---

### `internal/litellm/users.go` (NEW — `UserNew`, `UserInfoByEmail`, `TeamMemberAdd`)

**Analog:** `internal/litellm/team.go` (lines 18-78) — same REST surface (POST with JSON body, decode envelope, return typed struct).

**Pattern (NEW — per D-25):**
```go
// UserNew issues POST /user/new with {user_email, user_id?, teams=[default]}.
// Idempotency: caller (sso.go) checks UserInfoByEmail first; UserNew is only
// called when the user does not exist. The LiteLLM /user/new endpoint
// also has its own duplicate-email handling but we avoid relying on it.
func (c *RESTClient) UserNew(ctx context.Context, req *UserNewRequest) (*UserInfo, error) {
    raw, err := c.makeRequest(ctx, "POST", "/user/new", req)
    if err != nil {
        return nil, err
    }
    var out UserInfo
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("litellm: decode POST /user/new: %w", err)
    }
    return &out, nil
}

// UserInfoByEmail issues GET /user/info?user_email=<email>. Returns
// (nil, ErrNotFound) when LiteLLM returns 404; callers (sso.go) use
// errors.Is(err, ErrNotFound) to distinguish "user absent" from
// "upstream unreachable" per the package convention (errors.go line 25).
func (c *RESTClient) UserInfoByEmail(ctx context.Context, email string) (*UserInfo, error) {
    path := "/user/info?user_email=" + url.QueryEscape(email)
    raw, err := c.makeRequest(ctx, "GET", path, nil)
    if err != nil {
        // makeRequest returns 4xx errors wrapped via fmt.Errorf. The
        // resolver caller treats any 404-class error as "user absent"
        // by string-matching on the LiteLLM code; alternatively we may
        // extend makeRequest to return ErrNotFound on 404 here.
        return nil, err
    }
    var out UserInfo
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("litellm: decode GET /user/info: %w", err)
    }
    return &out, nil
}

// TeamMemberAdd issues POST /team/member_add. Idempotency:
// caller (sso.go) reads team-membership first; LiteLLM treats a duplicate
// add as 4xx, which propagates back via makeRequest; the caller swallows
// any 4xx that decodes to a duplicate-add error code.
func (c *RESTClient) TeamMemberAdd(ctx context.Context, teamID, userID, role string) error {
    body := &TeamMemberAddRequest{TeamID: teamID, Member: TeamMember{UserID: userID, Role: role}}
    _, err := c.makeRequest(ctx, "POST", "/team/member_add", body)
    return err
}
```

**Divergence notes:**
- `UserInfoByEmail` 404 handling: the existing `makeRequest` returns the 4xx as a wrapped error (lines 153-158); Phase 3 may extend `makeRequest` with an additional `GET 404 → ErrNotFound` branch matching the existing `DELETE 404 → success` branch. Decision deferred to the planner — both options are pattern-consistent.
- All three methods funnel through `c.makeRequest`, so the §9.1 no-body-in-error-strings discipline (`internal/litellm/transport.go` redactingRoundTripper, errors.go lines 95-108) is automatic.
- `UserNewRequest`, `UserInfo`, `TeamMemberAddRequest`, `TeamMember` added to `internal/litellm/types.go` per the existing struct convention (lines 89-148 — `NewTeamRequest`, `UpdateTeamRequest`, etc.).

**Tests to mirror:** `internal/litellm/team_test.go` — same httptest.Server + mock-response idiom.

---

### `internal/litellm/keygen.go` (NEW — `KeyGenerate`)

**Analog:** `internal/litellm/team.go` `CreateTeam` (lines 19-29) — identical "POST with JSON body, decode envelope, return typed struct" shape.

**Pattern (NEW — per D-25):**
```go
// KeyGenerate issues POST /key/generate with the ACH-supplied key, user_id,
// access_groups, etc. Per Phase 3 D-13 ACH generates the bearer plaintext
// server-side (crypto/rand → base32-no-pad) and supplies it via the `key`
// request-body field — LiteLLM supports caller-supplied keys via this field.
//
// The response carries `token` (the LiteLLM-internal opaque hex stored in
// personal_keys.litellm_token / environment_keys.litellm_token per
// Phase 02.2 D-01 / migration 000003) and other LiteLLM-side metadata.
//
// ACH never sets max_budget on first-SSO user creation (KEY-10 / §6.3).
// Callers pass max_budget=nil explicitly to make the omission visible.
func (c *RESTClient) KeyGenerate(ctx context.Context, req *KeyGenerateRequest) (*KeyGenerateResponse, error) {
    raw, err := c.makeRequest(ctx, "POST", "/key/generate", req)
    if err != nil {
        return nil, err
    }
    var out KeyGenerateResponse
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("litellm: decode POST /key/generate: %w", err)
    }
    return &out, nil
}
```

**Types** (added to `internal/litellm/types.go`):
```go
type KeyGenerateRequest struct {
    UserID       string   `json:"user_id,omitempty"`
    Key          string   `json:"key,omitempty"`            // ACH-generated plaintext
    KeyAlias     string   `json:"key_alias,omitempty"`
    Models       []string `json:"models,omitempty"`
    MaxBudget    *float64 `json:"max_budget,omitempty"`     // ALWAYS nil for ACH
    Tags         []string `json:"tags,omitempty"`
    AccessGroups []string `json:"access_groups,omitempty"`
}

type KeyGenerateResponse struct {
    Key        string `json:"key"`                          // echo of ACH-supplied
    Token      string `json:"token"`                        // LiteLLM-internal hex
    UserID     string `json:"user_id,omitempty"`
    KeyAlias   string `json:"key_alias,omitempty"`
    ExpiresAt  string `json:"expires,omitempty"`            // LiteLLM string format; ACH ignores
}
```

**Divergence notes:**
- The `KeyGenerateResponse.Token` field IS the value Phase 3 INSERT writes into `personal_keys.litellm_token` / `environment_keys.litellm_token` — closes the Phase 02.2 D-02 prerequisite.
- `MaxBudget *float64` (pointer) — Go-idiomatic way to encode "JSON null vs not present" with `omitempty`. Phase 3 ALWAYS passes nil (KEY-10).

**Tests to mirror:** `internal/litellm/team_test.go`.

---

### `internal/litellm/client.go` (MODIFY — extend `Client` interface)

**Analog (the file itself):** Phase 2 widened this interface; Phase 3 widens again, same pattern.

**Add to the existing interface (line 61):**
```go
// Phase 3 — per D-25.

// UserNew issues POST /user/new with {user_email, user_id?, teams}. Used
// by /platform/auth/sso/callback to idempotently provision a LiteLLM user
// after a first SSO login.
UserNew(ctx context.Context, req *UserNewRequest) (*UserInfo, error)

// UserInfoByEmail issues GET /user/info?user_email=<email>. Returns
// (nil, ErrNotFound) when the user is absent per the package's err
// convention.
UserInfoByEmail(ctx context.Context, email string) (*UserInfo, error)

// TeamMemberAdd issues POST /team/member_add. Idempotent client-side;
// LiteLLM treats duplicate add as 4xx, caller swallows.
TeamMemberAdd(ctx context.Context, teamID, userID, role string) error

// KeyGenerate issues POST /key/generate with an ACH-generated `key` value
// and the LiteLLM-side `user_id` / `access_groups` metadata. Response's
// `token` field stores into personal_keys.litellm_token / environment_keys.litellm_token.
KeyGenerate(ctx context.Context, req *KeyGenerateRequest) (*KeyGenerateResponse, error)
```

**Compile-time canary stays at line 107** (verbatim):
```go
var _ Client = (*RESTClient)(nil)
```
Same canary applies in `noop.go` line 104.

**Divergence notes:**
- NO method removed; only additive. Phase 1's `DeleteAccessGroup`/`DeleteTag` and Phase 2's `ListModels`/etc + `ListUserKeys`/`RevokeKey` stay. The build breaks if any implementation drifts (the canary's load-bearing property).

**Tests to mirror:** `internal/litellm/client_test.go` (existing — extend with table cases for the four new methods).

---

### `internal/litellm/noop.go` (MODIFY — stubs for new methods)

**Analog (the file itself):** lines 51-98 — every existing method follows the same "log, return canned value" pattern.

**Add to the bottom (before the canary on line 104):**
```go
// UserNew is the Phase 3 SSO call. NoopClient returns a canned UserInfo with
// the requested email — handler-test invariants check user_email round-trips
// without spinning a real LiteLLM.
func (c *NoopClient) UserNew(_ context.Context, req *UserNewRequest) (*UserInfo, error) {
    c.Log.Info("stub: would create LiteLLM user", "email", req.UserEmail)
    return &UserInfo{UserID: "noop-" + req.UserEmail, UserEmail: req.UserEmail}, nil
}

// UserInfoByEmail returns ErrNotFound by default so first-SSO tests can
// drive the UserNew branch deterministically.
func (c *NoopClient) UserInfoByEmail(_ context.Context, email string) (*UserInfo, error) {
    c.Log.Info("stub: would look up LiteLLM user by email", "email", email)
    return nil, ErrNotFound
}

// TeamMemberAdd is the Phase 3 SSO call after UserNew. NoopClient logs and
// returns nil unconditionally.
func (c *NoopClient) TeamMemberAdd(_ context.Context, teamID, userID, role string) error {
    c.Log.Info("stub: would add LiteLLM team member",
        "teamID", teamID, "userID", userID, "role", role)
    return nil
}

// KeyGenerate echoes the caller-supplied key into the response and synthesizes
// a deterministic token from the user_id so handler tests can assert end-to-end
// without parsing LiteLLM-side opaque tokens.
func (c *NoopClient) KeyGenerate(_ context.Context, req *KeyGenerateRequest) (*KeyGenerateResponse, error) {
    c.Log.Info("stub: would generate LiteLLM key", "userID", req.UserID, "alias", req.KeyAlias)
    return &KeyGenerateResponse{
        Key:    req.Key,
        Token:  "noop-token-" + req.UserID,
        UserID: req.UserID,
    }, nil
}
```

**Divergence notes:**
- `UserInfoByEmail` returns `ErrNotFound` by default — this drives unit tests through the UserNew → TeamMemberAdd path. Tests that need the "user exists" branch can wrap NoopClient with a thin adapter.
- The canary at line 104 stays untouched; build fails until these four methods are added.

**Tests to mirror:** `internal/litellm/client_test.go` `TestNoopClient_*` cases.

---

### `internal/litellm/types.go` (MODIFY — add Phase 3 request/response types)

**Analog (the file itself):** lines 89-148 (`NewTeamRequest`, `UpdateTeamRequest`, `TeamListEntry`, `TeamListResponse`) — same struct convention.

**Append at the bottom:**
```go
// UserNewRequest is the POST /user/new request body.
type UserNewRequest struct {
    UserEmail string   `json:"user_email"`
    UserID    string   `json:"user_id,omitempty"`
    Teams     []string `json:"teams,omitempty"`  // ["default"] per D-04
}

// UserInfo is the response from POST /user/new and GET /user/info.
type UserInfo struct {
    UserID    string `json:"user_id"`
    UserEmail string `json:"user_email"`
    // Other fields exist but ACH doesn't depend on them; add as needed.
}

// TeamMember is the member sub-object of TeamMemberAddRequest.
type TeamMember struct {
    UserID string `json:"user_id"`
    Role   string `json:"role,omitempty"`  // "user" / "admin"
}

// TeamMemberAddRequest is the POST /team/member_add request body.
type TeamMemberAddRequest struct {
    TeamID string     `json:"team_id"`
    Member TeamMember `json:"member"`
}

// KeyGenerateRequest is the POST /key/generate request body.
type KeyGenerateRequest struct {
    UserID       string   `json:"user_id,omitempty"`
    Key          string   `json:"key,omitempty"`             // ACH-generated plaintext
    KeyAlias     string   `json:"key_alias,omitempty"`
    Models       []string `json:"models,omitempty"`
    MaxBudget    *float64 `json:"max_budget,omitempty"`      // ALWAYS nil for ACH (KEY-10)
    Tags         []string `json:"tags,omitempty"`
    AccessGroups []string `json:"access_groups,omitempty"`
}

// KeyGenerateResponse is the response from POST /key/generate.
type KeyGenerateResponse struct {
    Key       string `json:"key"`       // echo of ACH-supplied key
    Token     string `json:"token"`     // LiteLLM-internal opaque hex
    UserID    string `json:"user_id,omitempty"`
    KeyAlias  string `json:"key_alias,omitempty"`
    ExpiresAt string `json:"expires,omitempty"`
}
```

**Divergence notes:**
- Convention from the existing file: required fields without `omitempty`, optional with `omitempty`. Pointer types for optional booleans/numbers where "null vs absent" matters (e.g. `MaxBudget *float64`).
- The `UserKeyInfo` / `ListUserKeysResponse` types (lines 270-290) added in Phase 02.2 stay untouched.

**Tests to mirror:** Existing tests verify JSON marshal round-trip; add similar cases for the new types in `internal/litellm/client_test.go`.

---

### `config/rbac/platformapi_role.yaml` (MODIFY — add `secrets` rule)

**Analog:** `config/rbac/operator_role.yaml` lines 44-46 (Phase 2 D-11 carve-out for the same kind):
```yaml
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

**Phase 3 append (after line 31 of platformapi_role.yaml):**
```yaml
# Phase 3 D-04: Dex client secret rotation observation. Namespace-scoped
# per MULTI-01 (Role, not ClusterRole). Mirrors operator_role.yaml lines 44-46.
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

**Divergence notes:**
- Stays `Role` (not `ClusterRole`). RoleBinding at `platformapi_role_binding.yaml` does NOT change.
- The new rule is the THIRD rules-block in the file (after lines 11-20 read-on-CRDs and lines 25-31 patch-on-external-refs).

**Tests to mirror:** `config/rbac/role.yaml` (existing aggregated test surface — kustomize-generated, regenerated by `make manifests`).

---

### `docker-compose.yml` (MODIFY — add `dex` service)

**Analog:** `docker-compose.yml` lines 50-90 (Phase 02.2 D-04 pattern — `litellm-db` + `litellm` under `profiles: [litellm]`):
```yaml
litellm:
  profiles: ["litellm"]
  image: ghcr.io/berriai/litellm-database:v1.83.10-stable
  container_name: ach-dev-litellm
  depends_on:
    litellm-db:
      condition: service_healthy
  environment:
    LITELLM_MASTER_KEY: sk-spike-mk
    ...
  ports:
    - "${LITELLM_PORT:-14000}:4000"
  ...
```

**Phase 3 append** (under the existing services block, before `volumes:`):
```yaml
dex:
  profiles: ["dex"]
  image: ghcr.io/dexidp/dex:v2.41.1   # (planner picks pinned tag)
  container_name: ach-dev-dex
  volumes:
    - ./scripts/dex-config.yaml:/etc/dex/config.yaml:ro
  ports:
    - "${DEX_PORT:-5556}:5556"
  command: ["dex", "serve", "/etc/dex/config.yaml"]
  healthcheck:
    test: ["CMD-SHELL", "wget -qO- http://localhost:5556/dex/healthz || exit 1"]
    interval: 5s
    timeout: 3s
    retries: 30
    start_period: 10s
```

A companion `scripts/dex-config.yaml` (NEW) mirrors `scripts/litellm-config.yaml`'s shape — small YAML, version-pinned, intended for local UAT only.

**Divergence notes:**
- `profiles: ["dex"]` keeps `make dev-up` (no profile) bringing up only `postgres` + `redis` for day-to-day Phase 3 work.
- The `--profile dex` invocation pattern matches `--profile litellm` (Phase 02.2 D-04). A `make dev-up-dex` Makefile sugar target is at planner discretion.
- NO modifications to existing `postgres`/`redis`/`litellm`/`litellm-db` services.

**Tests to mirror:** `scripts/uat-g1.sh` (Phase 02.2) for the UAT-runner pattern. Phase 3 may ship `scripts/uat-phase3-sso.sh` mirroring the same shape if a live-Dex integration test is in scope (planner's discretion).

---

### `internal/platformapi/render/json.go` (NEW — error envelope helpers)

**Analog:** No existing analog (no JSON-envelope writer in the codebase yet). Specification source: Hub §15.5 + Phase 3 CONTEXT lines 29 + D-02.

**Pattern (NEW — per API-12):**
```go
// Error envelope: {"error": {"code": "<outcome>", "message": "..."}, "request_id": "req_..."}.
// "code" is one of the audit.Outcome* constants — the same closed enum the
// audit channel uses, so logs and HTTP responses share vocabulary.

func Error(w http.ResponseWriter, status int, code, msg string, requestID string) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]any{
        "error":      map[string]string{"code": code, "message": msg},
        "request_id": requestID,
    })
}

func JSON(w http.ResponseWriter, status int, body any) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(body)
}
```

**Divergence notes:**
- Encoder errors are swallowed (`_ =`) — by the time we hit Encode the status has been written and there's no recovery path. Mirrors the `internal/litellm/transport.go` drainAndClose pattern (line 124-130: best-effort).
- `request_id` is read by callers from `ctx.Value(RequestIDKey{})` and passed in explicitly (not inferred from request) so the helper stays decoupled from chi context internals.

**Tests to mirror:** New test file `render/json_test.go` — httptest.ResponseRecorder + JSON-decode assertions on the body shape per §15.5.

---

## Shared Patterns

### Authentication / Authorization

**Source:** `internal/keystore/keystore.go` (NEW — D-07, D-08) + `internal/platformapi/server.go` `authn` middleware
**Apply to:** Every handler under `internal/platformapi/{envkeys,environments,hydrate,admin}/`.
**Excerpt** (target):
```go
func authn(deps Deps) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            plaintext := r.Header.Get("x-ach-key")
            if plaintext == "" {
                render.Error(w, 401, "missing_key", "x-ach-key required", requestIDFromCtx(r.Context()))
                return
            }
            // Resolve via keystore — Redis hit or DB fallback (single-flight).
            info, err := deps.Resolver.Resolve(r.Context(), plaintext)
            if err != nil {
                render.Error(w, 500, "internal_error", "...", requestIDFromCtx(r.Context()))
                return
            }
            if info == nil {
                render.Error(w, 401, "expired_or_revoked", "...", requestIDFromCtx(r.Context()))
                return
            }
            // Discard plaintext from headers (D-19) before handing off.
            r.Header.Del("x-ach-key")
            ctx := WithKeyContext(r.Context(), info)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Error Handling Discipline

**Source:** `internal/db/external_refs.go` lines 178-195 (`isTransientPgErr` — pgconn class 08/57)
**Apply to:** All Phase 3 `internal/db/*.go` files (`personal_keys.go`, `environment_keys.go`, `check_extend.go`, `ek_resolve.go`).
**Excerpt:**
```go
func isTransientPgErr(err error) bool {
    var pgErr *pgconn.PgError
    if !errors.As(err, &pgErr) { return false }
    if len(pgErr.Code) < 2 { return false }
    class := pgErr.Code[:2]
    return class == "08" || class == "57"
}
// Usage in every helper:
if isTransientPgErr(err) { return err }   // raw → controller-runtime backoff
return fmt.Errorf("db: <helper>(%s): %w", id, err)  // wrap with safe identifiers
```

### Audit Emission

**Source:** `internal/orphan/runnable.go` lines 200-237 + `internal/audit/handler.go` lines 44-49 + the new `internal/audit/emit.go` `EmitAudit`.
**Apply to:** Every Phase 3 HTTP handler (8 handler files under `internal/platformapi/`).
**Excerpt:**
```go
audit.EmitAudit(ctx, deps.Audit, audit.Event{
    Action:    audit.ActionEkCreate,
    Outcome:   audit.OutcomeCreated,
    Actor:     actorFromCtx(ctx),          // "<namespace>/<email>" per Hub §18.3
    RequestID: requestIDFromCtx(ctx),       // "req_<ulid>"
    KeyID:     newKeyID,                    // "ekid_..."
    Target:    &audit.Target{Kind: "environment", Name: envName},
})
```

### LiteLLM Client Surface

**Source:** `internal/litellm/team.go` `CreateTeam` (lines 19-29) + `internal/litellm/restclient.go` `makeRequest` (lines 110-168).
**Apply to:** All new LiteLLM-side methods in `internal/litellm/users.go` and `internal/litellm/keygen.go`.
**Excerpt:**
```go
func (c *RESTClient) <Method>(ctx context.Context, req *<Type>) (*<RespType>, error) {
    raw, err := c.makeRequest(ctx, "POST", "/<path>", req)
    if err != nil {
        return nil, err
    }
    var out <RespType>
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("litellm: decode POST /<path>: %w", err)
    }
    return &out, nil
}
```
Every Phase 3 method funnels through `makeRequest` — §9.1 no-body-in-error-strings + REL-04 drain+close + REL-06 typed 401 all enforced automatically.

### Config Plumbing

**Source:** `internal/config/config.go` `MustEnvNonEmpty` (lines 96-102), `EnvOr` (lines 57-62).
**Apply to:** `cmd/platform-api/main.go` rewrite for all Phase 3 env vars (`ACH_BASE_URL`, `ACH_DEX_*`, `ACH_REDIS_*`, `ACH_ADMIN_ALLOWLIST_PATH`, `ACH_PLATFORM_API_BIND_ADDRESS`).
**Excerpt:**
```go
baseURL, err := config.MustEnvNonEmpty("ACH_BASE_URL")
if err != nil { logger.Error("fatal: ACH_BASE_URL required (D-03)", "err", err); os.Exit(1) }
if !strings.HasPrefix(baseURL, "https://") {
    logger.Error("fatal: ACH_BASE_URL must be https:// (CLAUDE.md Security)", "url", baseURL); os.Exit(1)
}
```

### Plaintext-Never-Persisted Discipline

**Source:** `internal/credhash/credhash.go` doc + `internal/db/db.go` lines 70-72 (no url-logging).
**Apply to:** Every code path that touches `pk_*` / `ek_*` plaintext: `auth/sso.go` `CallbackHandler`, `envkeys/handler.go` `CreateHandler`, `keystore/keystore.go`.
**Excerpt (call site invariant):**
```go
// IMMEDIATELY hash and discard:
hash, err := credhash.Hash(deps.Pepper, []byte(plaintext))
if err != nil { /* render 500 */ }
// plaintext stays local; never logged, never stored, never put in audit Extra.
```
The Specifics-block grep gate (scan handler files for `pk_*`/`ek_*` write patterns and fail if found outside `sso.go` and `envkeys/handler.go`) is a CI hook the planner may add as a `scripts/check-plaintext-leak.sh`.

---

## No Analog Found

| File | Role | Data Flow | Reason / Mitigation |
|------|------|-----------|---------------------|
| `internal/platformapi/render/json.go` | utility | n/a | First JSON-envelope writer in the codebase. Spec from Hub §15.5 (already canonical in CONTEXT). Planner writes from spec — no codebase analog needed. |
| `internal/platformapi/auth/sso.go` (PKCE+state cookie path) | controller | request-response | No prior OIDC code. Planner relies on `coreos/go-oidc/v3` documentation + existing `internal/litellm/team.go` JSON-decode idiom. |
| `scripts/dex-config.yaml` | infra config | n/a | No prior Dex config. Planner mirrors `scripts/litellm-config.yaml`'s shape (small YAML, version-pinned, intended for local UAT). |
| The chi.Mux router + middleware composition | controller (HTTP routing) | n/a | chi is a NEW go.mod dep. Planner relies on chi's official `chi.Router` + `middleware.RequestID`/`middleware.Recoverer` examples; the wrapping idiom matches the sister-project's controller-runtime + manager patterns. |

For each "no analog" item, the planner should:
1. Read the spec section listed above.
2. Reference the matching sister-project pattern via `../ach_litellm/`. (Note: no chi/OIDC in sister either — these are net-new to the ACH codebase tree.)
3. Cite the library's official quickstart in the plan's `<read_first>` block when it is the only authoritative reference.

---

## Metadata

**Analog search scope:**
- `/home/jcm/Projects/ach/cmd/`
- `/home/jcm/Projects/ach/internal/{audit,cachefs,config,credhash,db,litellm,orphan,snapshot,sources}/`
- `/home/jcm/Projects/ach/config/rbac/`
- `/home/jcm/Projects/ach/db/migrations/`
- `/home/jcm/Projects/ach/docker-compose.yml`

**Files scanned:** 28 Go files + 6 YAML files + 3 SQL migrations + 4 markdown context docs.

**Pattern extraction date:** 2026-05-19

**Notable not-yet-existing concerns flagged for planner:**
- `chi` HTTP router (D-01) — first router in the codebase; no precedent.
- `coreos/go-oidc/v3` (D-04) — first OIDC integration; no precedent.
- `go-redis/v9` (D-09) — Redis is in `docker-compose.yml` already but no Go client wiring exists yet.
- `oklog/ulid/v2` (Claude's discretion) — first ULID source; no precedent.
- `x/sync/singleflight` (D-07) — first cache-miss-dedup pattern; no precedent.
- `x/oauth2` (D-04) — already indirect dep; promote to direct.

**Carry-forward invariants that the planner MUST NOT relitigate:**
- Audit emitter is `slog.NewJSONHandler` with `audit=true`. NO redaction. NO scrubbing. Composing safe events is the caller's responsibility (audit/doc.go contract).
- Plaintext bearer values (`pk_*`/`ek_*`) NEVER persisted anywhere (DB, Redis, logs, audit, metrics). Only `credential_hash` and `key_id` flow downstream.
- LiteLLM REST exclusively. Phase 3 does NOT read `litellm.ackstorm.ai/*` CRDs.
- `pk_` is permanent first-class on runtime forwarding routes (carry-forward from PROJECT.md / memory `feedback_ach_pk_runtime_first_class`).
- `ACH_LITELLM_*` env-var prefix (Phase 2 D-02) is canonical — no rename.
- HTTPS-only on Platform API ingress (CLAUDE.md Security bullet, qualified by Phase 02.1 / 02.2 D-13).

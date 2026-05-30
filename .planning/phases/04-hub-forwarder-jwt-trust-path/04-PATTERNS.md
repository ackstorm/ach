# Phase 4: Hub Forwarder & JWT Trust Path — Pattern Map

**Mapped:** 2026-05-26
**Files in scope:** 15 (13 new + 2 modified)
**Analogs found:** 13 / 15 (2 are genuinely novel — JWT signer + JWKS handler — see "No Analog Found")

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `cmd/ach/cmd/forwarder.go` (modify) | cobra entrypoint + service wiring | bootstrap | `cmd/ach/cmd/platform_api.go` | exact (same package, same idiom) |
| `internal/forwarder/server.go` | HTTP server builder (chi.Mux) | request-response | `internal/platformapi/server.go` | exact |
| `internal/forwarder/proxy/proxy.go` | `httputil.ReverseProxy` + Director | streaming pass-through | `internal/litellm/transport.go` (RoundTripper) + stdlib `net/http/httputil` | partial — Director pattern is novel to this repo, but RoundTripper hygiene transfers |
| `internal/forwarder/headers/strip.go` | header transform (pure func) | transform | `internal/platformapi/middleware/middleware.go` (statusCapturingWriter + AccessLog redaction) | partial — header-iteration discipline analog |
| `internal/forwarder/jwt/signer.go` | crypto signer interface + impl | transform | `internal/credhash/credhash.go` (interface + HMAC compute, ErrEmpty* refuse-to-construct) | role-match (both are crypto primitives with constructor-time validation) |
| `internal/forwarder/jwt/secret.go` | Secret loader + informer watch + atomic swap | event-driven | `internal/snapshot/snapshot.go` (atomic.Pointer publish, Stale fallback) | role-match (same single-writer / many-reader pattern; different trigger — Secret events vs ticker) |
| `internal/forwarder/jwt/jwks.go` | HTTP handler emitting JWK Set | request-response | `internal/platformapi/render/json.go` (handler-shape JSON marshal) | partial — render is the closest "set Content-Type + WriteHeader + Encode" idiom we have |
| `internal/forwarder/bip/index.go` | informer field-indexer + lookup | request-response | (NONE in this repo) — analog: `internal/platformapi/store/store.go` (cache-backed reader, namespace-scoped) | partial — Store is closest read-only-via-mgr.GetClient analog; `GetFieldIndexer().IndexField` is novel |
| `internal/forwarder/precheck/check.go` | per-route authz helpers | request-response | `internal/platformapi/teams/lookup.go` + `internal/platformapi/store/store.go` | exact — `LookupCallerTeams` IS the pk_ team-intersection helper Phase 4 lifts |
| `internal/forwarder/metrics/counters.go` | no-op metric stubs | transform | (no codebase analog — first counter-stub package) — closest concept: snapshot's `litellmUnreachableCount atomic.Int64` field | partial — the inline `atomic.Int64` Counter idiom is the closest precedent |
| `internal/keystore/teamsresolver.go` | Redis-cached resolver | request-response → cache | `internal/keystore/keystore.go` (redisCachedResolver) + `internal/keystore/dbresolver.go` (interface + constructor + factory) | exact (same package, mirrors the existing pattern verbatim) |
| `config/rbac/forwarder_role.yaml` (modify) | K8s RBAC Role | config | `config/rbac/platformapi_role.yaml` | exact (sister file, same namespace-scoped Role idiom) |
| `deploy/helm/ach/templates/forwarder-deployment.yaml` (modify) | Helm template | config | `deploy/helm/ach/templates/platform-api-deployment.yaml` | exact (sister template; current forwarder template lacks Secret mount + POD_NAMESPACE + traffic port) |
| `docs/runbooks/jwt-key-rotation.md` | operator runbook | docs | (no `docs/runbooks/` subtree exists) — closest: `docs/developer-guide/release-process.md` | role-match (operator-facing procedural doc) |
| `internal/controller/ach/backendidentitypolicy_controller.go` (modify) | reconciler (doc scrub only) | n/a | self — no code change, only doc-comment scrub lines 17–25 + 80–83 | n/a — scope-bounded edit |
| `test/e2e/phase4_invariants_test.go` (new) | e2e suite extension | test | `test/e2e/phase3_invariants_test.go` | exact |

---

## Pattern Assignments

### `cmd/ach/cmd/forwarder.go` (cobra entrypoint, bootstrap)

**Analog:** `cmd/ach/cmd/platform_api.go` (Phase 3, 315 LoC)

**File header + package decl + cobra registration** (lines 1–59):
```go
// SPDX-License-Identifier: Apache-2.0

// `ach platform-api` boots the ACH Hub Platform REST API. … Body lifted
// from ach-old/cmd/platform-api/main.go and adapted to a cobra RunE
// for the single-binary layout.

package cmd

import (
    "context"
    "crypto/tls"
    "errors"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "strings"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/redis/go-redis/v9"
    "github.com/spf13/cobra"
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/runtime"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    clientgoscheme "k8s.io/client-go/kubernetes/scheme"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/cache"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

    achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
    // … per-domain imports
)

var platformAPIScheme = runtime.NewScheme()

func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(platformAPIScheme))
    utilruntime.Must(achv1alpha1.AddToScheme(platformAPIScheme))
    rootCmd.AddCommand(platformAPICmd)
}

var platformAPICmd = &cobra.Command{
    Use:   "platform-api",
    Short: "Run the ACH Platform REST API server (chi + Dex SSO)",
    Long:  `Boot the chi-backed REST API … Refuses to start without ACH_BASE_URL (https://...) …`,
    RunE:  runPlatformAPI,
}
```

**Validated-config struct + validate function** (lines 73–142):
```go
type platformAPIConfig struct {
    BaseURL          string
    DBURL            string
    Pepper           []byte
    LiteLLMBaseURL   string
    LiteLLMMasterKey string
    // …
    BindAddr         string
    Namespace        string
}

func validatePlatformAPIConfig() (*platformAPIConfig, error) {
    cfg := &platformAPIConfig{}
    baseURL, err := config.MustEnvNonEmpty("ACH_BASE_URL")
    if err != nil {
        return nil, fmt.Errorf("ACH_BASE_URL required: %w", err)
    }
    if !strings.HasPrefix(baseURL, "https://") {
        return nil, errors.New("ACH_BASE_URL must be https:// (Hub §9.1 / T-API-04)")
    }
    cfg.BaseURL = baseURL
    // … sequential MustEnvNonEmpty calls; cfg.BindAddr default via EnvOr
    return cfg, nil
}
```

**buildDeps function — pgxpool + redis + litellm + manager.Manager + informers + keystore resolvers** (lines 144–265):
```go
func buildPlatformAPIDeps(ctx context.Context, cfg *platformAPIConfig, logger *slog.Logger) (*platformAPIProcessDeps, error) {
    out := &platformAPIProcessDeps{}

    pool, err := db.Open(ctx, cfg.DBURL)
    if err != nil { return nil, fmt.Errorf("db.Open: %w", err) }
    out.pool = pool

    redisOpts := &redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB}
    if cfg.RedisTLS {
        redisOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec
    }
    out.redis = redis.NewClient(redisOpts)

    liteLLM := litellm.NewRESTClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey, ctrl.Log.WithName("litellm"))

    mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
        Scheme:                 platformAPIScheme,
        LeaderElection:         false,
        HealthProbeBindAddress: "0",
        Metrics:                metricsserver.Options{BindAddress: "0"},
        Cache: cache.Options{
            DefaultNamespaces: map[string]cache.Config{cfg.Namespace: {}},
        },
    })
    if err != nil { return nil, fmt.Errorf("ctrl.NewManager: %w", err) }
    out.manager = mgr

    if _, err := mgr.GetCache().GetInformer(ctx, &corev1.Secret{}); err != nil {
        return nil, fmt.Errorf("informer Secret: %w", err)
    }
    for _, obj := range []client.Object{
        &achv1alpha1.Environment{},
        &achv1alpha1.BackendIdentityPolicy{},
        // …
    } {
        if _, err := mgr.GetCache().GetInformer(ctx, obj); err != nil {
            return nil, fmt.Errorf("informer: %w", err)
        }
    }

    dbResolver, err := keystore.NewDBResolver(pool, cfg.Pepper)
    if err != nil { return nil, fmt.Errorf("keystore.NewDBResolver: %w", err) }
    cachedResolver, err := keystore.NewCachedResolver(dbResolver, out.redis, cfg.Pepper)
    if err != nil { return nil, fmt.Errorf("keystore.NewCachedResolver: %w", err) }

    out.server = platformapi.Deps{ … Resolver: cachedResolver … }
    return out, nil
}
```

**Manager.Add + manager.Start blocking call** (lines 267–314):
```go
func runPlatformAPIServer(ctx context.Context, deps *platformAPIProcessDeps, bindAddr string) error {
    httpHandler := platformapi.New(deps.server)
    runnable := platformapi.NewRunnable(bindAddr, httpHandler, deps.server.Logger)
    if err := deps.manager.Add(runnable); err != nil {
        return fmt.Errorf("manager.Add(serverRunnable): %w", err)
    }
    return deps.manager.Start(ctx)
}

func runPlatformAPI(_ *cobra.Command, _ []string) error {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    ctrl.SetLogger(zap.New(zap.UseDevMode(false))) // suppress reflector warning
    cfg, err := validatePlatformAPIConfig()
    if err != nil { return fmt.Errorf("validateConfig: %w", err) }
    ctx := ctrl.SetupSignalHandler()
    deps, err := buildPlatformAPIDeps(ctx, cfg, logger)
    if err != nil {
        if deps != nil { deps.close() }
        return fmt.Errorf("buildDeps: %w", err)
    }
    defer deps.close()
    if err := runPlatformAPIServer(ctx, deps, cfg.BindAddr); err != nil {
        if !errors.Is(err, http.ErrServerClosed) { return fmt.Errorf("runServer: %w", err) }
    }
    return nil
}
```

**Adaptation notes:**
- Rename throughout: `platformAPI*` → `forwarder*`, `platformAPIConfig` → `forwarderConfig`, etc.
- DROP from cfg: `DexIssuerURL`, `DexClientID`, `DexClientSecret`, `DexRedirectURL`, `AllowlistPath` (forwarder is not an SSO endpoint, has no admin allowlist).
- ADD to cfg: `LiteLLMSharedKey` (reuse `ACH_LITELLM_SHARED_KEY` env from Phase 2), `TrafficBindAddr` (`ACH_FORWARDER_BIND_ADDRESS`, default `:8080`), `HealthBindAddr` (`FORWARDER_HEALTH_BIND_ADDRESS`, default `:8081` — Phase 1 stub carry-forward), `JWTSecretName` (default `ach-jwt-signing-keys`).
- DROP `oidc.NewProvider`/`oauth2.Config`/`admin.LoadAllowlist` blocks entirely.
- DROP `platformStore := store.New(...)` (forwarder has no /platform endpoints).
- ADD informer for `corev1.Secret` filtered by name (`ach-jwt-signing-keys`) — see `internal/forwarder/jwt/secret.go`.
- ADD `mgr.GetFieldIndexer().IndexField(ctx, &achv1alpha1.BackendIdentityPolicy{}, "spec.target", indexFn)` BEFORE the first informer GetInformer call (see `internal/forwarder/bip/index.go`).
- ADD `keystore.TeamsResolver` construction alongside `keystore.NewCachedResolver` (mirrors KeyResolver chain).
- ADD JWT Secret loader construction (`jwt.NewSecretLoader`) + register its informer-event handler.
- REPLACE `platformapi.New(deps.server)` + `platformapi.NewRunnable(...)` with the forwarder's `internal/forwarder.New(deps)` returning the traffic-mux handler, then `mgr.Add(serverRunnable{traffic, health, handler})` — two `http.Server` instances per D-03 dual-port topology.
- ADD `ServerRunnable` variant that owns BOTH `http.Server` instances (traffic on `:8080`, health on `:8081`) — see "ServerRunnable adaptation" below.
- DROP `audit.NewLogger(os.Stdout)` — Forwarder NEVER audits per OBS-01 (CONTEXT §"Phase 4 explicitly excludes").

---

### `internal/forwarder/server.go` (HTTP server builder)

**Analog:** `internal/platformapi/server.go` (Phase 3)

**Deps struct + factory shape** (lines 33–86):
```go
type Deps struct {
    Pool             *pgxpool.Pool   // (forwarder may drop — only Resolver needs DB)
    Redis            *redis.Client
    LiteLLM          litellm.Client
    Pepper           []byte
    K8sClient        client.Client
    Resolver         keystore.Resolver
    TeamsResolver    keystore.TeamsResolver  // NEW for Phase 4
    Signer           jwt.Signer              // NEW for Phase 4
    Logger           *slog.Logger
    BaseURL          string
    Namespace        string
    LiteLLMUpstream  *url.URL                // NEW — parsed ACH_LITELLM_BASE_URL
    LiteLLMSharedKey string                  // NEW — for x-litellm-api-key write
}
```

**Middleware chain (D-02) + route registration** (lines 108–195):
```go
func New(deps Deps) http.Handler {
    r := chi.NewRouter()

    // Middleware chain (D-02 outer → inner) — RequestID, RecoverPanic,
    // AccessLog. Forwarder DROPS ContentTypeJSON middleware (LiteLLM
    // sets its own content type per CONTEXT D-02).
    r.Use(pamw.RequestID)
    r.Use(pamw.RecoverPanic(deps.Logger, nil)) // nil audit — OBS-01
    r.Use(pamw.AccessLog(deps.Logger))

    // Health probes — ONLY routes outside the authn group.
    // /healthz + /livez + /readyz live on the SEPARATE health mux
    // (per D-03 dual-port). This New(...) returns the TRAFFIC mux only.

    // JWKS — anonymous, lives on the traffic mux per D-12.
    r.Get("/.well-known/jwks.json", jwt.JWKSHandler(deps.Signer))

    // Authenticated subtree — Authn middleware reads x-ach-key, resolves
    // via keystore, populates KeyContext.
    r.Group(func(r chi.Router) {
        r.Use(pamw.Authn(deps.Resolver, nil /* no allowlist */, nil /* no audit */))

        // Per-route handlers — each wraps proxy.ServeHTTP after precheck +
        // optional JWT attach.
        r.Handle("/v1/*", proxy.HandlerV1(deps))
        r.Handle("/gemini/*", proxy.HandlerGemini(deps))
        r.Handle("/mcp/{name}/*", proxy.HandlerMCP(deps))
        r.Handle("/a2a/{name}/*", proxy.HandlerA2A(deps))
    })

    return r
}
```

**ServerRunnable adaptation** — copy `internal/platformapi/runnable.go` (lines 1–98) verbatim with these changes:
- Constructor takes TWO bind addresses (`trafficAddr`, `healthAddr`) and TWO handlers.
- Spawn TWO `http.Server` goroutines (per D-03).
- Traffic listener: `WriteTimeout: 0` (D-04 streaming pass-through for SSE on `/v1/chat/completions`).
- Health listener: keep Phase 3 timeouts (`WriteTimeout: 30s` etc.).
- `Start(ctx)` cancellation drains BOTH servers via `srv.Shutdown(ctx, 10s)`.

**Adaptation notes:**
- DROP ContentTypeJSON middleware (forwarder is pass-through on success path; render.Error sets JSON content type on ACH-originated errors only — see error envelope adaptation).
- DROP SSO routes (`auth.LoginHandler`, `auth.CallbackHandler`).
- DROP hydrate/envkeys/environments/admin Mount calls.
- ADD `/v1/*`, `/gemini/*`, `/mcp/{name}/*`, `/a2a/{name}/*` route handlers — each is a thin wrapper around `precheck.Check*` → `bip.ResolveWinner` (only on /mcp /a2a) → `proxy.ServeHTTP`.
- ADD `/.well-known/jwks.json` route OUTSIDE the Authn group (D-02 anonymous carve-out).
- The `readyHandler` lives on the health mux: gates on `mgr.GetCache().WaitForCacheSync(ctx)` AND `signer.Loaded()` (current slot ≥1 valid Ed25519 seed) per CONTEXT D-Discretion "Health probe semantics".

---

### `internal/forwarder/proxy/proxy.go` (httputil.ReverseProxy + Director)

**Analog (partial):** `internal/litellm/transport.go` (RoundTripper hygiene, redaction discipline)

The `httputil.ReverseProxy + Director` shape itself has NO codebase analog (Phase 4 is the first proxy in ACH). The closest peer is the `litellm.redactingRoundTripper` for the "wrap an http transport without leaking secrets in logs" discipline.

**Transport hygiene excerpt** (`internal/litellm/transport.go` lines 38–86):
```go
func (r *redactingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    start := time.Now()
    resp, err := r.base.RoundTrip(req)
    latency := time.Since(start)
    if err != nil {
        // Network/transport error — log ONLY status="error" WITHOUT the
        // err.Error() string itself (it can contain the URL with embedded
        // credentials or master-key fragments leaked via DNS).
        r.log.Info("litellm request",
            "method", req.Method, "path", req.URL.Path,
            "status", "error", "latency_ms", latency.Milliseconds(),
        )
        return resp, err
    }
    r.log.Info("litellm request",
        "method", req.Method, "path", req.URL.Path,
        "status", resp.StatusCode, "latency_ms", latency.Milliseconds(),
    )
    return resp, nil
}
```

**New Phase 4 proxy shape** (no existing analog — pattern lifted from stdlib `net/http/httputil` examples + CONTEXT D-05):
```go
// proxy.New constructs one *httputil.ReverseProxy shared by every route.
// Director rewrites the request to the LiteLLM upstream; ModifyResponse
// is a no-op pass-through (D-05).
func New(deps Deps) *httputil.ReverseProxy {
    return &httputil.ReverseProxy{
        Director: func(req *http.Request) {
            // 1. Set scheme + host from parsed ACH_LITELLM_BASE_URL.
            req.URL.Scheme = deps.LiteLLMUpstream.Scheme
            req.URL.Host   = deps.LiteLLMUpstream.Host
            // 2. Preserve req.URL.Path verbatim (D-05 step 2).
            // 3. Header strip+rewrite (D-06, D-07) via internal/forwarder/headers.
            headers.StripAndRewrite(req.Header, deps.LiteLLMSharedKey, /* litellmToken from KeyContext */)
            // 4. JWT attach handled by per-route handler BEFORE calling proxy.ServeHTTP
            //    (the JWT lives in req.Header by then).
        },
        ModifyResponse: nil, // pass-through (D-05)
        ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
            // Render 502 / 503 envelope per D-21 (json error envelope on
            // ACH-originated failures); never echo upstream body.
            render.Error(w, http.StatusBadGateway, "upstream_error", "upstream unreachable",
                middleware.RequestIDFromCtx(r.Context()))
        },
    }
}
```

**Per-route handler split (CONTEXT D-Discretion "Per-route handler split"):**
```go
func HandlerMCP(deps Deps) http.HandlerFunc {
    rp := New(deps)
    return func(w http.ResponseWriter, r *http.Request) {
        kc, _ := middleware.KeyContextFromCtx(r.Context())
        name := chi.URLParam(r, "name")
        // (a) precheck
        if err := precheck.CheckMCP(r.Context(), kc, name, deps); err != nil {
            // render envelope per outcome → render.Error(...)
            return
        }
        // (b) BIP lookup
        if winner := bip.ResolveWinner(r.Context(), deps.K8sClient, "MCPServer", name, deps.Namespace); winner != nil && winner.Spec.ForwardIdentityJWT {
            token, err := deps.Signer.Sign(r.Context(), jwt.Claims{
                Iss: deps.BaseURL,
                Sub: deps.Namespace + "/" + kc.OwnerEmail,
                Aud: "mcp:" + name,
            })
            if err != nil {
                metrics.IncJWTSuppressed("MCPServer", "signing_failure")
                render.Error(w, http.StatusInternalServerError, "internal_error", "jwt sign failed", middleware.RequestIDFromCtx(r.Context()))
                return
            }
            r.Header.Set("Authorization", "Bearer "+token)
            metrics.IncJWTSigned("MCPServer")
        }
        rp.ServeHTTP(w, r)
    }
}
```

**Adaptation notes:**
- Streaming pass-through: `httputil.ReverseProxy` defaults are correct (does NOT call `io.ReadAll(req.Body)`); Director MUST NOT touch `req.Body` (CONTEXT §"Specific Ideas").
- ErrorHandler: log upstream-unreachable as `status="error"` per litellm transport convention.
- Per-route handler split goes in `proxy/proxy.go` OR per-route files (planner's call) — but the SAME `*httputil.ReverseProxy` instance is shared across all four routes (D-05 "one instance per process").
- All header strip+rewrite happens INSIDE Director — except the `Authorization: Bearer <JWT>` write, which happens in the per-route handler BEFORE `rp.ServeHTTP` (because JWT minting needs the resolved KeyContext + BIP winner, and Director's signature doesn't accept those).

---

### `internal/forwarder/headers/strip.go` (header transform + table-driven tests)

**Analog (partial):** `internal/platformapi/middleware/middleware.go` — `statusCapturingWriter` for the wrapper discipline, `AccessLog` for the no-leak invariant.

No exact analog exists for the case-insensitive prefix-strip pattern. CONTEXT D-06 specifies the full contract; the table-driven test shape lifts from existing tests like `internal/credhash/credhash_test.go` and `internal/db/check_extend_test.go`.

**Pure-func signature target:**
```go
// Package headers ships the D-06/D-07 strip+rewrite contract as a pure
// function so the test surface is exhaustive (case-insensitive prefix
// matches, multi-value Connection tokens, hop-by-hop list per RFC 7230 §6.1).
package headers

// StripAndRewrite mutates h in place: strips client Authorization, every
// x-litellm-* (any case), every x-ach-* (any case), every hop-by-hop
// header per RFC 7230 §6.1 + every header named in incoming Connection
// token list. Then writes x-litellm-api-key + x-litellm-key-id.
func StripAndRewrite(h http.Header, sharedKey string, litellmToken string) {
    // … strip pass (D-06)
    // … write pass (D-07)
}
```

**Test shape — table-driven, lifted from `internal/credhash/credhash_test.go` style:**
```go
func TestStripAndRewrite(t *testing.T) {
    cases := []struct {
        name string
        in   http.Header
        want http.Header
    }{
        {"strip Authorization", …},
        {"strip x-litellm-* case-insensitive", …},
        {"strip x-ach-key", …},
        {"strip hop-by-hop", …},
        {"strip Connection-named tokens", …},
        {"write x-litellm-api-key + x-litellm-key-id", …},
        // ~30 cases per CONTEXT D-23
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { … })
    }
}
```

**Adaptation notes:**
- Use `strings.HasPrefix(strings.ToLower(k), "x-ach-")` etc. for case-insensitive matches; `http.Header` is canonical-case so iterate keys via `for k := range h`.
- Hop-by-hop static list: `Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`.
- Connection-named tokens: parse `req.Header.Values("Connection")`, split on `,`, trim, strip each.
- Pure func with no side effects beyond the passed `http.Header` — keeps Director thin and lets the test suite cover ~30 cases without HTTP plumbing.

---

### `internal/forwarder/jwt/signer.go` (Ed25519 JWT signer interface + impl)

**Analog (role-match):** `internal/credhash/credhash.go` (crypto primitive package with constructor-time validation + `ErrEmpty*` refuse-to-construct pattern)

**Constructor-time validation pattern** (`internal/credhash/credhash.go` lines 13–38):
```go
var ErrEmptyPepper = errors.New("credhash: pepper is empty")

func Hash(pepper, plaintext []byte) (string, error) {
    if len(pepper) == 0 {
        return "", ErrEmptyPepper
    }
    h := hmac.New(sha256.New, pepper)
    h.Write(plaintext)
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

**Phase 4 signer interface shape (novel — lift jwt/v5 usage from CONTEXT D-11):**
```go
package jwt

import (
    "crypto/ed25519"
    "github.com/golang-jwt/jwt/v5"
)

// Signer is the contract every forwarder handler types its JWT dependency
// as. Production wires *Ed25519Signer with the current Secret slot.
type Signer interface {
    Sign(ctx context.Context, claims Claims) (string, error)
    JWKS() []JWK            // current + optional next, for /.well-known/jwks.json
    Loaded() bool           // readiness gate — true once current slot loaded
}

type Claims struct {
    Iss, Sub, Aud string
    // iat + exp synthesized inside Sign (iat=now, exp=iat+120s) per FWD-07
}

var ErrEmptySeed   = errors.New("jwt: ed25519 seed must be exactly 32 bytes")
var ErrEmptyKid    = errors.New("jwt: kid must be non-empty")

type Ed25519Signer struct {
    current atomic.Pointer[signerSlot]
    next    atomic.Pointer[signerSlot] // may be nil
}

type signerSlot struct {
    kid     string
    priv    ed25519.PrivateKey   // 64-byte form = seed||pub
    pub     ed25519.PublicKey
}

func newSignerSlot(kid string, seed []byte) (*signerSlot, error) {
    if kid == "" { return nil, ErrEmptyKid }
    if len(seed) != ed25519.SeedSize {
        return nil, ErrEmptySeed
    }
    priv := ed25519.NewKeyFromSeed(seed)
    return &signerSlot{kid: kid, priv: priv, pub: priv.Public().(ed25519.PublicKey)}, nil
}

func (s *Ed25519Signer) Sign(_ context.Context, c Claims) (string, error) {
    slot := s.current.Load()
    if slot == nil { return "", errors.New("jwt: no current slot loaded") }
    now := time.Now().Unix()
    token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
        "iss": c.Iss, "sub": c.Sub, "aud": c.Aud,
        "iat": now, "exp": now + 120,
    })
    token.Header["kid"] = slot.kid
    return token.SignedString(slot.priv)
}
```

**Adaptation notes:**
- Mirror `credhash`'s "refuse to construct on zero-length pepper" → "refuse to construct on len(seed) != 32".
- `atomic.Pointer[signerSlot]` chosen over RWMutex for sub-ns Load() on the hot path (CONTEXT D-Discretion).
- ALWAYS sign with `current`; `next` is published in JWKS only (rotation overlap).
- `time.Now().Unix()` for `iat`; `+120` for `exp` (FWD-07 / Hub §9.1).
- Header injection: `token.Header["kid"] = slot.kid` BEFORE `SignedString`.

---

### `internal/forwarder/jwt/secret.go` (Secret loader + informer-driven atomic swap)

**Analog (role-match):** `internal/snapshot/snapshot.go` (single-writer atomic.Pointer publication, stale fallback)

**atomic.Pointer publication pattern** (`internal/snapshot/snapshot.go` lines 70–102, 157–216):
```go
type Snapshotter struct {
    client                  litellm.Client
    interval                time.Duration
    snap                    atomic.Pointer[LiteLLMSnapshot]
    log                     logr.Logger
    litellmUnreachableCount atomic.Int64
}

func (s *Snapshotter) Snapshot() LiteLLMSnapshot {
    if p := s.snap.Load(); p != nil { return *p }
    return LiteLLMSnapshot{}
}

func (s *Snapshotter) refresh(ctx context.Context) {
    // … fetch data
    next := &LiteLLMSnapshot{ … RefreshedAt: time.Now() }
    s.snap.Store(next)
    s.log.Info("litellm snapshot refreshed", "models", len(next.Models), …)
}
```

**Phase 4 secret loader adaptation:**
```go
// SecretLoader watches the ach-jwt-signing-keys Secret via informer
// and atomically swaps the *Ed25519Signer's slots on event.
type SecretLoader struct {
    signer    *Ed25519Signer
    namespace string
    name      string  // "ach-jwt-signing-keys"
    log       logr.Logger
}

func NewSecretLoader(signer *Ed25519Signer, ns, name string, log logr.Logger) *SecretLoader { … }

// Reload reads the Secret, extracts current.kid/current.seed (+ optional
// next.{kid,seed}), constructs fresh signerSlot pointers, and atomic-swaps
// into the signer. Returns error on missing/malformed current slot
// (refuse-to-load, not refuse-to-start — startup uses a separate one-shot
// LoadOnce that DOES refuse-to-start).
func (l *SecretLoader) Reload(secret *corev1.Secret) error {
    curKid  := string(secret.Data["current.kid"])
    curSeed := secret.Data["current.seed"]
    curSlot, err := newSignerSlot(curKid, curSeed)
    if err != nil { return fmt.Errorf("jwt secret: current: %w", err) }
    l.signer.current.Store(curSlot)
    // Optional next slot
    if nxtKid := string(secret.Data["next.kid"]); nxtKid != "" {
        if nxtSlot, err := newSignerSlot(nxtKid, secret.Data["next.seed"]); err == nil {
            l.signer.next.Store(nxtSlot)
        }
    } else {
        l.signer.next.Store(nil) // clear after rotation completes
    }
    l.log.Info("jwt signing keys reloaded", "current.kid", curKid, "next.present", l.signer.next.Load() != nil)
    return nil
}
```

**Wire into manager via informer event handler (in `cmd/ach/cmd/forwarder.go` buildDeps):**
```go
secretInformer, err := mgr.GetCache().GetInformer(ctx, &corev1.Secret{})
if err != nil { return nil, err }
_, err = secretInformer.AddEventHandler(toolscache.FilteringResourceEventHandler{
    FilterFunc: func(obj interface{}) bool {
        s, ok := obj.(*corev1.Secret)
        return ok && s.Name == cfg.JWTSecretName && s.Namespace == cfg.Namespace
    },
    Handler: toolscache.ResourceEventHandlerFuncs{
        AddFunc:    func(obj interface{}) { _ = loader.Reload(obj.(*corev1.Secret)) },
        UpdateFunc: func(_, obj interface{}) { _ = loader.Reload(obj.(*corev1.Secret)) },
    },
})
```

**Adaptation notes:**
- Snapshot's `atomic.Pointer[LiteLLMSnapshot]` is the EXACT lock-free publication idiom Phase 4 needs.
- Event-driven (informer events) replaces snapshot's timer-driven `ticker.C` loop.
- Refuse-to-start (LoadOnce) at process start; refuse-to-update (silent error log + keep prior slot) on informer events — drift in either direction surfaces in logs without dropping traffic.

---

### `internal/forwarder/jwt/jwks.go` (JWKS HTTP handler)

**Analog (partial):** `internal/platformapi/render/json.go` (Content-Type + WriteHeader + Encode pattern)

**Pattern excerpt** (`internal/platformapi/render/json.go` lines 23–38):
```go
const contentType = "application/json; charset=utf-8"

func JSON(w http.ResponseWriter, status int, body any) {
    w.Header().Set("Content-Type", contentType)
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(body)
}
```

**Phase 4 JWKS handler (novel — shape per CONTEXT D-12):**
```go
type jwk struct {
    Kty string `json:"kty"`            // always "OKP"
    Crv string `json:"crv"`            // always "Ed25519"
    Use string `json:"use,omitempty"`  // "sig"
    Alg string `json:"alg,omitempty"`  // "EdDSA"
    Kid string `json:"kid"`
    X   string `json:"x"`              // base64url(pub-32B), no padding
}

type jwks struct { Keys []jwk `json:"keys"` }

func JWKSHandler(signer Signer) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        out := jwks{Keys: signer.JWKS()}
        w.Header().Set("Content-Type", "application/jwk-set+json")
        w.Header().Set("Cache-Control", "public, max-age=3600") // §9.2
        w.WriteHeader(http.StatusOK)
        _ = json.NewEncoder(w).Encode(out)
    }
}

// In Ed25519Signer:
func (s *Ed25519Signer) JWKS() []jwk {
    var out []jwk
    if cur := s.current.Load(); cur != nil { out = append(out, slotToJWK(cur)) }
    if nxt := s.next.Load(); nxt != nil    { out = append(out, slotToJWK(nxt)) }
    return out
}

func slotToJWK(s *signerSlot) jwk {
    return jwk{
        Kty: "OKP", Crv: "Ed25519",
        Use: "sig", Alg: "EdDSA",
        Kid: s.kid,
        X:   base64.RawURLEncoding.EncodeToString(s.pub), // unpadded base64url
    }
}
```

**Adaptation notes:**
- Use `base64.RawURLEncoding` (NOT `base64.URLEncoding`) — JWK spec mandates unpadded.
- Cache-Control header is verbatim per Hub §9.2.
- Anonymous — register OUTSIDE Authn group in server.go.
- The 4 fields (`kty`, `crv`, `kid`, `x`) are REQUIRED; `use` and `alg` are RECOMMENDED per FWD-08.

---

### `internal/forwarder/bip/index.go` (controller-runtime IndexField + alpha-LAST lookup)

**Analog (partial):** `internal/platformapi/store/store.go` (cache-backed reader with namespace-scoped client)

`IndexField` itself has NO codebase precedent (search confirmed: zero `IndexField` or `GetFieldIndexer` usages in `/home/jcm/Projects/ach` or `/home/jcm/Projects/ach-old/internal`). The pattern below lifts from CONTEXT D-09 + controller-runtime upstream docs.

**Store-shape (read-only via cached client) excerpt** (`internal/platformapi/store/store.go` lines 35–80):
```go
type Store struct {
    client client.Client  // cache-backed (mgr.GetClient() after WaitForCacheSync)
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
        return nil, fmt.Errorf("store: GetEnvironment(%s): %w", name, err)
    }
    return env, nil
}
```

**Phase 4 BIP index registration + lookup (novel; per CONTEXT D-09):**
```go
package bip

const TargetIndexKey = "spec.target" // index field name registered with controller-runtime

// RegisterIndex MUST be called BEFORE mgr.Start (and BEFORE the first
// GetInformer call). It teaches the cache to index BIPs by
// "<kind>/<name>" so List(ctx, …, MatchingFields{TargetIndexKey: "MCPServer/foo"})
// hits an O(log N) lookup instead of a full namespace scan.
func RegisterIndex(ctx context.Context, mgr ctrl.Manager) error {
    return mgr.GetFieldIndexer().IndexField(ctx, &achv1alpha1.BackendIdentityPolicy{},
        TargetIndexKey,
        func(obj client.Object) []string {
            bip := obj.(*achv1alpha1.BackendIdentityPolicy)
            return []string{string(bip.Spec.Target.Kind) + "/" + bip.Spec.Target.Name}
        })
}

// ResolveWinner returns the alphabetically-LAST BIP for the given target,
// or nil when no BIP exists or the winner has ForwardIdentityJWT=false.
// Mirrors CONTEXT D-09 verbatim.
func ResolveWinner(ctx context.Context, c client.Client, kind, name, ns string) *achv1alpha1.BackendIdentityPolicy {
    var list achv1alpha1.BackendIdentityPolicyList
    if err := c.List(ctx, &list,
        client.MatchingFields{TargetIndexKey: kind + "/" + name},
        client.InNamespace(ns)); err != nil {
        return nil
    }
    if len(list.Items) == 0 { return nil }
    sort.SliceStable(list.Items, func(i, j int) bool {
        return list.Items[i].Name < list.Items[j].Name
    })
    winner := list.Items[len(list.Items)-1] // alpha-LAST
    if !winner.Spec.ForwardIdentityJWT { return nil }
    return &winner
}
```

**Adaptation notes:**
- `RegisterIndex` MUST be called from `cmd/ach/cmd/forwarder.go`'s buildDeps AFTER `ctrl.NewManager` but BEFORE the first `mgr.GetCache().GetInformer(ctx, ...)` call (controller-runtime requirement).
- Use `client.MatchingFields` (not `client.ListOption{}` with arbitrary selectors) — only IndexField-registered keys work here.
- Alpha-LAST per CONTEXT D-09 (`list.Items[len(list.Items)-1]` after sort ASC) — NOT alpha-FIRST.
- `ForwardIdentityJWT=false` returns nil (explicit opt-out, equivalent to no policy) per CONTEXT D-09.

---

### `internal/forwarder/precheck/check.go` (ek_ + pk_ pre-check helpers)

**Analog (exact):** `internal/platformapi/teams/lookup.go` (pk_ team-intersection helper) + `internal/platformapi/store/store.go` (Environment read)

**Pattern excerpt** (`internal/platformapi/teams/lookup.go` lines 1–62):
```go
package teams

func LookupCallerTeams(ctx context.Context, ll litellm.Client, email string) ([]string, error) {
    info, err := ll.UserInfoByEmail(ctx, email)
    if err != nil {
        if isNotFound(err) { return []string{}, nil }
        return nil, err  // → 503 litellm_unreachable upstream
    }
    if info == nil || len(info.Teams) == 0 { return []string{}, nil }
    return info.Teams, nil
}

func isNotFound(err error) bool {
    if errors.Is(err, litellm.ErrNotFound) { return true }
    if err == nil { return false }
    return strings.Contains(err.Error(), "404")
}
```

**Comment from teams/doc.go (the "Phase 4 takes this verbatim" note):**
```go
// The Phase 3 implementation calls LiteLLM directly on every invocation
// — no caching. The Phase 4 Forwarder will replace the body with a
// Redis-cached variant sharing the keystore 60s TTL (Hub §5.1 / FWD-02
// — "the cached Team-membership lookup, ≤60s TTL — same cache the
// Forwarder uses in Phase 4").
```

**Phase 4 precheck adaptation:**
```go
package precheck

type Deps struct {
    Store         *store.Store
    TeamsResolver keystore.TeamsResolver // Redis-cached variant (D-17)
}

var (
    ErrUnauthorizedResource = errors.New("unauthorized_resource")
    ErrUnauthorizedTeam     = errors.New("unauthorized_team")
    ErrLiteLLMUnreachable   = errors.New("litellm_unreachable")
    ErrEnvironmentNotFound  = errors.New("environment_not_found")
)

// CheckMCP runs the §5.1 step-4 pre-check for /mcp/<name>:
//   ek_: name must appear in Environment.spec.runtime.mcpServers[]
//   pk_: caller's Teams must intersect spec.authorizedTeams[] of an
//        Environment hosting <name>
func CheckMCP(ctx context.Context, kc middleware.KeyContext, name string, deps Deps) error {
    switch kc.KeyType {
    case keys.PrefixEk:
        env, err := deps.Store.GetEnvironment(ctx, kc.Environment)
        if err != nil { return err }
        if env == nil { return ErrEnvironmentNotFound }
        if env.DeletionTimestamp != nil { return ErrUnauthorizedResource }
        for _, m := range env.Spec.Runtime.MCPServers { if m == name { return nil } }
        return ErrUnauthorizedResource
    case keys.PrefixPk:
        teams, err := deps.TeamsResolver.Resolve(ctx, kc.OwnerEmail)
        if err != nil { return ErrLiteLLMUnreachable }
        // List envs hosting <name>; intersect with caller's teams
        // … same shape as platformapi/hydrate's authorizeTeam check
    }
    return ErrUnauthorizedResource
}
```

**Adaptation notes:**
- `ek_` path is a single informer cache hit (`store.GetEnvironment` + slice scan) — O(1), no LiteLLM round-trip.
- `pk_` path uses `keystore.TeamsResolver` (D-17 Redis-cached) — NOT `teams.LookupCallerTeams` directly. The teams package's doc comment explicitly forecasts this replacement.
- Errors are TYPED sentinels (not strings) — the per-route handler maps each to the right HTTP status + outcome code (`render.Error(w, 403, "unauthorized_resource", …)`).
- DeletionTimestamp check renders `403 unauthorized_resource` per CONTEXT D-15 pre-decision (narrow error surface).

---

### `internal/forwarder/metrics/counters.go` (no-op counter stubs)

**Analog (partial):** `internal/snapshot/snapshot.go` `litellmUnreachableCount atomic.Int64` field — the only existing "metric stub" pattern in the codebase.

**Pattern excerpt** (`internal/snapshot/snapshot.go` lines 70–76, 104–111):
```go
type Snapshotter struct {
    // …
    litellmUnreachableCount atomic.Int64
}

// LiteLLMUnreachableCount returns the cumulative number of refresh
// attempts that failed because LiteLLM was unreachable. Phase 5 wires
// this counter into the litellm_unreachable_total{caller="operator"}
// Prometheus counter per Hub §18.5.
func (s *Snapshotter) LiteLLMUnreachableCount() int64 {
    return s.litellmUnreachableCount.Load()
}
```

**Phase 4 stub shape (CONTEXT D-18 + D-Discretion "Counter-hook package"):**
```go
// Package metrics declares Phase 4 counter-hook stubs (D-18). Phase 5
// (OBS-03..06) replaces the stub bodies with real
// prometheus.CounterVec.WithLabelValues(...).Inc() calls.
package metrics

// IncRequests increments forwarder_requests_total{route, key_type, outcome}.
// Stub today; Phase 5 wires Prometheus.
func IncRequests(route, keyType, outcome string) { /* no-op */ }

// IncJWTSigned increments forwarder_jwt_signed_total{kind}.
func IncJWTSigned(kind string) { /* no-op */ }

// IncJWTSuppressed increments forwarder_jwt_suppressed_total{kind, reason}.
func IncJWTSuppressed(kind, reason string) { /* no-op */ }

// IncLiteLLMUnreachable increments litellm_unreachable_total{caller="forwarder"}.
func IncLiteLLMUnreachable() { /* no-op */ }
```

**Adaptation notes:**
- Bodies stay `/* no-op */` for Phase 4; Phase 5 fills them in.
- Call sites (in middleware, per-route handlers, precheck) emit these even though they no-op — keeps the Phase 5 transition a body-only edit, never a call-site edit.
- DO NOT use `atomic.Int64` here — the snapshotter's counter is exposed via `LiteLLMUnreachableCount()` for tests; Phase 4 forwarder counters don't need to be inspected from tests (Phase 5's `/metrics` endpoint is the inspection surface).

---

### `internal/keystore/teamsresolver.go` (new TeamsResolver, mirrors KeyResolver)

**Analog (exact):** `internal/keystore/keystore.go` (redisCachedResolver) + `internal/keystore/dbresolver.go` (interface + constructor + factory)

**Pattern excerpt — interface, constructor-time validation, single-flight cache** (`internal/keystore/keystore.go` lines 32–110):
```go
var ErrEmptyPepper = errors.New("keystore: pepper is empty")

type Resolver interface {
    Resolve(ctx context.Context, plaintext string) (*KeyInfo, error)
}

type redisCachedResolver struct {
    inner  Resolver
    redis  *redis.Client
    pepper []byte
    sf     singleflight.Group
    ttl    time.Duration
}

func NewCachedResolver(inner Resolver, redisClient *redis.Client, pepper []byte) (Resolver, error) {
    if len(pepper) == 0 { return nil, ErrEmptyPepper }
    if inner == nil    { return nil, errors.New("keystore: nil inner resolver") }
    if redisClient == nil { return nil, errors.New("keystore: nil redis client") }
    return &redisCachedResolver{
        inner:  inner,
        redis:  redisClient,
        pepper: append([]byte(nil), pepper...),
        ttl:    defaultTTL,
    }, nil
}
```

**Cache hit/miss flow** (`internal/keystore/keystore.go` lines 127–166):
```go
func (r *redisCachedResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
    hash, err := credhash.Hash(r.pepper, []byte(plaintext))
    if err != nil { return nil, err }
    cacheKey := cacheKeyPrefix + hash

    if raw, getErr := r.redis.Get(ctx, cacheKey).Bytes(); getErr == nil {
        var info KeyInfo
        if jsonErr := json.Unmarshal(raw, &info); jsonErr == nil { return &info, nil }
    }

    v, sfErr, _ := r.sf.Do(hash, func() (any, error) {
        return r.inner.Resolve(ctx, plaintext)
    })
    if sfErr != nil { return nil, sfErr }
    info, _ := v.(*KeyInfo)
    if info == nil { return nil, nil } // never cache nil

    if b, marshalErr := json.Marshal(info); marshalErr == nil {
        _ = r.redis.Set(ctx, cacheKey, b, r.ttl).Err()
    }
    return info, nil
}
```

**Phase 4 TeamsResolver adaptation (per CONTEXT D-17):**
```go
package keystore

const teamsCacheKeyPrefix = "ach:teams:" // separate keyspace (D-17)

type TeamsResolver interface {
    Resolve(ctx context.Context, ownerEmail string) ([]string, error)
}

// liteLLMTeamsResolver — base resolver wraps litellm.Client.
type liteLLMTeamsResolver struct {
    ll litellm.Client
}

func (r *liteLLMTeamsResolver) Resolve(ctx context.Context, email string) ([]string, error) {
    info, err := r.ll.UserInfoByEmail(ctx, email)
    if err != nil {
        if errors.Is(err, litellm.ErrNotFound) { return []string{}, nil }
        return nil, err
    }
    if info == nil || len(info.Teams) == 0 { return []string{}, nil }
    return info.Teams, nil
}

// redisCachedTeamsResolver wraps the base resolver with Redis + single-flight.
type redisCachedTeamsResolver struct {
    base  TeamsResolver
    rdb   *redis.Client
    sf    singleflight.Group
    ttl   time.Duration
}

func NewCachedTeamsResolver(base TeamsResolver, rdb *redis.Client) (TeamsResolver, error) {
    if base == nil { return nil, errors.New("keystore: nil base teams resolver") }
    if rdb == nil  { return nil, errors.New("keystore: nil redis client") }
    return &redisCachedTeamsResolver{
        base: base, rdb: rdb, ttl: defaultTTL, // 60s — same ceiling as KeyResolver
    }, nil
}

func (r *redisCachedTeamsResolver) Resolve(ctx context.Context, email string) ([]string, error) {
    key := teamsCacheKeyPrefix + email
    if raw, err := r.rdb.Get(ctx, key).Bytes(); err == nil {
        var teams []string
        if jsonErr := json.Unmarshal(raw, &teams); jsonErr == nil { return teams, nil }
    }
    v, sfErr, _ := r.sf.Do(email, func() (any, error) {
        return r.base.Resolve(ctx, email)
    })
    if sfErr != nil { return nil, sfErr }
    teams := v.([]string)
    if b, err := json.Marshal(teams); err == nil {
        _ = r.rdb.Set(ctx, key, b, r.ttl).Err()
    }
    return teams, nil
}
```

**Adaptation notes:**
- LIFT verbatim: constructor-time validation, `singleflight.Group`, `defaultTTL = 60s` ceiling.
- Cache key prefix is `ach:teams:` (parallel keyspace per D-17; existing KeyResolver uses `ach:key:`).
- Cache key SHAPE difference: KeyResolver hashes the plaintext (secret material); TeamsResolver uses `email` directly (non-secret). NO peppering needed.
- DO cache empty-slice results (`[]string{}` is a valid LiteLLM "user has no teams" answer; not a sentinel for unknown).
- Phase 5 Content Service (CS-04) reuses this verbatim per CONTEXT §"Code Insights".
- Compile-time canary at bottom of file: `var _ TeamsResolver = (*redisCachedTeamsResolver)(nil)` (mirrors the existing `var _ Resolver = (*xxxResolver)(nil)` canary the Phase 3 keystore uses).

---

### `config/rbac/forwarder_role.yaml` (modify — already exists with minimal rules)

**Analog (exact):** `config/rbac/platformapi_role.yaml`

**Existing forwarder_role.yaml** (current state):
```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ach-forwarder-role
  namespace: system
  labels:
    app.kubernetes.io/name: ach
    app.kubernetes.io/component: forwarder
rules:
- apiGroups: ["ach.ackstorm.ai"]
  resources:
  - environments
  - backendidentitypolicies
  verbs: ["get", "list", "watch"]
```

**Pattern from platformapi_role.yaml — Secret read rule** (lines 32–40):
```yaml
# Phase 3 D-04 / D-06: Dex client secret rotation observation. The
# Platform API's OIDC provider re-reads the Dex client secret from a
# K8s Secret on rotation; the informer-backed read is what makes that
# rotation a hot-reload rather than a Pod restart. Namespace-scoped per
# MULTI-01 (Role, not ClusterRole). Mirrors operator_role.yaml's
# Phase 2 D-11 Secret rule.
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

**Phase 4 ADD (per CONTEXT D-22 — least-privilege via resourceNames):**
```yaml
# Phase 4 D-22: Forwarder reads ONLY the ach-jwt-signing-keys Secret.
# resourceNames carve-out enforces SC#4 "only the Forwarder
# ServiceAccount can read" the signing material. Namespace-scoped
# (Role, not ClusterRole) per MULTI-01.
- apiGroups: [""]
  resources: ["secrets"]
  resourceNames: ["ach-jwt-signing-keys"]
  verbs: ["get", "list", "watch"]
```

**Adaptation notes:**
- DO use `resourceNames` to scope to the single Secret — DIFFERENT from platformapi (which watches all Secrets for Dex client rotation).
- Keep `Role` (namespace-scoped) per MULTI-01 — never `ClusterRole`. NOTE: the existing Helm template (`deploy/helm/ach/templates/forwarder-rbac.yaml`) currently uses `ClusterRole` for the forwarder; this is a BUG to fix in Phase 4 OR a deliberate carry-over decision the planner should reconcile.
- Mirror the Helm template (`deploy/helm/ach/templates/forwarder-rbac.yaml`) to match the kustomize file: change ClusterRole→Role + add the Secret rule.
- The Phase 4 RBAC update also lands in `deploy/helm/ach/templates/forwarder-rbac.yaml` (planner decides whether one or two PR edits).

---

### `deploy/helm/ach/templates/forwarder-deployment.yaml` (modify — current is minimal)

**Analog (exact):** `deploy/helm/ach/templates/platform-api-deployment.yaml`

**Current forwarder template** missing (per CONTEXT §"Specifics" + D-Discretion):
- `POD_NAMESPACE` env from downward API.
- `ach-jwt-signing-keys` Secret volume mount.
- Traffic port (8080) — currently only health (8081) is exposed.
- Service for cluster ingress.

**Pattern excerpt — POD_NAMESPACE injection** (`deploy/helm/ach/templates/platform-api-deployment.yaml` lines 68–75):
```yaml
env:
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
  {{- with .Values.extraEnv }}
  {{- toYaml . | nindent 12 }}
  {{- end }}
```

**Pattern excerpt — Service + volume mount** (lines 88–105 + 56–59 + 77–80):
```yaml
volumes:
  - name: admins
    configMap:
      name: ach-platform-api-admins
# … containers:
volumeMounts:
  - name: admins
    mountPath: /etc/ach/admins
    readOnly: true
# …
apiVersion: v1
kind: Service
metadata:
  name: ach-platform-api
  namespace: {{ .Release.Namespace }}
spec:
  selector:
    app.kubernetes.io/name: ach
    app.kubernetes.io/component: platform-api
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

**Phase 4 ADD blocks (per CONTEXT §"Specifics" pre-decision: mount Secret as files):**
```yaml
volumes:
  - name: jwt-keys
    secret:
      secretName: ach-jwt-signing-keys
      optional: false   # refuse-to-start when missing per CONTEXT D-10
# …
ports:
  - name: traffic
    containerPort: 8080
  - name: healthz
    containerPort: 8081
env:
  - name: POD_NAMESPACE
    valueFrom: { fieldRef: { fieldPath: metadata.namespace } }
  - name: ACH_BASE_URL
    value: {{ required "forwarder.baseUrl required" .Values.forwarder.baseUrl | quote }}
  - name: ACH_LITELLM_BASE_URL
    value: {{ required "forwarder.litellmBaseUrl required" .Values.forwarder.litellmBaseUrl | quote }}
  - name: ACH_LITELLM_SHARED_KEY
    valueFrom:
      secretKeyRef:
        name: {{ .Values.forwarder.litellmSharedKeySecret.name }}
        key:  {{ .Values.forwarder.litellmSharedKeySecret.key }}
  # … DB + Redis env vars per Phase 3 pattern
volumeMounts:
  - name: jwt-keys
    mountPath: /etc/ach/jwt
    readOnly: true
readinessProbe:
  httpGet: { path: /readyz, port: 8081 }   # gates on cache + Secret loaded
livenessProbe:
  httpGet: { path: /livez, port: 8081 }
# … add Service exposing port 8080 → traffic
```

**Adaptation notes:**
- Reference `deploy/helm/ach/templates/platform-api-deployment.yaml` LINE BY LINE for the canonical injection pattern; the existing forwarder-deployment.yaml is a minimal stub.
- Mount Secret as volume (NOT envFrom) — Ed25519 seed is 32 binary bytes, env-var encoding forces base64 indirection (CONTEXT §"Specifics" pre-decision).
- Service exposes port 8080 → cluster ingress; port 8081 stays internal (probes only).
- `readinessProbe` hits `/readyz` (NEW for Phase 4 — Phase 1 stub used `/healthz` for both). Phase 4 `/readyz` gates on `mgr.WaitForCacheSync` + signer loaded.
- WORK ITEM PER PLANNER: `forwarder-rbac.yaml` currently uses `ClusterRole`; CONTEXT D-22 says `Role` (namespace-scoped). The mismatch with `config/rbac/forwarder_role.yaml` (which IS a Role) needs reconciling.

---

### `docs/runbooks/jwt-key-rotation.md` (new — no analog directory)

**Analog (role-match):** `docs/developer-guide/release-process.md` (operator-facing procedural doc)

No `docs/runbooks/` directory exists. Create it. The closest mkdocs-rendered procedural doc is `docs/developer-guide/release-process.md`.

**Content target — verbatim from CONTEXT D-14:**
1. Generate new keypair (Go snippet).
2. `kubectl patch secret ach-jwt-signing-keys --type=json -p='[…next.kid…next.seed…]'`.
3. Forwarder informer reloads → JWKS publishes both slots.
4. Wait ≥24h (≥24× the `max-age=3600` JWKS cache TTL).
5. Promote `next` → `current`.
6. Forwarder reloads; JWTs now signed with new `current.kid`.

**Adaptation notes:**
- Add `docs/runbooks/` to the mkdocs nav (`mkdocs.yml`) so the page is discoverable; planner decides whether to backfill an `index.md` for the section.
- Lift verbatim from CONTEXT §"Specific Ideas" Ed25519 keygen snippet.
- Cross-link from Hub §9.2 docstring once the operator-facing render exists.

---

### `internal/controller/ach/backendidentitypolicy_controller.go` (modify — doc scrub only)

**No analog needed — surgical edit.**

**Current stale comments to scrub:**

Lines 17–25 (Phase 1 doc forecasting Phase-4 DuplicateTarget logic):
```go
// finalizer exists for consistency with the other six kinds and so
// Phase 4 can layer real Synced=DuplicateTarget reconciliation on
// top without a CRD migration.
```

Lines 60–63 (inline forecast inside Reconcile):
```go
// No PVC file to clean. Phase 4 may need to invalidate a
// Forwarder cache entry here; Phase 1 just removes the
// finalizer so K8s deletion can complete.
```

Lines 80–84 (Phase 1 closing comment):
```go
// Steady state — no status write in Phase 1 (Synced=DuplicateTarget
// is Phase 4's owner; CRD-07 doesn't admit an "Initializing" reason
// for this kind).
```

**Adaptation notes:**
- Reconciler BODY unchanged. Only the three doc-comment blocks above get rewritten.
- Replacement text should reference TODO.md §6 + `[[feedback_bip_no_shadow_logic.md]]` and state the permanent design decision (no `Synced=DuplicateTarget` ever, alpha-LAST winner resolved by Forwarder at READ time).
- The `+kubebuilder:rbac:groups=...,resources=backendidentitypolicies/status` marker on line 41 STAYS — the Operator IS the sole status writer per OP-16, it just never writes `DuplicateTarget`.

---

### `test/e2e/phase4_invariants_test.go` (new e2e suite extension)

**Analog (exact):** `test/e2e/phase3_invariants_test.go` (Phase 3 invariants — same shape)

**Pattern excerpt** (lines 41–62):
```go
//go:build e2e

package e2e

func TestPhase4Invariants(t *testing.T) {
    t.Run("SC1_HeaderRewrite", testPhase4SC1HeaderRewrite)
    t.Run("SC2_McpA2aPrecheck", testPhase4SC2McpA2aPrecheck)
    t.Run("SC3_JwtMintAndBipResolution", testPhase4SC3JwtMintAndBipResolution)
    t.Run("SC4_JwksAndSecretRbac", testPhase4SC4JwksAndSecretRbac)
    t.Run("SC5_RefuseToStartOnNonHttpsBaseURL", testPhase4SC5RefuseToStart)
}
```

**Adaptation notes:**
- Mirror Phase 3 suite-guard pattern: `phase4SuiteGuard(t)` inspects deployment env vars and `t.Skipf`s with engineer-pending message when Phase 4 vars are absent (forwarder bind addresses, JWT Secret, ACH_LITELLM_BASE_URL).
- Phase 3 uses `t.Run` per SC subtests — same structure for Phase 4's 5 SCs.
- Fixtures go in `test/e2e/phase4_fixtures/`: a mock MCP backend, two BackendIdentityPolicy CRs for the alpha-LAST test, a non-https `ACH_BASE_URL` Deployment overlay for SC#5.
- Reuse existing `test/e2e/utils/` helpers (forensics.go, secret_cr.go).

---

## Shared Patterns

### SPDX header (every new `*.go`)
```go
// SPDX-License-Identifier: Apache-2.0
```
Source: `hack/boilerplate.go.txt` + pre-push gate (CLAUDE.md "Publication"). Apply to ALL new files.

### Cobra subcommand registration
**Source:** `cmd/ach/cmd/platform_api.go` lines 55–59
**Apply to:** `cmd/ach/cmd/forwarder.go` (already exists; growth)
```go
func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(forwarderScheme))
    utilruntime.Must(achv1alpha1.AddToScheme(forwarderScheme))
    rootCmd.AddCommand(forwarderCmd)
}
```

### Env-var validation idiom
**Source:** `internal/config/config.go` + `cmd/ach/cmd/platform_api.go` lines 93–142
**Apply to:** `cmd/ach/cmd/forwarder.go` validateForwarderConfig
```go
baseURL, err := config.MustEnvNonEmpty("ACH_BASE_URL")
if err != nil { return nil, fmt.Errorf("ACH_BASE_URL required: %w", err) }
if !strings.HasPrefix(baseURL, "https://") {
    return nil, errors.New("ACH_BASE_URL must be https:// (Hub §9.1)")
}
```

### Informer-only controller-runtime manager (no leader election)
**Source:** `cmd/ach/cmd/platform_api.go` lines 204–235
**Apply to:** `cmd/ach/cmd/forwarder.go` buildDeps
```go
mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
    Scheme: forwarderScheme,
    LeaderElection: false,
    HealthProbeBindAddress: "0",
    Metrics: metricsserver.Options{BindAddress: "0"},
    Cache: cache.Options{
        DefaultNamespaces: map[string]cache.Config{cfg.Namespace: {}},
    },
})
```

### Middleware chain (D-02) — RequestID, RecoverPanic, AccessLog
**Source:** `internal/platformapi/server.go` lines 109–117
**Apply to:** `internal/forwarder/server.go`
```go
r.Use(pamw.RequestID)
r.Use(pamw.RecoverPanic(deps.Logger, nil))  // nil audit — Forwarder OBS-01
r.Use(pamw.AccessLog(deps.Logger))
// NOTE: NO ContentTypeJSON for Forwarder (pass-through on success)
```
Reuse `pamw "github.com/ackstorm/ach/internal/platformapi/middleware"` directly — it's a generic package, not platformapi-specific.

### JSON error envelope (Hub §15.5)
**Source:** `internal/platformapi/render/json.go` `render.Error(...)` lines 56–68
**Apply to:** every ACH-originated error response in forwarder (precheck failures, signing failures, BIP-lookup failures, etc.)
```go
render.Error(w, http.StatusForbidden, "unauthorized_resource", "name not in bound environment", reqID)
```
Reuse `render "github.com/ackstorm/ach/internal/platformapi/render"` package directly.

### Atomic.Pointer publication for lock-free reads
**Source:** `internal/snapshot/snapshot.go` (single-writer / many-reader pattern)
**Apply to:** `internal/forwarder/jwt/secret.go` + `signer.go`
```go
type Ed25519Signer struct {
    current atomic.Pointer[signerSlot]
    next    atomic.Pointer[signerSlot]
}
// Hot-path reader: slot := s.current.Load()  // sub-ns
// Writer (informer event): s.current.Store(newSlot)
```

### Compile-time interface canary
**Source:** `internal/keystore/dbresolver.go` (implicit via type assertions) + `internal/litellm/noop.go`
**Apply to:** `internal/keystore/teamsresolver.go`, `internal/forwarder/jwt/signer.go`
```go
var _ TeamsResolver = (*redisCachedTeamsResolver)(nil)
var _ Signer = (*Ed25519Signer)(nil)
```

### ULID-prefixed identifier convention
**Source:** `internal/platformapi/middleware/middleware.go` lines 35–37 (req_<ulid>) + Phase 3 D-22 idiom for pkid_/ekid_
**Apply to:** `internal/forwarder/jwt/secret.go` (for `kid: ach-jwt-<ulid>` per CONTEXT §"Specific Ideas")
```go
kid := "ach-jwt-" + strings.ToLower(ulid.Make().String())
```

---

## No Analog Found

| File | Role | Reason |
|---|---|---|
| `internal/forwarder/jwt/signer.go` (golang-jwt/jwt/v5 + crypto/ed25519 wiring) | crypto signer | First Ed25519/JWT/JWS implementation in the codebase. Closest pattern is `internal/credhash/` (HMAC), but the crypto primitive + claims marshaling + header injection are novel. Lift from CONTEXT D-11 + jwt/v5 upstream docs (Context7 / DeepWiki). |
| `internal/forwarder/jwt/jwks.go` (JWKS endpoint) | HTTP handler | First JWK Set publisher in the codebase. The `render.JSON` shape transfers but JWK marshaling, base64url-unpadded encoding, and `Cache-Control: public, max-age=3600` are all novel. Lift from RFC 7517 + Hub §9.2. |
| `internal/forwarder/bip/index.go` (`GetFieldIndexer().IndexField(...)`) | informer indexer | Zero existing `IndexField` calls in `/home/jcm/Projects/ach` OR `/home/jcm/Projects/ach-old/internal`. First field-indexer in the codebase. Lift from CONTEXT D-09 + controller-runtime upstream (Context7). |
| `internal/forwarder/proxy/proxy.go` (`httputil.ReverseProxy` + Director) | reverse proxy | First reverse proxy in the codebase. Closest pattern is `internal/litellm/transport.go` (RoundTripper hygiene), but the Director + ModifyResponse + streaming pass-through shape is novel. Lift from CONTEXT D-05 + stdlib `net/http/httputil` examples. |
| `docs/runbooks/jwt-key-rotation.md` | operator runbook | `docs/runbooks/` subtree does not exist. Closest peer doc is `docs/developer-guide/release-process.md`. Create the directory; add to mkdocs nav. |

**Caveat per CONTEXT:** `ach-old/internal/forwarder/` does NOT exist — the predecessor codebase ships only `ach-old/cmd/forwarder/main.go` (76 LoC, identical to current Phase 1 stub on `:8081`). The CONTEXT.md "lift from ach-old/internal/forwarder/" reference is OBSOLETE — there is no predecessor body to lift. ALL Phase 4 forwarder code is new.

**Critical CAVEAT (per CONTEXT + TODO.md §6):** `ach-old` BackendIdentityPolicy reconciler at `/home/jcm/Projects/ach-old/internal/controller/ach/backendidentitypolicy_controller.go` ALSO has no DuplicateTarget logic in the predecessor codebase (verified: it is the same finalizer-only Phase 1 stub). There is genuinely no DuplicateTarget code anywhere to skip. The CONTEXT.md "MUST verify each lift against TODO §6" caveat applies to FUTURE port attempts only; for Phase 4 today the predecessor reconciler offers nothing to lift OR to skip.

---

## Metadata

**Analog search scope:**
- `/home/jcm/Projects/ach/cmd/ach/cmd/` (cobra subcommands)
- `/home/jcm/Projects/ach/internal/` (all subpackages)
- `/home/jcm/Projects/ach/config/rbac/` (Role manifests)
- `/home/jcm/Projects/ach/deploy/helm/ach/templates/` (Helm templates)
- `/home/jcm/Projects/ach/test/e2e/` (e2e fixtures)
- `/home/jcm/Projects/ach/docs/` (mkdocs site)
- `/home/jcm/Projects/ach-old/` (predecessor — verified empty for forwarder body)

**Files scanned for analog matches:** ~60
**Pattern extraction date:** 2026-05-26

## PATTERN MAPPING COMPLETE

**Phase:** 4 - Hub Forwarder & JWT Trust Path
**Files classified:** 15 (13 new + 2 modified)
**Analogs found:** 13 / 15 (5 partial-match items; 2 fully novel)

### Coverage
- Files with exact analog: 7 (`forwarder.go` ← `platform_api.go`; `server.go` ← `platformapi/server.go`; `precheck/check.go` ← `teams/lookup.go`+`store/store.go`; `teamsresolver.go` ← `keystore.go`+`dbresolver.go`; `forwarder_role.yaml` ← `platformapi_role.yaml`; `forwarder-deployment.yaml` ← `platform-api-deployment.yaml`; `phase4_invariants_test.go` ← `phase3_invariants_test.go`)
- Files with partial / role-match analog: 6 (`proxy.go`, `headers/strip.go`, `signer.go`, `secret.go`, `jwks.go`, `metrics/counters.go`)
- Files with no analog: 2 in spirit — JWT signer + JWKS handler are novel crypto+HTTP surfaces; lift directly from Hub spec §9.1/§9.2 + golang-jwt/jwt/v5 upstream docs; the `bip/index.go` IndexField call is also novel but trivially documented in controller-runtime upstream
- Modify-only files (no new pattern needed): 1 (`backendidentitypolicy_controller.go` doc scrub)

### Key Patterns Identified
- **All forwarder code reuses the platformapi infrastructure** — chi middleware (`pamw.RequestID/RecoverPanic/AccessLog`), error rendering (`render.Error`), keystore (`keystore.Resolver`), config helpers (`config.MustEnvNonEmpty`), middleware key-context (`middleware.KeyContext`). These packages are import-able without circular deps.
- **The Phase 1 forwarder stub at `cmd/ach/cmd/forwarder.go` grows in place** following the EXACT same `validateConfig → buildDeps → manager.Add(runnable) → mgr.Start(ctx)` shape as `cmd/ach/cmd/platform_api.go`.
- **TeamsResolver mirrors the KeyResolver pattern verbatim** in the same `internal/keystore/` package — same constructor-time validation, same `singleflight.Group`, same 60s TTL ceiling, parallel Redis keyspace.
- **JWT + JWKS are genuinely new code surfaces** — copy the `internal/snapshot/` atomic.Pointer publication discipline for hot-reload, and the `internal/credhash/` constructor-time refuse-to-load discipline for length-checks.
- **The forwarder reads ZERO BIP status fields** (per OP-16) — only `spec.target` via the new IndexField. The Phase 1 BIP reconciler doc comment forecasting Phase-4 DuplicateTarget logic is stale; permanently removed per TODO §6.

### File Created
`/home/jcm/Projects/ach/.planning/phases/04-hub-forwarder-jwt-trust-path/04-PATTERNS.md`

### Ready for Planning
Pattern mapping complete. Planner can now reference analog patterns + concrete code excerpts in PLAN.md files for Phase 4.

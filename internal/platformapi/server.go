// SPDX-License-Identifier: Apache-2.0

package platformapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/admin"
	"github.com/ackstorm/ach/internal/platformapi/auth"
	authcli "github.com/ackstorm/ach/internal/platformapi/auth/cli"
	"github.com/ackstorm/ach/internal/platformapi/environments"
	"github.com/ackstorm/ach/internal/platformapi/envkeys"
	"github.com/ackstorm/ach/internal/platformapi/hydrate"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

// Deps is the top-level dependency bag cmd/platform-api/main.go
// constructs and hands to New(). Each subpackage's local Deps struct is
// a SUBSET of this top-level Deps; server.go composes each subpackage's
// Deps from it.
type Deps struct {
	// Pool is the Postgres pool (Phase 1 D-08).
	Pool *pgxpool.Pool

	// Redis is the go-redis Client (Phase 3 D-09).
	Redis *redis.Client

	// LiteLLM is the REST client (Phase 2 lift, Phase 3 D-25 extensions).
	LiteLLM litellm.Client

	// Pepper is the server-side HMAC pepper (Phase 1 D-09).
	Pepper []byte

	// Allowlist is the admin allowlist (loaded by admin.LoadAllowlist at
	// process start per D-22). Threaded into middleware.Authn so
	// KeyContext.IsAdmin is populated uniformly per BLK-02.
	Allowlist map[string]struct{}

	// OIDCProvider is the Dex OIDC provider (constructed via
	// oidc.NewProvider).
	OIDCProvider *oidc.Provider

	// IDTokenVerifier is the Dex ID-token verifier (constructed via
	// OIDCProvider.Verifier).
	IDTokenVerifier auth.IDTokenVerifier

	// OAuth2Cfg is the OAuth2 PKCE config.
	OAuth2Cfg *oauth2.Config

	// Store is the Postgres-backed Environment projection reader
	// (issue #34 / Phase B1). Replaces the pre-issue-34 informer-backed
	// reader; platform-api no longer holds a K8s client.
	Store *store.Store

	// Resolver is the per-request key resolution path (Plan 03-05).
	Resolver keystore.Resolver

	// Audit is the audit.NewLogger handle (audit=true predicate
	// attached).
	Audit *slog.Logger

	// Logger is the operational (NOT audit) logger.
	Logger *slog.Logger

	// BaseURL is the deployment-configured https:// ingress URL the
	// hydrate handler uses to build runtime + content download URLs.
	BaseURL string

	// Namespace is POD_NAMESPACE (downward API) — used to compose the
	// audit `actor` field and as the namespace for K8s reads/writes.
	Namespace string

	// InsecureCookie, when true, drops the __Host- prefix and Secure flag
	// from the SSO state cookie. DERIVED from the ACH_BASE_URL scheme in
	// cmd/ach/cmd/platform_api.go: a plain-http base (internal/dev) ⇒ true,
	// an https base ⇒ false (hardened cookie).
	InsecureCookie bool
}

// envKeysStoreAdapter wraps a *store.Store so it satisfies the
// envkeys.envStore interface (which is unexported on the envkeys
// package side). Since the interface is structural, *store.Store
// already satisfies it directly — we keep this named type only to
// document the dependency.
//
// Same is true for environments.Mount and hydrate.HydrateHandler — they
// accept a *store.Store directly.

// envkeysDBAdapter wires the db package helpers behind the envkeys.dbOps
// structural interface. envkeys.dbOps is an unexported interface inside
// the envkeys package so we cannot name it from here, but we can
// satisfy it structurally because every method signature matches.
//
// Defined inline in the file that wires them — see envkeysDB / adminDB
// below.

// New returns the composed chi.Mux. The Mux is the manager.Runnable's
// inner handler; server.go does NOT own the *http.Server lifecycle
// (that belongs to runnable.go).
func New(deps Deps) http.Handler {
	r := chi.NewRouter()

	// Middleware chain (D-02 outer → inner).
	r.Use(pamw.RequestID)
	r.Use(pamw.RecoverPanic(deps.Logger, deps.Audit))
	r.Use(pamw.AccessLog(deps.Logger))
	r.Use(pamw.ContentTypeJSON)

	// Health probes (unauthenticated). These are the ONLY routes that
	// are not under /platform/ — API-01's documented carve-out.
	r.Get("/healthz", healthHandler)
	r.Get("/livez", healthHandler)
	r.Get("/readyz", readyHandler(deps.Pool, deps.Redis))

	// SSO endpoints (unauthenticated; D-02 carve-out).
	authDeps := auth.Deps{
		OIDCProvider:    deps.OIDCProvider,
		IDTokenVerifier: deps.IDTokenVerifier,
		OAuth2Cfg:       deps.OAuth2Cfg,
		LiteLLM:         deps.LiteLLM,
		Pool:            deps.Pool,
		Redis:           deps.Redis, // Phase 6 D-20: callback writeback target.
		Pepper:          deps.Pepper,
		Audit:           deps.Audit,
		Logger:          deps.Logger,
		Namespace:       deps.Namespace,
		InsecureCookie:  deps.InsecureCookie,
	}
	r.Get("/platform/auth/login", auth.LoginHandler(authDeps))
	r.Get("/platform/auth/sso/callback", auth.CallbackHandler(authDeps))

	// Phase 6 device-code endpoints (unauthenticated; D-02 + D-19).
	// /init mints session_id anonymously; /token gates by session_id
	// alone (one-shot Redis GETDEL). Mount OUTSIDE the Authn-gated
	// chi.Group — both endpoints sit alongside the SSO routes above.
	r.Route("/platform/auth/cli", authcli.Mount(authcli.Deps{
		Redis:     deps.Redis,
		Audit:     deps.Audit,
		Logger:    deps.Logger,
		Namespace: deps.Namespace,
		BaseURL:   deps.BaseURL,
	}))

	// Authenticated subtree — BLK-02: middleware.Authn(deps.Resolver,
	// deps.Allowlist, deps.Audit) — allowlist passed positionally so
	// KeyContext.IsAdmin is populated uniformly for downstream
	// handlers.
	r.Group(func(r chi.Router) {
		r.Use(pamw.Authn(deps.Resolver, deps.Allowlist, deps.Audit))

		// BLK-03: hydrate.Deps now exposes LiteLLM litellm.Client as a
		// first-class field (Plan 03-09 ships the contract).
		hydrateDeps := hydrate.Deps{
			Store:     deps.Store,
			LiteLLM:   deps.LiteLLM,
			BaseURL:   deps.BaseURL,
			Allowlist: deps.Allowlist,
			Audit:     deps.Audit,
			Namespace: deps.Namespace,
		}
		r.Post("/platform/hydrate", hydrate.HydrateHandler(hydrateDeps))

		envkeysDeps := envkeys.Deps{
			LiteLLM:   deps.LiteLLM,
			DB:        newEnvkeysDB(deps.Pool),
			Store:     deps.Store,
			Redis:     newRedisDelAdapter(deps.Redis),
			Pepper:    deps.Pepper,
			Audit:     deps.Audit,
			Logger:    deps.Logger,
			Namespace: deps.Namespace,
		}
		r.Route("/platform/env-keys", envkeys.Mount(envkeysDeps))

		// WARN-06: environments.Deps now carries LiteLLM (for
		// internal/platformapi/teams.LookupCallerTeams).
		envDeps := environments.Deps{
			Store:     deps.Store,
			LiteLLM:   deps.LiteLLM,
			Allowlist: deps.Allowlist,
			Audit:     deps.Audit,
			Namespace: deps.Namespace,
		}
		r.Route("/platform/environments", environments.Mount(envDeps))

		adminDeps := admin.Deps{
			Pool:      deps.Pool,
			LiteLLM:   deps.LiteLLM,
			Redis:     deps.Redis,
			Allowlist: deps.Allowlist,
			Audit:     deps.Audit,
			Logger:    deps.Logger,
			Pepper:    deps.Pepper,
			Namespace: deps.Namespace,
		}
		r.Route("/platform/admin", admin.Mount(adminDeps))
	})

	return r
}

// healthHandler is the /healthz + /livez handler — fixed 200 OK,
// no body.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// readyHandler returns the /readyz handler. Per D-20 readiness gates on
// the DB + Redis being reachable. Both pings are bounded by a 2s
// context timeout so a hung dependency cannot block the readiness
// probe forever.
func readyHandler(pool *pgxpool.Pool, redisClient *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if pool != nil {
			if err := pool.Ping(ctx); err != nil {
				http.Error(w, "db unreachable", http.StatusServiceUnavailable)
				return
			}
		}
		if redisClient != nil {
			if err := redisClient.Ping(ctx).Err(); err != nil {
				http.Error(w, "redis unreachable", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

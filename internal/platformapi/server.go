// SPDX-License-Identifier: Apache-2.0

package platformapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi/admin"
	"github.com/ackstorm/ach/internal/platformapi/auth"
	"github.com/ackstorm/ach/internal/platformapi/console"
	"github.com/ackstorm/ach/internal/platformapi/environments"
	"github.com/ackstorm/ach/internal/platformapi/envkeys"
	"github.com/ackstorm/ach/internal/platformapi/hydrate"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/objects"
	"github.com/ackstorm/ach/internal/platformapi/opencodeauth"
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

	// KeyEncryptionKey is the 32-byte AES-256 DEK (ACH_KEY_ENCRYPTION_KEY,
	// G3) used to keycrypt.Seal LiteLLM virtual-key material at rest on the
	// SSO + env-key mint paths. Required (validated at process start).
	KeyEncryptionKey []byte

	// Allowlist is the admin allowlist (loaded by admin.LoadAllowlist at
	// process start per D-22). Threaded into middleware.Authn so
	// KeyContext.IsAdmin is populated uniformly per BLK-02.
	Allowlist map[string]struct{}

	// IDTokenVerifier is the Dex ID-token verifier (constructed via
	// oidc.Provider.Verifier).
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

	// Metrics is the platform-api collector set (G7): hydrate duration +
	// login total. Nil-tolerant — tests that don't scrape leave it unset.
	Metrics *achmetrics.PlatformAPICollectors

	// AuthnOptions: the RFC 9728 challenge on 401. platform-api never
	// allows a raw LiteLLM key (nothing here proxies).
	AuthnOptions pamw.AuthnOptions

	// OAuth is the OAuth 2.1 authorization server (/platform/oauth/*);
	// nil → not mounted (no ACH_JWT_SECRET_DIR). Auth is filled in by New.
	OAuth *auth.OAuthDeps
}

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

	// Dex + LiteLLM provisioning deps shared by the OAuth AS.
	authDeps := auth.Deps{
		IDTokenVerifier:  deps.IDTokenVerifier,
		OAuth2Cfg:        deps.OAuth2Cfg,
		LiteLLM:          deps.LiteLLM,
		Pool:             deps.Pool,
		Pepper:           deps.Pepper,
		KeyEncryptionKey: deps.KeyEncryptionKey,
		Audit:            deps.Audit,
		Logger:           deps.Logger,
		Namespace:        deps.Namespace,
		Issuer:           deps.BaseURL,
		InsecureCookie:   deps.InsecureCookie,
	}
	// OAuth 2.1 AS (unauthenticated by nature: every endpoint is reached by
	// a client that does not yet hold a credential).
	authnOpts := deps.AuthnOptions
	if deps.OAuth != nil {
		od := *deps.OAuth
		od.Auth = authDeps
		r.Route("/platform/oauth", auth.MountOAuth(od))
		// The OpenCode client of that AS, as an npm tarball (anonymous too).
		r.Get("/platform/opencode-auth", opencodeauth.Handler())
		// Web console login/logout (D-27): the same AS, one more pending kind.
		r.Route("/platform/console/session", auth.MountConsole(od))
		// …and the cookie it sets resolves, through Authn, to the same
		// KeyContext a bearer produces (§5.3): the user's oauth pk_ row.
		authnOpts.Cookie = &pamw.CookieAuth{
			Name: auth.ConsoleCookieName(deps.InsecureCookie), Issuer: deps.BaseURL,
			Session: od.ConsoleSession,
			PK: func(ctx context.Context, email string) (*db.PkKeyInfo, error) {
				return db.ActiveOAuthPK(ctx, deps.Pool, email)
			},
		}
	}

	// Authenticated subtree — BLK-02: middleware.Authn(deps.Resolver,
	// deps.Allowlist, deps.Audit) — allowlist passed positionally so
	// KeyContext.IsAdmin is populated uniformly for downstream
	// handlers.
	r.Group(func(r chi.Router) {
		r.Use(pamw.Authn(deps.Resolver, deps.Allowlist, deps.Audit, authnOpts))

		// BLK-03: hydrate.Deps now exposes LiteLLM litellm.Client as a
		// first-class field (Plan 03-09 ships the contract).
		hydrateDeps := hydrate.Deps{
			Store:   deps.Store,
			LiteLLM: deps.LiteLLM,
			BaseURL: deps.BaseURL,
			Audit:   deps.Audit,
			Metrics: deps.Metrics,
		}
		r.Post("/platform/hydrate", hydrate.HydrateHandler(hydrateDeps))

		envkeysDeps := envkeys.Deps{
			LiteLLM:          deps.LiteLLM,
			DB:               newEnvkeysDB(deps.Pool),
			Store:            deps.Store,
			Redis:            newRedisDelAdapter(deps.Redis),
			Pepper:           deps.Pepper,
			KeyEncryptionKey: deps.KeyEncryptionKey,
			Audit:            deps.Audit,
			Logger:           deps.Logger,
			Namespace:        deps.Namespace,
			Issuer:           deps.BaseURL,
		}
		envkeys.MountKeys(r, envkeysDeps)

		// WARN-06: environments.Deps now carries LiteLLM (for
		// internal/platformapi/teams.LookupCallerTeams).
		envDeps := environments.Deps{
			Store:   deps.Store,
			LiteLLM: deps.LiteLLM,
			Audit:   deps.Audit,
		}
		r.Route("/platform/environments", environments.Mount(envDeps))

		adminDeps := admin.Deps{
			Pool:      deps.Pool,
			LiteLLM:   deps.LiteLLM,
			Redis:     deps.Redis,
			Allowlist: deps.Allowlist,
			Audit:     deps.Audit,
			Logger:    deps.Logger,
			Namespace: deps.Namespace,
		}
		r.Route("/platform/admin", admin.Mount(adminDeps))

		// UI Objects API (G2) — GitOps-wins authoring surface, admin-gated and
		// scoped to Environment only. ACH_DISABLE_UI_WRITES turns the write
		// verbs into 403 ui_writes_disabled (reads still served).
		r.Route("/platform/objects", func(r chi.Router) {
			r.Use(admin.AdminOnly(deps.Allowlist, deps.Audit, deps.Namespace))
			objects.Mount(objects.Deps{
				Pool:            deps.Pool,
				Namespace:       deps.Namespace,
				Audit:           deps.Audit,
				Logger:          deps.Logger,
				DisableUIWrites: config.EnvBool("ACH_DISABLE_UI_WRITES", false),
			})(r)
		})
	})

	// The console at "/" (D-26). chi's NotFound is the fallback for every
	// unmatched path; SPA itself keeps /platform/* and the probes as JSON
	// 404s so the fallback never shadows the API. The OpenWork handoff
	// rescue on "/?desktopAuth=1" switches on with the Den (Task 8).
	r.NotFound(console.SPA(console.Dist(), false).ServeHTTP)

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

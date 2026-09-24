// SPDX-License-Identifier: Apache-2.0

// `ach platform-api` boots the ACH Hub Platform REST API. Issue #34 made
// Postgres the source of truth: platform-api no longer constructs a
// controller-runtime manager, no longer watches Secret or any ACH CRD, and
// no longer holds a K8s client at all. The read path goes through the
// pgxpool-backed store (Phase B1), and /admin/refresh signals the operator
// via NOTIFY ach_refresh (Phase B2).
//
// The remaining bootstrap wires env-vars (D-06 / D-08 / D-09), Postgres pool,
// Redis client (D-09), LiteLLM REST client (D-25), OIDC provider + OAuth2
// PKCE config (D-04 / D-06), admin allowlist (D-22 / D-23), and the chi.Mux
// server — then blocks on the stdlib http.Server under a SetupSignalHandler
// for graceful shutdown.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/credhash/pepperenv"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/keycrypt/dekenv"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi"
	"github.com/ackstorm/ach/internal/platformapi/admin"
	"github.com/ackstorm/ach/internal/platformapi/auth"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/openwork"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

func init() {
	rootCmd.AddCommand(platformAPICmd)
}

var platformAPICmd = &cobra.Command{
	Use:   "platform-api",
	Short: "Run the ACH Platform REST API server (chi + Dex SSO)",
	Long: `Boot the chi-backed REST API exposing the ACH platform surface (Login,
EnvKey lifecycle, hydration, marketplace, teams, admin). Refuses to start
without ACH_BASE_URL (http(s)://...), ACH_DB_URL, the credential-hash
pepper, ACH_KEY_ENCRYPTION_KEY (the AES-256 DEK for at-rest key material),
the three Dex OAuth2 vars, ACH_JWT_SECRET_DIR (the OAuth signing seed),
ACH_LITELLM_BASE_URL + ACH_LITELLM_MASTER_KEY, ACH_REDIS_ADDR, and POD_NAMESPACE.`,
	RunE: runPlatformAPI,
}

// platformAPIConfig holds the validated env-var surface; never mutated
// after validatePlatformAPIConfig returns.
type platformAPIConfig struct {
	BaseURL           string
	CredentialHeaders []pamw.CredentialHeader // ACH_CREDENTIAL_HEADERS (resolve only)
	DBURL             string
	Pepper            []byte
	KeyEncryptionKey  []byte
	LiteLLMBaseURL    string
	LiteLLMMasterKey  string
	DexIssuerURL      string
	DexClientID       string
	DexClientSecret   string
	RedisAddr         string
	RedisPassword     string
	RedisTLS          bool
	RedisDB           int
	AllowlistPath     string
	BindAddr          string
	Namespace         string
	InsecureCookie    bool
	// UserBudget is the default per-user spend ceiling (ACH_USER_MAX_BUDGET
	// + ACH_USER_BUDGET_DURATION, chart platformApi.userDefaults). nil =
	// unset = users are uncapped.
	UserBudget *litellm.TagBudget
	// DefaultMaxKeys is the chart-wide fallback ek_ ceiling. Zero is
	// deny-by-default; explicit per-user allowances live in Postgres.
	DefaultMaxKeys int
	// ConsoleChatURL is the hosted chat UI the console's Chat button opens
	// (ACH_CONSOLE_CHAT_URL, chart platformApi.console.chatUrl). Empty hides
	// the button.
	ConsoleChatURL string
	// OAuth front door (docs/plans/2026-09-17-oauth-front-door.md).
	JWTSecretDir    string        // ACH_JWT_SECRET_DIR: ach-jwt-signing-keys mounted as files (the AS signs with it)
	OAuthAccessTTL  time.Duration // ACH_OAUTH_ACCESS_TTL, default 1h
	OAuthRefreshTTL time.Duration // ACH_OAUTH_REFRESH_TTL, default 720h
}

func validatePlatformAPIConfig() (*platformAPIConfig, error) {
	cfg := &platformAPIConfig{}
	baseURL, err := config.MustEnvNonEmpty("ACH_BASE_URL")
	if err != nil {
		return nil, fmt.Errorf("ACH_BASE_URL required: %w", err)
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, errors.New("ACH_BASE_URL must be http(s)://")
	}
	cfg.BaseURL = baseURL
	if cfg.CredentialHeaders, err = pamw.ParseCredentialHeaders(os.Getenv("ACH_CREDENTIAL_HEADERS")); err != nil {
		return nil, fmt.Errorf("ACH_CREDENTIAL_HEADERS: %w", err)
	}
	for _, h := range cfg.CredentialHeaders {
		if h.Mode != pamw.ModeResolve {
			return nil, fmt.Errorf("ACH_CREDENTIAL_HEADERS: %s: platform-api only resolves (passthrough is a forwarder mode)", h.Name)
		}
	}

	if cfg.DBURL, err = config.MustEnvNonEmpty("ACH_DB_URL"); err != nil {
		return nil, err
	}
	pepper, err := pepperenv.Load()
	if err != nil {
		return nil, err
	}
	cfg.Pepper = pepper
	dek, err := dekenv.Load()
	if err != nil {
		return nil, err
	}
	cfg.KeyEncryptionKey = dek
	if cfg.LiteLLMBaseURL, err = config.MustEnvNonEmpty("ACH_LITELLM_BASE_URL"); err != nil {
		return nil, err
	}
	if cfg.LiteLLMMasterKey, err = config.MustEnvNonEmpty("ACH_LITELLM_MASTER_KEY"); err != nil {
		return nil, err
	}
	if cfg.DexIssuerURL, err = config.MustEnvNonEmpty("ACH_DEX_ISSUER_URL"); err != nil {
		return nil, err
	}
	if cfg.DexClientID, err = config.MustEnvNonEmpty("ACH_DEX_CLIENT_ID"); err != nil {
		return nil, err
	}
	if cfg.DexClientSecret, err = config.MustEnvNonEmpty("ACH_DEX_CLIENT_SECRET"); err != nil {
		return nil, err
	}
	if cfg.RedisAddr, err = config.MustEnvNonEmpty("ACH_REDIS_ADDR"); err != nil {
		return nil, err
	}
	cfg.RedisPassword = os.Getenv("ACH_REDIS_PASSWORD")
	cfg.RedisTLS = config.EnvBool("ACH_REDIS_TLS", false)
	// ACH_REDIS_DB=0 is the default Redis logical DB and a legitimate value;
	// EnvIntNonNeg permits 0 (MustEnvIntPositive would reject) and the error
	// is surfaced, not dropped.
	if cfg.RedisDB, err = config.EnvIntNonNeg("ACH_REDIS_DB", 0); err != nil {
		return nil, err
	}
	if cfg.Namespace, err = config.MustEnvNonEmpty("POD_NAMESPACE"); err != nil {
		return nil, err
	}
	cfg.AllowlistPath = config.EnvOr("ACH_ADMIN_ALLOWLIST_PATH", "/etc/ach/admins/admins.txt")
	cfg.BindAddr = config.EnvOr("ACH_PLATFORM_API_BIND_ADDRESS", ":8080")
	// The SSO state cookie's hardening is DERIVED from the externally-
	// visible scheme (ACH_BASE_URL) — no separate flag. An https base
	// (production / TLS-terminating ingress) gets the hardened
	// __Host-/Secure cookie; a plain-http base (internal or dev, e.g. the
	// kind gateway on http://localhost:8080) gets a working non-Secure
	// cookie. ACH_BASE_URL is the scheme the BROWSER sees on the SSO
	// round-trip, which is exactly what governs whether a Secure cookie
	// survives — so it is the single correct source of truth (the
	// platform-api itself always listens plain http behind the ingress).
	cfg.InsecureCookie = !strings.HasPrefix(cfg.BaseURL, "https://")
	if cfg.JWTSecretDir, err = config.MustEnvNonEmpty("ACH_JWT_SECRET_DIR"); err != nil {
		return nil, err
	}
	if cfg.OAuthAccessTTL, err = config.MustEnvDurationAtLeast("ACH_OAUTH_ACCESS_TTL", time.Hour, time.Minute); err != nil {
		return nil, err
	}
	if cfg.OAuthRefreshTTL, err = config.MustEnvDurationAtLeast("ACH_OAUTH_REFRESH_TTL", 30*24*time.Hour, time.Hour); err != nil {
		return nil, err
	}
	if raw := os.Getenv("ACH_USER_MAX_BUDGET"); raw != "" {
		maxBudget, perr := strconv.ParseFloat(raw, 64)
		if perr != nil {
			return nil, fmt.Errorf("ACH_USER_MAX_BUDGET %q: %w", raw, perr)
		}
		cfg.UserBudget = &litellm.TagBudget{
			MaxBudget:      maxBudget,
			BudgetDuration: os.Getenv("ACH_USER_BUDGET_DURATION"),
		}
	}
	// ACH_USER_MAX_KEYS: chart-wide fallback ek_ ceiling for anyone without
	// a user_limits row. Unset or empty means 0 — deny-by-default.
	if raw := os.Getenv("ACH_USER_MAX_KEYS"); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil || n < 0 {
			return nil, fmt.Errorf("ACH_USER_MAX_KEYS %q: must be a non-negative integer", raw)
		}
		cfg.DefaultMaxKeys = n
	}
	if raw := os.Getenv("ACH_CONSOLE_CHAT_URL"); raw != "" {
		u, perr := url.Parse(raw)
		if perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("ACH_CONSOLE_CHAT_URL %q: must be an absolute http(s) URL", raw)
		}
		cfg.ConsoleChatURL = raw
	}
	return cfg, nil
}

type platformAPIProcessDeps struct {
	pool   *pgxpool.Pool
	redis  *redis.Client
	server platformapi.Deps
	// signer is the OAuth access-token signer, loaded from the mounted
	// ach-jwt-signing-keys Secret; nil when the AS is disabled.
	signer *jwt.Ed25519Signer
	// Plan 05-06 D-10: metricsReg holds the process-local Registry +
	// metricsHandler is the corresponding /metrics http.Handler that
	// runPlatformAPIServer composes onto the chi router. litellmUnreachable
	// is the shared §18.5 counter registered with caller="platform_api"
	// dimension pre-declared — registered-but-unused at end of Phase 5
	// (see Plan 05-06 spec_divergence: Phase 3 handlers emit
	// audit.OutcomeLitellmUnreachable as a response body code via
	// render.Error, NOT as a counter Inc; per-call-site Inc retrofit is
	// out of scope for Phase 5).
	metricsReg         *prometheus.Registry
	metricsHandler     http.Handler
	litellmUnreachable *prometheus.CounterVec
}

func (p *platformAPIProcessDeps) close() {
	if p == nil {
		return
	}
	if p.redis != nil {
		_ = p.redis.Close()
	}
	if p.pool != nil {
		p.pool.Close()
	}
}

//nolint:gocyclo // single bootstrap function intentionally linear
func buildPlatformAPIDeps(ctx context.Context, cfg *platformAPIConfig, logger *slog.Logger) (*platformAPIProcessDeps, error) {
	out := &platformAPIProcessDeps{}

	// Fulfill controller-runtime's global logr root from platform-api's slog
	// handler. The shared litellm client logs via ctrl.Log (NewRESTClient
	// below); without a fulfilled root, the first litellm request dumps a
	// one-time "log.SetLogger(...) was never called" stack and drops the log
	// line. operator.go / forwarder.go set this via zap — platform-api bridges
	// slog so litellm logs land in the same JSON stream.
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))

	// ─── Phase 5 D-09 / D-10 / OBS-05: process-local Prometheus
	//     Registry + shared ach_litellm_unreachable_total counter (caller
	//     dimension pre-declared with all four §18.5 values). Platform
	//     API does NOT receive a typed PlatformAPICollectors struct in
	//     §18.5 — only Forwarder and Content Service have typed
	//     collectors. The /metrics endpoint here exposes the shared
	//     litellm_unreachable counter (registered-but-unused at end of
	//     Phase 5; see Plan 05-06 spec_divergence) and any future
	//     additive metrics that land here.
	out.metricsReg = prometheus.NewRegistry()
	registerRuntimeCollectors(out.metricsReg)
	out.litellmUnreachable = metrics.MustRegisterLitellmUnreachable(out.metricsReg)
	out.litellmUnreachable.WithLabelValues("platform_api").Add(0) // expose family at 0 (§18.5)
	// /metrics is unauthenticated on the main traffic listener (D-10);
	// internal cluster network only — see Helm values.yaml metricsAuth
	// note in Plan 05-07. T-05-06-01 (Information Disclosure) accepted.
	out.metricsHandler = metrics.Handler(out.metricsReg)

	// G7: typed platform-api collectors (hydrate duration + login total)
	// + key-resolution cache hit/miss counters, registered on the same
	// process-local Registry.
	platformAPICollectors := metrics.NewPlatformAPICollectors(out.metricsReg)
	keystoreCollectors := metrics.NewKeystoreCollectors(out.metricsReg)

	pool, err := db.Open(ctx, cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("db.Open: %w", err)
	}
	out.pool = pool

	out.redis = newRedisClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.RedisTLS)

	liteLLM := litellm.NewRESTClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey, ctrl.Log.WithName("litellm"))
	auditLog := audit.NewLogger(os.Stdout)

	oidcProvider, err := oidc.NewProvider(ctx, cfg.DexIssuerURL)
	if err != nil {
		return out, fmt.Errorf("oidc.NewProvider: %w", err)
	}
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.DexClientID,
		ClientSecret: cfg.DexClientSecret,
		Endpoint:     oidcProvider.Endpoint(),
		// offline_access: Dex hands ACH a refresh token, and every ACH
		// refresh asks Dex (and through it the IdP) again — a user disabled
		// at the IdP is out at the next refresh, not 30 days later.
		Scopes: []string{oidc.ScopeOpenID, "email", "profile", oidc.ScopeOfflineAccess},
	}
	idTokenVerifier := oidcProvider.Verifier(&oidc.Config{ClientID: cfg.DexClientID})

	allowlist, err := admin.LoadAllowlist(cfg.AllowlistPath, logger)
	if err != nil {
		return out, fmt.Errorf("admin.LoadAllowlist: %w", err)
	}

	platformStore := store.New(pool, cfg.Namespace, logr.Discard())

	dbResolver, err := keystore.NewDBResolver(pool, cfg.Pepper,
		keystore.NewLiteLLMPkExtendHook(liteLLM, db.PkSlidingWindow, ctrl.Log.WithName("pk-extend")))
	if err != nil {
		return out, fmt.Errorf("keystore.NewDBResolver: %w", err)
	}
	// OAuth AS — the only login: the signer is loaded from the mounted Secret.
	signer := jwt.NewEd25519Signer()
	if err := jwt.LoadFromDir(signer, cfg.JWTSecretDir); err != nil {
		return out, fmt.Errorf("oauth signer: %w", err) // fail closed: no key, no login
	}
	out.signer = signer
	oauthDeps := &auth.OAuthDeps{
		Store: &auth.OAuthStore{RDB: out.redis}, Signer: signer,
		Issuer: cfg.BaseURL, Audience: "ach", Namespace: cfg.Namespace,
		AccessTTL: cfg.OAuthAccessTTL, RefreshTTL: cfg.OAuthRefreshTTL,
	}
	oauthResolver := keystore.NewOAuthResolverDB(dbResolver, signer, cfg.BaseURL, "ach", pool)
	cachedResolver, err := keystore.NewCachedResolver(oauthResolver, out.redis, cfg.Pepper,
		keystore.WithCacheMetrics(keystoreCollectors))
	if err != nil {
		return out, fmt.Errorf("keystore.NewCachedResolver: %w", err)
	}

	out.server = platformapi.Deps{
		OAuth: oauthDeps,
		AuthnOptions: pamw.AuthnOptions{
			Challenge: func(*http.Request) string {
				return `Bearer resource_metadata="` + strings.TrimRight(cfg.BaseURL, "/") + `/.well-known/oauth-protected-resource"`
			},
			Headers: cfg.CredentialHeaders,
		},
		Pool:             pool,
		Redis:            out.redis,
		LiteLLM:          liteLLM,
		LiteLLMREST:      liteLLM,
		OpenWork:         openwork.FromEnv(),
		Pepper:           cfg.Pepper,
		KeyEncryptionKey: cfg.KeyEncryptionKey,
		Allowlist:        allowlist,
		IDTokenVerifier:  idTokenVerifier,
		OAuth2Cfg:        oauth2Cfg,
		Store:            platformStore,
		Resolver:         cachedResolver,
		Audit:            auditLog,
		Logger:           logger,
		BaseURL:          cfg.BaseURL,
		Namespace:        cfg.Namespace,
		InsecureCookie:   cfg.InsecureCookie,
		UserBudget:       cfg.UserBudget,
		DefaultMaxKeys:   cfg.DefaultMaxKeys,
		ConsoleChatURL:   cfg.ConsoleChatURL,
		Metrics:          platformAPICollectors,
	}
	return out, nil
}

// runPlatformAPIServer wires the chi handler + /metrics endpoint behind the
// shared ServerRunnable (which already encodes the D-03 timeouts + graceful
// shutdown semantics) and waits on the supplied context. Issue #34 dropped
// the controller-runtime manager — the runnable now starts directly on its
// own goroutine and the signal context drives shutdown.
func runPlatformAPIServer(ctx context.Context, deps *platformAPIProcessDeps, bindAddr string) error {
	httpHandler := platformapi.New(deps.server)

	// Plan 05-06 Task 4 / D-10: /metrics is served on the SAME port as
	// the traffic listener. A tiny stdlib ServeMux fronts the chi-built
	// platform-api handler so /metrics has its own dedicated path
	// (precedence by path-specificity per net/http ServeMux semantics)
	// while every other request falls through to the platform-api
	// router. The metrics handler is unauthenticated; production
	// scrape clients access it via the in-cluster Service IP / Pod IP.
	composed := http.NewServeMux()
	composed.Handle("/metrics", deps.metricsHandler)
	composed.Handle("/", httpHandler)

	runnable := platformapi.NewRunnable(bindAddr, composed, deps.server.Logger)
	return runnable.Start(ctx)
}

func runPlatformAPI(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := validatePlatformAPIConfig()
	if err != nil {
		return fmt.Errorf("validateConfig: %w", err)
	}

	ctx := ctrl.SetupSignalHandler()
	deps, err := buildPlatformAPIDeps(ctx, cfg, logger)
	if err != nil {
		if deps != nil {
			deps.close()
		}
		return fmt.Errorf("buildDeps: %w", err)
	}
	defer deps.close()

	logger.Info("platform-api starting",
		"bind", cfg.BindAddr,
		"namespace", cfg.Namespace,
		"baseURL", cfg.BaseURL,
	)

	if err := runPlatformAPIServer(ctx, deps, cfg.BindAddr); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("runServer: %w", err)
		}
	}
	logger.Info("platform-api shutdown complete")
	return nil
}

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
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

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
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi"
	"github.com/ackstorm/ach/internal/platformapi/admin"
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
pepper, the four Dex OAuth2 vars, ACH_LITELLM_BASE_URL +
ACH_LITELLM_MASTER_KEY, ACH_REDIS_ADDR, and POD_NAMESPACE.`,
	RunE: runPlatformAPI,
}

// platformAPIConfig holds the validated env-var surface; never mutated
// after validatePlatformAPIConfig returns.
type platformAPIConfig struct {
	BaseURL          string
	DBURL            string
	Pepper           []byte
	LiteLLMBaseURL   string
	LiteLLMMasterKey string
	DexIssuerURL     string
	DexClientID      string
	DexClientSecret  string
	DexRedirectURL   string
	RedisAddr        string
	RedisPassword    string
	RedisTLS         bool
	RedisDB          int
	AllowlistPath    string
	BindAddr         string
	Namespace        string
	InsecureCookie   bool
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

	if cfg.DBURL, err = config.MustEnvNonEmpty("ACH_DB_URL"); err != nil {
		return nil, err
	}
	pepper, err := pepperenv.Load()
	if err != nil {
		return nil, err
	}
	cfg.Pepper = pepper
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
	if cfg.DexRedirectURL, err = config.MustEnvNonEmpty("ACH_DEX_REDIRECT_URL"); err != nil {
		return nil, err
	}
	if cfg.RedisAddr, err = config.MustEnvNonEmpty("ACH_REDIS_ADDR"); err != nil {
		return nil, err
	}
	cfg.RedisPassword = os.Getenv("ACH_REDIS_PASSWORD")
	cfg.RedisTLS = config.EnvBool("ACH_REDIS_TLS", false)
	cfg.RedisDB, _ = config.MustEnvIntPositive("ACH_REDIS_DB", 0)
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
	return cfg, nil
}

type platformAPIProcessDeps struct {
	pool   *pgxpool.Pool
	redis  *redis.Client
	server platformapi.Deps
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

	// ─── Phase 5 D-09 / D-10 / OBS-05: process-local Prometheus
	//     Registry + shared litellm_unreachable_total counter (caller
	//     dimension pre-declared with all four §18.5 values). Platform
	//     API does NOT receive a typed PlatformAPICollectors struct in
	//     §18.5 — only Forwarder and Content Service have typed
	//     collectors. The /metrics endpoint here exposes the shared
	//     litellm_unreachable counter (registered-but-unused at end of
	//     Phase 5; see Plan 05-06 spec_divergence) and any future
	//     additive metrics that land here.
	out.metricsReg = metrics.NewRegistry()
	out.litellmUnreachable = metrics.MustRegisterLitellmUnreachable(out.metricsReg)
	// /metrics is unauthenticated on the main traffic listener (D-10);
	// internal cluster network only — see Helm values.yaml metricsAuth
	// note in Plan 05-07. T-05-06-01 (Information Disclosure) accepted.
	out.metricsHandler = metrics.Handler(out.metricsReg)

	pool, err := db.Open(ctx, cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("db.Open: %w", err)
	}
	out.pool = pool

	redisOpts := &redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	if cfg.RedisTLS {
		redisOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec
	}
	out.redis = redis.NewClient(redisOpts)

	liteLLM := litellm.NewRESTClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey, ctrl.Log.WithName("litellm"))
	auditLog := audit.NewLogger(os.Stdout)

	oidcProvider, err := oidc.NewProvider(ctx, cfg.DexIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc.NewProvider: %w", err)
	}
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.DexClientID,
		ClientSecret: cfg.DexClientSecret,
		RedirectURL:  cfg.DexRedirectURL,
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	idTokenVerifier := oidcProvider.Verifier(&oidc.Config{ClientID: cfg.DexClientID})

	allowlist, err := admin.LoadAllowlist(cfg.AllowlistPath, logger)
	if err != nil {
		return nil, fmt.Errorf("admin.LoadAllowlist: %w", err)
	}

	platformStore := store.New(pool, cfg.Namespace, logr.Discard())

	dbResolver, err := keystore.NewDBResolver(pool, cfg.Pepper)
	if err != nil {
		return nil, fmt.Errorf("keystore.NewDBResolver: %w", err)
	}
	cachedResolver, err := keystore.NewCachedResolver(dbResolver, out.redis, cfg.Pepper)
	if err != nil {
		return nil, fmt.Errorf("keystore.NewCachedResolver: %w", err)
	}

	out.server = platformapi.Deps{
		Pool:            pool,
		Redis:           out.redis,
		LiteLLM:         liteLLM,
		Pepper:          cfg.Pepper,
		Allowlist:       allowlist,
		OIDCProvider:    oidcProvider,
		IDTokenVerifier: idTokenVerifier,
		OAuth2Cfg:       oauth2Cfg,
		Store:           platformStore,
		Resolver:        cachedResolver,
		Audit:           auditLog,
		Logger:          logger,
		BaseURL:         cfg.BaseURL,
		Namespace:       cfg.Namespace,
		InsecureCookie:  cfg.InsecureCookie,
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

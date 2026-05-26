// SPDX-License-Identifier: Apache-2.0

// `ach platform-api` boots the ACH Hub Platform REST API. It wires every
// Phase 3 piece — env-var validation (D-06 / D-08 / D-09), Postgres pool,
// Redis client (D-09), LiteLLM REST client (D-25), OIDC provider + OAuth2
// PKCE config (D-04 / D-06), admin allowlist (D-22 / D-23), informer-only
// controller-runtime manager (D-20), and chi.Mux server wrapped as
// manager.Runnable — then blocks on mgr.Start under
// ctrl.SetupSignalHandler (D-03 graceful shutdown). Body lifted from
// ach-old/cmd/platform-api/main.go and adapted to a cobra RunE for the
// single-binary layout.

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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
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
	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/credhash/pepperenv"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi"
	"github.com/ackstorm/ach/internal/platformapi/admin"
	"github.com/ackstorm/ach/internal/platformapi/store"
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
	Long: `Boot the chi-backed REST API exposing the ACH platform surface (Login,
EnvKey lifecycle, hydration, marketplace, teams, admin). Refuses to start
without ACH_BASE_URL (https://...), ACH_DB_URL, the credential-hash
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
	return cfg, nil
}

type platformAPIProcessDeps struct {
	pool    *pgxpool.Pool
	redis   *redis.Client
	manager manager.Manager
	server  platformapi.Deps
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

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 platformAPIScheme,
		LeaderElection:         false,
		HealthProbeBindAddress: "0",
		Metrics:                metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				cfg.Namespace: {},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ctrl.NewManager: %w", err)
	}
	out.manager = mgr

	if _, err := mgr.GetCache().GetInformer(ctx, &corev1.Secret{}); err != nil {
		return nil, fmt.Errorf("informer Secret: %w", err)
	}
	for _, obj := range []client.Object{
		&achv1alpha1.Environment{},
		&achv1alpha1.Plugin{},
		&achv1alpha1.Prompt{},
		&achv1alpha1.Artifact{},
		&achv1alpha1.PluginMarketplace{},
		&achv1alpha1.BackendIdentityPolicy{},
	} {
		if _, err := mgr.GetCache().GetInformer(ctx, obj); err != nil {
			return nil, fmt.Errorf("informer: %w", err)
		}
	}

	platformStore := store.New(mgr.GetClient(), cfg.Namespace, ctrl.Log.WithName("store"))

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
		K8sClient:       mgr.GetClient(),
		Store:           platformStore,
		Resolver:        cachedResolver,
		Audit:           auditLog,
		Logger:          logger,
		BaseURL:         cfg.BaseURL,
		Namespace:       cfg.Namespace,
	}
	return out, nil
}

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

	// Install a controller-runtime logger before any client-go cache or
	// reflector is constructed. Without this, the reflector's first
	// ListAndWatchWithContext call hits an unset delegating sink and the
	// runtime prints a "log.SetLogger(...) was never called" stack dump
	// to stderr on every Pod start.
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

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

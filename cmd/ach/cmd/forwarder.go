// SPDX-License-Identifier: Apache-2.0

// `ach forwarder` boots the Hub Forwarder service per Plan 04-08. RunE
// validates env vars, builds the controller-runtime manager (no
// controllers, no leader election) + informers (BIP, Environment, Secret),
// loads the ach-jwt-signing-keys Secret (refuse-to-start on missing /
// malformed current slot per FWD-09), wires the Ed25519Signer + Secret
// hot-reload event handler, and starts the dual-port Runnable (traffic
// :8080, health :8081) under the manager. SIGINT/SIGTERM drains via
// ctrl.SetupSignalHandler.
//
// Refuses to start when:
//   - ACH_BASE_URL is not https:// (FWD-10 / Hub §9.1)
//   - ACH_LITELLM_BASE_URL is missing or has an unknown scheme
//   - ach-jwt-signing-keys Secret is missing OR current.kid is empty OR
//     current.seed is not exactly 32 bytes (FWD-09 + D-10)

package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/credhash/pepperenv"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder"
	"github.com/ackstorm/ach/internal/forwarder/bip"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
)

var forwarderScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(forwarderScheme))
	utilruntime.Must(achv1alpha1.AddToScheme(forwarderScheme))
	rootCmd.AddCommand(forwarderCmd)
}

var forwarderCmd = &cobra.Command{
	Use:   "forwarder",
	Short: "Run the ACH Hub Forwarder (runtime trust path + JWT mint + JWKS)",
	Long: `Boot the chi-backed reverse proxy on /v1, /gemini, /mcp/{name},
/a2a/{name} with §5.1 header strip+rewrite, §9 Ed25519 JWT signing gated
by BackendIdentityPolicy, and /.well-known/jwks.json. Refuses to start
when ACH_BASE_URL is not https:// (FWD-10) or the ach-jwt-signing-keys
Secret is missing/malformed (FWD-09).`,
	RunE: runForwarder,
}

// forwarderConfig holds the validated env-var surface; never mutated
// after validateForwarderConfig returns.
type forwarderConfig struct {
	BaseURL          string
	DBURL            string
	Pepper           []byte
	LiteLLMBaseURL   string
	LiteLLMSharedKey string
	RedisAddr        string
	RedisPassword    string
	RedisTLS         bool
	RedisDB          int
	TrafficBindAddr  string
	HealthBindAddr   string
	Namespace        string
	JWTSecretName    string
}

func validateForwarderConfig() (*forwarderConfig, error) {
	cfg := &forwarderConfig{}
	baseURL, err := config.MustEnvNonEmpty("ACH_BASE_URL")
	if err != nil {
		return nil, fmt.Errorf("ACH_BASE_URL required: %w", err)
	}
	if !strings.HasPrefix(baseURL, "https://") {
		return nil, errors.New("ACH_BASE_URL must be https:// (FWD-10 / Hub §9.1)")
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
	u, err := url.Parse(cfg.LiteLLMBaseURL)
	if err != nil {
		return nil, fmt.Errorf("ACH_LITELLM_BASE_URL parse: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("ACH_LITELLM_BASE_URL must use http:// or https:// (got %q)", u.Scheme)
	}

	if cfg.LiteLLMSharedKey, err = config.MustEnvNonEmpty("ACH_LITELLM_SHARED_KEY"); err != nil {
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

	cfg.TrafficBindAddr = config.EnvOr("ACH_FORWARDER_BIND_ADDRESS", ":8080")
	cfg.HealthBindAddr = config.EnvOr("FORWARDER_HEALTH_BIND_ADDRESS", ":8081")
	cfg.JWTSecretName = config.EnvOr("ACH_JWT_SECRET_NAME", jwt.SecretName)
	return cfg, nil
}

type forwarderProcessDeps struct {
	pool    *pgxpool.Pool
	redis   *redis.Client
	manager manager.Manager
	server  forwarder.Deps
	signer  *jwt.Ed25519Signer
	loader  *jwt.SecretLoader
	logger  *slog.Logger
}

func (d *forwarderProcessDeps) close() {
	if d == nil {
		return
	}
	if d.redis != nil {
		_ = d.redis.Close()
	}
	if d.pool != nil {
		d.pool.Close()
	}
}

//nolint:gocyclo // single bootstrap function intentionally linear
func buildForwarderDeps(ctx context.Context, cfg *forwarderConfig, logger *slog.Logger) (*forwarderProcessDeps, error) {
	out := &forwarderProcessDeps{logger: logger}

	pool, err := db.Open(ctx, cfg.DBURL)
	if err != nil {
		return out, fmt.Errorf("db.Open: %w", err)
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

	ll := litellm.NewRESTClient(cfg.LiteLLMBaseURL, cfg.LiteLLMSharedKey, ctrl.Log.WithName("litellm"))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 forwarderScheme,
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
		return out, fmt.Errorf("ctrl.NewManager: %w", err)
	}
	out.manager = mgr

	// D-09 ORDER: bip.RegisterIndex MUST be called BEFORE the first
	// GetInformer(ctx, &achv1alpha1.BackendIdentityPolicy{}).
	if err := bip.RegisterIndex(ctx, mgr); err != nil {
		return out, fmt.Errorf("bip.RegisterIndex: %w", err)
	}

	for _, obj := range []client.Object{
		&achv1alpha1.BackendIdentityPolicy{},
		&achv1alpha1.Environment{},
		&corev1.Secret{},
	} {
		if _, err := mgr.GetCache().GetInformer(ctx, obj); err != nil {
			return out, fmt.Errorf("informer %T: %w", obj, err)
		}
	}

	dbResolver, err := keystore.NewDBResolver(pool, cfg.Pepper)
	if err != nil {
		return out, fmt.Errorf("keystore.NewDBResolver: %w", err)
	}
	cachedResolver, err := keystore.NewCachedResolver(dbResolver, out.redis, cfg.Pepper)
	if err != nil {
		return out, fmt.Errorf("keystore.NewCachedResolver: %w", err)
	}

	baseTeamsResolver, err := keystore.NewLiteLLMTeamsResolver(ll)
	if err != nil {
		return out, fmt.Errorf("keystore.NewLiteLLMTeamsResolver: %w", err)
	}
	teamsResolver, err := keystore.NewCachedTeamsResolver(baseTeamsResolver, out.redis)
	if err != nil {
		return out, fmt.Errorf("keystore.NewCachedTeamsResolver: %w", err)
	}

	// JWT signer + Secret loader (refuse-to-start on missing/malformed).
	out.signer = jwt.NewEd25519Signer()
	out.loader = jwt.NewSecretLoader(out.signer, cfg.Namespace, cfg.JWTSecretName, ctrl.Log.WithName("jwt-loader"))

	// Direct API-server fetch (cache may not be synced yet at boot).
	apiClient, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: forwarderScheme})
	if err != nil {
		return out, fmt.Errorf("client.New (api-server): %w", err)
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.JWTSecretName}
	if err := apiClient.Get(ctx, key, secret); err != nil {
		return out, fmt.Errorf("get Secret %s/%s: %w", cfg.Namespace, cfg.JWTSecretName, err)
	}
	if err := out.loader.LoadOnce(secret); err != nil {
		return out, fmt.Errorf("jwt secret LoadOnce (refuse-to-start): %w", err)
	}

	// Wire Secret informer event handler for hot-reload.
	secretInformer, err := mgr.GetCache().GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		return out, fmt.Errorf("informer Secret (for event handler): %w", err)
	}
	_, err = secretInformer.AddEventHandler(toolscache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			s, ok := obj.(*corev1.Secret)
			return ok && s.Name == cfg.JWTSecretName && s.Namespace == cfg.Namespace
		},
		Handler: toolscache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if s, ok := obj.(*corev1.Secret); ok {
					_ = out.loader.Reload(s)
				}
			},
			UpdateFunc: func(_, obj interface{}) {
				if s, ok := obj.(*corev1.Secret); ok {
					_ = out.loader.Reload(s)
				}
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("secret informer AddEventHandler: %w", err)
	}

	upstream, err := url.Parse(cfg.LiteLLMBaseURL)
	if err != nil {
		return out, fmt.Errorf("url.Parse LiteLLM upstream: %w", err)
	}

	out.server = forwarder.Deps{
		Pool:             pool,
		Redis:            out.redis,
		LiteLLM:          ll,
		Pepper:           cfg.Pepper,
		K8sClient:        mgr.GetClient(),
		Resolver:         cachedResolver,
		TeamsResolver:    teamsResolver,
		Signer:           out.signer,
		Logger:           logger,
		BaseURL:          cfg.BaseURL,
		Namespace:        cfg.Namespace,
		LiteLLMUpstream:  upstream,
		LiteLLMSharedKey: cfg.LiteLLMSharedKey,
	}
	return out, nil
}

func runForwarderServer(ctx context.Context, deps *forwarderProcessDeps, cfg *forwarderConfig) error {
	trafficHandler := forwarder.New(deps.server)
	mgrCacheSync := func(checkCtx context.Context) bool {
		return deps.manager.GetCache().WaitForCacheSync(checkCtx)
	}
	healthHandler := forwarder.NewHealthHandler(deps.signer, mgrCacheSync)

	runnable := &forwarder.Runnable{
		TrafficAddr:    cfg.TrafficBindAddr,
		HealthAddr:     cfg.HealthBindAddr,
		TrafficHandler: trafficHandler,
		HealthHandler:  healthHandler,
		Logger:         deps.logger,
	}
	if err := deps.manager.Add(runnable); err != nil {
		return fmt.Errorf("manager.Add(forwarder.Runnable): %w", err)
	}
	return deps.manager.Start(ctx)
}

func runForwarder(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	cfg, err := validateForwarderConfig()
	if err != nil {
		return fmt.Errorf("validateConfig: %w", err)
	}

	ctx := ctrl.SetupSignalHandler()
	deps, err := buildForwarderDeps(ctx, cfg, logger)
	if err != nil {
		if deps != nil {
			deps.close()
		}
		return fmt.Errorf("buildDeps: %w", err)
	}
	defer deps.close()

	logger.Info("forwarder starting",
		"traffic", cfg.TrafficBindAddr,
		"health", cfg.HealthBindAddr,
		"namespace", cfg.Namespace,
		"baseURL", cfg.BaseURL,
		"jwtSecret", cfg.JWTSecretName,
	)

	if err := runForwarderServer(ctx, deps, cfg); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("runServer: %w", err)
		}
	}
	logger.Info("forwarder shutdown complete")
	return nil
}

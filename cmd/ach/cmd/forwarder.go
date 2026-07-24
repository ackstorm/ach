// SPDX-License-Identifier: Apache-2.0

// `ach forwarder` boots the Hub Forwarder service per Plan 04-08. RunE
// validates env vars, builds the controller-runtime manager (no
// controllers, no leader election) + informers (BIP, Environment, Secret),
// resolves the LiteLLM upstream endpoint + master key from the
// LiteLLMConnection/default CR (refuse-to-start on missing CR or Secret),
// loads the ach-jwt-signing-keys Secret (refuse-to-start on missing /
// malformed current slot per FWD-09), wires the Ed25519Signer + Secret
// hot-reload event handler, and starts the dual-port Runnable (traffic
// :8080, health :8081) under the manager. SIGINT/SIGTERM drains via
// ctrl.SetupSignalHandler.
//
// Refuses to start when:
//   - ACH_BASE_URL is not http(s)://
//   - ACH_KEY_ENCRYPTION_KEY is unset / placeholder / not a 32-byte base64
//     DEK (G3 — the proxy decrypts sealed LiteLLM key material per request)
//   - LiteLLMConnection/default CR is missing OR its Spec.Endpoint is
//     not a parseable http(s):// URL OR its MasterKeySecretRef does not
//     resolve to a non-empty Secret data entry (B2 refactor — replaces
//     the prior ACH_LITELLM_BASE_URL + ACH_LITELLM_SHARED_KEY env vars)
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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
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
	"github.com/ackstorm/ach/internal/forwarder/bipcache"
	"github.com/ackstorm/ach/internal/forwarder/envstore"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/forwarder/litellmconn"
	forwardermetrics "github.com/ackstorm/ach/internal/forwarder/metrics"
	"github.com/ackstorm/ach/internal/keycrypt/dekenv"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/metrics"
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
when ACH_BASE_URL is not http(s)://, ACH_KEY_ENCRYPTION_KEY is unset/invalid
(G3), or the ach-jwt-signing-keys Secret is missing/malformed (FWD-09).`,
	RunE: runForwarder,
}

// forwarderConfig holds the validated env-var surface; never mutated
// after validateForwarderConfig returns.
//
// B2 refactor: ACH_LITELLM_BASE_URL + ACH_LITELLM_SHARED_KEY are no
// longer env-sourced. The (endpoint, master key) pair is resolved from
// LiteLLMConnection/default + its MasterKeySecretRef Secret at the
// start of buildForwarderDeps (see litellmconn.Resolve). Keeping the
// resolved values out of forwarderConfig makes the config "env only"
// and the CR-derived values flow through local vars instead.
type forwarderConfig struct {
	BaseURL          string
	DBURL            string
	Pepper           []byte
	KeyEncryptionKey []byte
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
	dek, err := dekenv.Load()
	if err != nil {
		return nil, err
	}
	cfg.KeyEncryptionKey = dek

	// B2: LiteLLM endpoint + master key now sourced from
	// LiteLLMConnection/default CR at buildForwarderDeps time.

	if cfg.RedisAddr, err = config.MustEnvNonEmpty("ACH_REDIS_ADDR"); err != nil {
		return nil, err
	}
	cfg.RedisPassword = os.Getenv("ACH_REDIS_PASSWORD")
	cfg.RedisTLS = config.EnvBool("ACH_REDIS_TLS", false)
	// W3 (REVIEW): ACH_REDIS_DB=0 is the default Redis logical DB and a
	// legitimate value; MustEnvIntPositive rejects 0 by contract, so use
	// EnvIntNonNeg here and surface the parse error instead of dropping
	// it on the floor.
	if cfg.RedisDB, err = config.EnvIntNonNeg("ACH_REDIS_DB", 0); err != nil {
		return nil, err
	}

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
	// metricsHandler is the /metrics http.Handler from
	// metrics.Handler(reg). Composed onto the traffic listener in
	// runForwarderServer so a scrape client can hit
	// http://forwarder:8080/metrics on the SAME port as /v1, /gemini,
	// etc. (Plan 05-06 D-10).
	metricsHandler http.Handler
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

	// ─── Phase 5 D-09 / D-10: process-local Prometheus Registry +
	//     typed ForwarderCollectors + shared ach_litellm_unreachable_total.
	//     InitCollectors plumbs the typed collectors into the Phase 4
	//     internal/forwarder/metrics shim (counters.go), so existing
	//     IncRequests / IncJWTSigned / IncJWTSuppressed /
	//     IncLiteLLMUnreachable call sites in
	//     internal/forwarder/{proxy,bip} start emitting real samples
	//     without any signature change (D-19 thin-shim invariant).
	reg := prometheus.NewRegistry()
	registerRuntimeCollectors(reg)
	fwdCollectors := metrics.NewForwarderCollectors(reg)
	fwdCollectors.PreInitZeroSeries()
	litellmUnreachable := metrics.MustRegisterLitellmUnreachable(reg)
	litellmUnreachable.WithLabelValues("forwarder").Add(0) // expose family at 0 (§18.5)
	forwardermetrics.InitCollectors(fwdCollectors, litellmUnreachable)
	// G7: key-resolution cache hit/miss counters on the forwarder registry.
	keystoreCollectors := metrics.NewKeystoreCollectors(reg)
	// /metrics is unauthenticated on the main traffic listener (D-10);
	// internal cluster network only — see Helm values.yaml metricsAuth
	// note in Plan 05-07. T-05-06-01 (Information Disclosure) accepted:
	// Ingress/Service deployers MUST NOT route /metrics from external
	// traffic.
	out.metricsHandler = metrics.Handler(reg)

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

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 forwarderScheme,
		LeaderElection:         false,
		HealthProbeBindAddress: "0",
		Metrics:                metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				cfg.Namespace: {},
			},
			// C1 (REVIEW): the forwarder's Role grants secrets verbs
			// only with resourceNames: ["ach-jwt-signing-keys"]. K8s
			// RBAC honors resourceNames on list/watch ONLY when the
			// request carries fieldSelector=metadata.name=<name> —
			// without this selector the informer's bare LIST is 403'd
			// by the apiserver and the pod never reaches Ready.
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Field: fields.OneTermEqualSelector("metadata.name", cfg.JWTSecretName),
				},
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("ctrl.NewManager: %w", err)
	}
	out.manager = mgr

	// B2 refactor: resolve LiteLLMConnection/default + masterKey Secret
	// via uncached APIReader. The CR is typically seeded by the cluster
	// hydration step that runs AFTER the forwarder Deployment is applied,
	// so a missing CR/Secret at first boot is expected — poll once per
	// minute until it appears (or the boot ctx is cancelled by k8s
	// liveness restart). Other resolver errors (malformed endpoint,
	// missing secret key) still refuse-to-start so misconfiguration
	// surfaces at boot instead of as 401 storms under load.
	llmRes, err := resolveLiteLLMWithRetry(ctx, pool, mgr.GetAPIReader(), cfg.Namespace, logger)
	if err != nil {
		return out, fmt.Errorf("litellmconn.Resolve: %w", err)
	}
	llmUpstream, err := url.Parse(llmRes.Endpoint)
	if err != nil {
		return out, fmt.Errorf("parse LiteLLMConnection.spec.endpoint %q: %w", llmRes.Endpoint, err)
	}
	if llmUpstream.Scheme != "http" && llmUpstream.Scheme != "https" {
		return out, fmt.Errorf("LiteLLMConnection.spec.endpoint must use http:// or https:// (got %q)", llmUpstream.Scheme)
	}
	ll := litellm.NewRESTClient(llmRes.Endpoint, llmRes.MasterKey, ctrl.Log.WithName("litellm"))

	// Issue #34 C6: BIP + Environment now flow from Postgres-backed
	// caches; controller-runtime informers for those kinds are dropped.
	// The Secret informer for ach-jwt-signing-keys is retained below for
	// JWT seed hot-reload.
	bipCache := bipcache.New(pool, cfg.Namespace, ctrl.Log.WithName("bipcache"))
	envStore := envstore.New(pool, cfg.Namespace, ctrl.Log.WithName("envstore"))
	if err := mgr.Add(runnableFn(bipCache.Run)); err != nil {
		return out, fmt.Errorf("manager.Add(bipcache): %w", err)
	}
	if err := mgr.Add(runnableFn(envStore.Run)); err != nil {
		return out, fmt.Errorf("manager.Add(envstore): %w", err)
	}

	if _, err := mgr.GetCache().GetInformer(ctx, &corev1.Secret{}); err != nil {
		return out, fmt.Errorf("informer Secret: %w", err)
	}

	dbResolver, err := keystore.NewDBResolver(pool, cfg.Pepper)
	if err != nil {
		return out, fmt.Errorf("keystore.NewDBResolver: %w", err)
	}
	cachedResolver, err := keystore.NewCachedResolver(dbResolver, out.redis, cfg.Pepper,
		keystore.WithCacheMetrics(keystoreCollectors))
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

	// W9 (REVIEW): the cached client requires mgr.Start() to populate,
	// and LoadOnce runs before manager start. controller-runtime exposes
	// mgr.GetAPIReader() exactly for this pre-cache scenario — an
	// uncached reader sharing the manager's rest.Config. Previously this
	// path called ctrl.GetConfigOrDie() a second time + client.New,
	// duplicating the in-cluster config lookup.
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.JWTSecretName}
	if err := mgr.GetAPIReader().Get(ctx, key, secret); err != nil {
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

	out.server = forwarder.Deps{
		BIPResolver:      bipCache, // C1/C4: Postgres-backed BIP cache
		EnvProvider:      envStore, // C2/C5: Postgres-backed Env cache
		Resolver:         cachedResolver,
		TeamsResolver:    teamsResolver,
		Signer:           out.signer,
		Logger:           logger,
		BaseURL:          cfg.BaseURL,
		KeyEncryptionKey: cfg.KeyEncryptionKey,
		LiteLLMUpstream:  llmUpstream, // B2: from LiteLLMConnection CR
	}
	return out, nil
}

// runnableFn adapts a `func(ctx) error` to controller-runtime's
// manager.Runnable interface so the bipcache + envstore Run methods can
// be added to the manager without a wrapper type per package.
type runnableFn func(ctx context.Context) error

func (f runnableFn) Start(ctx context.Context) error { return f(ctx) }

func runForwarderServer(ctx context.Context, deps *forwarderProcessDeps, cfg *forwarderConfig) error {
	trafficHandler := forwarder.New(deps.server)
	mgrCacheSync := func(checkCtx context.Context) bool {
		return deps.manager.GetCache().WaitForCacheSync(checkCtx)
	}
	healthHandler := forwarder.NewHealthHandler(deps.signer, mgrCacheSync)

	// Plan 05-06 Task 3 / D-10: /metrics is served on the SAME port as
	// the traffic listener (no separate metrics port). A tiny stdlib
	// ServeMux fronts the chi-built trafficHandler so /metrics has its
	// own dedicated path (precedence by path-specificity per net/http
	// ServeMux semantics) while every other request falls through to
	// the chi router. The metrics handler is unauthenticated; production
	// scrape clients access it via the in-cluster Service IP / Pod IP.
	composedTraffic := http.NewServeMux()
	composedTraffic.Handle("/metrics", deps.metricsHandler)
	composedTraffic.Handle("/", trafficHandler)

	runnable := &forwarder.Runnable{
		TrafficAddr:    cfg.TrafficBindAddr,
		HealthAddr:     cfg.HealthBindAddr,
		TrafficHandler: composedTraffic,
		HealthHandler:  healthHandler,
		Logger:         deps.logger,
	}
	if err := deps.manager.Add(runnable); err != nil {
		return fmt.Errorf("manager.Add(forwarder.Runnable): %w", err)
	}
	return deps.manager.Start(ctx)
}

// resolveLiteLLMWithRetry wraps litellmconn.Resolve in a 60s-interval
// poll loop that retries while the CR or its masterKey Secret hasn't
// been created yet (cluster-hydration race — the forwarder Deployment
// is rolled out before scripts/cluster.sh hydrate_fixtures seeds
// LiteLLMConnection/default). Any other resolver error (malformed
// endpoint, missing secret key, transport error) returns immediately
// so misconfiguration still surfaces at boot.
func resolveLiteLLMWithRetry(
	ctx context.Context,
	pool *pgxpool.Pool,
	reader client.Reader,
	namespace string,
	logger *slog.Logger,
) (*litellmconn.Resolution, error) {
	const retryInterval = 60 * time.Second
	for {
		res, err := litellmconn.Resolve(ctx, pool, reader, namespace)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, litellmconn.ErrLiteLLMConnectionNotReady) && !errors.Is(err, litellmconn.ErrSecretNotFound) {
			return nil, err
		}
		logger.Info("LiteLLMConnection not yet hydrated; will retry",
			"namespace", namespace,
			"err", err.Error(),
			"retryIn", retryInterval.String(),
		)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
	}
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

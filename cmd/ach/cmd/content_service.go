// SPDX-License-Identifier: Apache-2.0

// `ach content-service` serves the §15.6 Content Service surface:
//
//	GET /healthz
//	GET /metrics
//	GET /content/prompt/{name}
//	GET /content/plugin/{name}
//	GET /content/artifact/{name}
//
// Files are streamed from ACH_CACHE_ROOT (default /var/cache/ach), the
// RWO PVC mounted by the operator Pod that this container shares.
//
// Phase 5 D-09 / D-10 / D-16 + spec v4 §5.2 reversal: this binary
// removes the controller-runtime manager that earlier waves held —
// Content Service reads ACH CRD state ONLY from the Postgres projection
// tables (Plan 05-02 schema + Plan 05-04 reconciler writes) and the
// Redis-backed Environment row cache (Plan 05-03 envcache). No
// informers, no cached k8s client, no ACH-scheme registration. RBAC
// belt-and-braces for that removal lives in Plan 05-07's Helm chart;
// the read-side enforcement is here — without an informer cache the
// code physically cannot initiate ACH-CRD reads (T-05-06-05).
//
// /metrics is served on the same chi mux as the traffic listener
// (D-10) — internal cluster network only per T-05-06-01.

package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/contentservice"
	"github.com/ackstorm/ach/internal/contentservice/envcache"
	"github.com/ackstorm/ach/internal/credhash/pepperenv"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

var contentServiceCmd = &cobra.Command{
	Use:   "content-service",
	Short: "Run the ACH artifact content service",
	Long: `Boot the Content Service. Binds /healthz, /metrics, and
/content/{prompt,plugin,artifact}/{name} on
CONTENT_SERVICE_HEALTH_BIND_ADDRESS (default :8082). Streams cached
files from ACH_CACHE_ROOT (default /var/cache/ach) via io.Copy →
sendfile(2). Reads ACH CRD state from Postgres + Redis envcache only
(no Kubernetes informers per spec v4 §5.2).`,
	RunE: runContentService,
}

func init() {
	rootCmd.AddCommand(contentServiceCmd)
}

// contentServiceConfig holds the validated env-var surface; never
// mutated after parseContentServiceConfig returns. Mirrors the
// platformAPIConfig pattern from cmd/ach/cmd/platform_api.go.
type contentServiceConfig struct {
	CacheRoot        string
	Namespace        string
	DBURL            string
	RedisAddr        string
	RedisPassword    string
	RedisTLS         bool
	RedisDB          int
	LiteLLMBaseURL   string
	LiteLLMMasterKey string
	BindAddr         string
	Pepper           []byte
}

// parseContentServiceConfig validates and returns the env-var surface.
// Required vars fail-fast with fmt.Errorf so operators see the missing
// var at boot rather than as a runtime nil-deref. Pepper-placeholder
// rejection delegates to pepperenv.Load (matches Phase 1 D-09 /
// REPLACE-ME-WITH-RANDOM- prefix enforcement).
func parseContentServiceConfig() (*contentServiceConfig, error) {
	cfg := &contentServiceConfig{
		CacheRoot: config.EnvOr("ACH_CACHE_ROOT", "/var/cache/ach"),
		Namespace: config.EnvOr("ACH_NAMESPACE", "ach-system"),
		BindAddr:  config.EnvOr("CONTENT_SERVICE_HEALTH_BIND_ADDRESS", ":8082"),
	}

	var err error
	if cfg.DBURL, err = config.MustEnvNonEmpty("ACH_DB_URL"); err != nil {
		return nil, fmt.Errorf("ACH_DB_URL required (Phase 1 D-18): %w", err)
	}
	if cfg.LiteLLMBaseURL, err = config.MustEnvNonEmpty("ACH_LITELLM_BASE_URL"); err != nil {
		return nil, err
	}
	if cfg.LiteLLMMasterKey, err = config.MustEnvNonEmpty("ACH_LITELLM_MASTER_KEY"); err != nil {
		return nil, err
	}

	cfg.RedisAddr = config.EnvOr("ACH_REDIS_ADDR", "localhost:6379")
	cfg.RedisPassword = os.Getenv("ACH_REDIS_PASSWORD")
	cfg.RedisTLS = config.EnvBool("ACH_REDIS_TLS", false)
	// ACH_REDIS_DB=0 is the default Redis logical DB and a legitimate
	// value; EnvIntNonNeg permits 0 (MustEnvIntPositive would reject).
	if cfg.RedisDB, err = config.EnvIntNonNeg("ACH_REDIS_DB", 0); err != nil {
		return nil, err
	}

	// Pepper: REPLACE-ME-WITH-RANDOM- prefix rejected here by reusing
	// the Phase 1 pepperenv.Load() validator (same code path the operator
	// uses; see internal/credhash/pepperenv/pepperenv.go).
	// Pepper validation: pepperenv.Load() rejects empty values AND
	// the REPLACE-ME-WITH-RANDOM- placeholder prefix (Phase 1 D-09 /
	// Hub §16.1 parity with the operator's enforcement).
	pepper, err := pepperenv.Load()
	if err != nil {
		return nil, fmt.Errorf("ACH_CREDENTIAL_HASH_PEPPER invalid: %w", err)
	}
	cfg.Pepper = pepper

	return cfg, nil
}

func runContentService(cmd *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cfg, err := parseContentServiceConfig()
	if err != nil {
		return fmt.Errorf("parseConfig: %w", err)
	}
	logger.Info("content-service starting",
		"cacheRoot", cfg.CacheRoot, "namespace", cfg.Namespace, "addr", cfg.BindAddr)

	// ─── Postgres pool ───
	pool, err := db.Open(ctx, cfg.DBURL)
	if err != nil {
		return fmt.Errorf("db.Open: %w", err)
	}
	defer pool.Close()

	// ─── Redis client ───
	redisOpts := &redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	if cfg.RedisTLS {
		redisOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec
	}
	redisClient := redis.NewClient(redisOpts)
	defer func() { _ = redisClient.Close() }()

	// ─── LiteLLM REST client (Phase 3 D-25 pattern reused) ───
	// Bridge the slog handler into a logr.Logger; content-service no
	// longer registers a controller-runtime manager (Plan 05-02 §5.2),
	// so the ctrl.Log root is unavailable here.
	liteLLMLog := logr.FromSlogHandler(logger.Handler()).WithName("litellm")
	liteLLM := litellm.NewRESTClient(cfg.LiteLLMBaseURL, cfg.LiteLLMMasterKey, liteLLMLog)

	// ─── audit logger (D-Discretion: one audit event per CS GET) ───
	auditLog := audit.NewLogger(os.Stdout)

	// ─── keystore.Resolver chain (Phase 3 D-08) ───
	dbResolver, err := keystore.NewDBResolver(pool, cfg.Pepper)
	if err != nil {
		return fmt.Errorf("keystore.NewDBResolver: %w", err)
	}
	resolver, err := keystore.NewCachedResolver(dbResolver, redisClient, cfg.Pepper)
	if err != nil {
		return fmt.Errorf("keystore.NewCachedResolver: %w", err)
	}

	// ─── keystore.TeamsResolver chain (Phase 4 D-17) ───
	litellmTeamsResolver, err := keystore.NewLiteLLMTeamsResolver(liteLLM)
	if err != nil {
		return fmt.Errorf("keystore.NewLiteLLMTeamsResolver: %w", err)
	}
	teamsResolver, err := keystore.NewCachedTeamsResolver(litellmTeamsResolver, redisClient)
	if err != nil {
		return fmt.Errorf("keystore.NewCachedTeamsResolver: %w", err)
	}

	// ─── envcache: in-memory Environment snapshot refreshed via
	//     ach_environments_changed LISTEN/NOTIFY (mirrors forwarder envstore).
	//     Replaces the prior Redis 60s-TTL cache; Redis is retained above for
	//     the key/teams resolvers only. ───
	envCacheLog := logr.FromSlogHandler(logger.Handler()).WithName("envcache")
	envCache := envcache.New(pool, cfg.Namespace, envCacheLog)
	// Synchronous initial load fails fast on a bad DB at startup and guarantees
	// the cache is warm before the first request. Run re-refreshes once more
	// (one redundant load, harmless) and then drives the LISTEN loop.
	if err := envCache.Refresh(ctx); err != nil {
		return fmt.Errorf("content-service: initial env cache load: %w", err)
	}
	go func() {
		if err := envCache.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("env cache refresh loop exited", "err", err)
		}
	}()

	// ─── metrics: process-local Registry + ContentServiceCollectors +
	//     shared litellm_unreachable_total (caller="content_service" Inc
	//     happens inside enforceTeams in internal/contentservice/authz.go).
	reg := prometheus.NewRegistry()
	registerRuntimeCollectors(reg)
	csCollectors := metrics.NewContentServiceCollectors(reg)
	csCollectors.PreInitZeroSeries()
	litellmUnreachable := metrics.MustRegisterLitellmUnreachable(reg)
	litellmUnreachable.WithLabelValues("content_service").Add(0) // expose family at 0 (§18.5)

	// ─── Deps wiring (Plan 05-05 D-16 surface) ───
	deps := contentservice.Deps{
		CacheRoot:          cfg.CacheRoot,
		Namespace:          cfg.Namespace,
		Pool:               pool,
		EnvCache:           envCache,
		Resolver:           resolver,
		Teams:              teamsResolver,
		Metrics:            csCollectors,
		LiteLLMUnreachable: litellmUnreachable,
		AuditLog:           auditLog,
		Logger:             logger,
	}

	// ─── chi router: RequestID middleware + /metrics + content routes ───
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// /metrics is unauthenticated on the main traffic listener (D-10);
	// internal cluster network only — see Helm values.yaml + Plan 05-07
	// service exposure note. T-05-06-01 disposition: accept (in-cluster
	// scrape only; ingress MUST NOT route /metrics from external traffic).
	r.Handle("/metrics", metrics.Handler(reg))
	contentservice.RegisterRoutes(r, deps)

	srv := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		// WHY WriteTimeout=0:
		// D-Discretion (Phase 5 CONTEXT.md) — large artifact tarballs may
		// exceed any non-zero deadline; rely on Request.Context()
		// cancellation for client-disconnect propagation. See spec §15.6.
		// /metrics responses are tiny (KBs) and complete well inside
		// stdlib net/http's idle/header bounds (T-05-06-04 accepted).
		WriteTimeout: 0,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.BindAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		logger.Info("shutdown signal received, draining")
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

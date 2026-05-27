// SPDX-License-Identifier: Apache-2.0

// `ach content-service` serves the §15.2 Content Service surface:
//
//	GET /healthz
//	GET /content/prompt/{name}
//	GET /content/plugin/{name}
//	GET /content/artifact/{name}
//
// Files are streamed from ACH_CACHE_ROOT (default /var/cache/ach), the
// RWO PVC mounted by the operator Pod that this container shares. The
// real handler lives in internal/contentservice; this file wires the
// k8s-cached Prompt lookup, the chi router, and graceful shutdown.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/contentservice"
)

var contentServiceCmd = &cobra.Command{
	Use:   "content-service",
	Short: "Run the ACH artifact content service",
	Long: `Boot the Content Service. Binds /healthz and
/content/{prompt,plugin,artifact}/{name} on
CONTENT_SERVICE_HEALTH_BIND_ADDRESS (default :8082). Streams cached
files from ACH_CACHE_ROOT (default /var/cache/ach) using stdlib
http.ServeContent (sendfile-backed on Linux).`,
	RunE: runContentService,
}

func init() {
	rootCmd.AddCommand(contentServiceCmd)
}

func runContentService(cmd *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cacheRoot := config.EnvOr("ACH_CACHE_ROOT", "/var/cache/ach")
	ns := config.EnvOr("ACH_NAMESPACE", "ach-system")
	addr := config.EnvOr("CONTENT_SERVICE_HEALTH_BIND_ADDRESS", ":8082")
	logger.Info("content-service starting",
		"cacheRoot", cacheRoot, "namespace", ns, "addr", addr)

	// Build a controller-runtime manager scoped to the watch namespace
	// just to get a cached client over Prompt. We don't reconcile
	// anything — the manager runs purely for its informer cache.
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(achv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{ns: {}},
		},
		Metrics: metricsserver.Options{BindAddress: "0"}, // operator owns metrics
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()
	mgrErr := make(chan error, 1)
	go func() { mgrErr <- mgr.Start(mgrCtx) }()
	if !mgr.GetCache().WaitForCacheSync(mgrCtx) {
		mgrCancel()
		return errors.New("cache failed to sync")
	}
	logger.Info("informer cache synced")

	r := chi.NewRouter()
	// TODO(Plan 05-06 Task 1): full Deps wiring lands here — this file gets a
	// complete rewrite in Wave 3. Today (post Plan 05-05) the new Deps fields
	// (Pool, Resolver, Teams, EnvCache, Metrics, ...) are zero-valued: requests
	// would panic at runtime. RegisterRoutes accepts the partial Deps so the
	// build stays green between waves 2 and 3; the operator manager wiring
	// above is preserved (no functional regression for /healthz).
	contentservice.RegisterRoutes(r, contentservice.Deps{
		CacheRoot: cacheRoot,
		Namespace: ns,
		Logger:    logger,
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
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
		mgrCancel()
		<-mgrErr
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	case err := <-mgrErr:
		if err != nil {
			return fmt.Errorf("manager error: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		mgrCancel()
		<-mgrErr
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	mgrCancel()
	<-mgrErr
	logger.Info("shutdown complete")
	return nil
}

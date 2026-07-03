// SPDX-License-Identifier: Apache-2.0

// `ach gateway` runs the ACH edge reverse proxy. It fronts the four ACH
// HTTP surfaces behind one origin:
//
//	/platform/      -> ach-platform-api:80
//	/content/       -> ach-content-service:8082
//	/v1/ /gemini/   -> ach-forwarder:80
//	/mcp/ /a2a/     -> ach-forwarder:80
//	/.well-known/   -> ach-forwarder:80   (JWKS)
//	/healthz        -> local 200          (probes)
//
// It is a dumb router: no auth, no /metrics, no /dex (see internal/gateway
// doc.go). Upstream Service DNS is built from POD_NAMESPACE (default
// ach-system). Binds ACH_GATEWAY_BIND_ADDRESS (default :8080).

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

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/gateway"
	"github.com/ackstorm/ach/internal/gateway/agentstore"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Run the ACH edge reverse proxy (single-origin front for all HTTP surfaces)",
	Long: `Boot the ACH gateway. Reverse-proxies /platform, /content, /v1,
/gemini, /mcp, /a2a, and /.well-known to their in-cluster Services, and
serves a local /healthz. Routing is hardcoded; upstream namespace comes
from POD_NAMESPACE (default ach-system). Binds ACH_GATEWAY_BIND_ADDRESS
(default :8080). It performs no authentication and never exposes /metrics
or /dex.`,
	RunE: runGateway,
}

func init() {
	rootCmd.AddCommand(gatewayCmd)
}

func runGateway(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	namespace := config.EnvOr("POD_NAMESPACE", "ach-system")
	bindAddr := config.EnvOr("ACH_GATEWAY_BIND_ADDRESS", ":8080")

	// Optional webhook routing: only when ACH_DB_URL is set. Absent → the
	// gateway is the classic dumb proxy with no /hook route (back-compat).
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	var resolver gateway.UpstreamResolver
	if dbURL := config.EnvOr("ACH_DB_URL", ""); dbURL != "" {
		pool, err := db.Open(rootCtx, dbURL)
		if err != nil {
			return fmt.Errorf("db.Open: %w", err)
		}
		defer pool.Close()
		store := agentstore.New(pool, logr.FromSlogHandler(logger.Handler()))
		go func() { _ = store.Run(rootCtx) }()
		resolver = store // *agentstore.Store satisfies gateway.UpstreamResolver
		logger.Info("webhook routing enabled", "channel", db.AgentsChannel)
	} else {
		logger.Info("webhook routing disabled (ACH_DB_URL unset)")
	}

	routes := gateway.ServiceRoutes(namespace)
	handler, err := gateway.Handler(routes, resolver, logger)
	if err != nil {
		return fmt.Errorf("build gateway handler: %w", err)
	}

	// Access logging: Apache/nginx combined format to stdout, ON by default.
	// ACH_GATEWAY_ACCESS_LOG=combined|common|off (empty => combined).
	accessFmt, known := gateway.ParseAccessLogFormat(os.Getenv("ACH_GATEWAY_ACCESS_LOG"))
	if !known {
		logger.Warn("unknown ACH_GATEWAY_ACCESS_LOG value, defaulting to combined",
			"value", os.Getenv("ACH_GATEWAY_ACCESS_LOG"))
	}
	handler = gateway.AccessLog(os.Stdout, accessFmt, nil)(handler)

	logger.Info("gateway starting", "addr", bindAddr, "namespace", namespace,
		"routes", len(routes), "access_log", string(accessFmt))

	srv := &http.Server{
		Addr:              bindAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout=0: /v1 SSE + /mcp streamable-http have no bounded
		// response size; rely on Request.Context() cancellation for
		// client-disconnect propagation (matches forwarder traffic listener).
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MiB
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", bindAddr)
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
		rootCancel() // stop the agentstore refresh loop promptly
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

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

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/gateway"
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

	routes := gateway.ServiceRoutes(namespace)
	handler, err := gateway.Handler(routes, logger)
	if err != nil {
		return fmt.Errorf("build gateway handler: %w", err)
	}

	logger.Info("gateway starting", "addr", bindAddr, "namespace", namespace, "routes", len(routes))

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

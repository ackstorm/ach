// SPDX-License-Identifier: Apache-2.0

// `ach forwarder` is the Phase 1 stub for the Forwarder service (D-03).
// It exposes /healthz on FORWARDER_HEALTH_BIND_ADDRESS (default :8081) and
// blocks on SIGINT/SIGTERM. The real Forwarder route handlers — MCP/A2A
// proxy with EdDSA-signed identity JWT, BackendIdentityPolicy lookups,
// budget tagging — land in Phase 4; this stub gives the Helm chart's
// Deployment readiness probe a healthy long-running process so Plan 08's
// manager.yaml has a real binary for every container reference. Body
// lifted from ach-old/cmd/forwarder/main.go and adapted to a cobra RunE
// for the single-binary layout.

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
)

var forwarderCmd = &cobra.Command{
	Use:   "forwarder",
	Short: "Run the ACH MCP/A2A forwarder (Phase 1 stub: /healthz only)",
	Long: `Boot the Forwarder service. Phase 1 binds /healthz on
FORWARDER_HEALTH_BIND_ADDRESS (default :8081) and blocks on
SIGINT/SIGTERM; real MCP/A2A proxy handlers land in Phase 4.`,
	RunE: runForwarder,
}

func init() {
	rootCmd.AddCommand(forwarderCmd)
}

func runForwarder(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("Phase 1 stub: forwarder starting", "phase", 1, "binary", "forwarder")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	addr := config.EnvOr("FORWARDER_HEALTH_BIND_ADDRESS", ":8081")
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
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

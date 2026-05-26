// SPDX-License-Identifier: Apache-2.0

// `ach content-service` is the Phase 1 stub for the Content Service
// (D-03). It exposes /healthz on CONTENT_SERVICE_HEALTH_BIND_ADDRESS
// (default :8082) and blocks on SIGINT/SIGTERM. The real surface
// (sendfile(2) streaming of prompt/, plugin/, marketplace/, artifact/
// files with scope-aware authorization) lands in Phase 5; this stub
// gives the Plan 08 Pod's second container a healthy long-running
// process so the readiness probe passes. Body lifted from
// ach-old/cmd/content-service/main.go and adapted to a cobra RunE for
// the single-binary layout.

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

var contentServiceCmd = &cobra.Command{
	Use:   "content-service",
	Short: "Run the ACH artifact content service (Phase 1 stub: /healthz only)",
	Long: `Boot the Content Service. Phase 1 binds /healthz on
CONTENT_SERVICE_HEALTH_BIND_ADDRESS (default :8082) and blocks on
SIGINT/SIGTERM; real sendfile(2)-backed artifact streaming lands in
Phase 5.`,
	RunE: runContentService,
}

func init() {
	rootCmd.AddCommand(contentServiceCmd)
}

func runContentService(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("Phase 1 stub: content-service starting", "phase", 1, "binary", "content-service")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	addr := config.EnvOr("CONTENT_SERVICE_HEALTH_BIND_ADDRESS", ":8082")
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

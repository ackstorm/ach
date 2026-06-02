// SPDX-License-Identifier: Apache-2.0

package forwarder

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Runnable owns BOTH http.Server instances (traffic + health) and
// satisfies sigs.k8s.io/controller-runtime/pkg/manager.Runnable so
// the controller-runtime manager can supervise it alongside informers.
//
// D-04 timeout matrix:
//   - traffic:  WriteTimeout=0 (SSE streaming pass-through)
//   - health:   WriteTimeout=10s (bounded probes)
type Runnable struct {
	TrafficAddr    string
	HealthAddr     string
	TrafficHandler http.Handler
	HealthHandler  http.Handler
	Logger         *slog.Logger
}

// Start runs both servers and blocks until either ctx is cancelled or one
// listener fails. On cancellation both servers receive a 10s graceful
// shutdown. On failure the OTHER server is also shut down before return.
func (r *Runnable) Start(ctx context.Context) error {
	traffic := &http.Server{
		Addr:              r.TrafficAddr,
		Handler:           r.TrafficHandler,
		ReadHeaderTimeout: 5 * time.Second, // gosec G112
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // D-04: SSE streaming pass-through
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	health := &http.Server{
		Addr:              r.HealthAddr,
		Handler:           r.HealthHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64 KiB
	}

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if r.Logger != nil {
			r.Logger.Info("forwarder traffic listener starting", "addr", r.TrafficAddr)
		}
		if err := traffic.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if r.Logger != nil {
			r.Logger.Info("forwarder health listener starting", "addr", r.HealthAddr)
		}
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = traffic.Shutdown(shutdownCtx)
		_ = health.Shutdown(shutdownCtx)
		wg.Wait()
	}
	select {
	case <-ctx.Done():
		shutdown()
		return nil
	case err := <-errCh:
		shutdown()
		return err
	}
}

// NeedLeaderElection returns false — Forwarder is stateless and multi-replica.
// Implementing the LeaderElectionRunnable interface as false also documents
// this so future maintainers don't accidentally toggle it on.
func (r *Runnable) NeedLeaderElection() bool { return false }

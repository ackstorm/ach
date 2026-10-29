// SPDX-License-Identifier: Apache-2.0

package platformapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServerRunnable implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable
// so the HTTP server lifecycle ties to the controller-runtime manager's
// signal context. The http.Server is configured per D-03:
//
//   - ReadHeaderTimeout 5s   (gosec G112 / T-RESOURCE-01)
//   - ReadTimeout       30s  (T-RESOURCE-02)
//   - WriteTimeout      30s
//   - IdleTimeout       120s
//   - MaxHeaderBytes    1 MiB
//
// Graceful shutdown via http.Server.Shutdown(ctx, 10s) tied to the
// manager signal context.
type ServerRunnable struct {
	srv    *http.Server
	logger *slog.Logger

	// listener is set ONLY by tests (via newRunnableWithListener) so
	// the suite can drive Start without binding to a real port. When
	// nil the runnable calls srv.ListenAndServe directly (production
	// behavior).
	listener net.Listener
}

// NewRunnable constructs a *ServerRunnable wrapping the supplied handler
// with the D-03 timeout config. The logger is used ONLY for the
// "draining" log line when the signal context is cancelled.
func NewRunnable(addr string, handler http.Handler, logger *slog.Logger) *ServerRunnable {
	return &ServerRunnable{
		srv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20, // 1 MiB
		},
		logger: logger,
	}
}

// newRunnableWithListener is the test seam — same config as NewRunnable
// but the supplied net.Listener is served instead of binding the
// configured Addr. Used by server_test.go to exercise lifecycle
// without hitting a real port.
func newRunnableWithListener(handler http.Handler, l net.Listener, logger *slog.Logger) *ServerRunnable {
	rn := NewRunnable("", handler, logger)
	rn.listener = l
	return rn
}

// Start blocks until ctx is cancelled OR ListenAndServe returns. On
// cancellation the runnable drains via Shutdown with a 10s timeout
// per D-03; ListenAndServe's http.ErrServerClosed is folded to nil
// (clean shutdown) so the manager sees Start return without error.
func (s *ServerRunnable) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if s.listener != nil {
			errCh <- s.srv.Serve(s.listener)
			return
		}
		errCh <- s.srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		if s.logger != nil {
			s.logger.Info("platform-api: signal received, draining")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	}
}

// NeedLeaderElection returns false — Platform API runs informer-only
// without leader election per D-20.
func (s *ServerRunnable) NeedLeaderElection() bool { return false }

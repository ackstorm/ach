// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a freshly-constructed process-local
// prometheus.Registry. Per Phase 5 D-09, every Hub service builds its
// own Registry via this function — NOT prometheus.DefaultRegisterer —
// so controller-runtime's default-registry collectors (workqueue,
// leader-election, etc.) do not bleed onto the chi /metrics mux and
// so unit tests can construct isolated Registries without
// re-register panics.
//
// Each call returns a NEW *prometheus.Registry; callers must hold and
// reuse the returned pointer for the process's lifetime.
func NewRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// Handler returns the chi-mountable http.Handler that serves the
// Prometheus text-format scrape for the supplied Registry only.
// Per D-10, every Hub service mounts the returned handler at /metrics
// on the same chi mux as its traffic listener.
//
// The handler exposes only collectors registered against reg —
// global DefaultGatherer state is invisible to it (the isolation
// invariant D-09 establishes).
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

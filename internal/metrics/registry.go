// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns the chi-mountable http.Handler that serves the
// Prometheus text-format scrape for the supplied Registry only.
// Per D-10, every Hub service mounts the returned handler at /metrics
// on the same chi mux as its traffic listener.
//
// Per Phase 5 D-09, every Hub service builds its own Registry via
// prometheus.NewRegistry() — NOT prometheus.DefaultRegisterer — so
// controller-runtime's default-registry collectors do not bleed onto
// the chi /metrics mux; the handler exposes only collectors registered
// against reg (global DefaultGatherer state is invisible to it).
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

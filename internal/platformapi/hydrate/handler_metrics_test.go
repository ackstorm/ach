// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

// TestHydrateHandler_ObservesDuration asserts the G7 hydrate-duration timer
// fires exactly once per request. The timer is a defer registered at the top
// of the handler closure, so it records even on the early "auth context
// missing" 500 path — which lets this test exercise the wiring without a real
// Postgres-backed store.
func TestHydrateHandler_ObservesDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	collectors := achmetrics.NewPlatformAPICollectors(reg)

	h := HydrateHandler(Deps{Metrics: collectors})

	rec := httptest.NewRecorder()
	// No KeyContext on the request ctx → handler returns 500 early, but the
	// deferred Observe still runs.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/platform/hydrate", http.NoBody))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var count uint64
	found := false
	for _, fam := range families {
		if fam.GetName() != "ach_platform_api_hydrate_duration_seconds" {
			continue
		}
		found = true
		for _, m := range fam.GetMetric() {
			if h := m.GetHistogram(); h != nil {
				count = h.GetSampleCount()
			}
		}
	}
	if !found {
		t.Fatal("ach_platform_api_hydrate_duration_seconds family not registered")
	}
	if count != 1 {
		t.Errorf("hydrate duration sample count = %d, want 1", count)
	}
}

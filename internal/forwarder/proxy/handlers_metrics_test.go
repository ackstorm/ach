// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	coremetrics "github.com/ackstorm/ach/internal/metrics"

	"github.com/ackstorm/ach/internal/forwarder/metrics"
)

// TestObserveDuration_EmitsHistogram pins §18.5 duration emission: one
// request through a wrapped handler must land exactly one histogram
// sample with the handler's route label.
func TestObserveDuration_EmitsHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := coremetrics.NewForwarderCollectors(reg)
	metrics.InitCollectors(c, nil)
	t.Cleanup(func() { metrics.InitCollectors(nil, nil) })

	h := observeDuration("/v1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	n, err := testutil.GatherAndCount(reg, "ach_forwarder_request_duration_seconds")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if n != 1 {
		t.Fatalf("histogram series = %d, want 1", n)
	}
}

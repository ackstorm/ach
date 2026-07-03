// SPDX-License-Identifier: Apache-2.0

// counters_test.go regression-guards Plan 05-06 Task 2's nil-tolerant
// shim swap. Phase 4 deliberately shipped no instrumentation tests
// because the bodies were no-op; Phase 5 inherits the responsibility
// of asserting:
//
//  1. Inc* calls before InitCollectors do not panic (T-05-06-06
//     mitigation: tests that don't wire the metrics layer keep
//     working — preserves the Phase 4 zero-cost stub semantics).
//  2. After InitCollectors, Inc* delegations land on the wired
//     collectors (the counter goes up; this catches a regression where
//     someone deletes the delegation body or shadows the package var).
//  3. IncLiteLLMUnreachable consistently uses caller="forwarder" — the
//     caller label is hidden inside the shim per D-19 and a regression
//     where the shim labels with an empty string or "unknown" would
//     break the §18.5 cross-component dashboard.
//  4. InitCollectors is last-init-wins so per-test t.Cleanup isolation
//     works (Phase 5 unit tests rely on this when each test wants its
//     own private Registry).
//  5. The /metrics chi-router wiring used by cmd/ach/cmd/forwarder.go
//     (Task 3) actually exposes the wired collectors over HTTP — an
//     in-process precursor to the Plan 05-08 E2E `curl /metrics` gate
//     (OBS-03 verification).
//
// White-box test (same package) so we can reset the unexported
// `collectors` and `litellmUnreachable` vars between test cases.
// Verification uses prometheus/client_golang/prometheus/testutil
// directly on the shared LiteLLM CounterVec (which is exposed by
// MustRegisterLitellmUnreachable) and uses HTTP scrape via the
// chi-mounted handler for the typed ForwarderCollectors (whose
// internal CounterVecs are unexported by design).
//
// stdlib testing + prometheus/client_golang/prometheus/testutil. No
// testify, no fragile counter-vec reach-arounds.

package metrics

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	coremetrics "github.com/ackstorm/ach/internal/metrics"
)

// resetCollectors returns the shim to the "InitCollectors never called"
// state. Tests register this via t.Cleanup so the next test sees a
// fresh slate (avoids inter-test leakage when tests call InitCollectors
// with their own Registries).
func resetCollectors(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		collectors = nil
		litellmUnreachable = nil
	})
}

// scrapeMetrics returns the /metrics text body for reg, exercising
// the SAME chi-router wiring cmd/ach/cmd/forwarder.go uses in Task 3.
// Tests use scrapeMetricLineValue to extract individual sample values.
func scrapeMetrics(t *testing.T, r chi.Router) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// scrapeMetricValue extracts the float value of a Prometheus text-format
// sample line matching `name{labels} <value>`. labelPattern is a regex
// matching the label set in any order. Returns -1 if no match (so the
// caller can distinguish 0 from missing).
func scrapeMetricValue(t *testing.T, body, name, labelPattern string) float64 {
	t.Helper()
	// Sample line shape: `name{l1="v1",l2="v2",...} 3` (one space, no #).
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\{` + labelPattern + `\}\s+([0-9eE.+-]+)\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return -1
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("scrapeMetricValue: parse %q: %v", m[1], err)
	}
	return v
}

// makeMux is the shared chi-router build that EVERY test uses — keeping
// it inline confirms the test exercises the same wiring shape as
// cmd/ach/cmd/forwarder.go's r.Handle("/metrics", metrics.Handler(reg)).
func makeMux(reg *prometheus.Registry) chi.Router {
	r := chi.NewRouter()
	r.Handle("/metrics", coremetrics.Handler(reg))
	return r
}

// TestIncRequests_NilCollectors_NoPanic asserts the Phase 4 stub
// invariant: callers that haven't wired collectors via InitCollectors
// get a no-op (used by every Phase 4 unit test that imports this
// package without setting up a Registry).
func TestIncRequests_NilCollectors_NoPanic(t *testing.T) {
	resetCollectors(t)
	// MUST NOT panic; MUST NOT touch any global state.
	IncRequests("/v1", "pk", "forwarded")
}

// TestIncJWTSigned_NilCollectors_NoPanic mirrors the IncRequests
// nil-tolerance check for the JWT-signed delegation and every other
// shim function.
func TestIncJWTSigned_NilCollectors_NoPanic(t *testing.T) {
	resetCollectors(t)
	IncJWTSigned("MCPServer")
	IncJWTSuppressed("MCPServer", "no_policy")
	IncLiteLLMUnreachable()
	ObserveRequestDuration("/v1", "pk", "2xx", 0.123)
}

// TestIncRequests_AfterInit_Forwards asserts that after InitCollectors
// the shim Inc* delegation actually moves the underlying counter. This
// is the primary regression guard against accidental deletion of the
// delegation body (T-05-06-06). Verifies via /metrics scrape rather
// than reaching into the typed collector's unexported CounterVecs.
func TestIncRequests_AfterInit_Forwards(t *testing.T) {
	resetCollectors(t)
	reg := prometheus.NewRegistry()
	c := coremetrics.NewForwarderCollectors(reg)
	lu := coremetrics.MustRegisterLitellmUnreachable(reg)
	InitCollectors(c, lu)

	IncRequests("/v1", "pk", "forwarded")
	IncRequests("/v1", "pk", "forwarded")
	IncRequests("/v1", "pk", "forwarded")

	body := scrapeMetrics(t, makeMux(reg))
	v := scrapeMetricValue(t, body, "forwarder_requests_total",
		`key_type="pk",outcome="forwarded",route="/v1"`)
	if v != 3 {
		t.Errorf("forwarder_requests_total{route=/v1,key_type=pk,outcome=forwarded}: got %v, want 3\nbody=%s", v, body)
	}
}

// TestIncLiteLLMUnreachable_AfterInit_LabelsForwarder asserts the
// shim hides the caller="forwarder" label internally — the §18.5
// cross-component dashboard depends on this being correct.
func TestIncLiteLLMUnreachable_AfterInit_LabelsForwarder(t *testing.T) {
	resetCollectors(t)
	reg := prometheus.NewRegistry()
	c := coremetrics.NewForwarderCollectors(reg)
	lu := coremetrics.MustRegisterLitellmUnreachable(reg)
	InitCollectors(c, lu)

	for range 5 {
		IncLiteLLMUnreachable()
	}

	got := testutil.ToFloat64(lu.WithLabelValues("forwarder"))
	if got != 5 {
		t.Errorf("litellm_unreachable_total{caller=forwarder}: got %v, want 5", got)
	}
	// Other caller labels MUST be untouched.
	for _, other := range []string{"content_service", "platform_api", "operator"} {
		if v := testutil.ToFloat64(lu.WithLabelValues(other)); v != 0 {
			t.Errorf("litellm_unreachable_total{caller=%s}: got %v, want 0", other, v)
		}
	}
}

// TestIncJWTSuppressed_AllReasons exercises every §18.5 normative
// reason value. Each label set increments once → 4 distinct series.
func TestIncJWTSuppressed_AllReasons(t *testing.T) {
	resetCollectors(t)
	reg := prometheus.NewRegistry()
	c := coremetrics.NewForwarderCollectors(reg)
	lu := coremetrics.MustRegisterLitellmUnreachable(reg)
	InitCollectors(c, lu)

	reasons := []string{"no_policy", "policy_opt_out", "signing_failure", "list_failure"}
	for _, reason := range reasons {
		IncJWTSuppressed("MCPServer", reason)
	}

	body := scrapeMetrics(t, makeMux(reg))
	for _, reason := range reasons {
		v := scrapeMetricValue(t, body, "forwarder_jwt_suppressed_total",
			`kind="MCPServer",reason="`+reason+`"`)
		if v != 1 {
			t.Errorf("forwarder_jwt_suppressed_total{kind=MCPServer,reason=%s}: got %v, want 1\nbody=%s", reason, v, body)
		}
	}
}

// TestInit_ResetSemantics confirms last-init-wins. After InitCollectors
// runs with one Registry's collectors and Inc'ing some metrics, a
// second InitCollectors with a fresh Registry's collectors causes all
// subsequent Inc calls to register against the SECOND Registry.
func TestInit_ResetSemantics(t *testing.T) {
	resetCollectors(t)

	reg1 := prometheus.NewRegistry()
	c1 := coremetrics.NewForwarderCollectors(reg1)
	lu1 := coremetrics.MustRegisterLitellmUnreachable(reg1)
	InitCollectors(c1, lu1)
	IncRequests("/v1", "pk", "forwarded")

	reg2 := prometheus.NewRegistry()
	c2 := coremetrics.NewForwarderCollectors(reg2)
	lu2 := coremetrics.MustRegisterLitellmUnreachable(reg2)
	InitCollectors(c2, lu2)

	IncRequests("/v1", "pk", "forwarded")
	IncRequests("/v1", "pk", "forwarded")

	body1 := scrapeMetrics(t, makeMux(reg1))
	v1 := scrapeMetricValue(t, body1, "forwarder_requests_total",
		`key_type="pk",outcome="forwarded",route="/v1"`)
	if v1 != 1 {
		t.Errorf("reg1 counter: got %v, want 1 (first Inc landed before re-init)", v1)
	}
	body2 := scrapeMetrics(t, makeMux(reg2))
	v2 := scrapeMetricValue(t, body2, "forwarder_requests_total",
		`key_type="pk",outcome="forwarded",route="/v1"`)
	if v2 != 2 {
		t.Errorf("reg2 counter: got %v, want 2 (two Inc after re-init)", v2)
	}

	// Suppress unused-variable warnings for the per-collector pointers
	// — the test exercises the shim layer, not the collectors directly.
	_, _, _, _ = c1, c2, lu1, lu2
}

// TestMetricsHandler_RegistersOnChiMux is the in-process precursor to
// the Plan 05-08 OBS-03 E2E gate. Confirms the chi-router wiring
// pattern cmd/ach/cmd/forwarder.go uses in Task 3 actually exposes
// the wired collectors over HTTP.
func TestMetricsHandler_RegistersOnChiMux(t *testing.T) {
	resetCollectors(t)

	reg := prometheus.NewRegistry()
	c := coremetrics.NewForwarderCollectors(reg)
	lu := coremetrics.MustRegisterLitellmUnreachable(reg)
	InitCollectors(c, lu)

	// Inc once via the shim so the scrape output contains a non-zero
	// sample for forwarder_requests_total AND for the shared
	// litellm_unreachable_total — both Counters omit zero-valued series
	// from the scrape body until first Inc, so both need an Inc call to
	// appear in the assertion below.
	IncRequests("/v1", "pk", "forwarded")
	IncLiteLLMUnreachable()

	r := chi.NewRouter()
	r.Handle("/metrics", coremetrics.Handler(reg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"forwarder_requests_total",
		"litellm_unreachable_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics body missing %q", want)
		}
	}
	// Suppress unused-warning for the per-collector pointer.
	_, _ = c, lu
}

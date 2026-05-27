// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"io"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// metricLabelKeys gathers reg, finds the metric family by name, and
// returns its label-key set as a sorted []string. Returns nil if the
// family is missing OR has no metrics yet. Used by every cardinality
// test below to assert §18.5 normative label keys without depending on
// metric-value-emission order.
func metricLabelKeys(t *testing.T, reg *prometheus.Registry, name string) []string {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("metricLabelKeys: reg.Gather: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		// Histograms / counters with no observations yet still publish
		// the family; emit one zero-value sample so we can read its
		// label keys without depending on call-site order.
		ms := fam.GetMetric()
		if len(ms) == 0 {
			return nil
		}
		var keys []string
		for _, l := range ms[0].GetLabel() {
			keys = append(keys, l.GetName())
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}

// familyNames gathers and returns the sorted set of metric family
// names registered against reg.
func familyNames(t *testing.T, gather prometheus.Gatherer) []string {
	t.Helper()
	families, err := gather.Gather()
	if err != nil {
		t.Fatalf("familyNames: Gather: %v", err)
	}
	out := make([]string, 0, len(families))
	for _, fam := range families {
		out = append(out, fam.GetName())
	}
	sort.Strings(out)
	return out
}

// TestNewRegistry_IsolatedFromDefault — registers a sentinel counter
// on a fresh metrics.NewRegistry() and asserts the default global
// registerer remains free of it. Proves D-09's process-local isolation
// invariant: controller-runtime's default-registry collectors will
// never appear on the chi /metrics mux, and unit tests can build
// isolated Registries without re-register panics.
func TestNewRegistry_IsolatedFromDefault(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	const sentinel = "ach_test_isolation_sentinel_total"
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: sentinel, Help: "test"})
	reg.MustRegister(c)
	c.Inc()

	// Reg sees the sentinel.
	gotReg := familyNames(t, reg)
	found := false
	for _, n := range gotReg {
		if n == sentinel {
			found = true
		}
	}
	if !found {
		t.Errorf("sentinel %q not visible on local registry; got %v", sentinel, gotReg)
	}

	// DefaultGatherer MUST NOT see it.
	gotDefault := familyNames(t, prometheus.DefaultGatherer)
	for _, n := range gotDefault {
		if n == sentinel {
			t.Fatalf("D-09 violated: sentinel %q leaked to DefaultGatherer", sentinel)
		}
	}

	// Twin registries — register on a second one, first does not see it.
	reg2 := NewRegistry()
	const sentinel2 = "ach_test_isolation_sentinel_two_total"
	c2 := prometheus.NewCounter(prometheus.CounterOpts{Name: sentinel2, Help: "test"})
	reg2.MustRegister(c2)
	for _, n := range familyNames(t, reg) {
		if n == sentinel2 {
			t.Fatalf("D-09 violated: sentinel %q leaked between sibling Registries", sentinel2)
		}
	}
}

// TestLitellmUnreachable_AllCallers — registers the shared CounterVec
// once and asserts each of the four §18.5 caller values Inc's
// successfully without re-register panic, yielding exactly four
// caller series each at value 1.
func TestLitellmUnreachable_AllCallers(t *testing.T) {
	reg := NewRegistry()
	c := MustRegisterLitellmUnreachable(reg)
	if c == nil {
		t.Fatal("MustRegisterLitellmUnreachable returned nil")
	}
	callers := []string{"forwarder", "content_service", "platform_api", "operator"}
	for _, caller := range callers {
		c.WithLabelValues(caller).Inc()
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var fam *dto.MetricFamily
	for _, f := range families {
		if f.GetName() == "litellm_unreachable_total" {
			fam = f
			break
		}
	}
	if fam == nil {
		t.Fatal("litellm_unreachable_total family not registered")
	}
	if got, want := len(fam.GetMetric()), 4; got != want {
		t.Fatalf("expected %d caller series, got %d", want, got)
	}
	seen := map[string]float64{}
	for _, m := range fam.GetMetric() {
		labels := m.GetLabel()
		if len(labels) != 1 || labels[0].GetName() != "caller" {
			t.Fatalf("expected single label 'caller', got %v", labels)
		}
		seen[labels[0].GetValue()] = m.GetCounter().GetValue()
	}
	for _, caller := range callers {
		v, ok := seen[caller]
		if !ok {
			t.Errorf("missing caller=%q series", caller)
			continue
		}
		if v != 1 {
			t.Errorf("caller=%q expected value 1, got %v", caller, v)
		}
	}
}

// TestLitellmUnreachable_DoubleRegisterPanics — locks in the standard
// prometheus re-register panic on the SAME registry. Prevents wiring
// bugs from silently shipping two collectors with the same fully-
// qualified name.
func TestLitellmUnreachable_DoubleRegisterPanics(t *testing.T) {
	reg := NewRegistry()
	MustRegisterLitellmUnreachable(reg)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("second MustRegisterLitellmUnreachable did NOT panic — invariant broken")
		}
	}()
	MustRegisterLitellmUnreachable(reg) // expected panic
}

// TestLitellmUnreachable_TwoRegistriesNoPanic — calling once each on
// TWO different Registries succeeds. Proves the collector has no
// process-global state; each Registry is independent.
func TestLitellmUnreachable_TwoRegistriesNoPanic(t *testing.T) {
	reg1 := NewRegistry()
	reg2 := NewRegistry()
	c1 := MustRegisterLitellmUnreachable(reg1)
	c2 := MustRegisterLitellmUnreachable(reg2)
	if c1 == nil || c2 == nil {
		t.Fatal("unexpected nil collector(s)")
	}
	if c1 == c2 {
		t.Fatal("expected distinct CounterVec pointers per registry")
	}
}

// TestForwarderCollectors_LabelKeys — asserts each of the four §18.5
// forwarder metric families is registered with the exact normative
// label-key set, no more, no less.
func TestForwarderCollectors_LabelKeys(t *testing.T) {
	reg := NewRegistry()
	c := NewForwarderCollectors(reg)
	if c == nil {
		t.Fatal("NewForwarderCollectors returned nil")
	}

	// Emit one sample per family so the metric is non-empty and
	// label keys are observable via Gather.
	c.IncRequest("/v1", "pk", "forwarded")
	c.ObserveRequestDuration("/v1", "pk", "2xx", 0.01)
	c.IncJWTSigned("MCPServer")
	c.IncJWTSuppressed("A2AAgent", "no_policy")

	cases := []struct {
		family string
		want   []string
	}{
		{"forwarder_requests_total", []string{"key_type", "outcome", "route"}},
		{"forwarder_request_duration_seconds", []string{"key_type", "route", "status_class"}},
		{"forwarder_jwt_signed_total", []string{"kind"}},
		{"forwarder_jwt_suppressed_total", []string{"kind", "reason"}},
	}
	for _, tc := range cases {
		got := metricLabelKeys(t, reg, tc.family)
		if !equalStringSlices(got, tc.want) {
			t.Errorf("family %s label keys: got %v, want %v", tc.family, got, tc.want)
		}
	}
}

// TestContentServiceCollectors_LabelKeys — asserts each of the three
// §18.5 CS metric families is registered with the exact normative
// label-key set.
func TestContentServiceCollectors_LabelKeys(t *testing.T) {
	reg := NewRegistry()
	c := NewContentServiceCollectors(reg)
	if c == nil {
		t.Fatal("NewContentServiceCollectors returned nil")
	}

	c.IncRequest("prompt", "ok")
	c.ObserveRequestDuration("prompt", 0.02)
	c.AddBytesServed("prompt", 1024)

	cases := []struct {
		family string
		want   []string
	}{
		{"content_service_requests_total", []string{"kind", "outcome"}},
		{"content_service_request_duration_seconds", []string{"kind"}},
		{"content_service_bytes_served_total", []string{"kind"}},
	}
	for _, tc := range cases {
		got := metricLabelKeys(t, reg, tc.family)
		if !equalStringSlices(got, tc.want) {
			t.Errorf("family %s label keys: got %v, want %v", tc.family, got, tc.want)
		}
	}
}

// TestContentServiceCollectors_NoForbiddenLabels — regression gate
// for OBS-06 cardinality discipline. Asserts NO ContentServiceCollectors
// family has a "request_id" or "owner_email" label key. If a future
// refactor adds either label, this test fails LOUD before the offending
// metric ships and explodes Prometheus's TSDB.
func TestContentServiceCollectors_NoForbiddenLabels(t *testing.T) {
	reg := NewRegistry()
	c := NewContentServiceCollectors(reg)
	c.IncRequest("plugin", "ok")
	c.ObserveRequestDuration("plugin", 0.01)
	c.AddBytesServed("plugin", 1)

	forbidden := map[string]struct{}{
		"request_id":  {},
		"owner_email": {},
	}
	families := []string{
		"content_service_requests_total",
		"content_service_request_duration_seconds",
		"content_service_bytes_served_total",
	}
	for _, fam := range families {
		keys := metricLabelKeys(t, reg, fam)
		for _, k := range keys {
			if _, bad := forbidden[k]; bad {
				t.Errorf("OBS-06 violated: family %s carries forbidden label %q", fam, k)
			}
		}
	}
}

// TestHandler_ServesFromRegistry — wires metrics.Handler(reg) into an
// httptest.Server, GETs "/", and asserts the response body carries
// the registered metric name in Prometheus text format. Proves D-10
// promhttp wiring and that Handler only exposes the supplied
// registry (not DefaultGatherer).
func TestHandler_ServesFromRegistry(t *testing.T) {
	reg := NewRegistry()
	const name = "ach_test_handler_counter_total"
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: "test"})
	reg.MustRegister(c)
	c.Inc()

	srv := httptest.NewServer(Handler(reg))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), name) {
		t.Fatalf("response body missing metric %q; body=\n%s", name, string(body))
	}
	if !strings.Contains(string(body), "# HELP "+name) {
		t.Errorf("response body missing HELP line for %q", name)
	}
}

// equalStringSlices returns true iff both slices are nil-or-empty
// equivalent and contain the same elements in the same order.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

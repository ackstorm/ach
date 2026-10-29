// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	core "github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// thanosFake answers /api/v1/query with body(query) and records each query.
func thanosFake(t *testing.T, body func(q string) string) (*Thanos, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		mu.Lock()
		seen = append(seen, q)
		mu.Unlock()
		_, _ = w.Write([]byte(body(q)))
	}))
	t.Cleanup(srv.Close)
	return NewThanos(srv.URL, nil), &seen
}

const oneSeries = `{"status":"success","data":{"result":[{"value":[1,"%s"]}]}}`

func TestPerformanceScopesEveryQueryToTheCaller(t *testing.T) {
	// ports: read_metrics — fixed audited PromQL, the subject is the only label
	th, seen := thanosFake(t, func(string) string { return strings.Replace(oneSeries, "%s", "2.5", 1) })
	rep, err := th.Performance(context.Background(), scope(t, "Ana@Corp.com"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 6 {
		t.Fatalf("queries = %d, want 6", len(*seen))
	}
	for _, q := range *seen {
		if !strings.Contains(q, `user="ana@corp.com"`) || strings.Count(q, "{") != 1 {
			t.Errorf("query not scoped to the caller alone: %s", q)
		}
		if !strings.Contains(q, "["+rep.LatencyP95.Window+"]") {
			t.Errorf("query %s does not cover the window %s", q, rep.LatencyP95.Window)
		}
	}
	if rep.LatencyP95.Value == nil || *rep.LatencyP95.Value != 2.5 || rep.LatencyP95.State != "derived" || rep.LatencyP95.Unit != "seconds" {
		t.Errorf("latency_p95 = %+v", rep.LatencyP95)
	}
}

func TestPerformanceRejectsAnEmailThatCouldBreakTheLabel(t *testing.T) {
	th, seen := thanosFake(t, func(string) string { return "" })
	s, err := core.NewUserScope(`a"}or{x="@x`)
	if err == nil {
		if _, err = th.Performance(context.Background(), s, testWindow()); err == nil {
			t.Fatal("a label-breaking subject must be refused")
		}
	}
	if len(*seen) != 0 {
		t.Errorf("issued %d queries", len(*seen))
	}
}

func TestPerformanceMissingDataIsNotZero(t *testing.T) {
	// ports: read_metrics no_series / no_observations / source_unavailable
	th, _ := thanosFake(t, func(q string) string {
		switch {
		case strings.Contains(q, "histogram_quantile"):
			return strings.Replace(oneSeries, "%s", "NaN", 1) // no observations in the window
		case strings.Contains(q, "failed"):
			return `{"status":"success","data":{"result":[]}}`
		case strings.Contains(q, "mcp"):
			return `not json`
		}
		return `{"status":"success","warnings":["partial"],"data":{"result":[{"value":[1,"7"]}]}}`
	})
	rep, _ := th.Performance(context.Background(), scope(t, "a@x"), testWindow())
	for name, m := range map[string]Metric{"p95": rep.LatencyP95, "failures": rep.Failures, "mcp": rep.MCPCalls} {
		if m.Value != nil || m.State != core.StatePartial || m.Reason == nil {
			t.Errorf("%s = %+v, want null partial with a reason", name, m)
		}
	}
	if *rep.LatencyP95.Reason != "no_observations" || *rep.Failures.Reason != "no_series" || *rep.MCPCalls.Reason != core.SourceUnavailable {
		t.Errorf("reasons = %s %s %s", *rep.LatencyP95.Reason, *rep.Failures.Reason, *rep.MCPCalls.Reason)
	}
	if rep.Requests.Value == nil || rep.Requests.State != core.StatePartial || *rep.Requests.Reason != "source_partial" {
		t.Errorf("a warned answer is a partial value: %+v", rep.Requests)
	}
}

func TestPerformanceNotConfiguredIsNotInstrumented(t *testing.T) {
	var th *Thanos
	rep, err := th.Performance(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil || rep.Failures.State != "not_instrumented" || rep.Failures.Value != nil {
		t.Errorf("rep = %+v err=%v", rep.Failures, err)
	}
}

func TestPerformanceMatchesWireContract(t *testing.T) {
	th, _ := thanosFake(t, func(string) string { return strings.Replace(oneSeries, "%s", "3", 1) })
	var nilTh *Thanos
	for name, th := range map[string]*Thanos{"available": th, "unconfigured": nilTh} {
		rep, _ := th.Performance(context.Background(), scope(t, "a@x"), testWindow())
		b, _ := json.Marshal(rep)
		var v any
		_ = json.Unmarshal(b, &v)
		s, err := jsonschema.NewCompiler().Compile("testdata/contracts/performance.schema.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(v); err != nil {
			t.Errorf("%s: performance does not match the contract: %v\n%s", name, err, b)
		}
	}
}

func TestFailureOwner(t *testing.T) {
	for _, c := range []struct{ class, status, want string }{
		{"ProxyException", "403", "you"}, // model outside the caller's access
		{"Openai.BadRequestError", "400", "you"},
		{"ProxyModelNotFoundError", "400", "you"},
		{"BudgetExceededError", "429", "you"},
		{"Anthropic.RateLimitError", "429", "provider"},
		{"Anthropic.Timeout", "408", "provider"},
		{"Litellm-httpx-handler.Timeout", "", "provider"},
		{"Bedrock.ServiceUnavailableError", "503", "provider"},
		{"MCPToolResultError", "", "tool"},
		{"Exception", "", "unattributed"},
	} {
		if got := failureOwner(c.class, c.status); got != c.want {
			t.Errorf("failureOwner(%q, %q) = %q, want %q", c.class, c.status, got, c.want)
		}
	}
}

func TestFailureCausesByOwner(t *testing.T) {
	th, seen := thanosFake(t, func(q string) string {
		if strings.Contains(q, "exception_class") {
			return `{"status":"success","data":{"result":[
{"metric":{"exception_class":"Anthropic.RateLimitError","exception_status":"429"},"value":[1,"368.2"]},
{"metric":{"exception_class":"ProxyException","exception_status":"403"},"value":[1,"3678.5"]},
{"metric":{"exception_class":"HTTPException","exception_status":"404"},"value":[1,"0"]},
{"metric":{"exception_class":"MCPToolResultError","exception_status":"None"},"value":[1,"16"]}]}}`
		}
		return strings.Replace(oneSeries, "%s", "1", 1)
	})
	rep, _ := th.Performance(context.Background(), scope(t, "a@x"), testWindow())
	fc := rep.FailureCauses
	if fc.State != "derived" || len(fc.Causes) != 3 || fc.Causes[0].Owner != "you" || fc.Causes[1].Owner != "provider" {
		t.Errorf("causes = %+v: zero series dropped, sorted by value", fc)
	}
	if fc.Causes[2].Status != "" || fc.Causes[2].Owner != "tool" {
		t.Errorf("a \"None\" status label is no status: %+v", fc.Causes[2])
	}
	for _, q := range *seen {
		if strings.Contains(q, "exception_class") && !strings.Contains(q, `{user="a@x"}`) {
			t.Errorf("failure causes not scoped to the caller: %s", q)
		}
	}
}

func TestFailureCausesMissingIsNotZero(t *testing.T) {
	th, _ := thanosFake(t, func(q string) string {
		if strings.Contains(q, "exception_class") {
			return `{"status":"success","data":{"result":[]}}`
		}
		return `not json`
	})
	rep, _ := th.Performance(context.Background(), scope(t, "a@x"), testWindow())
	if rep.FailureCauses.State != core.StatePartial || *rep.FailureCauses.Reason != "no_series" {
		t.Errorf("no series must be partial, not zero failures: %+v", rep.FailureCauses)
	}
	var nilTh *Thanos
	rep, _ = nilTh.Performance(context.Background(), scope(t, "a@x"), testWindow())
	if rep.FailureCauses.State != "not_instrumented" || rep.FailureCauses.Causes == nil {
		t.Errorf("unconfigured = %+v", rep.FailureCauses)
	}
}

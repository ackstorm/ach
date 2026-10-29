// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// Performance reads the caller's own LiteLLM gateway metrics from Thanos: p95
// latency and gateway queue time from their histograms, failure, MCP-call and
// request increases over the window, and failures by exception class. LiteLLM labels these series with user = the key owner's email
// (verified live 2026-10-27), the same identity a Scope carries. They are
// estimates: increases extrapolate and can be fractional, p95 interpolates
// histogram buckets. So every value is "derived", never an accounting figure.
//
// ponytail: instant values only; the preview's daily series had no reader.

// Thanos is a read-only Prometheus HTTP API client. A nil *Thanos means not
// configured: every metric is not_instrumented.
type Thanos struct {
	url string
	hc  *http.Client
}

func NewThanos(baseURL string, hc *http.Client) *Thanos {
	if hc == nil {
		hc = &http.Client{Timeout: 35 * time.Second}
	}
	return &Thanos{url: strings.TrimRight(baseURL, "/"), hc: hc}
}

// WithMetrics sets the Thanos source for Performance; nil leaves it
// not_instrumented.
func (r *Reader) WithMetrics(t *Thanos) *Reader {
	r.metrics = t
	return r
}

// Performance reads the caller's gateway metrics (see Thanos.Performance).
func (r *Reader) Performance(ctx context.Context, s core.Scope, w core.Window) (PerformanceReport, error) {
	return r.metrics.Performance(ctx, s, w)
}

type PerformanceReport struct {
	LatencyP95    Metric        `json:"latency_p95"`
	Failures      Metric        `json:"failures"`
	MCPCalls      Metric        `json:"mcp_calls"`
	Requests      Metric        `json:"requests"`
	QueueP95      Metric        `json:"queue_p95"`
	FailureCauses FailureCauses `json:"failure_causes"`
}

// FailureCauses splits the caller's gateway failures by who can act on them,
// from the exception class and HTTP status LiteLLM labels each failure with.
// Values are extrapolated increases, like Failures.
type FailureCauses struct {
	State  string         `json:"state"` // derived | partial | not_instrumented
	Reason *string        `json:"reason"`
	Causes []FailureCause `json:"causes"`
}

type FailureCause struct {
	Owner  string  `json:"owner"` // you | provider | tool | unattributed
	Class  string  `json:"class"`
	Status string  `json:"status"`
	Value  float64 `json:"value"`
}

// failureOwner maps a LiteLLM failure to who can act on it. ProxyException 403
// is the gateway refusing a model outside the caller's access, so it is the
// caller's to change (pick an allowed model or ask for access).
func failureOwner(class, status string) string {
	switch {
	case class == "MCPToolResultError":
		return "tool"
	case class == "BudgetExceededError", class == "ProxyModelNotFoundError",
		status == "400", status == "401", status == "403", status == "404", status == "413", status == "422":
		return "you"
	case status == "408", status == "429", strings.HasPrefix(status, "5"),
		strings.HasSuffix(class, ".Timeout"), strings.HasSuffix(class, "RateLimitError"):
		return "provider"
	}
	return "unattributed"
}

type Metric struct {
	Value       *float64 `json:"value"`
	Unit        string   `json:"unit"`   // seconds | extrapolated_events
	Window      string   `json:"window"` // PromQL range, e.g. "2592000s"
	Source      string   `json:"source"`
	EvaluatedAt string   `json:"evaluated_at"`
	State       string   `json:"state"` // derived | partial | not_instrumented
	Reason      *string  `json:"reason"`
}

// Each query names exactly one label, the caller's; %s is the quoted email,
// %[2]s the range.
var performanceQueries = []struct{ name, unit, promql string }{
	{"latency_p95", "seconds", `histogram_quantile(0.95, sum by (le) (rate(litellm_request_total_latency_metric_bucket{user=%[1]s}[%[2]s])))`},
	{"failures", "extrapolated_events", `sum(increase(litellm_proxy_failed_requests_metric_total{user=%[1]s}[%[2]s]))`},
	{"mcp_calls", "extrapolated_events", `sum(increase(litellm_mcp_tool_calls_total{user=%[1]s}[%[2]s]))`},
	{"requests", "extrapolated_events", `sum(increase(litellm_request_total_latency_metric_count{user=%[1]s}[%[2]s]))`},
	{"queue_p95", "seconds", `histogram_quantile(0.95, sum by (le) (rate(litellm_request_queue_time_seconds_bucket{user=%[1]s}[%[2]s])))`},
}

const failureCausesQuery = `sum by (exception_class, exception_status) (increase(litellm_proxy_failed_requests_metric_total{user=%[1]s}[%[2]s]))`

func (t *Thanos) Performance(ctx context.Context, s core.Scope, w core.Window) (PerformanceReport, error) {
	if !s.Valid() {
		return PerformanceReport{}, core.ErrNoScope
	}
	if !w.Valid() {
		return PerformanceReport{}, core.ErrInvalidArgument
	}
	// A quote or backslash would let the subject escape its label matcher.
	if strings.ContainsAny(s.Email(), "\"\\\n") {
		return PerformanceReport{}, core.ErrNoScope
	}
	at := w.End
	if now := time.Now().UTC(); now.Before(at) {
		at = now
	}
	span := at.Sub(w.Start)
	if span <= 0 {
		return PerformanceReport{}, core.ErrInvalidArgument
	}
	window := strconv.Itoa(int(span.Seconds())) + "s"
	metrics := make([]Metric, len(performanceQueries))
	causes := FailureCauses{State: "not_instrumented", Reason: core.Ptr("not_configured"), Causes: []FailureCause{}}
	var wg sync.WaitGroup
	if t != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			causes = t.failureCauses(ctx, fmt.Sprintf(failureCausesQuery, strconv.Quote(s.Email()), window), at)
		}()
	}
	for i, q := range performanceQueries {
		metrics[i] = Metric{Unit: q.unit, Window: window, Source: "thanos", EvaluatedAt: at.Format(time.RFC3339), State: "not_instrumented", Reason: core.Ptr("not_configured")}
		if t == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := &metrics[i]
			m.Value, m.State, m.Reason = t.instant(ctx, fmt.Sprintf(q.promql, strconv.Quote(s.Email()), window), at)
		}()
	}
	wg.Wait()
	return PerformanceReport{LatencyP95: metrics[0], Failures: metrics[1], MCPCalls: metrics[2], Requests: metrics[3], QueueP95: metrics[4], FailureCauses: causes}, nil
}

// instant evaluates one query at at. A missing series, a NaN or a failed read
// is a null value with a reason, never 0.
func (t *Thanos) instant(ctx context.Context, query string, at time.Time) (*float64, string, *string) {
	partial := func(reason string) (*float64, string, *string) { return nil, core.StatePartial, &reason }
	res, warned, err := t.query(ctx, query, at)
	if err != nil {
		return partial(core.SourceUnavailable)
	}
	if len(res) != 1 {
		return partial("no_series")
	}
	v, ok := sampleValue(res[0].Value)
	if !ok {
		return partial("no_observations")
	}
	if warned {
		return &v, core.StatePartial, core.Ptr("source_partial")
	}
	return &v, stateDerived, nil
}

// failureCauses reads one series per exception class and status. No series is
// partial with a reason, never "no failures": a user who never failed and a
// missing metric look the same to Prometheus.
func (t *Thanos) failureCauses(ctx context.Context, query string, at time.Time) FailureCauses {
	res, warned, err := t.query(ctx, query, at)
	if err != nil {
		return FailureCauses{State: core.StatePartial, Reason: core.Ptr(core.SourceUnavailable), Causes: []FailureCause{}}
	}
	out := FailureCauses{State: stateDerived, Causes: []FailureCause{}}
	if warned {
		out.State, out.Reason = core.StatePartial, core.Ptr("source_partial")
	}
	for _, r := range res {
		v, ok := sampleValue(r.Value)
		if !ok || v <= 0 {
			continue
		}
		class, status := r.Metric["exception_class"], r.Metric["exception_status"]
		if status == "None" { // LiteLLM labels a failure without an HTTP status "None"
			status = ""
		}
		out.Causes = append(out.Causes, FailureCause{Owner: failureOwner(class, status), Class: class, Status: status, Value: v})
	}
	if len(out.Causes) == 0 {
		out.State, out.Reason = core.StatePartial, core.Ptr("no_series")
	}
	sort.Slice(out.Causes, func(i, j int) bool { return out.Causes[i].Value > out.Causes[j].Value })
	return out
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

func (t *Thanos) query(ctx context.Context, query string, at time.Time) ([]promSample, bool, error) {
	q := url.Values{"query": {query}, "time": {at.Format(time.RFC3339)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url+"/api/v1/query?"+q.Encode(), nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Data     struct {
			Result []promSample `json:"result"`
		} `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &body) != nil || body.Status != statusSuccess {
		return nil, false, errors.New("thanos: read failed")
	}
	return body.Data.Result, len(body.Warnings) > 0, nil
}

func sampleValue(v [2]any) (float64, bool) {
	str, _ := v[1].(string)
	f, err := strconv.ParseFloat(str, 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

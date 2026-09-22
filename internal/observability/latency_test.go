// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"fmt"
	"testing"
)

// Ports app/tests/test_latency.py (alitellm-auth) case by case. The Python
// tests build synthetic rows (the same shape LiteLLM emits) rather than
// loading fixtures, so this file does too.

var latencyWindow = Window{Start: "2026-07-05", End: "2026-07-11", Days: 7}

type rowOpts struct {
	durMs     *float64
	status    string
	model     string
	outTokens float64
	ttftMs    *float64
}

// row builds one synthetic spend-log row. ttftMs nil → no
// completionStartTime (non-streaming), matching Python's _row helper.
func row(o rowOpts) SpendLogRow {
	status := o.status
	if status == "" {
		status = "success"
	}
	model := o.model
	if model == "" {
		model = "gemini/flash"
	}
	outTokens := o.outTokens
	if outTokens == 0 {
		outTokens = 100
	}
	r := SpendLogRow{
		RequestDurationMs: o.durMs,
		Status:            status,
		ModelGroup:        model,
		CompletionTokens:  f64p(outTokens),
		StartTime:         "2026-07-08T09:00:00Z",
	}
	if o.ttftMs != nil {
		secs := *o.ttftMs / 1000.0
		r.CompletionStartTime = fmt.Sprintf("2026-07-08T09:00:%06.3fZ", secs)
	}
	return r
}

// ---------------------------------------------------------------------------
// Percentile
// ---------------------------------------------------------------------------

func TestPercentile_EmptyIsNil(t *testing.T) {
	if p := Percentile(nil, 50); p != nil {
		t.Errorf("Percentile = %v, want nil", *p)
	}
}

func TestPercentile_NearestRank(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := Percentile(vals, 50); p == nil || *p != 5.0 {
		t.Errorf("p50 = %v, want 5.0", derefF(p))
	}
	if p := Percentile(vals, 95); p == nil || *p != 10.0 {
		t.Errorf("p95 = %v, want 10.0", derefF(p))
	}
	if p := Percentile(vals, 99); p == nil || *p != 10.0 {
		t.Errorf("p99 = %v, want 10.0", derefF(p))
	}
	if p := Percentile(vals, 0); p == nil || *p != 1.0 {
		t.Errorf("p0 = %v, want 1.0", derefF(p))
	}
}

func TestPercentile_SingleValue(t *testing.T) {
	if p := Percentile([]float64{42.0}, 50); p == nil || *p != 42.0 {
		t.Errorf("p50 = %v, want 42.0", derefF(p))
	}
	if p := Percentile([]float64{42.0}, 99); p == nil || *p != 42.0 {
		t.Errorf("p99 = %v, want 42.0", derefF(p))
	}
}

// ---------------------------------------------------------------------------
// ComputeLatencyContract — happy path
// ---------------------------------------------------------------------------

func TestContract_LatencyPercentiles(t *testing.T) {
	var rows []SpendLogRow
	for x := 100; x < 1100; x += 100 {
		v := float64(x)
		rows = append(rows, row(rowOpts{durMs: &v}))
	}
	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	if !c.Available {
		t.Error("Available = false, want true")
	}
	if c.Sampled {
		t.Error("Sampled = true, want false")
	}
	if c.RowCount != 10 {
		t.Errorf("RowCount = %d, want 10", c.RowCount)
	}
	if c.Window == nil || *c.Window != latencyWindow {
		t.Errorf("Window = %+v, want %+v", c.Window, latencyWindow)
	}
	if c.Latency.P50Ms == nil || *c.Latency.P50Ms != 500.0 {
		t.Errorf("P50Ms = %v, want 500.0", derefF(c.Latency.P50Ms))
	}
	if c.Latency.P95Ms == nil || *c.Latency.P95Ms != 1000.0 {
		t.Errorf("P95Ms = %v, want 1000.0", derefF(c.Latency.P95Ms))
	}
	if c.Latency.AvgMs == nil || *c.Latency.AvgMs != 550.0 {
		t.Errorf("AvgMs = %v, want 550.0", derefF(c.Latency.AvgMs))
	}
}

func TestContract_StatusSplitAndOutcomes(t *testing.T) {
	var rows []SpendLogRow
	dur := 100.0
	for i := 0; i < 9; i++ {
		rows = append(rows, row(rowOpts{durMs: &dur, status: "success"}))
	}
	rows = append(rows, row(rowOpts{durMs: &dur, status: "failure"}))

	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	counts := map[string]int{}
	for _, o := range c.Outcomes {
		counts[o.Status] = o.Count
	}
	if counts["success"] != 9 || counts["failure"] != 1 {
		t.Errorf("counts = %v, want success:9 failure:1", counts)
	}
	if c.Outcomes[0].Status != "success" {
		t.Errorf("Outcomes[0].Status = %q, want success", c.Outcomes[0].Status)
	}
}

func TestContract_ByModelFoldAndFailures(t *testing.T) {
	d200, d400, d600 := 200.0, 400.0, 600.0
	rows := []SpendLogRow{
		row(rowOpts{durMs: &d200, model: "a", status: "success"}),
		row(rowOpts{durMs: &d400, model: "a", status: "failure"}),
		row(rowOpts{durMs: &d600, model: "b", status: "success"}),
	}
	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	byModel := map[string]ModelLatency{}
	for _, m := range c.ByModel {
		byModel[m.Model] = m
	}
	if byModel["a"].Requests != 2 || byModel["a"].Failed != 1 {
		t.Errorf("model a = %+v, want requests:2 failed:1", byModel["a"])
	}
	if byModel["b"].Requests != 1 || byModel["b"].Failed != 0 {
		t.Errorf("model b = %+v, want requests:1 failed:0", byModel["b"])
	}
	if c.ByModel[0].Model != "a" {
		t.Errorf("ByModel[0].Model = %q, want a", c.ByModel[0].Model)
	}
}

func TestContract_TtftAndThroughput(t *testing.T) {
	dur, ttft := 1000.0, 300.0
	rows := []SpendLogRow{row(rowOpts{durMs: &dur, outTokens: 200, ttftMs: &ttft})}
	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	if c.Latency.TtftP50Ms == nil || *c.Latency.TtftP50Ms != 300.0 {
		t.Errorf("TtftP50Ms = %v, want 300.0", derefF(c.Latency.TtftP50Ms))
	}
	if c.Latency.TokensPerSecP50 == nil || *c.Latency.TokensPerSecP50 != 200.0 {
		t.Errorf("TokensPerSecP50 = %v, want 200.0", derefF(c.Latency.TokensPerSecP50))
	}
}

func TestContract_NoTtftWhenNonStreaming(t *testing.T) {
	dur := 1000.0
	rows := []SpendLogRow{row(rowOpts{durMs: &dur})}
	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	if c.Latency.TtftP50Ms != nil {
		t.Errorf("TtftP50Ms = %v, want nil", *c.Latency.TtftP50Ms)
	}
}

// ---------------------------------------------------------------------------
// degradation + bounds
// ---------------------------------------------------------------------------

func TestContract_EmptyRowsAvailableButNull(t *testing.T) {
	c := ComputeLatencyContract([]SpendLogRow{}, latencyWindow, DefaultRowCap, false)
	if !c.Available {
		t.Error("Available = false, want true")
	}
	if c.RowCount != 0 {
		t.Errorf("RowCount = %d, want 0", c.RowCount)
	}
	if c.Latency.P50Ms != nil {
		t.Errorf("P50Ms = %v, want nil", *c.Latency.P50Ms)
	}
	if len(c.Outcomes) != 0 {
		t.Errorf("Outcomes = %v, want empty", c.Outcomes)
	}
	if len(c.ByModel) != 0 {
		t.Errorf("ByModel = %v, want empty", c.ByModel)
	}
}

// TestContract_NilRowsDegrades is the Go analogue of Python's
// test_contract_non_list_degrades: there is no non-list rows value in Go's
// type system, so the nearest equivalent is a nil slice.
func TestContract_NilRowsDegrades(t *testing.T) {
	c := ComputeLatencyContract(nil, latencyWindow, DefaultRowCap, false)
	if !c.Available {
		t.Error("Available = false, want true")
	}
	if c.RowCount != 0 {
		t.Errorf("RowCount = %d, want 0", c.RowCount)
	}
}

func TestContract_RowCapSetsSampled(t *testing.T) {
	dur := 100.0
	rows := make([]SpendLogRow, DefaultRowCap+50)
	for i := range rows {
		rows[i] = row(rowOpts{durMs: &dur})
	}
	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	if !c.Sampled {
		t.Error("Sampled = false, want true")
	}
	if c.RowCount != DefaultRowCap {
		t.Errorf("RowCount = %d, want %d", c.RowCount, DefaultRowCap)
	}
}

func TestContract_TruncatedFlagSetsSampled(t *testing.T) {
	dur := 100.0
	c := ComputeLatencyContract([]SpendLogRow{row(rowOpts{durMs: &dur})}, latencyWindow, DefaultRowCap, true)
	if !c.Sampled {
		t.Error("Sampled = false, want true")
	}
	if c.RowCount != 1 {
		t.Errorf("RowCount = %d, want 1", c.RowCount)
	}
}

func TestContract_FailedRowMissingDurationStillCountsOutcome(t *testing.T) {
	dur := 500.0
	rows := []SpendLogRow{
		row(rowOpts{durMs: nil, status: "failure", model: "x"}),
		row(rowOpts{durMs: &dur, status: "success", model: "x"}),
	}
	c := ComputeLatencyContract(rows, latencyWindow, DefaultRowCap, false)
	counts := map[string]int{}
	for _, o := range c.Outcomes {
		counts[o.Status] = o.Count
	}
	if counts["failure"] != 1 || counts["success"] != 1 {
		t.Errorf("counts = %v, want failure:1 success:1", counts)
	}
	var x ModelLatency
	for _, m := range c.ByModel {
		if m.Model == "x" {
			x = m
		}
	}
	if x.Requests != 2 {
		t.Errorf("x.Requests = %d, want 2", x.Requests)
	}
	if x.Failed != 1 {
		t.Errorf("x.Failed = %d, want 1", x.Failed)
	}
	if x.P50Ms == nil || *x.P50Ms != 500.0 {
		t.Errorf("x.P50Ms = %v, want 500.0", derefF(x.P50Ms))
	}
}

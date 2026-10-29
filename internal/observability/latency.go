// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"math"
	"sort"
	"strings"
	"time"
)

// SpendLogRow is the lean projection of one /spend/logs/v2 row — the only
// fields the fold reads (litellm_client.py _LEAN_SPEND_FIELDS). Nothing
// else is ever decoded, so messages/response/metadata never enter memory.
// A pointer field is nil when the source row omitted or null'd it.
type SpendLogRow struct {
	RequestDurationMs   *float64 `json:"request_duration_ms"`
	Status              string   `json:"status"`
	ModelGroup          string   `json:"model_group"`
	Model               string   `json:"model"`
	CompletionTokens    *float64 `json:"completion_tokens"`
	StartTime           string   `json:"startTime"`
	CompletionStartTime string   `json:"completionStartTime"`
}

// DefaultRowCap bounds how many rows a single fold processes (latency.py
// DEFAULT_ROW_CAP): /spend/logs pagination is a no-op, so LiteLLM may
// return the whole window at once; a heavy user over 7 days is thousands
// of rows. Percentiling at most this many bounds CPU and signals (via
// LatencyContract.Sampled) that the figures are a sample, not exhaustive.
const DefaultRowCap = 5000

// topModels is how many per-model rows to surface, ranked by request count.
const topModels = 8

// statusSuccess is LiteLLM's spend-log "success" status value (not an HTTP
// code) — the only non-failure status.
const statusSuccess = "success"

// Window is the {start, end, days} echoed back verbatim in LatencyContract.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Days  int    `json:"days"`
}

// LatencyFigures is LatencyContract's "latency" block. Every figure is nil
// when no row carries the datum (D-08): a non-streaming window has no
// TTFT; an all-error window has no durations.
type LatencyFigures struct {
	P50Ms           *float64 `json:"p50_ms"`
	P95Ms           *float64 `json:"p95_ms"`
	P99Ms           *float64 `json:"p99_ms"`
	AvgMs           *float64 `json:"avg_ms"`
	TtftP50Ms       *float64 `json:"ttft_p50_ms"`
	TtftP95Ms       *float64 `json:"ttft_p95_ms"`
	TokensPerSecP50 *float64 `json:"tokens_per_sec_p50"`
}

// Outcome is one status's request count, for the request-outcome donut.
type Outcome struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// ModelLatency is one model's fold, ranked by Requests desc, capped at
// topModels.
type ModelLatency struct {
	Model    string   `json:"model"`
	Requests int      `json:"requests"`
	Failed   int      `json:"failed"`
	P50Ms    *float64 `json:"p50_ms"`
	P95Ms    *float64 `json:"p95_ms"`
}

// LatencyContract is the page-ready payload GET /platform/console/latency
// serves verbatim (latency.py compute_latency_contract). Available=false
// (LatencyUnavailable) sends Window/Latency as JSON null and Outcomes/ByModel
// as empty arrays — see §10.2 (a 401 role=unknown /spend/logs/v2 response,
// never a master-key retry).
type LatencyContract struct {
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"`
	Sampled   bool            `json:"sampled"`
	RowCount  int             `json:"row_count"`
	Window    *Window         `json:"window"`
	Latency   *LatencyFigures `json:"latency"`
	Outcomes  []Outcome       `json:"outcomes"`
	ByModel   []ModelLatency  `json:"by_model"`
}

// LatencyUnavailable is the degraded {"available":false,"reason":…}
// contract for when /spend/logs/v2 could not be fetched at all: Window and
// Latency serialize as null, Outcomes and ByModel as empty arrays.
func LatencyUnavailable(reason string) LatencyContract {
	return LatencyContract{Available: false, Reason: reason, Outcomes: []Outcome{}, ByModel: []ModelLatency{}}
}

// Percentile is the nearest-rank percentile of an ALREADY-SORTED slice, nil
// on empty (latency.py percentile). p is in [0, 100]. Nearest-rank (not
// interpolated) is plenty for a latency headline and avoids float-index
// ambiguity: index = ceil(p/100 * n) - 1, clamped to [0, n-1].
func Percentile(sorted []float64, p float64) *float64 {
	n := len(sorted)
	if n == 0 {
		return nil
	}
	if p <= 0 {
		v := sorted[0]
		return &v
	}
	idx := int(math.Ceil(p/100*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx > n-1 {
		idx = n - 1
	}
	v := sorted[idx]
	return &v
}

// parseRowTime parses a LiteLLM spend-log ISO timestamp. RFC3339Nano
// natively accepts a trailing "Z", so no "Z" → "+00:00" rewrite is needed
// the way latency.py's _parse_iso does for datetime.fromisoformat.
func parseRowTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ttftMs is time-to-first-token = completionStartTime - startTime, in ms.
// nil if either timestamp is absent/unparseable, or the delta is negative
// (clock skew) — latency.py _ttft_ms. Only streaming responses carry
// completionStartTime.
func ttftMs(row SpendLogRow) *float64 {
	start, ok1 := parseRowTime(row.StartTime)
	first, ok2 := parseRowTime(row.CompletionStartTime)
	if !ok1 || !ok2 {
		return nil
	}
	delta := first.Sub(start).Seconds() * 1000.0
	if delta < 0 {
		return nil
	}
	return &delta
}

// tokensPerSec is output tokens per wall-second (latency.py _tokens_per_sec).
func tokensPerSec(row SpendLogRow) *float64 {
	if row.CompletionTokens == nil || row.RequestDurationMs == nil {
		return nil
	}
	out := *row.CompletionTokens
	dur := *row.RequestDurationMs
	if out == 0 || dur <= 0 {
		return nil
	}
	v := out / (dur / 1000.0)
	return &v
}

// rowModel is the display model for a row: prefer the public group, fall
// back to the raw model (latency.py _row_model).
func rowModel(row SpendLogRow) string {
	if row.ModelGroup != "" {
		return row.ModelGroup
	}
	if row.Model != "" {
		return row.Model
	}
	return unknownLabel
}

// isFailure mirrors latency.py _is_failure: LiteLLM's spend-log status is
// "success" | "failure" (not an HTTP code). An empty status (SpendLogRow.Status
// collapses "absent" and "null" to "", unlike Python's None-vs-string
// distinction) is treated as not-a-failure rather than Python's literal
// `"".lower() != "success"` — untested either way; see the batch report.
func isFailure(status string) bool {
	return status != "" && strings.ToLower(status) != statusSuccess
}

type modelLatencyAcc struct {
	requests, failed int
	durations        []float64
}

// ComputeLatencyContract folds /spend/logs rows into the page-ready
// latency+outcomes contract (latency.py compute_latency_contract). Pure: no
// mutation of rows, no I/O. truncated is set by the caller when the
// chunked fetch already hit its own row cap (the figures are a sample). A
// nil rows slice yields an available, all-empty contract.
func ComputeLatencyContract(rows []SpendLogRow, w Window, rowCap int, truncated bool) LatencyContract {
	sampled := truncated || len(rows) > rowCap
	safe := rows
	if len(safe) > rowCap {
		safe = safe[:rowCap]
	}

	var durations, ttfts, tokPerSec []float64
	outcomeCounts := map[string]int{}
	perModel := map[string]*modelLatencyAcc{}

	for _, row := range safe {
		model := rowModel(row)
		mrec, ok := perModel[model]
		if !ok {
			mrec = &modelLatencyAcc{}
			perModel[model] = mrec
		}
		mrec.requests++

		label := row.Status
		if label == "" {
			label = unknownLabel
		}
		outcomeCounts[label]++
		if isFailure(row.Status) {
			mrec.failed++
		}

		if row.RequestDurationMs != nil && *row.RequestDurationMs >= 0 {
			durations = append(durations, *row.RequestDurationMs)
			mrec.durations = append(mrec.durations, *row.RequestDurationMs)
		}
		if t := ttftMs(row); t != nil {
			ttfts = append(ttfts, *t)
		}
		if tps := tokensPerSec(row); tps != nil {
			tokPerSec = append(tokPerSec, *tps)
		}
	}

	sort.Float64s(durations)
	sort.Float64s(ttfts)
	sort.Float64s(tokPerSec)

	var avgMs *float64
	if len(durations) > 0 {
		sum := 0.0
		for _, d := range durations {
			sum += d
		}
		v := sum / float64(len(durations))
		avgMs = &v
	}

	latency := LatencyFigures{
		P50Ms:           Percentile(durations, 50),
		P95Ms:           Percentile(durations, 95),
		P99Ms:           Percentile(durations, 99),
		AvgMs:           avgMs,
		TtftP50Ms:       Percentile(ttfts, 50),
		TtftP95Ms:       Percentile(ttfts, 95),
		TokensPerSecP50: Percentile(tokPerSec, 50),
	}

	outcomes := make([]Outcome, 0, len(outcomeCounts))
	for s, c := range outcomeCounts {
		outcomes = append(outcomes, Outcome{Status: s, Count: c})
	}
	sort.SliceStable(outcomes, func(i, j int) bool { return outcomes[i].Count > outcomes[j].Count })

	byModel := make([]ModelLatency, 0, len(perModel))
	for model, rec := range perModel {
		sort.Float64s(rec.durations)
		byModel = append(byModel, ModelLatency{
			Model:    model,
			Requests: rec.requests,
			Failed:   rec.failed,
			P50Ms:    Percentile(rec.durations, 50),
			P95Ms:    Percentile(rec.durations, 95),
		})
	}
	sort.SliceStable(byModel, func(i, j int) bool { return byModel[i].Requests > byModel[j].Requests })
	if len(byModel) > topModels {
		byModel = byModel[:topModels]
	}

	return LatencyContract{
		Available: true,
		Sampled:   sampled,
		RowCount:  len(safe),
		Window:    &w,
		Latency:   &latency,
		Outcomes:  outcomes,
		ByModel:   byModel,
	}
}

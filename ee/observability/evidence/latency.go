// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"math"
	"time"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// Latency times the caller's own generations over the whole window, from the
// trace timestamps rather than LiteLLM's raw spend logs (which age out).
// Duration is start..end; TTFT is LiteLLM's time_to_first_chunk, so only
// streamed calls have one; tokens/s is end-to-end (output / whole duration),
// not streaming speed.
// An unmeasured generation is left out of percentiles and the histogram and
// shows up in source_coverage instead. A generation with no error level is
// "unknown", never "success": the gateway does not observe task success.

const latencyTiming = `
WITH if(end_time >= start_time, dateDiff('microsecond', start_time, end_time) / 1000.0, NULL) AS duration_ms,
     toFloat64OrNull(metadata_values[indexOf(metadata_names, 'attributes.gen_ai.response.time_to_first_chunk')]) * 1000 AS ttft_ms,
     metadata_values[indexOf(metadata_names, 'attributes.gen_ai.response.finish_reasons')] AS finish,
     if(end_time > start_time AND mapContains(usage_details, 'output') AND usage_details['output'] > 0,
        usage_details['output'] / (dateDiff('microsecond', start_time, end_time) / 1000000.0), NULL) AS tokens_sec`

const latencySource = `
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type = 'GENERATION'`

const latencySQL = latencyTiming + `
SELECT count() AS generation_count, count(duration_ms) AS timing_coverage, count(ttft_ms) AS ttft_coverage,
       count(tokens_sec) AS tokens_per_sec_coverage, countIf(level = 'ERROR') AS failed,
       quantileExactOrNull(0.5)(duration_ms) AS p50_ms, quantileExactOrNull(0.95)(duration_ms) AS p95_ms,
       quantileExactOrNull(0.99)(duration_ms) AS p99_ms, avgOrNull(duration_ms) AS avg_ms,
       quantileExactOrNull(0.5)(ttft_ms) AS ttft_p50_ms, quantileExactOrNull(0.95)(ttft_ms) AS ttft_p95_ms,
       quantileExactOrNull(0.5)(tokens_sec) AS tokens_per_sec_p50,
       countIf(finish != '') AS finish_coverage,
       countIf(position(finish, '"length"') > 0) AS truncated,
       countIf(position(finish, '"content_filter"') > 0) AS content_filtered,
       countIf(duration_ms < 100) AS hist0,
       countIf(duration_ms >= 100 AND duration_ms < 500) AS hist1,
       countIf(duration_ms >= 500 AND duration_ms < 1000) AS hist2,
       countIf(duration_ms >= 1000 AND duration_ms < 2000) AS hist3,
       countIf(duration_ms >= 2000 AND duration_ms < 5000) AS hist4,
       countIf(duration_ms >= 5000 AND duration_ms < 10000) AS hist5,
       countIf(duration_ms >= 10000 AND duration_ms < 30000) AS hist6,
       countIf(duration_ms >= 30000 AND duration_ms < 60000) AS hist7,
       countIf(duration_ms >= 60000) AS hist8,
       formatDateTime(minOrNull(start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS first,
       formatDateTime(maxOrNull(start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS last` + latencySource + `
FORMAT JSON`

const latencyModelsSQL = latencyTiming + `
SELECT provided_model_name AS model, count() AS requests, countIf(level = 'ERROR') AS failed,
       quantileExactOrNull(0.5)(duration_ms) AS p50_ms, quantileExactOrNull(0.95)(duration_ms) AS p95_ms,
       quantileExactOrNull(0.5)(ttft_ms) AS ttft_p50_ms,
       quantileExactOrNull(0.5)(tokens_sec) AS tokens_per_sec_p50` + latencySource + `
GROUP BY model ORDER BY requests DESC, model LIMIT 101
FORMAT JSON`

// Guardrail rows carry no user_id: their owner is the key's user, which the
// gateway writes from the authenticated key (__KEY_OWNER_SCOPE__). Each
// execution is one row, so a call checked by two guardrails counts twice.
const latencyGuardrailSQL = `
WITH metadata_values[indexOf(metadata_names, 'attributes.litellm.guardrail.name')] AS guardrail,
     metadata_values[indexOf(metadata_names, 'attributes.litellm.guardrail.status')] AS status
SELECT guardrail AS name, count() AS checked, countIf(status = 'guardrail_intervened') AS intervened
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__KEY_OWNER_SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND has(metadata_names, 'attributes.litellm.guardrail.status')
GROUP BY name ORDER BY checked DESC, name LIMIT 50
FORMAT JSON`

var (
	latencyEdges  = []float64{0, 100, 500, 1000, 2000, 5000, 10000, 30000, 60000}
	latencyLabels = []string{"<100ms", "100–500ms", "500ms–1s", "1–2s", "2–5s", "5–10s", "10–30s", "30–60s", ">=60s"}
)

type LatencyReport struct {
	Available        bool                   `json:"available"`
	Sampled          bool                   `json:"sampled"` // always false: full-window aggregate
	RowCount         *int                   `json:"row_count"`
	Window           DayWindow              `json:"window"`
	Latency          *LatencySummary        `json:"latency"`
	Outcomes         []Outcome              `json:"outcomes"`
	ByModel          []ModelLatency         `json:"by_model"`
	ByModelTruncated bool                   `json:"by_model_truncated"`
	ByModelReason    *string                `json:"by_model_reason,omitempty"`
	Finish           *FinishCounts          `json:"finish"`
	Guardrails       []GuardrailCount       `json:"guardrails"`
	GuardrailsReason *string                `json:"guardrails_reason,omitempty"`
	Histogram        []LatencyBin           `json:"histogram"`
	SourceCoverage   *LatencySourceCoverage `json:"source_coverage,omitempty"`
	Eligibility      string                 `json:"eligibility"`
	Provenance       core.Provenance        `json:"provenance"`
	Reason           *string                `json:"reason,omitempty"`
}

// DayWindow is the latency contract's window: UTC dates and a day count.
type DayWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Days  int    `json:"days"`
}

type LatencySummary struct {
	P50             *float64 `json:"p50_ms"`
	P95             *float64 `json:"p95_ms"`
	P99             *float64 `json:"p99_ms"`
	Avg             *float64 `json:"avg_ms"`
	TTFTP50         *float64 `json:"ttft_p50_ms"`
	TTFTP95         *float64 `json:"ttft_p95_ms"`
	TokensPerSecP50 *float64 `json:"tokens_per_sec_p50"`
}

// FinishCounts are generations whose finish reasons include length (cut at
// the output limit) or content_filter, out of those that report any.
type FinishCounts struct {
	Coverage        int `json:"coverage"`
	Truncated       int `json:"truncated"`
	ContentFiltered int `json:"content_filtered"`
}

// GuardrailCount is one guardrail's executions on the caller's calls. A nil
// list is unread, an empty one means no guardrail ran.
type GuardrailCount struct {
	Name       string `json:"name"`
	Checked    int    `json:"checked"`
	Intervened int    `json:"intervened"`
}

type guardrailRow struct {
	Name       string  `json:"name"`
	Checked    float64 `json:"checked"`
	Intervened float64 `json:"intervened"`
}

type Outcome struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type ModelLatency struct {
	Model           string   `json:"model"`
	Requests        int      `json:"requests"`
	Failed          int      `json:"failed"`
	P50             *float64 `json:"p50_ms"`
	P95             *float64 `json:"p95_ms"`
	TTFTP50         *float64 `json:"ttft_p50_ms"`
	TokensPerSecP50 *float64 `json:"tokens_per_sec_p50"`
}

type LatencyBin struct {
	Label   string   `json:"label"`
	LowerMS float64  `json:"lower_ms"`
	UpperMS *float64 `json:"upper_ms"`
	Count   int      `json:"count"`
}

type LatencySourceCoverage struct {
	GenerationCount int `json:"generation_count"`
	Timing          int `json:"timing"`
	TTFT            int `json:"ttft"`
	TokensPerSec    int `json:"tokens_per_sec"`
}

type latencyAgg struct {
	GenerationCount      float64    `json:"generation_count"`
	TimingCoverage       float64    `json:"timing_coverage"`
	TTFTCoverage         float64    `json:"ttft_coverage"`
	TokensPerSecCoverage float64    `json:"tokens_per_sec_coverage"`
	Failed               float64    `json:"failed"`
	P50                  *float64   `json:"p50_ms"`
	P95                  *float64   `json:"p95_ms"`
	P99                  *float64   `json:"p99_ms"`
	Avg                  *float64   `json:"avg_ms"`
	TTFTP50              *float64   `json:"ttft_p50_ms"`
	TTFTP95              *float64   `json:"ttft_p95_ms"`
	TokensPerSecP50      *float64   `json:"tokens_per_sec_p50"`
	FinishCoverage       float64    `json:"finish_coverage"`
	Truncated            float64    `json:"truncated"`
	ContentFiltered      float64    `json:"content_filtered"`
	Hist0                float64    `json:"hist0"`
	Hist1                float64    `json:"hist1"`
	Hist2                float64    `json:"hist2"`
	Hist3                float64    `json:"hist3"`
	Hist4                float64    `json:"hist4"`
	Hist5                float64    `json:"hist5"`
	Hist6                float64    `json:"hist6"`
	Hist7                float64    `json:"hist7"`
	Hist8                float64    `json:"hist8"`
	First                *time.Time `json:"first"`
	Last                 *time.Time `json:"last"`
}

type modelLatencyRow struct {
	Model           string   `json:"model"`
	Requests        float64  `json:"requests"`
	Failed          float64  `json:"failed"`
	P50             *float64 `json:"p50_ms"`
	P95             *float64 `json:"p95_ms"`
	TTFTP50         *float64 `json:"ttft_p50_ms"`
	TokensPerSecP50 *float64 `json:"tokens_per_sec_p50"`
}

func (r *Reader) Latency(ctx context.Context, s core.Scope, w core.Window) (LatencyReport, error) {
	if !s.Valid() {
		return LatencyReport{}, core.ErrNoScope
	}
	if !w.Valid() {
		return LatencyReport{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return LatencyReport{}, core.ErrProjectNotAllowed
	}
	rep := LatencyReport{
		Window: DayWindow{
			Start: w.Start.UTC().Format(time.DateOnly), End: w.End.UTC().Format(time.DateOnly),
			Days: int(math.Ceil(w.End.Sub(w.Start).Hours() / 24)),
		},
		Outcomes: []Outcome{}, ByModel: []ModelLatency{}, Histogram: []LatencyBin{},
		Eligibility: "observed_generations_not_logical_requests",
	}
	params := map[string]string{}
	core.WindowParams(w, params)
	var agg []latencyAgg
	if err := r.CH().Query(ctx, core.Scoped(latencySQL, r.Projects(), s, params), params, 1, &agg); err != nil || len(agg) != 1 {
		rep.Reason = core.Ptr(core.SourceUnavailable)
		rep.Provenance = core.UnavailableProvenance(w, "observed_generation", core.SourceUnavailable)
		return rep, nil
	}
	a := agg[0]
	n, failed := int(a.GenerationCount), int(a.Failed)
	rep.Available, rep.RowCount = true, &n
	rep.Latency = &LatencySummary{P50: a.P50, P95: a.P95, P99: a.P99, Avg: a.Avg, TTFTP50: a.TTFTP50, TTFTP95: a.TTFTP95, TokensPerSecP50: a.TokensPerSecP50}
	rep.Outcomes = []Outcome{{statusError, failed}, {core.StatusUnknown, n - failed}}
	for i, c := range []float64{a.Hist0, a.Hist1, a.Hist2, a.Hist3, a.Hist4, a.Hist5, a.Hist6, a.Hist7, a.Hist8} {
		bin := LatencyBin{Label: latencyLabels[i], LowerMS: latencyEdges[i], Count: int(c)}
		if i+1 < len(latencyEdges) {
			bin.UpperMS = &latencyEdges[i+1]
		}
		rep.Histogram = append(rep.Histogram, bin)
	}
	if a.FinishCoverage > 0 {
		rep.Finish = &FinishCounts{Coverage: int(a.FinishCoverage), Truncated: int(a.Truncated), ContentFiltered: int(a.ContentFiltered)}
	}
	rep.SourceCoverage = &LatencySourceCoverage{GenerationCount: n, Timing: int(a.TimingCoverage), TTFT: int(a.TTFTCoverage), TokensPerSec: int(a.TokensPerSecCoverage)}
	rep.Provenance = core.NewProvenance(w, "observed_generation", "all scoped deduplicated source observations in requested window", "full-window-final-aggregate")
	rep.Provenance.Coverage = core.Coverage{Numerator: n, Denominator: &n}
	rep.Provenance.ObservedStart, rep.Provenance.ObservedEnd = a.First, a.Last

	params = map[string]string{}
	core.WindowParams(w, params)
	var models []modelLatencyRow
	if err := r.CH().Query(ctx, core.Scoped(latencyModelsSQL, r.Projects(), s, params), params, core.GroupCap+1, &models); err != nil {
		rep.ByModelReason = core.Ptr(core.SourceUnavailable)
	}
	for i, m := range models {
		if i == core.GroupCap {
			break
		}
		name := m.Model
		if len(name) > 200 {
			name = name[:200]
		}
		rep.ByModel = append(rep.ByModel, ModelLatency{Model: name, Requests: int(m.Requests), Failed: int(m.Failed), P50: m.P50, P95: m.P95, TTFTP50: m.TTFTP50, TokensPerSecP50: m.TokensPerSecP50})
	}
	rep.ByModelTruncated = len(models) > core.GroupCap

	params = map[string]string{}
	core.WindowParams(w, params)
	var guards []guardrailRow
	if err := r.CH().Query(ctx, core.Scoped(latencyGuardrailSQL, r.Projects(), s, params), params, 50, &guards); err != nil {
		rep.GuardrailsReason = core.Ptr(core.SourceUnavailable)
		return rep, nil
	}
	rep.Guardrails = []GuardrailCount{}
	for _, g := range guards {
		rep.Guardrails = append(rep.Guardrails, GuardrailCount{Name: g.Name, Checked: int(g.Checked), Intervened: int(g.Intervened)})
	}
	return rep, nil
}

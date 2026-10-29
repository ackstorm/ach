// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"slices"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// Workloads counts the caller's generations by client runtime (Claude Code,
// OpenCode, ...), with their input/output tokens and recorded cost. The User-Agent is never on the caller's own generation: only
// the unowned gateway root of the trace carries it (measured: every trace has
// exactly one). So a generation takes the runtime of its trace, and the report
// says so with runtime_attribution = same_trace. A trace with no User-Agent
// stays "unknown"; nothing is inferred from tool or model names.
//
// The multiIf is the audit-runtime-v1 first-match rule of the Python preview's
// runtime.parse_runtime.
const workloadsSQL = `
WITH traces AS (
  SELECT project_id, trace_id,
         anyIf(lower(metadata_values[indexOf(metadata_names, 'attributes.http.user_agent')]),
               has(metadata_names, 'attributes.http.user_agent')) AS agent
  FROM default.events_full AS source FINAL
  WHERE is_deleted = 0 AND (__UNOWNED_SCOPE__) AND user_id = ''
    AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
    AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  GROUP BY project_id, trace_id
)
SELECT multiIf(agent = '', 'unknown',
               position(agent, 'claude') > 0, 'claude_code',
               position(agent, 'opencode') > 0, 'opencode',
               position(agent, 'codex') > 0, 'codex',
               position(agent, 'python') > 0 OR position(agent, 'httpx') > 0, 'python_client',
               position(agent, 'node') > 0 OR position(agent, 'undici') > 0, 'node_client',
               'other') AS name,
       count() AS count,
       sumIf(usage_details['input'], mapContains(usage_details, 'input')) AS input_tokens,
       sumIf(usage_details['output'], mapContains(usage_details, 'output')) AS output_tokens,
       sumIf(toFloat64(cost_details['total']), mapContains(cost_details, 'total')) AS cost,
       medianExactOrNullIf(usage_details['input'], mapContains(usage_details, 'input')) AS median_input,
       sumIf(usage_details['input'], mapContains(usage_details, 'input') AND mapContains(usage_details, 'input_cached_tokens')) AS cache_pair_input,
       sumIf(usage_details['input_cached_tokens'], mapContains(usage_details, 'input') AND mapContains(usage_details, 'input_cached_tokens')) AS cache_pair_read,
       countIf(mapContains(usage_details, 'input') AND mapContains(usage_details, 'input_cached_tokens')) AS cache_coverage,
       countIf(toFloat64(cost_details['total']) > 0) AS priced,
       sumIf(toFloat64(cost_details['total']), toFloat64(cost_details['total']) > 0) AS priced_cost
FROM default.events_full AS source FINAL
LEFT JOIN traces USING (project_id, trace_id)
WHERE is_deleted = 0 AND (__SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type = 'GENERATION'
GROUP BY name
FORMAT JSON`

var runtimeNames = []string{core.StatusUnknown, "claude_code", "opencode", "codex", "python_client", "node_client", "other"}

type WorkloadReport struct {
	Window                 core.Window             `json:"window"`
	Provenance             core.Provenance         `json:"provenance"`
	GenerationCount        *int                    `json:"generation_count"`
	Eligibility            string                  `json:"eligibility"`
	Runtimes               []RuntimeUsage          `json:"runtimes"`
	RuntimeParserVersion   int                     `json:"runtime_parser_version"`
	RuntimeAttribution     string                  `json:"runtime_attribution"`
	BusinessClassification *string                 `json:"business_classification"` // not classified yet
	TaskOutcomes           *string                 `json:"task_outcomes"`           // not observed
	SourceCoverage         *WorkloadSourceCoverage `json:"source_coverage,omitempty"`
}

// RuntimeUsage is one runtime's generations and what they used. A generation
// with no recorded usage or cost adds 0, so subscription (zero-cost) traffic
// shows as $0, not as missing.
type RuntimeUsage struct {
	Name         string  `json:"name"`
	Count        int     `json:"count"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
	// Per-client shape, each over the generations that carry it; null when
	// none does. CostPerGeneration counts priced (cost > 0) generations only,
	// so subscription traffic does not pull it to 0.
	MedianInput       *float64 `json:"median_input"`
	CacheReadShare    *float64 `json:"cache_read_share"`
	CostPerGeneration *float64 `json:"cost_per_generation"`
}

type WorkloadSourceCoverage struct {
	GenerationCount int `json:"generation_count"`
	Runtime         int `json:"runtime"` // generations with a known runtime
}

func (r *Reader) Workloads(ctx context.Context, s core.Scope, w core.Window) (WorkloadReport, error) {
	if !s.Valid() {
		return WorkloadReport{}, core.ErrNoScope
	}
	if !w.Valid() {
		return WorkloadReport{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return WorkloadReport{}, core.ErrProjectNotAllowed
	}
	rep := WorkloadReport{
		Window: w, Eligibility: "observed_generations_not_logical_requests",
		Runtimes: []RuntimeUsage{}, RuntimeParserVersion: 1, RuntimeAttribution: "same_trace",
	}
	params := map[string]string{}
	core.WindowParams(w, params)
	var rows []struct {
		Name           string   `json:"name"`
		Count          float64  `json:"count"`
		InputTokens    float64  `json:"input_tokens"`
		OutputTokens   float64  `json:"output_tokens"`
		Cost           float64  `json:"cost"`
		MedianInput    *float64 `json:"median_input"`
		CachePairInput float64  `json:"cache_pair_input"`
		CachePairRead  float64  `json:"cache_pair_read"`
		CacheCoverage  float64  `json:"cache_coverage"`
		Priced         float64  `json:"priced"`
		PricedCost     float64  `json:"priced_cost"`
	}
	failed := r.CH().Query(ctx, core.Scoped(workloadsSQL, r.Projects(), s, params), params, len(runtimeNames), &rows) != nil
	for _, row := range rows {
		failed = failed || !slices.Contains(runtimeNames, row.Name)
	}
	if failed {
		rep.Provenance = core.UnavailableProvenance(w, "observed_generation", core.SourceUnavailable)
		return rep, nil
	}
	total, known := 0, 0
	for _, row := range rows {
		u := RuntimeUsage{Name: row.Name, Count: int(row.Count), InputTokens: int(row.InputTokens), OutputTokens: int(row.OutputTokens), Cost: row.Cost, MedianInput: row.MedianInput}
		if row.CacheCoverage > 0 {
			u.CacheReadShare = core.Ratio(&row.CachePairRead, &row.CachePairInput)
		}
		if row.Priced > 0 {
			u.CostPerGeneration = core.Ratio(&row.PricedCost, &row.Priced)
		}
		rep.Runtimes = append(rep.Runtimes, u)
		total += int(row.Count)
		if row.Name != core.StatusUnknown {
			known += int(row.Count)
		}
	}
	slices.SortFunc(rep.Runtimes, func(a, b RuntimeUsage) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		if a.Name < b.Name {
			return -1
		}
		return 1
	})
	rep.GenerationCount = &total
	rep.SourceCoverage = &WorkloadSourceCoverage{GenerationCount: total, Runtime: known}
	rep.Provenance = core.NewProvenance(w, "observed_generation", "all scoped deduplicated source observations in requested window", "full-window-final-aggregate")
	rep.Provenance.Coverage = core.Coverage{Numerator: total, Denominator: &total}
	return rep, nil
}

// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// DailyActivity is the merged LiteLLM /user/daily/activity window (all
// pages already concatenated by the caller): Metadata's total_* figures are
// pre-summed across pages, Results carries one raw per-day row per page.
type DailyActivity struct {
	Results  []json.RawMessage `json:"results"`
	Metadata map[string]any    `json:"metadata"`
}

// DaySeries is one point on the Daily Spend / Requests-by-Day chart.
type DaySeries struct {
	Date         string  `json:"date"`
	Spend        float64 `json:"spend"`
	Requests     int     `json:"requests"`
	Tokens       int     `json:"tokens"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Failed       int     `json:"failed"`
}

// ModelAgg is one model's totals summed across every day in the window.
type ModelAgg struct {
	Model           string
	Requests        int
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	CacheReadTokens int
	Spend           float64
}

// KeyAgg is one API key's totals summed across every day in the window.
type KeyAgg struct {
	ID       string
	KeyAlias *string
	Requests int
	Spend    float64
}

// WindowAggregate is the output of AggregateWindow for a single
// daily-activity window (current or prior/compare).
type WindowAggregate struct {
	Requests         int
	Tokens           int
	Spend            float64
	FailedRequests   int
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	Series           []DaySeries
	Models           []ModelAgg
	Keys             []KeyAgg
}

// Deltas is the period-over-period percentage change per headline metric.
type Deltas struct {
	RequestsPct           *float64 `json:"requests_pct"`
	TokensPct             *float64 `json:"tokens_pct"`
	SpendPct              *float64 `json:"spend_pct"`
	AvgCostPer1MTokensPct *float64 `json:"avg_cost_per_1m_tokens_pct"`
}

// TotalsOut is StatsContract's "totals" block.
type TotalsOut struct {
	Requests           int      `json:"requests"`
	Tokens             int      `json:"tokens"`
	Spend              float64  `json:"spend"`
	FailedRequests     int      `json:"failed_requests"`
	InputTokens        int      `json:"input_tokens"`
	OutputTokens       int      `json:"output_tokens"`
	CacheReadTokens    int      `json:"cache_read_tokens"`
	CacheHitPct        *float64 `json:"cache_hit_pct"`
	AvgCostPer1MTokens *float64 `json:"avg_cost_per_1m_tokens"`
	Deltas             Deltas   `json:"deltas"`
}

// ModelOut is one row of StatsContract's "models" table.
type ModelOut struct {
	Model           string   `json:"model"`
	Requests        int      `json:"requests"`
	InputTokens     int      `json:"input_tokens"`
	OutputTokens    int      `json:"output_tokens"`
	TotalTokens     int      `json:"total_tokens"`
	CacheReadTokens int      `json:"cache_read_tokens"`
	Spend           float64  `json:"spend"`
	SpendPct        *float64 `json:"spend_pct"`
	LastUsed        *string  `json:"last_used"`
}

// KeyOut is one row of StatsContract's "keys" table, ranked by spend desc.
type KeyOut struct {
	ID       string   `json:"id"`
	KeyAlias *string  `json:"key_alias"`
	Requests int      `json:"requests"`
	Spend    float64  `json:"spend"`
	SpendPct *float64 `json:"spend_pct"`
}

// Budget is the canonical {current, max_budget, budget_duration, source}
// block — both BuildStatsContract's input and BudgetBlock's (Task 2) output.
type Budget struct {
	Current        float64
	MaxBudget      *float64
	BudgetDuration *string
	Source         string
}

// BudgetSummary is Budget plus the derived pct/has_budget figures shown in
// StatsContract's "budget" block (stats.py build_stats_contract budget_block).
type BudgetSummary struct {
	Current        float64  `json:"current"`
	MaxBudget      *float64 `json:"max_budget"`
	BudgetDuration *string  `json:"budget_duration"`
	Source         string   `json:"source"`
	Pct            *float64 `json:"pct"`
	HasBudget      bool     `json:"has_budget"`
}

// Capabilities flags which optional figures the caller supplied source data
// for (per_model_last_used may be flipped false by BuildStatsContract when
// lastUsed is empty; the rest are never mutated here).
type Capabilities struct {
	TokenSplit       bool `json:"token_split"`
	PerModelLastUsed bool `json:"per_model_last_used"`
	Deltas           bool `json:"deltas"`
	PerKeySpend      bool `json:"per_key_spend"`
}

// Range is StatsContract's "range" block — the window + its compare window.
type Range struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Days    int    `json:"days"`
	Compare struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"compare"`
}

// StatsContract is the full page-ready payload GET /platform/console/stats
// serves verbatim (stats.py build_stats_contract).
type StatsContract struct {
	Range        Range         `json:"range"`
	Totals       TotalsOut     `json:"totals"`
	Series       []DaySeries   `json:"series"`
	Models       []ModelOut    `json:"models"`
	Keys         []KeyOut      `json:"keys"`
	Budget       BudgetSummary `json:"budget"`
	Capabilities Capabilities  `json:"capabilities"`
}

// zeroFillSeries fills missing in-window days with real-zero rows, in
// chronological order (stats.py _zero_fill_series). LiteLLM's results[]
// omits days with no activity, which would otherwise make a line chart
// interpolate across the gap — an in-window day with no usage IS a real
// zero (D-08). Defensive (D-09): an unparseable bound, an inverted range,
// or a span over 400 days returns the series unchanged.
func zeroFillSeries(series []DaySeries, startISO, endISO string) []DaySeries {
	start, ok1 := parseDatePrefix(startISO)
	end, ok2 := parseDatePrefix(endISO)
	if !ok1 || !ok2 {
		return series
	}
	if end.Before(start) || int(end.Sub(start).Hours()/24)+1 > 400 {
		return series
	}

	byDay := make(map[string]DaySeries, len(series))
	for _, p := range series {
		if p.Date == "" {
			continue
		}
		key := p.Date
		if len(key) > 10 {
			key = key[:10]
		}
		byDay[key] = p
	}

	filled := make([]DaySeries, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if p, ok := byDay[key]; ok {
			filled = append(filled, p)
			continue
		}
		filled = append(filled, DaySeries{Date: key})
	}
	return filled
}

func parseDatePrefix(s string) (time.Time, bool) {
	if len(s) > 10 {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func dayEntry(day map[string]any) DaySeries {
	m := asMap(day["metrics"])
	return DaySeries{
		Date:         asString(day["date"]),
		Spend:        num(m["spend"]),
		Requests:     int(num(m["api_requests"])),
		Tokens:       int(num(m["total_tokens"])),
		InputTokens:  int(num(m["prompt_tokens"])),
		OutputTokens: int(num(m["completion_tokens"])),
		Failed:       int(num(m["failed_requests"])),
	}
}

// accumulateDaySeries merges one results[] row into the accumulator for its
// date (stats.py _accumulate_day_series). LiteLLM's /user/daily/activity
// pagination is not always day-aligned — a single busy day's rows can be
// split across multiple pages, each carrying its OWN row for the SAME date.
// Without merging, a later page's row would overwrite an earlier page's row
// for that date, silently undercounting the day. idx gives a null-dated row
// its own accumulator slot instead of merging with other null-dated rows.
func accumulateDaySeries(dayAcc map[string]*DaySeries, idx int, day map[string]any) {
	entry := dayEntry(day)
	key := entry.Date
	if key == "" {
		key = fmt.Sprintf("\x00%d", idx)
	}
	acc, ok := dayAcc[key]
	if !ok {
		acc = &DaySeries{Date: entry.Date}
		dayAcc[key] = acc
	}
	acc.Spend += entry.Spend
	acc.Requests += entry.Requests
	acc.Tokens += entry.Tokens
	acc.InputTokens += entry.InputTokens
	acc.OutputTokens += entry.OutputTokens
	acc.Failed += entry.Failed
}

// accumulateDayModels folds one day's breakdown.models into the per-model
// accumulator, summed ACROSS days (stats.py _accumulate_day_models).
func accumulateDayModels(modelAcc map[string]*ModelAgg, breakdown map[string]any) {
	models := asMap(breakdown["models"])
	for name, raw := range models {
		mblock := asMap(raw)
		if mblock == nil {
			continue
		}
		m := asMap(mblock["metrics"])
		acc, ok := modelAcc[name]
		if !ok {
			acc = &ModelAgg{Model: name}
			modelAcc[name] = acc
		}
		acc.Requests += int(num(m["api_requests"]))
		acc.InputTokens += int(num(m["prompt_tokens"]))
		acc.OutputTokens += int(num(m["completion_tokens"]))
		acc.TotalTokens += int(num(m["total_tokens"]))
		acc.CacheReadTokens += int(num(m["cache_read_input_tokens"]))
		acc.Spend += num(m["spend"])
	}
}

// accumulateDayKeys folds one day's breakdown.api_keys into the per-key
// accumulator, summed ACROSS days (stats.py _accumulate_day_keys). Keeps
// the first non-null key_alias seen for a given key hash.
func accumulateDayKeys(keyAcc map[string]*KeyAgg, breakdown map[string]any) {
	apiKeys := asMap(breakdown["api_keys"])
	for hash, raw := range apiKeys {
		kblock := asMap(raw)
		if kblock == nil {
			continue
		}
		k := asMap(kblock["metrics"])
		kmeta := asMap(kblock["metadata"])
		acc, ok := keyAcc[hash]
		if !ok {
			acc = &KeyAgg{ID: hash}
			keyAcc[hash] = acc
		}
		if acc.KeyAlias == nil {
			if alias, isStr := kmeta["key_alias"].(string); isStr {
				acc.KeyAlias = &alias
			}
		}
		acc.Requests += int(num(k["api_requests"]))
		acc.Spend += num(k["spend"])
	}
}

// AggregateWindow folds ONE daily-activity window into window totals, a
// chronological daily series, and per-model/per-key totals summed across
// days (stats.py aggregate_window).
//
// Window totals come from data.Metadata, whose total_* are already summed
// across pages by the caller (LiteLLM's per-response metadata is only a
// per-page partial, never a full-range aggregate).
//
// WR-04 — headline totals vs summed breakdown rows MAY DIVERGE by design.
// requests/tokens/spend come from LiteLLM's pre-aggregated metadata.total_*,
// which counts EVERY request (including failed and model-less ones). The
// per-model breakdown only carries requests LiteLLM could attribute to a
// model, so sum(Models[].Requests) can be LESS than Requests for a window
// containing failed/model-less requests. The per-key breakdown DOES capture
// the failed requests, so sum(Keys[].Requests) tracks the total more
// closely. Totals is the source of truth; the per-model table is an
// attribution view, not a reconciliation of the headline.
//
// Models/Keys are returned sorted by name/id (not the JSON insertion order
// Python's dict preserves) for run-to-run determinism — see the Go/Python
// deviation note in the batch report; no test depends on either order since
// BuildStatsContract re-sorts Keys by spend and nothing asserts Models order.
func AggregateWindow(d DailyActivity) WindowAggregate {
	requests := int(num(d.Metadata["total_api_requests"]))
	tokens := int(num(d.Metadata["total_tokens"]))
	spend := num(d.Metadata["total_spend"])
	failed := int(num(d.Metadata["total_failed_requests"]))
	promptTokens := int(num(d.Metadata["total_prompt_tokens"]))
	completionTokens := int(num(d.Metadata["total_completion_tokens"]))
	cacheReadTokens := int(num(d.Metadata["total_cache_read_input_tokens"]))

	dayAcc := map[string]*DaySeries{}
	modelAcc := map[string]*ModelAgg{}
	keyAcc := map[string]*KeyAgg{}

	for i, raw := range d.Results {
		var day map[string]any
		if err := json.Unmarshal(raw, &day); err != nil || day == nil {
			continue
		}
		accumulateDaySeries(dayAcc, i, day)
		breakdown := asMap(day["breakdown"])
		accumulateDayModels(modelAcc, breakdown)
		accumulateDayKeys(keyAcc, breakdown)
	}

	series := make([]DaySeries, 0, len(dayAcc))
	for _, v := range dayAcc {
		series = append(series, *v)
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Date < series[j].Date })

	models := make([]ModelAgg, 0, len(modelAcc))
	for _, v := range modelAcc {
		models = append(models, *v)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })

	keys := make([]KeyAgg, 0, len(keyAcc))
	for _, v := range keyAcc {
		keys = append(keys, *v)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })

	return WindowAggregate{
		Requests:         requests,
		Tokens:           tokens,
		Spend:            spend,
		FailedRequests:   failed,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CacheReadTokens:  cacheReadTokens,
		Series:           series,
		Models:           models,
		Keys:             keys,
	}
}

// LastUsedFromWindow derives per-model last-used DATE from one
// daily-activity window at no extra HTTP cost (stats.py
// last_used_from_window): the latest in-window day on which a model
// appears IS its last-used date (day granularity). An empty/unparseable
// window yields an empty map, which flips
// Capabilities.PerModelLastUsed to false in BuildStatsContract.
func LastUsedFromWindow(d DailyActivity) map[string]string {
	lastUsed := map[string]string{}
	for _, raw := range d.Results {
		var day map[string]any
		if err := json.Unmarshal(raw, &day); err != nil || day == nil {
			continue
		}
		dayDate, ok := day["date"].(string)
		if !ok || dayDate == "" {
			continue
		}
		models := asMap(asMap(day["breakdown"])["models"])
		for modelName := range models {
			if existing, has := lastUsed[modelName]; !has || dayDate > existing {
				lastUsed[modelName] = dayDate
			}
		}
	}
	return lastUsed
}

// avgCostPer1M is spend / tokens * 1_000_000, guarded for 0 tokens → nil
// (D-08; stats.py _avg_cost_per_1m_tokens).
func avgCostPer1M(spend, tokens float64) *float64 {
	if tokens == 0 {
		return nil
	}
	v := spend / tokens * 1_000_000
	return &v
}

// ComputeDeltas is the period-over-period percentage change per metric,
// with a prior=0 → nil guard (stats.py compute_deltas). pct = (cur-prev)/
// prev; when prev is 0 the change is undefined → nil, never a crash or
// +Inf. The avg-cost delta compares the derived avgCostPer1M of each
// window (also guarded).
func ComputeDeltas(cur, prev WindowAggregate) Deltas {
	curReq, prevReq := float64(cur.Requests), float64(prev.Requests)
	curTok, prevTok := float64(cur.Tokens), float64(prev.Tokens)
	curSpend, prevSpend := cur.Spend, prev.Spend

	curAvg := avgCostPer1M(curSpend, curTok)
	prevAvg := avgCostPer1M(prevSpend, prevTok)
	var avgPct *float64
	if curAvg != nil && prevAvg != nil {
		avgPct = safePct(*curAvg-*prevAvg, *prevAvg)
	}

	return Deltas{
		RequestsPct:           safePct(curReq-prevReq, prevReq),
		TokensPct:             safePct(curTok-prevTok, prevTok),
		SpendPct:              safePct(curSpend-prevSpend, prevSpend),
		AvgCostPer1MTokensPct: avgPct,
	}
}

// BuildStatsContract assembles the full page-ready stats contract from two
// aggregated windows (stats.py build_stats_contract). prev nil is treated
// as a zero-value window (same result as Python's compare_agg={}).
//
// caps.PerModelLastUsed is flipped to false when lastUsed is empty — never
// forced back to true, so a caller that deliberately disabled the flag is
// respected (WR-06).
func BuildStatsContract(cur WindowAggregate, prev *WindowAggregate, budget Budget, lastUsed map[string]string, caps Capabilities, rng Range) StatsContract {
	if lastUsed == nil {
		lastUsed = map[string]string{}
	}
	var prevAgg WindowAggregate
	if prev != nil {
		prevAgg = *prev
	}

	totalSpend := cur.Spend
	totalRequests := cur.Requests
	totalTokens := cur.Tokens

	modelsTotalSpend := 0.0
	for _, m := range cur.Models {
		modelsTotalSpend += m.Spend
	}
	models := make([]ModelOut, 0, len(cur.Models))
	for _, m := range cur.Models {
		var lu *string
		if v, ok := lastUsed[m.Model]; ok {
			vv := v
			lu = &vv
		}
		models = append(models, ModelOut{
			Model:           m.Model,
			Requests:        m.Requests,
			InputTokens:     m.InputTokens,
			OutputTokens:    m.OutputTokens,
			TotalTokens:     m.TotalTokens,
			CacheReadTokens: m.CacheReadTokens,
			Spend:           m.Spend,
			SpendPct:        safePct(m.Spend, modelsTotalSpend),
			LastUsed:        lu,
		})
	}

	if len(lastUsed) == 0 {
		caps.PerModelLastUsed = false
	}

	keysTotalSpend := 0.0
	for _, k := range cur.Keys {
		keysTotalSpend += k.Spend
	}
	keys := make([]KeyOut, 0, len(cur.Keys))
	for _, k := range cur.Keys {
		keys = append(keys, KeyOut{
			ID:       k.ID,
			KeyAlias: k.KeyAlias,
			Requests: k.Requests,
			Spend:    k.Spend,
			SpendPct: safePct(k.Spend, keysTotalSpend),
		})
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Spend > keys[j].Spend })

	var maxB float64
	if budget.MaxBudget != nil {
		maxB = *budget.MaxBudget
	}
	budgetOut := BudgetSummary{
		Current:        budget.Current,
		MaxBudget:      budget.MaxBudget,
		BudgetDuration: budget.BudgetDuration,
		Source:         budget.Source,
		Pct:            safePct(budget.Current, maxB),
		HasBudget:      budget.MaxBudget != nil,
	}

	totals := TotalsOut{
		Requests:           totalRequests,
		Tokens:             totalTokens,
		Spend:              totalSpend,
		FailedRequests:     cur.FailedRequests,
		InputTokens:        cur.PromptTokens,
		OutputTokens:       cur.CompletionTokens,
		CacheReadTokens:    cur.CacheReadTokens,
		CacheHitPct:        safePct(float64(cur.CacheReadTokens), float64(cur.PromptTokens)),
		AvgCostPer1MTokens: avgCostPer1M(totalSpend, float64(totalTokens)),
		Deltas:             ComputeDeltas(cur, prevAgg),
	}

	return StatsContract{
		Range:        rng,
		Totals:       totals,
		Series:       zeroFillSeries(cur.Series, rng.Start, rng.End),
		Models:       models,
		Keys:         keys,
		Budget:       budgetOut,
		Capabilities: caps,
	}
}

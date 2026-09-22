// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Ports app/tests/test_stats.py (alitellm-auth) case by case against the
// SAME verbatim v1.85.1 fixtures captured there. Multi-day cases build a
// synthetic two-day input by reusing the real single-day result block, same
// as the Python original.

func loadDaily(t *testing.T, name string) DailyActivity {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var d DailyActivity
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return d
}

// loadDailyMeta re-decodes a fixture's raw metadata block, for tests that
// assert against the fixture's own numbers rather than a hardcoded literal.
func loadDailyMeta(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var raw struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return raw.Metadata
}

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ---------------------------------------------------------------------------
// AggregateWindow
// ---------------------------------------------------------------------------

func TestAggregateWindow_TotalsFromMetadata(t *testing.T) {
	agg := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	if agg.Requests != 11 {
		t.Errorf("Requests = %d, want 11", agg.Requests)
	}
	if agg.Tokens != 10018 {
		t.Errorf("Tokens = %d, want 10018", agg.Tokens)
	}
	if !approxEqual(agg.Spend, 0.023427) {
		t.Errorf("Spend = %v, want 0.023427", agg.Spend)
	}
}

func TestAggregateWindow_SeriesOneEntryPerDay(t *testing.T) {
	d := loadDaily(t, "daily_activity_current.json")
	agg := AggregateWindow(d)
	if len(agg.Series) != len(d.Results) {
		t.Fatalf("len(Series) = %d, want %d", len(agg.Series), len(d.Results))
	}
	entry := agg.Series[0]
	if entry.Date != "2026-04-01" {
		t.Errorf("Date = %q, want 2026-04-01", entry.Date)
	}
	if !approxEqual(entry.Spend, 0.023427) {
		t.Errorf("Spend = %v, want 0.023427", entry.Spend)
	}
	if entry.Requests != 11 {
		t.Errorf("Requests = %d, want 11", entry.Requests)
	}
}

func TestAggregateWindow_SeriesIncludesTokens(t *testing.T) {
	d := loadDaily(t, "daily_activity_current.json")
	agg := AggregateWindow(d)
	var day0 struct {
		Metrics struct {
			TotalTokens float64 `json:"total_tokens"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(d.Results[0], &day0); err != nil {
		t.Fatal(err)
	}
	if agg.Series[0].Tokens != int(day0.Metrics.TotalTokens) {
		t.Errorf("Tokens = %d, want %d", agg.Series[0].Tokens, int(day0.Metrics.TotalTokens))
	}
}

func TestAggregateWindow_FailedRequestsTotalAndPerDay(t *testing.T) {
	d := loadDaily(t, "daily_activity_current.json")
	agg := AggregateWindow(d)
	meta := loadDailyMeta(t, "daily_activity_current.json")
	if agg.FailedRequests != int(num(meta["total_failed_requests"])) {
		t.Errorf("FailedRequests = %d, want %d", agg.FailedRequests, int(num(meta["total_failed_requests"])))
	}
	var day0 struct {
		Metrics struct {
			FailedRequests float64 `json:"failed_requests"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(d.Results[0], &day0); err != nil {
		t.Fatal(err)
	}
	if agg.Series[0].Failed != int(day0.Metrics.FailedRequests) {
		t.Errorf("Failed = %d, want %d", agg.Series[0].Failed, int(day0.Metrics.FailedRequests))
	}
}

func TestBuildStatsContract_TotalsIncludeFailedRequests(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, testCapabilities, testRange)
	if contract.Totals.FailedRequests != cur.FailedRequests {
		t.Errorf("FailedRequests = %d, want %d", contract.Totals.FailedRequests, cur.FailedRequests)
	}
}

func TestAggregateWindow_ModelsSummedAcrossDays(t *testing.T) {
	d := loadDaily(t, "daily_activity_current.json")
	twoDay := DailyActivity{
		Results:  []json.RawMessage{d.Results[0], cloneDay(t, d.Results[0], "2026-04-02")},
		Metadata: d.Metadata,
	}

	agg := AggregateWindow(twoDay)
	models := modelsByName(agg.Models)

	lite := models["gemini/gemini-flash-lite-latest"]
	if lite.Requests != 2 {
		t.Errorf("Requests = %d, want 2", lite.Requests)
	}
	if lite.TotalTokens != 136 {
		t.Errorf("TotalTokens = %d, want 136", lite.TotalTokens)
	}
	if lite.InputTokens != 128 {
		t.Errorf("InputTokens = %d, want 128", lite.InputTokens)
	}
	if lite.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8", lite.OutputTokens)
	}
	if !approxEqual(lite.Spend, 8e-06*2) {
		t.Errorf("Spend = %v, want %v", lite.Spend, 8e-06*2)
	}
	if len(agg.Series) != 2 {
		t.Errorf("len(Series) = %d, want 2", len(agg.Series))
	}
}

func TestAggregateWindow_SeriesMergesSameDateAcrossPages(t *testing.T) {
	d := loadDaily(t, "daily_activity_current.json")
	sameDay := DailyActivity{
		Results:  []json.RawMessage{d.Results[0], d.Results[0]},
		Metadata: d.Metadata,
	}

	agg := AggregateWindow(sameDay)
	if len(agg.Series) != 1 {
		t.Fatalf("len(Series) = %d, want 1", len(agg.Series))
	}
	entry := agg.Series[0]
	if entry.Date != "2026-04-01" {
		t.Errorf("Date = %q, want 2026-04-01", entry.Date)
	}
	if !approxEqual(entry.Spend, 0.023427*2) {
		t.Errorf("Spend = %v, want %v", entry.Spend, 0.023427*2)
	}
	if entry.Requests != 22 {
		t.Errorf("Requests = %d, want 22", entry.Requests)
	}
}

func TestAggregateWindow_KeysSummedAndPresent(t *testing.T) {
	agg := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	keys := map[string]KeyAgg{}
	for _, k := range agg.Keys {
		keys[k.ID] = k
	}
	h := "195b8b1f2c4e46945209387ec13e08ea7d74714fd088cd118b928630a03f2317"
	k, ok := keys[h]
	if !ok {
		t.Fatalf("key %s not present", h)
	}
	if k.Requests != 11 {
		t.Errorf("Requests = %d, want 11", k.Requests)
	}
	if !approxEqual(k.Spend, 0.023427) {
		t.Errorf("Spend = %v, want 0.023427", k.Spend)
	}
}

func TestAggregateWindow_RealZeroTokensKept(t *testing.T) {
	agg := AggregateWindow(loadDaily(t, "daily_activity_prior.json"))
	veo := modelsByName(agg.Models)["veo-3.1-generate-preview"]
	if veo.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", veo.InputTokens)
	}
	if veo.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0", veo.OutputTokens)
	}
	if !approxEqual(veo.Spend, 3.2) {
		t.Errorf("Spend = %v, want 3.2", veo.Spend)
	}
}

func TestAggregateWindow_FailedRequestsDivergeFromModelBreakdown(t *testing.T) {
	agg := AggregateWindow(loadDaily(t, "daily_activity_prior.json"))
	if agg.Requests != 4 {
		t.Errorf("Requests = %d, want 4", agg.Requests)
	}
	modelReqs := 0
	for _, m := range agg.Models {
		modelReqs += m.Requests
	}
	if modelReqs != 1 {
		t.Errorf("sum(Models[].Requests) = %d, want 1", modelReqs)
	}
	keyReqs := 0
	for _, k := range agg.Keys {
		keyReqs += k.Requests
	}
	if keyReqs != 4 {
		t.Errorf("sum(Keys[].Requests) = %d, want 4", keyReqs)
	}
}

func TestAggregateWindow_EmptyIsRealZeroNotNull(t *testing.T) {
	agg := AggregateWindow(loadDaily(t, "daily_activity_empty.json"))
	if agg.Requests != 0 || agg.Tokens != 0 || agg.Spend != 0 {
		t.Errorf("got %+v, want all zero", agg)
	}
	if len(agg.Series) != 0 || len(agg.Models) != 0 || len(agg.Keys) != 0 {
		t.Errorf("got non-empty slices: %+v", agg)
	}
}

// ---------------------------------------------------------------------------
// ComputeDeltas
// ---------------------------------------------------------------------------

func TestComputeDeltas_NonzeroPrior(t *testing.T) {
	cur := WindowAggregate{Requests: 110, Tokens: 2000, Spend: 12.0}
	prev := WindowAggregate{Requests: 100, Tokens: 1000, Spend: 10.0}
	deltas := ComputeDeltas(cur, prev)

	if deltas.RequestsPct == nil || !approxEqual(*deltas.RequestsPct, 0.1) {
		t.Errorf("RequestsPct = %v, want 0.1", derefF(deltas.RequestsPct))
	}
	if deltas.TokensPct == nil || !approxEqual(*deltas.TokensPct, 1.0) {
		t.Errorf("TokensPct = %v, want 1.0", derefF(deltas.TokensPct))
	}
	if deltas.SpendPct == nil || !approxEqual(*deltas.SpendPct, 0.2) {
		t.Errorf("SpendPct = %v, want 0.2", derefF(deltas.SpendPct))
	}
}

func TestComputeDeltas_ZeroPriorIsNull(t *testing.T) {
	cur := WindowAggregate{Requests: 10, Tokens: 5, Spend: 1.0}
	prev := WindowAggregate{}
	deltas := ComputeDeltas(cur, prev)

	if deltas.RequestsPct != nil {
		t.Errorf("RequestsPct = %v, want nil", *deltas.RequestsPct)
	}
	if deltas.TokensPct != nil {
		t.Errorf("TokensPct = %v, want nil", *deltas.TokensPct)
	}
	if deltas.SpendPct != nil {
		t.Errorf("SpendPct = %v, want nil", *deltas.SpendPct)
	}
	if deltas.AvgCostPer1MTokensPct != nil {
		t.Errorf("AvgCostPer1MTokensPct = %v, want nil", *deltas.AvgCostPer1MTokensPct)
	}
}

// ---------------------------------------------------------------------------
// BuildStatsContract
// ---------------------------------------------------------------------------

var testCapabilities = Capabilities{TokenSplit: true, PerModelLastUsed: true, Deltas: true, PerKeySpend: true}

var testRange = mustRange("2026-04-01", "2026-04-30", 30, "2026-03-02", "2026-03-31")

func mustRange(start, end string, days int, cmpStart, cmpEnd string) Range {
	r := Range{Start: start, End: end, Days: days}
	r.Compare.Start = cmpStart
	r.Compare.End = cmpEnd
	return r
}

func TestBuildStatsContract_ShapeAndCapabilities(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	prev := AggregateWindow(loadDaily(t, "daily_activity_prior.json"))
	budget := Budget{Current: 0.0, MaxBudget: f64p(500.0), Source: "user"}

	contract := BuildStatsContract(cur, &prev, budget, nil, testCapabilities, testRange)

	if !contract.Capabilities.PerKeySpend {
		t.Error("PerKeySpend = false, want true")
	}
	if contract.Range.Days != 30 {
		t.Errorf("Range.Days = %d, want 30", contract.Range.Days)
	}
	if contract.Totals.AvgCostPer1MTokens == nil {
		t.Error("AvgCostPer1MTokens = nil, want non-nil")
	}
}

func TestBuildStatsContract_ZeroFillsMissingDays(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json")) // day: 2026-04-01
	rng := mustRange("2026-03-31", "2026-04-02", 3, "2026-03-28", "2026-03-30")
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, testCapabilities, rng)

	series := contract.Series
	if len(series) != 3 {
		t.Fatalf("len(Series) = %d, want 3", len(series))
	}
	wantDates := []string{"2026-03-31", "2026-04-01", "2026-04-02"}
	for i, want := range wantDates {
		if series[i].Date != want {
			t.Errorf("Series[%d].Date = %q, want %q", i, series[i].Date, want)
		}
	}
	if series[0].Spend != 0.0 || series[0].Requests != 0 || series[0].Tokens != 0 {
		t.Errorf("filled day not real-zero: %+v", series[0])
	}
	if series[1].Requests <= 0 {
		t.Errorf("real day Requests = %d, want > 0", series[1].Requests)
	}
}

func TestBuildStatsContract_CacheHitPct(t *testing.T) {
	meta := loadDailyMeta(t, "daily_activity_current.json")
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, testCapabilities, testRange)

	expected := num(meta["total_cache_read_input_tokens"]) / num(meta["total_prompt_tokens"])
	if contract.Totals.CacheReadTokens != int(num(meta["total_cache_read_input_tokens"])) {
		t.Errorf("CacheReadTokens = %d, want %d", contract.Totals.CacheReadTokens, int(num(meta["total_cache_read_input_tokens"])))
	}
	if contract.Totals.CacheHitPct == nil || !approxEqual(*contract.Totals.CacheHitPct, expected) {
		t.Errorf("CacheHitPct = %v, want %v", derefF(contract.Totals.CacheHitPct), expected)
	}
	if contract.Totals.InputTokens != int(num(meta["total_prompt_tokens"])) {
		t.Errorf("InputTokens = %d, want %d", contract.Totals.InputTokens, int(num(meta["total_prompt_tokens"])))
	}
	if contract.Totals.OutputTokens != int(num(meta["total_completion_tokens"])) {
		t.Errorf("OutputTokens = %d, want %d", contract.Totals.OutputTokens, int(num(meta["total_completion_tokens"])))
	}
}

func TestBuildStatsContract_CacheHitPctNoneWhenNoPromptTokens(t *testing.T) {
	contract := BuildStatsContract(WindowAggregate{}, &WindowAggregate{}, Budget{}, nil, testCapabilities, testRange)
	if contract.Totals.CacheHitPct != nil {
		t.Errorf("CacheHitPct = %v, want nil", *contract.Totals.CacheHitPct)
	}
	if contract.Totals.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", contract.Totals.CacheReadTokens)
	}
}

func TestBuildStatsContract_KeysRankedBySpendDesc(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_prior.json"))
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, testCapabilities, testRange)

	spends := make([]float64, len(contract.Keys))
	for i, k := range contract.Keys {
		spends[i] = k.Spend
	}
	if !sort.SliceIsSorted(spends, func(i, j int) bool { return spends[i] > spends[j] }) {
		t.Errorf("Keys not sorted by spend desc: %v", spends)
	}
}

func TestBuildStatsContract_ModelsSpendPctGuarded(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, testCapabilities, testRange)

	total := 0.0
	for _, m := range contract.Models {
		total += m.Spend
	}
	for _, m := range contract.Models {
		if total != 0 {
			if m.SpendPct == nil || math.Abs(*m.SpendPct-m.Spend/total) >= 1e-9 {
				t.Errorf("model %s: SpendPct = %v, want %v", m.Model, derefF(m.SpendPct), m.Spend/total)
			}
		} else if m.SpendPct != nil {
			t.Errorf("model %s: SpendPct = %v, want nil", m.Model, *m.SpendPct)
		}
	}
}

func TestBuildStatsContract_LastUsedNullFlipsCapability(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	caps := testCapabilities
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, caps, testRange)

	if contract.Capabilities.PerModelLastUsed {
		t.Error("PerModelLastUsed = true, want false")
	}
	for _, m := range contract.Models {
		if m.LastUsed != nil {
			t.Errorf("model %s: LastUsed = %v, want nil", m.Model, *m.LastUsed)
		}
	}
}

func TestBuildStatsContract_LastUsedPresentKeepsCapability(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_current.json"))
	lastUsed := map[string]string{"gemini/gemini-flash-latest": "2026-04-01T09:49:44.420000Z"}
	contract := BuildStatsContract(cur, &cur, Budget{}, lastUsed, testCapabilities, testRange)

	if !contract.Capabilities.PerModelLastUsed {
		t.Error("PerModelLastUsed = false, want true")
	}
	byModel := map[string]ModelOut{}
	for _, m := range contract.Models {
		byModel[m.Model] = m
	}
	got := byModel["gemini/gemini-flash-latest"]
	if got.LastUsed == nil || *got.LastUsed != "2026-04-01T09:49:44.420000Z" {
		t.Errorf("LastUsed = %v, want 2026-04-01T09:49:44.420000Z", derefS(got.LastUsed))
	}
	if byModel["gemini/gemini-3-pro-preview"].LastUsed != nil {
		t.Errorf("LastUsed = %v, want nil", *byModel["gemini/gemini-3-pro-preview"].LastUsed)
	}
}

func TestLastUsedFromWindow_PicksLatestDayPerModel(t *testing.T) {
	day1raw := loadDaily(t, "daily_activity_current.json").Results[0]
	day1 := cloneDay(t, day1raw, "2026-04-01")
	day2 := popModelAndRedate(t, day1raw, "gemini/gemini-3-pro-preview", "2026-04-03")

	out := LastUsedFromWindow(DailyActivity{Results: []json.RawMessage{day2, day1}}) // unordered input

	if out["gemini/gemini-flash-latest"] != "2026-04-03" {
		t.Errorf("gemini-flash-latest = %q, want 2026-04-03", out["gemini/gemini-flash-latest"])
	}
	if out["gemini/gemini-3-pro-preview"] != "2026-04-01" {
		t.Errorf("gemini-3-pro-preview = %q, want 2026-04-01", out["gemini/gemini-3-pro-preview"])
	}
}

func TestLastUsedFromWindow_EmptyWindowReturnsEmpty(t *testing.T) {
	if out := LastUsedFromWindow(loadDaily(t, "daily_activity_empty.json")); len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
	if out := LastUsedFromWindow(DailyActivity{}); len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
	dateOnly, err := json.Marshal(map[string]any{"date": "2026-04-01"})
	if err != nil {
		t.Fatal(err)
	}
	if out := LastUsedFromWindow(DailyActivity{Results: []json.RawMessage{dateOnly}}); len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
}

func TestBuildStatsContract_BudgetWithMax(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_empty.json"))
	budget := Budget{Current: 4.2, MaxBudget: f64p(10.0), BudgetDuration: sp("30d"), Source: "user"}
	contract := BuildStatsContract(cur, &cur, budget, nil, testCapabilities, testRange)

	b := contract.Budget
	if !b.HasBudget {
		t.Error("HasBudget = false, want true")
	}
	if b.Pct == nil || !approxEqual(*b.Pct, 0.42) {
		t.Errorf("Pct = %v, want 0.42", derefF(b.Pct))
	}
	if b.MaxBudget == nil || *b.MaxBudget != 10.0 {
		t.Errorf("MaxBudget = %v, want 10.0", derefF(b.MaxBudget))
	}
	if b.BudgetDuration == nil || *b.BudgetDuration != "30d" {
		t.Errorf("BudgetDuration = %v, want 30d", derefS(b.BudgetDuration))
	}
}

func TestBuildStatsContract_NullBudgetPctNone(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_empty.json"))
	budget := Budget{Current: 0.0, Source: "unknown"}
	contract := BuildStatsContract(cur, &cur, budget, nil, testCapabilities, testRange)

	if contract.Budget.HasBudget {
		t.Error("HasBudget = true, want false")
	}
	if contract.Budget.Pct != nil {
		t.Errorf("Pct = %v, want nil", *contract.Budget.Pct)
	}
}

func TestBuildStatsContract_EmptyTotalsAreZeroNotNull(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_empty.json"))
	contract := BuildStatsContract(cur, &cur, Budget{}, nil, testCapabilities, testRange)

	tot := contract.Totals
	if tot.Requests != 0 || tot.Tokens != 0 || tot.Spend != 0 {
		t.Errorf("got %+v, want all zero", tot)
	}
	if tot.AvgCostPer1MTokens != nil {
		t.Errorf("AvgCostPer1MTokens = %v, want nil", *tot.AvgCostPer1MTokens)
	}
}

// TestAccumulateDayModels_CacheReadTokensAggregated ports
// test_model_cache_read_tokens_aggregated: exercises the unexported fold
// directly, same as the Python test reaching into app.stats._accumulate_day_models.
func TestAccumulateDayModels_CacheReadTokensAggregated(t *testing.T) {
	acc := map[string]*ModelAgg{}
	breakdown := map[string]any{
		"models": map[string]any{
			"m": map[string]any{
				"metrics": map[string]any{
					"cache_read_input_tokens": 42.0,
					"prompt_tokens":           100.0,
				},
			},
		},
	}
	accumulateDayModels(acc, breakdown)
	if acc["m"].CacheReadTokens != 42 {
		t.Errorf("CacheReadTokens = %d, want 42", acc["m"].CacheReadTokens)
	}
}

// ---------------------------------------------------------------------------
// JSON shape
// ---------------------------------------------------------------------------

func TestStatsContract_JSONNullsNotOmitted(t *testing.T) {
	c := BuildStatsContract(WindowAggregate{}, nil, Budget{Source: "unknown"}, nil,
		Capabilities{true, true, true, true},
		Range{Start: "2026-01-01", End: "2026-01-01", Days: 1})
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"cache_hit_pct":null`, `"avg_cost_per_1m_tokens":null`, `"requests_pct":null`,
		`"max_budget":null`, `"pct":null`, `"has_budget":false`,
	} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("missing %s in %s", key, b)
		}
	}
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func modelsByName(models []ModelAgg) map[string]ModelAgg {
	out := make(map[string]ModelAgg, len(models))
	for _, m := range models {
		out[m.Model] = m
	}
	return out
}

func derefF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefS(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// cloneDay decodes a results[] row, overwrites its date, and re-encodes it —
// the Go equivalent of the Python tests' copy.deepcopy(day); day["date"] = ...
func cloneDay(t *testing.T, raw json.RawMessage, newDate string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["date"] = newDate
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// popModelAndRedate clones a results[] row, drops one breakdown.models
// entry, and sets a new date — used to build the two-day
// TestLastUsedFromWindow_PicksLatestDayPerModel fixture.
func popModelAndRedate(t *testing.T, raw json.RawMessage, dropModel, newDate string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["date"] = newDate
	if breakdown, ok := m["breakdown"].(map[string]any); ok {
		if models, ok := breakdown["models"].(map[string]any); ok {
			delete(models, dropModel)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// SPDX-License-Identifier: Apache-2.0

package console

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/observability"
)

// fakeDB is the console package's keyLister fake — a fixed set of
// owner-scoped key rows, ignoring the filter/limit/cursor args (no test
// here needs pagination).
type fakeDB struct {
	items []db.KeyListItem
	err   error
}

func (f fakeDB) ListKeys(context.Context, db.KeyListFilter, int, string) ([]db.KeyListItem, string, error) {
	return f.items, "", f.err
}

func loadDailyFixture(t *testing.T, name string) observability.DailyActivity {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "observability", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var d observability.DailyActivity
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStats_RangeParsing(t *testing.T) {
	d := testDeps(t)
	ctx := pkCtx(t, "u@x.com", false)

	if rec := do(t, d, "/platform/console/stats", ctx); rec.Code != 200 {
		t.Fatalf("default range: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Range struct {
			Days int `json:"days"`
		} `json:"range"`
	}
	rec := do(t, d, "/platform/console/stats", ctx)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Range.Days != 30 {
		t.Fatalf("default days = %d, want 30", got.Range.Days)
	}

	if rec := do(t, d, "/platform/console/stats?start_date=not-a-date", ctx); rec.Code != 400 {
		t.Fatalf("bad date: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, d, "/platform/console/stats?start_date=2026-09-20&end_date=2026-09-10", ctx); rec.Code != 400 {
		t.Fatalf("start>end: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, d, "/platform/console/stats?start_date=2025-09-21&end_date=2026-09-22", ctx); rec.Code != 400 {
		t.Fatalf("367 days: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, d, "/platform/console/stats?start_date=2025-09-22&end_date=2026-09-22", ctx); rec.Code != 200 {
		t.Fatalf("366 days: %d %s", rec.Code, rec.Body)
	}
}

func TestStats_ComposesAndScopesToUser(t *testing.T) {
	d := testDeps(t)
	d.LiteLLM.(*fakeLL).tag = &litellm.TagInfoEntry{
		Spend: 5, Budget: &litellm.TagBudget{MaxBudget: 20, BudgetDuration: "30d"},
	}
	cat := &fakeCatalog{
		daily: loadDailyFixture(t, "daily_activity_current.json"),
		prior: loadDailyFixture(t, "daily_activity_prior.json"),
	}
	var usedKey string
	d.AsUser = func(key string) UserReads { usedKey = key; return cat }

	rec := do(t, d, "/platform/console/stats", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if usedKey != "sk-user" {
		t.Fatalf("AsUser key = %q, want sk-user", usedKey)
	}
	var got struct {
		DataScope string `json:"data_scope"`
		Totals    struct {
			Requests int `json:"requests"`
			Deltas   struct {
				RequestsPct *float64 `json:"requests_pct"`
			} `json:"deltas"`
		} `json:"totals"`
		Budget struct {
			Source    string   `json:"source"`
			Current   float64  `json:"current"`
			MaxBudget *float64 `json:"max_budget"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DataScope != dataScopeUser {
		t.Fatalf("data_scope = %q, want user", got.DataScope)
	}
	if got.Totals.Requests != 11 {
		t.Fatalf("totals.requests = %d, want 11 (from daily_activity_current.json)", got.Totals.Requests)
	}
	if got.Totals.Deltas.RequestsPct == nil {
		t.Fatal("deltas.requests_pct is nil, want a computed delta against the prior fixture")
	}
	// The ceiling reported is the one the forwarder enforces: the caller's
	// own user:<email> tag, not a team membership.
	if got.Budget.Source != "tag" || got.Budget.Current != 5 ||
		got.Budget.MaxBudget == nil || *got.Budget.MaxBudget != 20 {
		t.Fatalf("budget = %+v, want {tag 5 20}", got.Budget)
	}
}

func TestStats_PriorFailureDegradesDeltas(t *testing.T) {
	d := testDeps(t)
	cat := &fakeCatalog{
		daily:    loadDailyFixture(t, "daily_activity_current.json"),
		priorErr: errors.New("litellm: transport error"),
	}
	d.AsUser = func(string) UserReads { return cat }

	rec := do(t, d, "/platform/console/stats", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Capabilities struct {
			Deltas bool `json:"deltas"`
		} `json:"capabilities"`
		Totals struct {
			Deltas struct {
				RequestsPct *float64 `json:"requests_pct"`
			} `json:"deltas"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Capabilities.Deltas {
		t.Fatal("capabilities.deltas = true, want false (prior window fetch failed)")
	}
	if got.Totals.Deltas.RequestsPct != nil {
		t.Fatalf("requests_pct = %v, want null", *got.Totals.Deltas.RequestsPct)
	}
}

// TestStats_BudgetFailureIsUnknownNot502 — a failed /tag/info leaves the
// rest of the payload intact with budget.source "unknown".
func TestStats_BudgetFailureIsUnknownNot502(t *testing.T) {
	d := testDeps(t)
	d.LiteLLM.(*fakeLL).tagErr = errors.New("litellm: transport error")
	cat := &fakeCatalog{
		daily: loadDailyFixture(t, "daily_activity_current.json"),
		prior: loadDailyFixture(t, "daily_activity_prior.json"),
	}
	d.AsUser = func(string) UserReads { return cat }

	rec := do(t, d, "/platform/console/stats", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Totals struct {
			Requests int `json:"requests"`
		} `json:"totals"`
		Budget struct {
			Source string `json:"source"`
		} `json:"budget"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Budget.Source != "unknown" {
		t.Fatalf("budget.source = %q, want unknown", got.Budget.Source)
	}
	if got.Totals.Requests != 11 {
		t.Fatalf("totals.requests = %d — the rest of the payload must survive", got.Totals.Requests)
	}
}

func TestStats_CurrentFailureIs502Or503_NeverMaster(t *testing.T) {
	d := testDeps(t)
	cat := &fakeCatalog{dailyErr: &litellm.Auth401Error{}}
	d.AsUser = func(string) UserReads { return cat }
	// The console handler has no master-key fallback path at all — Deps
	// carries no separate master client for stats/latency, only AsUser.
	// A current-window failure is classified straight from the UserReads
	// error, never retried.
	if rec := do(t, d, "/platform/console/stats", pkCtx(t, "u@x.com", false)); rec.Code != 502 || !strings.Contains(rec.Body.String(), "litellm_rejected") {
		t.Fatalf("401: %d %s", rec.Code, rec.Body)
	}

	cat2 := &fakeCatalog{dailyErr: errors.New("dial tcp: connection refused")}
	d.AsUser = func(string) UserReads { return cat2 }
	if rec := do(t, d, "/platform/console/stats", pkCtx(t, "u@x.com", false)); rec.Code != 503 {
		t.Fatalf("transport: %d %s", rec.Code, rec.Body)
	}
}

func TestStats_KeyRowsCarryACHNames(t *testing.T) {
	d := testDeps(t)
	d.DB = fakeDB{items: []db.KeyListItem{
		{KeyID: "ekid_1", Type: "ek", OwnerEmail: "u@x.com", Name: strPtr("agent-a")},
		{KeyID: "pkid_1", Type: "pk", OwnerEmail: "u@x.com"},
	}}
	day := `{"date":"2026-09-20",
		"metrics":{"spend":1,"api_requests":3,"total_tokens":10,"prompt_tokens":5,"completion_tokens":5,"failed_requests":0},
		"breakdown":{"models":{},"api_keys":{
			"hash1":{"metrics":{"api_requests":1,"spend":0.5},"metadata":{"key_alias":"ekid_1"}},
			"hash2":{"metrics":{"api_requests":1,"spend":0.3},"metadata":{"key_alias":"pkid_1"}},
			"hash3":{"metrics":{"api_requests":1,"spend":0.2},"metadata":{"key_alias":"ekid_unknown"}}
		}}}`
	cat := &fakeCatalog{daily: observability.DailyActivity{
		Results: []json.RawMessage{json.RawMessage(day)},
		Metadata: map[string]any{
			"total_api_requests": 3.0, "total_spend": 1.0, "total_tokens": 10.0,
			"total_prompt_tokens": 5.0, "total_completion_tokens": 5.0, "total_failed_requests": 0.0,
		},
	}}
	d.AsUser = func(string) UserReads { return cat }

	rec := do(t, d, "/platform/console/stats", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Keys []struct {
			ID       string  `json:"id"`
			KeyAlias *string `json:"key_alias"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]*string{}
	for _, k := range got.Keys {
		byID[k.ID] = k.KeyAlias
	}
	if alias := byID["ekid_1"]; alias == nil || *alias != "agent-a" {
		t.Fatalf("ekid_1 alias = %v, want agent-a", alias)
	}
	if alias := byID["pkid_1"]; alias == nil || *alias != scopePersonal {
		t.Fatalf("pkid_1 alias = %v, want personal", alias)
	}
	// A row ACH did not mint no longer leaks its raw hash into the table:
	// it is folded under the single external label (see nameKeys).
	if _, leaked := byID["hash3"]; leaked {
		t.Fatalf("foreign row still rendered under its raw hash: %v", byID)
	}
	if alias := byID["external"]; alias == nil || *alias != keyExternal {
		t.Fatalf("external row = %v, want %q", alias, keyExternal)
	}
}

func strPtr(s string) *string { return &s }

func TestLatency_ExclusiveEndAndFacts(t *testing.T) {
	d := testDeps(t)
	cat := &fakeCatalog{}
	d.AsUser = func(string) UserReads { return cat }

	rec := do(t, d, "/platform/console/latency?start_date=2026-09-20&end_date=2026-09-22", pkCtx(t, "u@x.com", false))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if cat.lastSpendEnd != "2026-09-23" {
		t.Fatalf("spend-logs end_date = %q, want 2026-09-23 (end+1, exclusive)", cat.lastSpendEnd)
	}
	if cat.lastSpendStart != "2026-09-20" {
		t.Fatalf("spend-logs start_date = %q, want 2026-09-20", cat.lastSpendStart)
	}
	var got struct {
		Available bool   `json:"available"`
		DataScope string `json:"data_scope"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Available || got.DataScope != dataScopeUser {
		t.Fatalf("%+v", got)
	}

	// 401 (role=unknown) -> unavailable, never a master retry.
	cat401 := &fakeCatalog{spendErr: &litellm.Auth401Error{}}
	d.AsUser = func(string) UserReads { return cat401 }
	rec = do(t, d, "/platform/console/latency", pkCtx(t, "u@x.com", false))
	var got401 struct {
		Available bool            `json:"available"`
		Reason    string          `json:"reason"`
		Sampled   bool            `json:"sampled"`
		RowCount  int             `json:"row_count"`
		Window    json.RawMessage `json:"window"`
		Latency   json.RawMessage `json:"latency"`
		Outcomes  json.RawMessage `json:"outcomes"`
		ByModel   json.RawMessage `json:"by_model"`
		DataScope string          `json:"data_scope"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got401)
	if rec.Code != 200 || got401.Available || got401.Reason != "unavailable" || got401.DataScope != dataScopeUser {
		t.Fatalf("401 case: %d %+v", rec.Code, got401)
	}
	if got401.Sampled || got401.RowCount != 0 {
		t.Fatalf("401 case sampled/row_count: %+v", got401)
	}
	if string(got401.Window) != "null" || string(got401.Latency) != "null" {
		t.Fatalf("401 case window/latency: window=%s latency=%s, want null/null", got401.Window, got401.Latency)
	}
	if string(got401.Outcomes) != "[]" || string(got401.ByModel) != "[]" {
		t.Fatalf("401 case outcomes/by_model: outcomes=%s by_model=%s, want []/[]", got401.Outcomes, got401.ByModel)
	}

	// Any other failure -> fetch_failed.
	catErr := &fakeCatalog{spendErr: errors.New("dial tcp: connection refused")}
	d.AsUser = func(string) UserReads { return catErr }
	rec = do(t, d, "/platform/console/latency", pkCtx(t, "u@x.com", false))
	var gotErr struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &gotErr)
	if gotErr.Reason != "fetch_failed" {
		t.Fatalf("transport failure reason = %q, want fetch_failed", gotErr.Reason)
	}

	// Truncated fetch -> sampled:true.
	catTrunc := &fakeCatalog{spendTruncated: true}
	d.AsUser = func(string) UserReads { return catTrunc }
	rec = do(t, d, "/platform/console/latency", pkCtx(t, "u@x.com", false))
	var gotTrunc struct {
		Sampled bool `json:"sampled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &gotTrunc)
	if !gotTrunc.Sampled {
		t.Fatal("sampled = false, want true (truncated fetch)")
	}
}

func TestStats_EkCallerRefused(t *testing.T) {
	if rec := do(t, testDeps(t), "/platform/console/stats", ekCtx()); rec.Code != 401 || !strings.Contains(rec.Body.String(), "invalid_key_type") {
		t.Fatalf("stats ek_: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, testDeps(t), "/platform/console/latency", ekCtx()); rec.Code != 401 || !strings.Contains(rec.Body.String(), "invalid_key_type") {
		t.Fatalf("latency ek_: %d %s", rec.Code, rec.Body)
	}
}

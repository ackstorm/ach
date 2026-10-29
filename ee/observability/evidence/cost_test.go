// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
	core "github.com/ackstorm/ach/internal/observability/evidence"
)

type fakePrices struct {
	p   map[string]ModelPrice
	err error
	n   int
}

func (f *fakePrices) Prices(context.Context) (map[string]ModelPrice, error) {
	f.n++
	return f.p, f.err
}

func f64(v float64) *float64 { return &v }

const costTotalsRow = `{"generations":10,"priced_generations":6,"zero_cost_generations":4,
"spend":3.0,"session_spend":0.6,"sessions":2}`

func readCost(t *testing.T, prices Prices, bodies ...string) (CostReport, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).WithPrices(prices).Cost(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	return rep, calls
}

func near(a *float64, b float64) bool { return a != nil && math.Abs(*a-b) < 1e-9 }

func TestCostSeparatesZeroCostTraffic(t *testing.T) {
	// ports: none — decision 2026-10-27: subscription traffic (cost 0) is reported apart, never in a denominator
	rep, calls := readCost(t, &fakePrices{}, page(costTotalsRow), page())
	if !near(rep.CostPerModelRequest, 0.5) || rep.PricedGenerations != 6 || rep.ZeroCostGenerations != 4 {
		t.Errorf("per request = %v priced=%d zero=%d, want 3.0/6", rep.CostPerModelRequest, rep.PricedGenerations, rep.ZeroCostGenerations)
	}
	if !near(rep.CostPerSession, 0.3) || rep.Sessions != 2 || !near(rep.SessionSpendCoverage, 0.2) {
		t.Errorf("per session = %v sessions=%d coverage=%v, want 0.6/2 over 20%% of spend", rep.CostPerSession, rep.Sessions, rep.SessionSpendCoverage)
	}
	b := (*calls)[0].Body
	for _, frag := range []string{"FINAL", "user_id={subject0:String}", "startsWith(name, 'chat ')", "cost_details['total'] > 0"} {
		if !strings.Contains(b, frag) {
			t.Errorf("totals sql missing %q", frag)
		}
	}
}

func TestCostUndefinedDenominatorsAreNull(t *testing.T) {
	// ports: test_missing_denominator_is_not_observed_zero — unavailable is not 0
	rep, _ := readCost(t, &fakePrices{}, page(`{"generations":3,"priced_generations":0,"zero_cost_generations":3,"spend":0,"session_spend":0,"sessions":0}`), page())
	if rep.CostPerModelRequest != nil || rep.CostPerSession != nil || rep.SessionSpendCoverage != nil {
		t.Errorf("rep = %+v, want null ratios", rep)
	}
	if rep.ZeroCostGenerations != 3 || rep.Provenance.State != "available" {
		t.Errorf("rep = %+v", rep)
	}
}

// The recorded input cost (what was billed, with the cache) vs the same input at
// full price. The list price gives the shape; the recorded / list ratio carries
// Vertex's regional endpoint uplift (live: exactly 1.1x on eu).
func TestCostCacheComparisonFromListPrices(t *testing.T) {
	// ports: none — decision 2026-10-27: cache comparison uses LiteLLM per-model prices
	prices := &fakePrices{p: map[string]ModelPrice{
		"ackstorm.smart": {Input: f64(1e-6), CacheRead: f64(1e-7)},
		"ackstorm.fast":  {Input: f64(1e-6)}, // no cache-read price: cannot be compared
		"free":           {Input: f64(1e-6), CacheRead: f64(1e-7)},
	}}
	rep, _ := readCost(t, prices, page(costTotalsRow), page(
		// list with cache: 200k*1e-6 + 800k*1e-7 = 0.28; billed 0.308 = 1.1x
		`{"model":"ackstorm.smart","recorded_input_cost":0.308,"input":1000000,"cache_read":800000,"cache_write":0}`,
		`{"model":"ackstorm.fast","recorded_input_cost":0.5,"input":500000,"cache_read":100000,"cache_write":0}`,
		`{"model":"unpriced","recorded_input_cost":0.1,"input":10,"cache_read":0,"cache_write":0}`,
		`{"model":"free","recorded_input_cost":0,"input":10,"cache_read":0,"cache_write":0}`,
	))
	c := rep.Cache
	// with cache = billed 0.308; without = 1M*1e-6*1.1 = 1.1; saving share unchanged by the markup
	if !near(c.WithCache, 0.308) || !near(c.WithoutCache, 1.1) || !near(c.Saving, 0.792) || !near(c.SavingShare, 0.72) {
		t.Errorf("cache = %+v", c)
	}
	if c.PricedModels != 1 || len(c.UnpricedModels) != 3 || c.Basis != costBasis {
		t.Errorf("coverage = %d priced, unpriced=%v basis=%q", c.PricedModels, c.UnpricedModels, c.Basis)
	}
	if prices.n != 1 {
		t.Errorf("prices read %d times", prices.n)
	}
}

func TestCostCacheUnavailableWhenPricesFail(t *testing.T) {
	// ports: none — a failed price read is unavailable, never a 0 saving
	rep, _ := readCost(t, &fakePrices{err: errors.New("boom")}, page(costTotalsRow), page(`{"model":"m","input":10,"cache_read":5,"cache_write":0}`))
	if rep.Cache.WithCache != nil || rep.Cache.Saving != nil || rep.Cache.Reason == nil || *rep.Cache.Reason != "prices_unavailable" {
		t.Errorf("cache = %+v", rep.Cache)
	}
	if !near(rep.CostPerModelRequest, 0.5) {
		t.Error("a price failure must not hide the measured cost figures")
	}
}

func TestCostNoPricesConfiguredIsUnavailable(t *testing.T) {
	rep, _ := readCost(t, nil, page(costTotalsRow), page())
	if rep.Cache.Reason == nil || *rep.Cache.Reason != "prices_unavailable" {
		t.Errorf("cache = %+v", rep.Cache)
	}
}

func TestCostFailedSourceIsUnavailable(t *testing.T) {
	// ports: test_failed_source_is_not_observed_empty
	rep, _ := readCost(t, &fakePrices{}, `{"exception":"boom"}`)
	if rep.Provenance.Reason == nil || rep.CostPerModelRequest != nil || rep.PricedGenerations != 0 {
		t.Errorf("rep = %+v", rep)
	}
	b, _ := json.Marshal(rep)
	if !strings.Contains(string(b), `"cost_per_model_request":null`) {
		t.Errorf("unavailable must serialise as null: %s", b)
	}
}

func TestCostRejectsBadScopeWithoutQuery(t *testing.T) {
	ch, calls := scriptedCH(t)
	if _, err := NewReader(core.NewReader(ch, []string{"p1"})).Cost(context.Background(), core.Scope{}, testWindow()); !errors.Is(err, core.ErrNoScope) {
		t.Errorf("err = %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %d", len(*calls))
	}
}

type fakeCatalog struct {
	caps    map[string]litellm.ModelCaps
	aliases map[string]string
}

func (f fakeCatalog) ListDeploymentCapabilities(context.Context) (map[string]litellm.ModelCaps, error) {
	return f.caps, nil
}
func (f fakeCatalog) ModelGroupAliases(context.Context) (map[string]string, error) {
	return f.aliases, nil
}

func TestLiteLLMPricesResolveAliasToTarget(t *testing.T) {
	// ports: none — live: ackstorm.smart has no deployment; its price is vertex.gemini-3.8-flash's
	p, err := LiteLLMPrices{Catalog: fakeCatalog{
		caps: map[string]litellm.ModelCaps{
			"vertex.gemini-3.8-flash": {InputCostPerToken: f64(7.5e-7), CacheReadInputTokenCost: f64(7.5e-8)},
			"own":                     {InputCostPerToken: f64(1)},
		},
		aliases: map[string]string{"ackstorm.smart": "vertex.gemini-3.8-flash", "own": "vertex.gemini-3.8-flash", "dangling": "nowhere"},
	}}.Prices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s := p["ackstorm.smart"]; !near(s.Input, 7.5e-7) || !near(s.CacheRead, 7.5e-8) {
		t.Errorf("alias price = %+v", s)
	}
	if o := p["own"]; !near(o.Input, 1) {
		t.Errorf("a deployment's own price must win over an alias, got %+v", o)
	}
	if _, ok := p["dangling"]; ok {
		t.Error("an alias to an unknown group has no price")
	}
}

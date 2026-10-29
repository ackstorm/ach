// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"slices"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// Cost adds what LiteLLM's daily activity (/platform/console/stats) cannot
// give: cost per model request, cost per session and the prompt-cache saving.
// Spend totals, by-model and by-key breakdowns stay on /platform/console/stats.
//
// Generations with no recorded cost (subscription traffic, e.g. Claude Code
// through /v1/messages) are counted apart and kept out of every denominator:
// counting them would make each request look cheaper than any priced one is.

// ModelPrice is a model's LiteLLM list price per token. nil = not published.
type ModelPrice struct {
	Input     *float64
	CacheRead *float64
}

// Prices returns list prices keyed by the public model alias the caller used.
type Prices interface {
	Prices(ctx context.Context) (map[string]ModelPrice, error)
}

// WithPrices sets the price source for the cache comparison; nil leaves it
// unavailable.
func (r *Reader) WithPrices(p Prices) *Reader {
	r.prices = p
	return r
}

const costTotalsSQL = `
SELECT count() AS generations,
       countIf(cost_details['total'] > 0) AS priced_generations,
       countIf(cost_details['total'] = 0) AS zero_cost_generations,
       sum(toFloat64(cost_details['total'])) AS spend,
       sumIf(toFloat64(cost_details['total']), session_id != '') AS session_spend,
       uniqExactIf(session_id, session_id != '' AND cost_details['total'] > 0) AS sessions
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type = 'GENERATION' AND startsWith(name, 'chat ')
FORMAT JSON`

// Input tokens per model for the cache comparison. Only priced generations:
// subscription traffic has no list price to save against.
const costCacheSQL = `
SELECT provided_model_name AS model,
       sum(toFloat64OrZero(metadata_values[indexOf(metadata_names, 'attributes.litellm.cost.input')])) AS recorded_input_cost,
       sum(usage_details['input']) AS input,
       sum(usage_details['input_cached_tokens']) AS cache_read,
       sum(usage_details['input_cache_creation']) AS cache_write
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type = 'GENERATION' AND startsWith(name, 'chat ') AND cost_details['total'] > 0
GROUP BY model ORDER BY input DESC LIMIT 101
FORMAT JSON`

type CostReport struct {
	Window               core.Window     `json:"window"`
	Provenance           core.Provenance `json:"provenance"`
	PricedGenerations    int             `json:"priced_generations"`
	ZeroCostGenerations  int             `json:"zero_cost_generations"`
	Spend                *float64        `json:"spend"`
	CostPerModelRequest  *float64        `json:"cost_per_model_request"`
	Sessions             int             `json:"sessions"`
	CostPerSession       *float64        `json:"cost_per_session"`
	SessionSpendCoverage *float64        `json:"session_spend_coverage"` // share of spend that carries a session id
	Cache                CacheCost       `json:"cache"`
}

// CacheCost is the input cost actually recorded (with the prompt cache) vs the
// same input at full price. LiteLLM's list price is only the shape: it is the
// global endpoint's, and LiteLLM bills Vertex's regional (eu) endpoint with the
// model's regional_endpoint_uplift_multiplier (1.1x), so each model's no-cache
// figure is scaled by its own recorded / list ratio. The saving share does not
// depend on that scale.
type CacheCost struct {
	Basis          string   `json:"basis"` // recorded_input_cost_vs_list_price_shape
	WithCache      *float64 `json:"with_cache"`
	WithoutCache   *float64 `json:"without_cache"`
	Saving         *float64 `json:"saving"`
	SavingShare    *float64 `json:"saving_share"` // saving / without_cache
	PricedModels   int      `json:"priced_models"`
	UnpricedModels []string `json:"unpriced_models"`
	Reason         *string  `json:"reason"`
}

type costTotals struct {
	Generations         float64 `json:"generations"`
	PricedGenerations   float64 `json:"priced_generations"`
	ZeroCostGenerations float64 `json:"zero_cost_generations"`
	Spend               float64 `json:"spend"`
	SessionSpend        float64 `json:"session_spend"`
	Sessions            float64 `json:"sessions"`
}

type cacheRow struct {
	Model             string  `json:"model"`
	RecordedInputCost float64 `json:"recorded_input_cost"`
	Input             float64 `json:"input"`
	CacheRead         float64 `json:"cache_read"`
	CacheWrite        float64 `json:"cache_write"`
}

func (r *Reader) Cost(ctx context.Context, s core.Scope, w core.Window) (CostReport, error) {
	if !s.Valid() {
		return CostReport{}, core.ErrNoScope
	}
	if !w.Valid() {
		return CostReport{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return CostReport{}, core.ErrProjectNotAllowed
	}
	rep := CostReport{Window: w, Cache: CacheCost{Basis: costBasis, UnpricedModels: []string{}}}
	params := map[string]string{}
	core.WindowParams(w, params)
	var totals []costTotals
	if err := r.CH().Query(ctx, core.Scoped(costTotalsSQL, r.Projects(), s, params), params, 1, &totals); err != nil || len(totals) != 1 {
		rep.Provenance = core.UnavailableProvenance(w, "observed_generation", core.SourceUnavailable)
		rep.Cache.Reason = core.Ptr(core.SourceUnavailable)
		return rep, nil
	}
	t := totals[0]
	rep.PricedGenerations, rep.ZeroCostGenerations, rep.Sessions = int(t.PricedGenerations), int(t.ZeroCostGenerations), int(t.Sessions)
	rep.Spend = &t.Spend
	rep.CostPerModelRequest = core.Ratio(&t.Spend, &t.PricedGenerations)
	rep.CostPerSession = core.Ratio(&t.SessionSpend, &t.Sessions)
	rep.SessionSpendCoverage = core.Ratio(&t.SessionSpend, &t.Spend)
	n := int(t.Generations)
	rep.Provenance = core.NewProvenance(w, "observed_generation", "all scoped deduplicated chat generations in requested window", "full-window-final-aggregate")
	rep.Provenance.Coverage = core.Coverage{Numerator: n, Denominator: &n}

	rep.Cache = r.cacheCost(ctx, s, w)
	return rep, nil
}

func (r *Reader) cacheCost(ctx context.Context, s core.Scope, w core.Window) CacheCost {
	c := CacheCost{Basis: costBasis, UnpricedModels: []string{}}
	if r.prices == nil {
		c.Reason = core.Ptr("prices_unavailable")
		return c
	}
	params := map[string]string{}
	core.WindowParams(w, params)
	var rows []cacheRow
	if err := r.CH().Query(ctx, core.Scoped(costCacheSQL, r.Projects(), s, params), params, core.GroupCap+1, &rows); err != nil {
		c.Reason = core.Ptr(core.SourceUnavailable)
		return c
	}
	prices, err := r.prices.Prices(ctx)
	if err != nil {
		c.Reason = core.Ptr("prices_unavailable")
		return c
	}
	var with, without float64
	for _, row := range rows {
		p, ok := prices[row.Model]
		// A model with no input or cache-read price cannot be compared; it is
		// named, never priced at zero.
		if !ok || p.Input == nil || p.CacheRead == nil || row.CacheRead > row.Input {
			c.UnpricedModels = append(c.UnpricedModels, row.Model)
			continue
		}
		list := (row.Input-row.CacheRead)**p.Input + row.CacheRead**p.CacheRead
		if list <= 0 || row.RecordedInputCost <= 0 {
			c.UnpricedModels = append(c.UnpricedModels, row.Model)
			continue
		}
		scale := row.RecordedInputCost / list
		with += row.RecordedInputCost
		without += row.Input * *p.Input * scale
		c.PricedModels++
	}
	slices.Sort(c.UnpricedModels)
	if c.PricedModels == 0 {
		c.Reason = core.Ptr("no_priced_models")
		return c
	}
	saving := without - with
	c.WithCache, c.WithoutCache, c.Saving = &with, &without, &saving
	c.SavingShare = core.Ratio(&saving, &without)
	return c
}

const costBasis = "recorded_input_cost_vs_list_price_shape"

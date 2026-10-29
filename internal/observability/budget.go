// SPDX-License-Identifier: Apache-2.0

package observability

import "time"

// TagBudget is the subset of a LiteLLM /tag/info entry BudgetBlock reads —
// the tag's accumulated spend plus its budget object. It mirrors
// litellm.TagInfoEntry, and is declared here because litellm imports
// observability, not the other way round.
type TagBudget struct {
	Spend          *float64 // nil = the cycle spend could not be read
	MaxBudget      *float64
	BudgetDuration *string
	ResetAt        *string
	Projection     *Projection // nil: no resetting cycle to project over
	CycleSeries    []DaySpend  // the cycle's spend per UTC day; nil = not read
}

// DaySpend is one UTC day of a tag's spend, as /tag/daily/activity books it.
type DaySpend struct {
	Date  string  `json:"date"`
	Spend float64 `json:"spend"`
}

// BudgetBlock renders the console's budget panel from the caller's own
// LiteLLM tag ("user:<email>"). That tag is the ONLY ceiling ACH enforces
// for a person: the forwarder stamps it on every request, so it covers
// their pk_ and every ek_ they own across Environments. Environment and
// per-key ceilings exist too but belong to those objects' own views.
// A nil tag means no budget is configured (or the read failed) — Source
// "unknown", never a fabricated zero.
func BudgetBlock(tag *TagBudget) Budget {
	if tag == nil {
		return Budget{Source: unknownLabel}
	}
	return Budget{
		Current:        tag.Spend,
		MaxBudget:      tag.MaxBudget,
		BudgetDuration: tag.BudgetDuration,
		ResetAt:        tag.ResetAt,
		Projection:     tag.Projection,
		CycleSeries:    tag.CycleSeries,
		Source:         "tag",
	}
}

// Projection is the budget cycle's spend carried linearly to reset_at. It is
// the one estimate Overview and Cost both show, so it follows the enforced
// cycle, never the page's date range. Spend is null with a reason when there
// is nothing honest to extrapolate: no_cycle (no reset_at, or it has passed),
// too_early (under a day in, where a few hours' rate swings wildly) or
// spend_unknown (the cycle's spend could not be read).
type Projection struct {
	Basis      string   `json:"basis"` // linear_cycle_rate
	CycleStart *string  `json:"cycle_start"`
	HorizonEnd *string  `json:"horizon_end"` // = reset_at
	Spend      *float64 `json:"spend"`
	Pct        *float64 `json:"pct"` // spend / max_budget; null without a ceiling
	Reason     *string  `json:"reason"`
}

const projectionBasis = "linear_cycle_rate"

// ProjectCycle extrapolates spend over [start, now) to the whole
// [start, reset) cycle. A nil spend is unknown, never 0.
func ProjectCycle(spend *float64, start, reset, now time.Time, maxBudget *float64) Projection {
	cs, he := start.UTC().Format(time.RFC3339), reset.UTC().Format(time.RFC3339)
	p := Projection{Basis: projectionBasis, CycleStart: &cs, HorizonEnd: &he}
	elapsed := now.Sub(start)
	switch {
	case !now.Before(reset) || !start.Before(reset):
		p.Reason = strp("no_cycle")
	case spend == nil:
		p.Reason = strp("spend_unknown")
	case elapsed < 24*time.Hour:
		p.Reason = strp("too_early")
	default:
		v := *spend / elapsed.Hours() * reset.Sub(start).Hours()
		p.Spend = &v
		if maxBudget != nil && *maxBudget > 0 {
			p.Pct = safePct(v, *maxBudget)
		}
	}
	return p
}

func noCycleProjection() Projection {
	return Projection{Basis: projectionBasis, Reason: strp("no_cycle")}
}

func strp(s string) *string { return &s }

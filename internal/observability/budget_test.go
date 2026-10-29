// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"reflect"
	"testing"
	"time"
)

// The budget panel reads the caller's OWN LiteLLM tag ("user:<email>") —
// the same ceiling the forwarder enforces. A nil tag is "no budget
// configured (or the read failed)", never a fabricated zero.
func TestBudgetBlockFromTag(t *testing.T) {
	cases := []struct {
		name string
		tag  *TagBudget
		want Budget
	}{
		{
			name: "configured",
			tag:  &TagBudget{Spend: f64p(1.25), MaxBudget: f64p(10), BudgetDuration: sp("30d"), ResetAt: sp("2026-10-31T23:00:00Z")},
			want: Budget{Current: f64p(1.25), MaxBudget: f64p(10), BudgetDuration: sp("30d"), ResetAt: sp("2026-10-31T23:00:00Z"), Source: "tag"},
		},
		{
			name: "spend but no ceiling",
			tag:  &TagBudget{Spend: f64p(3)},
			want: Budget{Current: f64p(3), Source: "tag"},
		},
		{
			name: "ceiling with unknown spend",
			tag:  &TagBudget{MaxBudget: f64p(10)},
			want: Budget{MaxBudget: f64p(10), Source: "tag"},
		},
		{
			name: "no tag at all",
			tag:  nil,
			want: Budget{Source: "unknown"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BudgetBlock(tc.tag); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("BudgetBlock() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The projection is the cycle's linear rate carried to reset_at: one figure
// for Overview and Cost, tied to the budget cycle, never to the page's range.
func TestProjectCycle(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) // 31-day cycle
	cases := []struct {
		name       string
		now        time.Time
		max        *float64
		wantSpend  *float64
		wantPct    *float64
		wantReason *string
		unknown    bool // the cycle spend could not be read
	}{
		// 10 days in, $10 spent: $1/day over 31 days.
		{"linear to reset", start.AddDate(0, 0, 10), f64p(62), f64p(31), f64p(0.5), nil, false},
		{"no ceiling: no pct", start.AddDate(0, 0, 10), nil, f64p(31), nil, nil, false},
		{"under a day is too early", start.Add(23 * time.Hour), f64p(62), nil, nil, sp("too_early"), false},
		{"reset already passed", reset.Add(time.Hour), f64p(62), nil, nil, sp("no_cycle"), false},
		{"unknown spend is not projected", start.AddDate(0, 0, 10), f64p(62), nil, nil, sp("spend_unknown"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spend := f64p(10)
			if tc.unknown {
				spend = nil
			}
			p := ProjectCycle(spend, start, reset, tc.now, tc.max)
			if !eqF(p.Spend, tc.wantSpend) || !eqF(p.Pct, tc.wantPct) || derefS(p.Reason) != derefS(tc.wantReason) {
				t.Errorf("ProjectCycle() = spend %v pct %v reason %v", derefF(p.Spend), derefF(p.Pct), derefS(p.Reason))
			}
			if p.Basis != projectionBasis || derefS(p.HorizonEnd) != "2026-11-01T00:00:00Z" || derefS(p.CycleStart) != "2026-10-01T00:00:00Z" {
				t.Errorf("horizon = %+v", p)
			}
		})
	}
}

func TestBudgetWithoutCycleHasNoProjection(t *testing.T) {
	cur := AggregateWindow(loadDaily(t, "daily_activity_empty.json"))
	for _, tag := range []*TagBudget{nil, {Spend: f64p(3)}} {
		p := BuildStatsContract(cur, &cur, BudgetBlock(tag), nil, testCapabilities, testRange).Budget.Projection
		if p.Spend != nil || derefS(p.Reason) != "no_cycle" || p.Basis != projectionBasis {
			t.Errorf("tag %+v: projection = %+v, want unavailable no_cycle", tag, p)
		}
	}
}

func eqF(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return approxEqual(*a, *b)
}

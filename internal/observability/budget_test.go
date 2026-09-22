// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"reflect"
	"testing"
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
			tag:  &TagBudget{Spend: 1.25, MaxBudget: f64p(10), BudgetDuration: sp("30d")},
			want: Budget{Current: 1.25, MaxBudget: f64p(10), BudgetDuration: sp("30d"), Source: "tag"},
		},
		{
			name: "spend but no ceiling",
			tag:  &TagBudget{Spend: 3},
			want: Budget{Current: 3, Source: "tag"},
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

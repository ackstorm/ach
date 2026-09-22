// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"reflect"
	"testing"
)

// Ports the _budget_block semantics asserted by app/tests/test_session.py
// (alitellm-auth): test_budget_block_prefers_enforced_member_budget,
// test_budget_block_member_falls_back_to_user_budget_duration,
// test_budget_block_falls_back_to_user_when_no_member,
// test_budget_block_unknown_when_nothing_configured.
func TestBudgetBlock(t *testing.T) {
	ten := 10.0
	cases := []struct {
		name   string
		user   UserInfo
		member *MemberBudget
		want   Budget
	}{
		{
			name:   "member budget wins",
			user:   UserInfo{Spend: f64p(1)},
			member: &MemberBudget{MaxBudget: &ten, Current: 3},
			want:   Budget{Current: 3, MaxBudget: &ten, Source: "team_member"},
		},
		{
			name:   "member without budget_duration falls back to user's",
			user:   UserInfo{BudgetDuration: sp("1mo")},
			member: &MemberBudget{Current: 0},
			want:   Budget{Current: 0, BudgetDuration: sp("1mo"), Source: "team_member"},
		},
		{
			name:   "user-level with max",
			user:   UserInfo{Spend: f64p(2), MaxBudget: &ten},
			member: nil,
			want:   Budget{Current: 2, MaxBudget: &ten, Source: "user"},
		},
		{
			name:   "user-level with spend only",
			user:   UserInfo{Spend: f64p(2)},
			member: nil,
			want:   Budget{Current: 2, Source: "user"},
		},
		{
			name:   "nothing configured is unknown, not user",
			user:   UserInfo{Spend: f64p(0)},
			member: nil,
			want:   Budget{Current: 0, Source: "unknown"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BudgetBlock(tc.user, tc.member)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("BudgetBlock() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

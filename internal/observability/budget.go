// SPDX-License-Identifier: Apache-2.0

package observability

// UserInfo is the subset of LiteLLM's GET /user/info user_info block that
// BudgetBlock reads (session.py _budget_block).
type UserInfo struct {
	Spend          *float64
	MaxBudget      *float64
	BudgetDuration *string
}

// MemberBudget is the subset of a /team/info team_memberships[user] entry —
// the ENFORCED per-member team budget.
type MemberBudget struct {
	MaxBudget      *float64
	Current        float64
	BudgetDuration *string
}

// BudgetBlock is the canonical {current, max_budget, budget_duration,
// source} for /me and /stats (session.py _budget_block).
//
// Prefers the ENFORCED per-member team budget when member is non-nil;
// falls back to the reporting-only user-level figures otherwise. A member
// budget without its own budget_duration falls back to the user-level
// value (informational only — membership budget_duration is usually null
// in this deployment).
//
// On the user-level fallback: source is "user" only when a budget is
// actually configured (MaxBudget set) OR spend is genuinely non-zero;
// otherwise "unknown", so a "no budget configured" user is not mislabeled
// as having a user-level budget (WR-01).
func BudgetBlock(user UserInfo, member *MemberBudget) Budget {
	if member != nil {
		duration := member.BudgetDuration
		if duration == nil {
			duration = user.BudgetDuration
		}
		return Budget{
			Current:        member.Current,
			MaxBudget:      member.MaxBudget,
			BudgetDuration: duration,
			Source:         "team_member",
		}
	}

	hasUserData := user.MaxBudget != nil || (user.Spend != nil && *user.Spend != 0)
	var current float64
	if hasUserData && user.Spend != nil {
		current = *user.Spend
	}
	source := unknownLabel
	if hasUserData {
		source = "user"
	}
	return Budget{
		Current:        current,
		MaxBudget:      user.MaxBudget,
		BudgetDuration: user.BudgetDuration,
		Source:         source,
	}
}

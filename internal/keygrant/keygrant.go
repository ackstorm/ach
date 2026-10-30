// SPDX-License-Identifier: Apache-2.0

// Package keygrant is the one rule deciding whether KeyGrants allow an
// exchange tuple (identity, Environment, "<namespace>/<serviceaccount>"): a
// single grant must name all three; lists from different grants are never
// combined. The exchange handler, the exchange-JWT resolver and the operator's
// cleanup sweep all decide through it.
package keygrant

import (
	"slices"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// Covering returns the grants that allow the tuple.
func Covering(grants []db.KeyGrantRow, identity, env, requester string) []db.KeyGrantRow {
	var out []db.KeyGrantRow
	for _, g := range grants {
		if g.Identity == identity && slices.Contains(g.Environments, env) && slices.Contains(g.ServiceAccounts, requester) {
			out = append(out, g)
		}
	}
	return out
}

// Miss names, for the audit record only, why grants already filtered to one
// identity allow nothing for env. The HTTP answer is always not_granted.
func Miss(grants []db.KeyGrantRow, env string) string {
	if len(grants) == 0 {
		return "no_grant_for_identity"
	}
	for _, g := range grants {
		if slices.Contains(g.Environments, env) {
			return "requester_not_granted"
		}
	}
	return "environment_not_granted"
}

// Budget is the lowest maxBudget among covering grants (first by name on a
// tie); nil when none sets one.
func Budget(covering []db.KeyGrantRow) *litellm.TagBudget {
	var best *litellm.TagBudget
	for _, g := range covering {
		if g.MaxBudget == nil {
			continue
		}
		if best == nil || *g.MaxBudget < best.MaxBudget {
			best = &litellm.TagBudget{MaxBudget: *g.MaxBudget, BudgetDuration: g.BudgetDuration}
		}
	}
	return best
}

// Plan splits non-revoked exchange keys into those no grant allows any more
// (revoke) and the ceiling each still-covered key carries (budgets, by key id;
// a key whose covering grants set no budget is absent).
func Plan(grants []db.KeyGrantRow, keys []db.EkKeyInfo) (revoke []db.EkKeyInfo, budgets map[string]litellm.TagBudget) {
	budgets = map[string]litellm.TagBudget{}
	for _, k := range keys {
		covering := Covering(grants, k.OwnerEmail, k.Environment, k.Requester)
		if len(covering) == 0 {
			revoke = append(revoke, k)
			continue
		}
		if b := Budget(covering); b != nil {
			budgets[k.KeyID] = *b
		}
	}
	return revoke, budgets
}

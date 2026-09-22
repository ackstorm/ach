// SPDX-License-Identifier: Apache-2.0

package observability

// TagBudget is the subset of a LiteLLM /tag/info entry BudgetBlock reads —
// the tag's accumulated spend plus its budget object. It mirrors
// litellm.TagInfoEntry, and is declared here because litellm imports
// observability, not the other way round.
type TagBudget struct {
	Spend          float64
	MaxBudget      *float64
	BudgetDuration *string
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
		Source:         "tag",
	}
}

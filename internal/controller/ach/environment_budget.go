// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// environmentBudgetCondition makes the "environment:<name>" tag budget match
// spec.budget and maps the outcome to the BudgetSynced condition. The second
// return is false when the Environment declares no budget — that publishes
// NO condition at all, rather than a True one claiming a ceiling that does
// not exist.
//
// Dropping spec.budget from a live Environment is a no-op: the ceiling stays
// until the tag is deleted, which happens on Environment DELETE
// (reconcileDeletion reaps both the tag and its budget object).
func environmentBudgetCondition(ctx context.Context, ll litellm.Client, env *achv1alpha1.Environment) (metav1.Condition, bool) {
	if env.Spec.Budget == nil {
		return metav1.Condition{}, false
	}
	tag := litellm.EnvironmentBudgetTag(env.Name)
	cond := metav1.Condition{
		Type:               "BudgetSynced",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("LiteLLM tag %s capped at %v", tag, env.Spec.Budget.MaxBudget),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if err := ll.UpsertTagBudget(ctx, tag, litellm.TagBudget{
		MaxBudget:      env.Spec.Budget.MaxBudget,
		BudgetDuration: env.Spec.Budget.BudgetDuration,
	}); err != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "TagWriteFailed"
		cond.Message = fmt.Sprintf("environment budget tag %s: %v", tag, err)
	}
	return cond, true
}

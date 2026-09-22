// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// reconcileEnvironmentBudget makes the "environment:<name>" tag match
// spec.budget. Dropping spec.budget from a live Environment is a no-op —
// the ceiling stays until the tag is deleted, which happens on Environment
// DELETE (reconcileDeletion reaps both the tag and its budget object).
func reconcileEnvironmentBudget(ctx context.Context, ll litellm.Client, env string, b *achv1alpha1.BudgetBlock) error {
	if b == nil {
		return nil
	}
	tag := litellm.EnvironmentBudgetTag(env)
	if err := ll.UpsertTagBudget(ctx, tag, litellm.TagBudget{
		MaxBudget:      b.MaxBudget,
		BudgetDuration: b.BudgetDuration,
	}); err != nil {
		return fmt.Errorf("environment budget tag %s: %w", tag, err)
	}
	return nil
}

// environmentBudgetCondition reconciles the budget and maps the outcome to
// the BudgetSynced condition. The second return is false when the
// Environment declares no budget — that publishes NO condition at all,
// rather than a True one claiming a ceiling that does not exist.
func environmentBudgetCondition(ctx context.Context, ll litellm.Client, env *achv1alpha1.Environment) (metav1.Condition, bool) {
	if env.Spec.Budget == nil {
		return metav1.Condition{}, false
	}
	cond := metav1.Condition{
		Type:               "BudgetSynced",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("LiteLLM tag %s capped at %v", litellm.EnvironmentBudgetTag(env.Name), env.Spec.Budget.MaxBudget),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if err := reconcileEnvironmentBudget(ctx, ll, env.Name, env.Spec.Budget); err != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "TagWriteFailed"
		cond.Message = err.Error()
	}
	return cond, true
}

// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// budgetFake records tag-budget writes. Everything else is the NoopClient.
type budgetFake struct {
	*litellm.NoopClient
	upsertedTags map[string]litellm.TagBudget
	upsertTagErr error
}

func newBudgetFake() *budgetFake {
	return &budgetFake{
		NoopClient:   &litellm.NoopClient{Log: logr.Discard()},
		upsertedTags: map[string]litellm.TagBudget{},
	}
}

func (f *budgetFake) UpsertTagBudget(_ context.Context, name string, b litellm.TagBudget) error {
	if f.upsertTagErr != nil {
		return f.upsertTagErr
	}
	f.upsertedTags[name] = b
	return nil
}

// TestReconcileEnvironmentBudget — a spec.budget writes the environment tag.
func TestReconcileEnvironmentBudget(t *testing.T) {
	cases := []struct {
		name    string
		budget  *achv1alpha1.BudgetBlock
		wantTag bool
		wantMax float64
		wantDur string
	}{
		{name: "set", budget: &achv1alpha1.BudgetBlock{MaxBudget: 250, BudgetDuration: "30d"}, wantTag: true, wantMax: 250, wantDur: "30d"},
		{name: "unset", budget: nil, wantTag: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newBudgetFake()
			if err := reconcileEnvironmentBudget(context.Background(), fake, "demo", tc.budget); err != nil {
				t.Fatalf("reconcileEnvironmentBudget: %v", err)
			}
			got, ok := fake.upsertedTags["environment:demo"]
			if ok != tc.wantTag {
				t.Fatalf("tag written = %v, want %v (%v)", ok, tc.wantTag, fake.upsertedTags)
			}
			if tc.wantTag && (got.MaxBudget != tc.wantMax || got.BudgetDuration != tc.wantDur) {
				t.Fatalf("tag budget = %+v", got)
			}
		})
	}
}

// TestReconcileEnvironmentBudgetErrorSurfaces — a LiteLLM refusal is an
// error so the caller can set BudgetSynced=False.
func TestReconcileEnvironmentBudgetErrorSurfaces(t *testing.T) {
	fake := newBudgetFake()
	fake.upsertTagErr = errors.New("boom")
	if err := reconcileEnvironmentBudget(context.Background(), fake,
		"demo", &achv1alpha1.BudgetBlock{MaxBudget: 1}); err == nil {
		t.Fatal("want an error")
	}
}

// TestEnvironmentBudgetCondition — the condition mapping the reconciler
// publishes: no spec.budget means NO condition at all (not a True one).
func TestEnvironmentBudgetCondition(t *testing.T) {
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	if _, ok := environmentBudgetCondition(context.Background(), newBudgetFake(), env); ok {
		t.Fatal("an Environment without spec.budget must publish no BudgetSynced condition")
	}

	env.Spec.Budget = &achv1alpha1.BudgetBlock{MaxBudget: 5}
	cond, ok := environmentBudgetCondition(context.Background(), newBudgetFake(), env)
	if !ok || cond.Type != "BudgetSynced" || cond.Status != "True" || cond.Reason != "Synced" {
		t.Fatalf("cond = %+v (ok=%v)", cond, ok)
	}

	failing := newBudgetFake()
	failing.upsertTagErr = errors.New("boom")
	cond, ok = environmentBudgetCondition(context.Background(), failing, env)
	if !ok || cond.Status != "False" || cond.Reason != "TagWriteFailed" {
		t.Fatalf("cond = %+v (ok=%v)", cond, ok)
	}
}

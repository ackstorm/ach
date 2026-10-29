// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// TestEnvironmentBudgetCondition — the reconcile + condition mapping: a
// spec.budget writes the environment tag, no spec.budget writes nothing and
// publishes NO condition, and a LiteLLM refusal is BudgetSynced=False.
func TestEnvironmentBudgetCondition(t *testing.T) {
	env := &achv1alpha1.Environment{}
	env.Name = "demo"

	t.Run("unset", func(t *testing.T) {
		fake := newBudgetFake()
		if _, ok := environmentBudgetCondition(context.Background(), fake, env); ok {
			t.Fatal("an Environment without spec.budget must publish no BudgetSynced condition")
		}
		if len(fake.upsertedTags) != 0 {
			t.Fatalf("want no tag writes, got %v", fake.upsertedTags)
		}
	})

	env.Spec.Budget = &achv1alpha1.BudgetBlock{MaxBudget: 250, BudgetDuration: "30d"}

	t.Run("set", func(t *testing.T) {
		fake := newBudgetFake()
		cond, ok := environmentBudgetCondition(context.Background(), fake, env)
		if !ok || cond.Type != "BudgetSynced" || cond.Status != metav1.ConditionTrue || cond.Reason != "Synced" {
			t.Fatalf("cond = %+v (ok=%v)", cond, ok)
		}
		got, present := fake.upsertedTags["environment:demo"]
		if !present || got.MaxBudget != 250 || got.BudgetDuration != "30d" {
			t.Fatalf("tag budget = %+v (present=%v)", got, present)
		}
	})

	t.Run("litellm refuses", func(t *testing.T) {
		fake := newBudgetFake()
		fake.upsertTagErr = errors.New("boom")
		cond, ok := environmentBudgetCondition(context.Background(), fake, env)
		if !ok || cond.Status != metav1.ConditionFalse || cond.Reason != "TagWriteFailed" {
			t.Fatalf("cond = %+v (ok=%v)", cond, ok)
		}
	})
}

// TestEnvironmentBudgetSyncedEnvtest drives a real Environment with a
// spec.budget through the manager and asserts BOTH halves of the feature:
// the BudgetSynced condition lands True on the CR, and the
// "environment:<name>" tag budget actually reached LiteLLM. The unit test
// above covers the mapping; this covers the reconciler wiring (a missing
// call site would leave the condition absent and the tag unwritten).
func TestEnvironmentBudgetSyncedEnvtest(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	upsertedTags.Delete("environment:test-env-budget")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-budget",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Budget:          &achv1alpha1.BudgetBlock{MaxBudget: 250, BudgetDuration: "30d"},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	var final *metav1.Condition
	ok := Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		final = apimeta.FindStatusCondition(got.Status.Conditions, "BudgetSynced")
		return final != nil && final.Status == metav1.ConditionTrue
	}, 15*time.Second, 250*time.Millisecond)
	if !ok {
		t.Fatalf("BudgetSynced never reached True within 15s: %+v", final)
	}
	if final.Reason != "Synced" {
		t.Errorf("BudgetSynced.Reason = %q, want Synced (message=%q)", final.Reason, final.Message)
	}

	raw, present := upsertedTags.Load("environment:test-env-budget")
	if !present {
		t.Fatal("the environment budget tag was never written to LiteLLM")
	}
	if got := raw.(litellm.TagBudget); got.MaxBudget != 250 || got.BudgetDuration != "30d" {
		t.Fatalf("tag budget = %+v, want {250 30d}", got)
	}
}

// TestEnvironmentWithoutBudgetPublishesNoCondition — an Environment that
// declares no ceiling must not carry a BudgetSynced condition at all; a
// True one would claim a ceiling that does not exist.
func TestEnvironmentWithoutBudgetPublishesNoCondition(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-nobudget",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{AuthorizedTeams: []string{"default"}},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Wait for the reconcile to have happened at all (Available is always
	// written), then assert BudgetSynced is absent.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return apimeta.FindStatusCondition(got.Status.Conditions, "Available") != nil
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("Environment never reconciled within 15s")
	}
	var got achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatal(err)
	}
	if c := apimeta.FindStatusCondition(got.Status.Conditions, "BudgetSynced"); c != nil {
		t.Fatalf("BudgetSynced present on a budgetless Environment: %+v", c)
	}
}

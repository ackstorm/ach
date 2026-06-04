// SPDX-License-Identifier: Apache-2.0

// envtest coverage for the skill content-present gate on
// ExecutionResourcesResolved (mirrors the plugin content-present gate).
//
// Gate: an Environment whose spec.context.skills references a skill that has
// a DB row but last_successful_refresh IS NULL must hold
// ExecutionResourcesResolved=False until the content is synced.
//
// Reuses setupPluginContentPresentDB + buildPluginTestReconciler from
// environment_plugin_contentpresent_test.go (same package). Skipped unless
// Docker is available.

package ach

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
)

// TestEnvSkillContentPresent_NotSynced: an Environment whose
// spec.context.skills references a skill with last_successful_refresh IS NULL
// must hold ExecutionResourcesResolved=False and list the ref in
// UnresolvedContextSkills.
func TestEnvSkillContentPresent_NotSynced(t *testing.T) {
	pool, cleanup := setupPluginContentPresentDB(t)
	defer cleanup()

	ctx := context.Background()
	const ns = WatchNamespace
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	// Seed a skills row with NULL last_successful_refresh (no content).
	if err := achdb.UpsertSkill(ctx, pool, achdb.SkillRow{
		Namespace:             ns,
		Name:                  "my-skill",
		StorageLocation:       "",
		LastSuccessfulRefresh: nil, // NOT synced — the gate trigger
		MaxStalenessSeconds:   86400,
		ResourceVersion:       "1",
	}); err != nil {
		t.Fatalf("seed skills row: %v", err)
	}

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-skill-not-synced",
			Namespace: ns,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context: achv1alpha1.ContextBlock{
				Skills: []string{"my-skill"},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment CR: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, f := range got.Finalizers {
			if f == environmentFinalizer {
				return true
			}
		}
		return false
	}, 10*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer never added within 10s")
	}

	r := buildPluginTestReconciler(t, ns, pool)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var final achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &final); err != nil {
		t.Fatalf("re-Get Environment: %v", err)
	}

	var errCond *metav1.Condition
	for i := range final.Status.Conditions {
		if final.Status.Conditions[i].Type == "ExecutionResourcesResolved" {
			errCond = &final.Status.Conditions[i]
			break
		}
	}
	if errCond == nil {
		t.Fatalf("ExecutionResourcesResolved condition not found; conditions=%+v", final.Status.Conditions)
	}
	if errCond.Status != metav1.ConditionFalse {
		t.Errorf("ExecutionResourcesResolved.Status = %q; want False (skill content absent must block). message=%q",
			errCond.Status, errCond.Message)
	}

	found := false
	for _, s := range final.Status.UnresolvedContextSkills {
		if s == "my-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("UnresolvedContextSkills = %v; want to contain \"my-skill\"", final.Status.UnresolvedContextSkills)
	}
}

// TestEnvSkillContentPresent_Synced: same scenario but with a non-null
// last_successful_refresh → ExecutionResourcesResolved=True and
// UnresolvedContextSkills=[].
func TestEnvSkillContentPresent_Synced(t *testing.T) {
	pool, cleanup := setupPluginContentPresentDB(t)
	defer cleanup()

	ctx := context.Background()
	const ns = WatchNamespace
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	now := time.Now().UTC()
	if err := achdb.UpsertSkill(ctx, pool, achdb.SkillRow{
		Namespace:             ns,
		Name:                  "my-synced-skill",
		StorageLocation:       "/cache/skill/my-synced-skill.tar.gz",
		LastSuccessfulRefresh: &now, // content IS present
		MaxStalenessSeconds:   86400,
		ResourceVersion:       "1",
	}); err != nil {
		t.Fatalf("seed skills row: %v", err)
	}

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-skill-synced",
			Namespace: ns,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context: achv1alpha1.ContextBlock{
				Skills: []string{"my-synced-skill"},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment CR: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, f := range got.Finalizers {
			if f == environmentFinalizer {
				return true
			}
		}
		return false
	}, 10*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer never added within 10s")
	}

	r := buildPluginTestReconciler(t, ns, pool)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var final achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &final); err != nil {
		t.Fatalf("re-Get Environment: %v", err)
	}

	var errCond *metav1.Condition
	for i := range final.Status.Conditions {
		if final.Status.Conditions[i].Type == "ExecutionResourcesResolved" {
			errCond = &final.Status.Conditions[i]
			break
		}
	}
	if errCond == nil {
		t.Fatalf("ExecutionResourcesResolved condition not found; conditions=%+v", final.Status.Conditions)
	}
	if errCond.Status != metav1.ConditionTrue {
		t.Errorf("ExecutionResourcesResolved.Status = %q; want True (synced skill should not block). message=%q",
			errCond.Status, errCond.Message)
	}
	if len(final.Status.UnresolvedContextSkills) != 0 {
		t.Errorf("UnresolvedContextSkills = %v; want [] (skill is content-present)", final.Status.UnresolvedContextSkills)
	}
}

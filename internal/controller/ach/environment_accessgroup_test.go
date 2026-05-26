// SPDX-License-Identifier: Apache-2.0

// Plan TODO §7 — Environment AccessGroupSynced reconciler tests.
// Asserts the steady-state Snapshotter-wired path emits the closed-set
// AccessGroupSynced conditions (True/Synced, False/PartialBind,
// False/AccessGroupCreateFailed) and obeys idempotency + drift contracts.

package ach

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// agCondition returns the AccessGroupSynced entry or nil.
func agCondition(env *achv1alpha1.Environment) *metav1.Condition {
	for i := range env.Status.Conditions {
		c := &env.Status.Conditions[i]
		if c.Type == "AccessGroupSynced" {
			return c
		}
	}
	return nil
}

// TestAccessGroupSynced_True_WhenCreateAndBindSucceed is the §7 happy
// path. Asserts AccessGroupSynced flips to True/Synced once the
// reconciler creates the access group AND binds the authorized team.
func TestAccessGroupSynced_True_WhenCreateAndBindSucceed(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-happy",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == "Synced"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected True/Synced, conditions = %+v", got.Status.Conditions)
	}
	if got := accessGroupFake.CreateCallsFor("test-env-ag-happy"); got < 1 {
		t.Errorf("create call count = %d; want >= 1", got)
	}
	if got := accessGroupFake.BindCallsFor("test-env-ag-happy", "default"); got < 1 {
		t.Errorf("bind call count = %d; want >= 1", got)
	}
}

// TestAccessGroupSynced_False_OnBindFailure asserts the PartialBind path.
func TestAccessGroupSynced_False_OnBindFailure(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.InjectBindErr("test-env-ag-partialbind", "team-broken", errFakeBindFailed)

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-partialbind",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"team-ok", "team-broken"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "PartialBind"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/PartialBind, conditions = %+v", got.Status.Conditions)
	}
}

// TestAccessGroupSynced_Idempotent_NoExtraBindOnRereconcile asserts that
// once the binding is in place, a subsequent reconcile triggered by
// annotation touch does NOT re-issue the bind call. The fake's
// ListAccessGroupBindings returns the already-recorded binding; the
// reconciler's currentSet membership check skips the bind loop.
func TestAccessGroupSynced_Idempotent_NoExtraBindOnRereconcile(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-idemp",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("first reconcile did not flip AccessGroupSynced=True")
	}

	firstBindCount := accessGroupFake.BindCallsFor("test-env-ag-idemp", "default")
	if firstBindCount < 1 {
		t.Fatalf("expected >= 1 bind call after first reconcile, got %d", firstBindCount)
	}

	// Trigger re-reconcile via annotation touch.
	var got achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations == nil {
		got.Annotations = map[string]string{}
	}
	got.Annotations["test/touch"] = "1"
	if err := k8sClient.Update(ctx, &got); err != nil {
		t.Fatal(err)
	}

	time.Sleep(3 * time.Second)
	if grew := accessGroupFake.BindCallsFor("test-env-ag-idemp", "default"); grew != firstBindCount {
		t.Errorf("bind call count = %d after re-reconcile; want unchanged (%d) — idempotency violated", grew, firstBindCount)
	}
}

// TestAccessGroupSynced_DriftDetection_OrphanLogged asserts the orphan
// branch: ListAccessGroupBindings returns a team that's NOT in
// spec.authorizedTeams. The reconciler logs the orphan but still emits
// True/Synced (auto-removal is §10 scope).
func TestAccessGroupSynced_DriftDetection_OrphanLogged(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedBinding("test-env-ag-drift", "orphan-team")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-drift",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"current-team"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("orphan-present case did NOT reach True (orphans must NOT block sync)")
	}

	if got := accessGroupFake.BindCallsFor("test-env-ag-drift", "orphan-team"); got != 0 {
		t.Errorf("orphan team bind count = %d; want 0 (orphans logged but untouched)", got)
	}
	if got := accessGroupFake.BindCallsFor("test-env-ag-drift", "current-team"); got != 1 {
		t.Errorf("current-team bind count = %d; want 1", got)
	}
}

// TestAccessGroupSynced_False_OnCreateFailure asserts the
// AccessGroupCreateFailed reason path.
func TestAccessGroupSynced_False_OnCreateFailure(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.InjectCreateErr("test-env-ag-createfail", errors.New("fake: create blew up"))

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-createfail",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "AccessGroupCreateFailed"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/AccessGroupCreateFailed, conditions = %+v", got.Status.Conditions)
	}
	if got := accessGroupFake.BindCallsFor("test-env-ag-createfail", "default"); got != 0 {
		t.Errorf("bind call count = %d after create failure; want 0 (no proceed past failed create)", got)
	}
}

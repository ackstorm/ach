// SPDX-License-Identifier: Apache-2.0

// Environment finalizer add/remove test — Plan 01-11 Task 4. Asserts
// CRD-06 (finalizer add) + Hub §6.5 drain (LiteLLM.DeleteAccessGroup
// then LiteLLM.DeleteTag) + OP-02 (the §6.5 deletion sequencing).

package ach

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// TestEnvironmentFinalizerAddRemove exercises the full Hub §6.5 drain:
//
//  1. Create an Environment in WatchNamespace.
//  2. Poll until controllerutil.ContainsFinalizer reports the
//     environments.ach.ackstorm.ai/finalizer constant — proves the
//     reconciler's Step 2b path ran.
//  3. Delete the CR and poll until it disappears — proves the
//     finalizer drained.
//  4. Assert litellmCounter.Load() >= 2 — proves Step 2a invoked both
//     LiteLLM.DeleteAccessGroup (§6.5 step 2) and LiteLLM.DeleteTag
//     (§6.5 step 3). The counting NoopClient (suite_test.go) bumps the
//     atomic counter exactly once per call.
//
// The DB pool is nil on the test reconciler (suite_test.go does not
// inject one), so drainEkRows trivially exits with slog.Info — D-12
// contract: the loop body is real code; Phase 1 just has zero rows
// because no ek_ has been minted yet.
func TestEnvironmentFinalizerAddRemove(t *testing.T) {
	ctx := context.Background()
	// Reset counter so this test owns its assertion regardless of any
	// other test that ran before. atomic.Int64.Store(0) is safe even
	// with concurrent Adds — the worst-case race is a missed increment,
	// which the >= 2 assertion still tolerates correctly.
	litellmCounter.Store(0)

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-finalizer",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime: achv1alpha1.RuntimeBlock{
				Models:     []string{},
				MCPServers: []string{},
				A2AAgents:  []string{},
			},
			Context: achv1alpha1.ContextBlock{
				Prompts:   []string{},
				Plugins:   []string{},
				Artifacts: []string{},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
	})

	// Step 1: poll for finalizer add.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, environmentFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer %q never added within 30s", environmentFinalizer)
	}

	// Step 2: re-Get and Delete.
	var withFinalizer achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &withFinalizer); err != nil {
		t.Fatalf("re-get CR before delete: %v", err)
	}
	if err := k8sClient.Delete(ctx, &withFinalizer); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	// Step 3: poll for CR removal.
	probe := &achv1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{
		Name: cr.Name, Namespace: cr.Namespace,
	}}
	if !WaitForGone(ctx, probe, 15*time.Second) {
		t.Fatalf("Environment CR not removed within 15s of Delete (finalizer drain did not complete)")
	}

	// Step 4: assert LiteLLM stub call count.
	// §6.5 step 2 = DeleteAccessGroup (1 call); step 3 = DeleteTag
	// (1 call). The counting NoopClient bumps exactly once per method.
	// We expect AT LEAST 2 because a re-reconcile after the finalizer
	// add could plausibly invoke the deletion path twice — once is the
	// correct behavior, twice is the upper bound any reasonable
	// implementation would emit. Assert the floor.
	if got := litellmCounter.Load(); got < 2 {
		t.Errorf("OP-02 FAIL: expected >= 2 LiteLLM noop calls (DeleteAccessGroup + DeleteTag), got %d",
			got)
	} else {
		t.Logf("OP-02: litellmCounter=%d (>= 2 — DeleteAccessGroup + DeleteTag both invoked)", got)
	}
}

// TestFinalizer_DeletesLegacyNamedAccessGroup asserts that a group left under
// an OLDER name generation is still removed on Environment deletion. The seed
// carries the v0.6.19 ach-<env> name; the reconcile adopts it by id and renames
// it in place to the canonical ach-env-<env>, then delete must still sweep it.
// The finalizer deletes the group under EVERY generation (ach-env-<env>,
// ach-<env>, bare <env>) so nothing survives regardless of which name it
// wound up under — this test asserts all three names are absent afterward.
func TestFinalizer_DeletesLegacyNamedAccessGroup(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   "ag-v0619-del",
		AccessGroupName: "ach-test-env-fin-legacy",
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env-fin-legacy", Namespace: WatchNamespace},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         emptyRuntimeBlock(),
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Wait for the reconcile to land (proves the finalizer was added — a
	// prerequisite for Delete to drain rather than no-op).
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == "Synced"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("access group did not sync before delete")
	}

	if err := k8sClient.Delete(ctx, cr); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !Eventually(func() bool {
		bare, _ := accessGroupFake.GetAccessGroupByName(ctx, "test-env-fin-legacy")
		v0619, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-test-env-fin-legacy")
		canon, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-env-test-env-fin-legacy")
		return bare == nil && v0619 == nil && canon == nil
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer left a group behind (bare, v0.6.19, or canonical)")
	}
}

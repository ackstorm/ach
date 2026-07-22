// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// TestReconcileAccessGroup_AttachesEntitledUserShells asserts an authorized
// human team's members whose ach-user-<email> shell already exists get that
// shell attached to the access group union alongside the env shell — and a
// member with no shell (never minted a pk_) is skipped, not phantom-added.
func TestReconcileAccessGroup_AttachesEntitledUserShells(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeamMembers("t-run", "run",
		litellm.TeamMemberRole{UserID: "a@b.com"}, litellm.TeamMemberRole{UserID: "c@d.com"})
	// a@b.com HAS a shell; c@d.com does NOT (never minted a pk_).
	accessGroupFake.SeedUserShellPresent("a@b.com")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-usershells",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"run"},
			Runtime:         emptyRuntimeBlock(),
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

	last := accessGroupFake.LastCreate("ach-test-env-usershells")
	if !slices.Contains(last.AssignedTeamIDs, "ach-user-a@b.com") {
		t.Errorf("AssignedTeamIDs = %v, want ach-user-a@b.com (entitled + shell exists)", last.AssignedTeamIDs)
	}
	if !slices.Contains(last.AssignedTeamIDs, "id-ach-env-test-env-usershells") {
		t.Errorf("AssignedTeamIDs = %v, want the env shell", last.AssignedTeamIDs)
	}
	if slices.Contains(last.AssignedTeamIDs, "ach-user-c@d.com") {
		t.Errorf("AssignedTeamIDs = %v, must NOT contain c@d.com's nonexistent shell", last.AssignedTeamIDs)
	}
}

// TestReconcileAccessGroup_MemberRemovalDetaches asserts that once a user is
// dropped from an authorized team's live membership, the next reconcile
// drops their shell from assigned_team_ids (detach == revoke).
func TestReconcileAccessGroup_MemberRemovalDetaches(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	// a@b.com already removed from "run" — only c@d.com (no shell) remains.
	accessGroupFake.SeedTeamMembers("t-run", "run", litellm.TeamMemberRole{UserID: "c@d.com"})
	accessGroupFake.SeedUserShellPresent("a@b.com")

	// Pre-seed an existing access group as if a@b.com's shell was attached by
	// a prior reconcile (before their removal from "run").
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   "ag-uuid-test-env-usershell-detach",
		AccessGroupName: "ach-test-env-usershell-detach",
		AssignedTeamIDs: []string{"t-run", "ach-user-a@b.com"},
	})
	accessGroupFake.SeedTeamMirror("t-run", "ag-uuid-test-env-usershell-detach")
	accessGroupFake.SeedTeamMirror("ach-user-a@b.com", "ag-uuid-test-env-usershell-detach")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-usershell-detach",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"run"},
			Runtime:         emptyRuntimeBlock(),
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

	last := accessGroupFake.LastUpdate("ach-test-env-usershell-detach")
	if slices.Contains(last.AssignedTeamIDs, "ach-user-a@b.com") {
		t.Errorf("AssignedTeamIDs = %v, want a@b.com's shell detached (member removed)", last.AssignedTeamIDs)
	}
}

// TestReconcileAccessGroup_TeamInfoError_ResolveFailed asserts a GetTeamInfo
// transport error fails the reconcile loud (AccessGroupSynced=False/
// ResolveFailed) rather than PUTting a union that would drop entitled shells.
func TestReconcileAccessGroup_TeamInfoError_ResolveFailed(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeamMembers("t-run", "run", litellm.TeamMemberRole{UserID: "a@b.com"})
	accessGroupFake.InjectTeamInfoErr("t-run", errors.New("fake: litellm unreachable"))

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-usershell-resolvefail",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"run"},
			Runtime:         emptyRuntimeBlock(),
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
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "ResolveFailed"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/ResolveFailed, conditions = %+v", got.Status.Conditions)
	}
	if accessGroupFake.CreateCallsFor("ach-test-env-usershell-resolvefail") > 0 {
		t.Error("access group was created despite GetTeamInfo error — must never PUT a union missing shells")
	}
}

// SPDX-License-Identifier: Apache-2.0

// Plan TODO §7 — Environment AccessGroupSynced reconciler tests
// (rewritten for issue #17: /v1/access_group surface).
//
// Asserts the closed-set AccessGroupSynced conditions emitted by the
// desired-state reconciler:
//   - True/Synced
//   - False/UnresolvedReferences  (one or more env-spec names had no upstream ID)
//   - False/AccessGroupCreateFailed
//   - False/AccessGroupUpdateFailed
//   - False/ResolveFailed

package ach

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
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

// emptyRuntimeBlock returns a RuntimeBlock with all three slices
// non-nil and empty. Required because the new reconciler always
// resolves env.Spec.Runtime.{Models,MCPServers,A2AAgents}.
func emptyRuntimeBlock() achv1alpha1.RuntimeBlock {
	return achv1alpha1.RuntimeBlock{
		Models:     []string{},
		MCPServers: []string{},
		A2AAgents:  []string{},
	}
}

// TestAccessGroupSynced_True_WhenCreateSucceeds is the §7 happy path
// (issue #17: replaces the legacy bind-loop assertion with a
// LastCreate.AssignedTeamIDs assertion).
func TestAccessGroupSynced_True_WhenCreateSucceeds(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-happy",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
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
	if got := accessGroupFake.CreateCallsFor("test-env-ag-happy"); got < 1 {
		t.Errorf("create call count = %d; want >= 1", got)
	}
	last := accessGroupFake.LastCreate("test-env-ag-happy")
	if len(last.AssignedTeamIDs) != 1 || last.AssignedTeamIDs[0] != "t-uuid-default" {
		t.Errorf("LastCreate.AssignedTeamIDs = %v; want [t-uuid-default]", last.AssignedTeamIDs)
	}
}

// TestAccessGroupSynced_True_OnEmptyLiteLLMLists is the regression guard
// for the prod incident where Environment `platform` wedged at
// AccessGroupSynced=False/ResolveFailed "LiteLLM ListMCPServers failed:
// litellm: not found". Root cause: a LiteLLM with zero registered MCP
// servers / A2A agents makes ListMCPServers/ListA2AAgents return
// litellm.ErrNotFound (empty-list REL-05 contract); reconcileAccessGroup
// must translate ErrNotFound → empty, NOT treat it as a resolve failure.
// The env here references nothing, so an empty upstream is a valid empty
// closed-set and the access group must still sync.
//
// The fake's ListMCPServers/ListA2AAgents return ErrNotFound on empty
// (mirroring the real RESTClient) — so this test fails if the
// translation is ever removed from the controller.
func TestAccessGroupSynced_True_OnEmptyLiteLLMLists(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	// Deliberately seed NO mcp / NO agent → fake returns ErrNotFound.

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-emptylists",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
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
		t.Fatalf("expected True/Synced on empty LiteLLM lists (ErrNotFound must be tolerated), conditions = %+v", got.Status.Conditions)
	}

	last := accessGroupFake.LastCreate("test-env-ag-emptylists")
	if len(last.AccessMCPServerIDs) != 0 || len(last.AccessAgentIDs) != 0 {
		t.Errorf("create body should carry empty mcp/agent IDs; got mcp=%v agent=%v", last.AccessMCPServerIDs, last.AccessAgentIDs)
	}
}

// TestAccessGroupSynced_False_OnUnresolvedTeam asserts the
// UnresolvedReferences branch: a team in spec.authorizedTeams that
// does not resolve via ListTeamsByAlias flips AccessGroupSynced to
// False with the distinct UnresolvedReferences reason (issue #17).
func TestAccessGroupSynced_False_OnUnresolvedTeam(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("team-ok", "t-uuid-ok")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-unresolved",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"team-ok", "team-missing"},
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
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "UnresolvedReferences"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/UnresolvedReferences, conditions = %+v", got.Status.Conditions)
	}
}

// TestAccessGroupSynced_Idempotent_NoExtraUpdateOnRereconcile asserts
// that once the access group is created in upstream state, a
// re-reconcile (triggered by annotation touch) does NOT re-issue
// either POST or PUT. With desired-state in sync, the reconciler
// short-circuits at the no-drift check.
func TestAccessGroupSynced_Idempotent_NoExtraUpdateOnRereconcile(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-idemp",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
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
		t.Fatalf("first reconcile did not flip AccessGroupSynced=True")
	}

	firstCreateCount := accessGroupFake.CreateCallsFor("test-env-ag-idemp")
	firstUpdateCount := accessGroupFake.UpdateCallsFor("test-env-ag-idemp")

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
	if grew := accessGroupFake.CreateCallsFor("test-env-ag-idemp"); grew != firstCreateCount {
		t.Errorf("create call count = %d after re-reconcile; want unchanged (%d) — idempotency violated", grew, firstCreateCount)
	}
	if grew := accessGroupFake.UpdateCallsFor("test-env-ag-idemp"); grew != firstUpdateCount {
		t.Errorf("update call count = %d after re-reconcile; want unchanged (%d) — drift-detection false positive", grew, firstUpdateCount)
	}
}

// TestAccessGroupSynced_DriftCorrected asserts that when the existing
// access group has bindings that diverge from spec, the reconciler
// emits PUT /v1/access_group/{id} to converge.
func TestAccessGroupSynced_DriftCorrected(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("current-team", "t-uuid-current")
	// Pre-seed a stored AG with a stale orphan team that's NOT in spec.
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   "ag-uuid-test-env-ag-drift",
		AccessGroupName: "test-env-ag-drift",
		AssignedTeamIDs: []string{"t-uuid-orphan"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-drift",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"current-team"},
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
		t.Fatalf("drift-correction did NOT reach True/Synced")
	}
	if got := accessGroupFake.UpdateCallsFor("test-env-ag-drift"); got < 1 {
		t.Errorf("update call count = %d; want >= 1 (PUT to correct drift)", got)
	}
}

// TestAccessGroupSynced_ClearsEmptiedRuntimeDimensions is the regression
// guard for the omitempty-drops-the-clear bug. An existing access group
// has stale mcp + agent bindings; the Environment's spec.runtime empties
// both. The reconciler must PUT `[]` for those dimensions so LiteLLM
// clears them — proved by the condition message (built from the PUT
// RESPONSE's len()) ending in "0 mcp, 0 agent".
//
// Pre-fix this is RED two ways: (1) mapResolve returns nil for empty
// input so the controller sends nil, and (2) omitempty drops the nil/[]
// on the wire — the fake's JSON round-trip then sees the field absent,
// its `!= nil` guard SKIPS the clear, the stale "1 mcp" survives, and the
// message reads "1 mcp, 1 agent". Post-fix the controller sends a non-nil
// `[]` that survives marshaling → fake clears → "0 mcp, 0 agent".
func TestAccessGroupSynced_ClearsEmptiedRuntimeDimensions(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	// Pre-seed a stored AG carrying stale mcp + agent bindings that the
	// spec below no longer references; team matches spec (no team drift).
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      "ag-uuid-test-env-ag-clear",
		AccessGroupName:    "test-env-ag-clear",
		AccessMCPServerIDs: []string{"mcp-stale"},
		AccessAgentIDs:     []string{"agent-stale"},
		AssignedTeamIDs:    []string{"t-uuid-default"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-clear",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         emptyRuntimeBlock(), // mcp + a2a emptied
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	var final achv1alpha1.Environment
	if !Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &final); err != nil {
			return false
		}
		c := agCondition(&final)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == "Synced" &&
			strings.Contains(c.Message, "0 mcp, 0 agent")
	}, 15*time.Second, 250*time.Millisecond) {
		c := agCondition(&final)
		msg := "<nil>"
		if c != nil {
			msg = c.Message
		}
		t.Fatalf("expected True/Synced with cleared mcp/agent (message containing %q); got message=%q, conditions=%+v",
			"0 mcp, 0 agent", msg, final.Status.Conditions)
	}
	if got := accessGroupFake.UpdateCallsFor("test-env-ag-clear"); got < 1 {
		t.Errorf("update call count = %d; want >= 1 (PUT to clear emptied dimensions)", got)
	}
}

// TestAccessGroupSynced_False_OnCreateFailure asserts the
// AccessGroupCreateFailed reason path.
func TestAccessGroupSynced_False_OnCreateFailure(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.InjectCreateErr("test-env-ag-createfail", errors.New("fake: create blew up"))

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-ag-createfail",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
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
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "AccessGroupCreateFailed"
	}, 15*time.Second, 250*time.Millisecond) {
		var got achv1alpha1.Environment
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("expected False/AccessGroupCreateFailed, conditions = %+v", got.Status.Conditions)
	}
}

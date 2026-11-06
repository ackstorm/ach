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
	"slices"
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
	if got := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-happy"); got < 1 {
		t.Errorf("create call count = %d; want >= 1", got)
	}
	// AssignedTeamIDs must also carry the per-Environment deny-all shell
	// team (ach-env-test-env-ag-happy): reconcileAccessGroup joins it into
	// the SAME write as the authorized teams (Task 4).
	last := accessGroupFake.LastCreate("ach-env-test-env-ag-happy")
	wantTeams := []string{"t-uuid-default", "id-ach-env-test-env-ag-happy"}
	if !slices.Equal(last.AssignedTeamIDs, wantTeams) {
		t.Errorf("LastCreate.AssignedTeamIDs = %v; want %v", last.AssignedTeamIDs, wantTeams)
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

	last := accessGroupFake.LastCreate("ach-env-test-env-ag-emptylists")
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

	firstCreateCount := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-idemp")
	firstUpdateCount := accessGroupFake.UpdateCallsFor("ach-env-test-env-ag-idemp")

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
	if grew := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-idemp"); grew != firstCreateCount {
		t.Errorf("create call count = %d after re-reconcile; want unchanged (%d) — idempotency violated", grew, firstCreateCount)
	}
	if grew := accessGroupFake.UpdateCallsFor("ach-env-test-env-ag-idemp"); grew != firstUpdateCount {
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
		AccessGroupName: "ach-env-test-env-ag-drift",
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
	if got := accessGroupFake.UpdateCallsFor("ach-env-test-env-ag-drift"); got < 1 {
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
		AccessGroupName:    "ach-env-test-env-ag-clear",
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
	if got := accessGroupFake.UpdateCallsFor("ach-env-test-env-ag-clear"); got < 1 {
		t.Errorf("update call count = %d; want >= 1 (PUT to clear emptied dimensions)", got)
	}
}

// TestAccessGroupSynced_False_OnCreateFailure asserts the
// AccessGroupCreateFailed reason path.
func TestAccessGroupSynced_False_OnCreateFailure(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.InjectCreateErr("ach-env-test-env-ag-createfail", errors.New("fake: create blew up"))

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

// TestMirrorRepairStep is the pure-logic table for the repair arithmetic:
// intermediate = (desired \ missing) ∪ stale. Empty-list vs nil matters —
// an all-drifted environment must yield a NON-nil empty intermediate.
func TestMirrorRepairStep(t *testing.T) {
	const G = "ag-1"
	for _, tc := range []struct {
		name    string
		teams   []string
		mirror  map[string][]string
		want    []string
		wantNed bool
	}{
		{
			name:   "healthy — no repair",
			teams:  []string{"t1", "t2"},
			mirror: map[string][]string{"t1": {G}, "t2": {G}},
			want:   []string{"t1", "t2"}, wantNed: false,
		},
		{
			name:   "one missing — healthy peer stays in the intermediate",
			teams:  []string{"t1", "t2"},
			mirror: map[string][]string{"t1": {}, "t2": {G}},
			want:   []string{"t2"}, wantNed: true,
		},
		{
			name:   "team unknown to LiteLLM counts as missing",
			teams:  []string{"t1"},
			mirror: map[string][]string{},
			want:   []string{}, wantNed: true,
		},
		{
			name:   "stale foreign team is pulled IN so it can be pushed out",
			teams:  []string{"t1"},
			mirror: map[string][]string{"t1": {G}, "t9": {G}},
			want:   []string{"t1", "t9"}, wantNed: true,
		},
		{
			name:   "foreign team with an unrelated group is ignored",
			teams:  []string{"t1"},
			mirror: map[string][]string{"t1": {G}, "t9": {"ag-other"}},
			want:   []string{"t1"}, wantNed: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, needed := mirrorRepairStep(G, tc.teams, tc.mirror)
			if needed != tc.wantNed {
				t.Errorf("needed = %v; want %v", needed, tc.wantNed)
			}
			if !sameSet(got, tc.want) {
				t.Errorf("intermediate = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestAccessGroupSynced_MirrorMissing — the prod bug, end to end. The group
// side agrees with desired state (assigned_team_ids == [t-run, t-dream]) but
// t-run's TEAM-side mirror is empty, and that is the side LiteLLM enforces
// on. Pre-fix this reconciled to True/Synced with ZERO writes and t-run
// resolved no tools.
//
// Asserts all three contract points:
//
//	(a) the sequence converges in ONE reconcile pass,
//	(b) the next pass writes nothing,
//	(c) the co-authorized HEALTHY team's mirror is never empty at any
//	    intermediate step — which is what rules out the PUT []/PUT [D]
//	    repair that would blink it.
func TestAccessGroupSynced_MirrorMissing(t *testing.T) {
	ctx := context.Background()
	const gid = "ag-uuid-test-env-mirror-missing"
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("run", "t-run")
	accessGroupFake.SeedTeam("dream", "t-dream")
	accessGroupFake.SeedTeamMirror("t-run")        // ← drifted: empty mirror
	accessGroupFake.SeedTeamMirror("t-dream", gid) // ← healthy peer
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: "ach-env-test-env-mirror-missing",
		AssignedTeamIDs: []string{"t-run", "t-dream"}, // group side looks correct
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-mirror-missing",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"run", "dream"},
			Runtime:         emptyRuntimeBlock(),
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// (a) converges — condition True AND the mirror actually repaired.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == "Synced"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("mirror repair did NOT reach True/Synced")
	}
	if m := accessGroupFake.Mirror("t-run"); !slices.Contains(m, gid) {
		t.Fatalf("t-run mirror = %v; want it to contain %s", m, gid)
	}
	if n := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-missing"); n != 2 {
		t.Errorf("update calls = %d; want exactly 2 (intermediate + final)", n)
	}
	// The final PUT also carries the deny-all shell team
	// (ach-env-test-env-mirror-missing) — it joins assigned_team_ids in the
	// same write as the authorized teams (Task 4).
	last := accessGroupFake.LastUpdate("ach-env-test-env-mirror-missing")
	wantTeams := []string{"t-run", "t-dream", "id-ach-env-test-env-mirror-missing"}
	if !sameSet(last.AssignedTeamIDs, wantTeams) {
		t.Errorf("final PUT assigned_team_ids = %v; want %v", last.AssignedTeamIDs, wantTeams)
	}

	// (c) the healthy peer never blinked.
	if accessGroupFake.MirrorEverEmpty("t-dream") {
		t.Error("healthy team t-dream had an empty mirror during the repair — " +
			"the intermediate PUT must exclude only the DRIFTED teams, not clear the list")
	}

	// (b) a second pass is a no-op.
	writes := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-missing")
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
	if grew := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-missing"); grew != writes {
		t.Errorf("update calls = %d after re-reconcile; want unchanged (%d) — repaired state must be a no-op",
			grew, writes)
	}
}

// TestAccessGroupSynced_MirrorStale — the symmetric direction: t-ghost is
// NOT in authorizedTeams but still carries this group in its
// access_group_ids, so it keeps grants ACH never declared. Stripping it
// needs a LEAVE delta, which needs t-ghost to be IN assigned_team_ids
// first — hence the intermediate PUT includes it and the final one drops
// it. The group side alone cannot see this (assigned_team_ids is already
// correct) and neither can an alias-filtered team lookup — which is why
// the resolver lists ALL teams.
func TestAccessGroupSynced_MirrorStale(t *testing.T) {
	ctx := context.Background()
	const gid = "ag-uuid-test-env-mirror-stale"
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("run", "t-run")
	accessGroupFake.SeedTeamMirror("t-run", gid)
	accessGroupFake.SeedTeam("ghost", "t-ghost")
	accessGroupFake.SeedTeamMirror("t-ghost", gid) // not authorized ← stale
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: "ach-env-test-env-mirror-stale",
		AssignedTeamIDs: []string{"t-run"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-mirror-stale",
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
		return len(accessGroupFake.Mirror("t-ghost")) == 0
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("stale mirror on t-ghost not stripped: %v", accessGroupFake.Mirror("t-ghost"))
	}
	if m := accessGroupFake.Mirror("t-run"); !slices.Contains(m, gid) {
		t.Errorf("authorized team t-run lost its mirror: %v", m)
	}
	if accessGroupFake.MirrorEverEmpty("t-run") {
		t.Error("authorized team t-run blinked during the stale-mirror repair")
	}
	if n := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-stale"); n != 2 {
		t.Errorf("update calls = %d; want exactly 2", n)
	}
}

// TestAccessGroupSynced_MirrorUnconverged — the safety net. The fake's
// mirror is frozen (a stand-in for a future LiteLLM that drops the
// delta-driven write), so the repair sequence cannot converge. The
// reconciler must fail LOUDLY and STOP, not settle into two futile writes
// every resync forever.
func TestAccessGroupSynced_MirrorUnconverged(t *testing.T) {
	ctx := context.Background()
	const gid = "ag-uuid-test-env-mirror-stuck"
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("run", "t-run")
	accessGroupFake.SeedTeamMirror("t-run") // drifted and it will stay that way
	accessGroupFake.FreezeMirror()
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: "ach-env-test-env-mirror-stuck",
		AssignedTeamIDs: []string{"t-run"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-mirror-stuck",
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
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "MirrorUnconverged"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("non-converging mirror did NOT surface AccessGroupSynced=False/MirrorUnconverged")
	}

	// And it must stop writing: further reconciles are suppressed at this
	// generation, so the write count stays put.
	writes := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-stuck")
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
	if grew := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-stuck"); grew != writes {
		t.Errorf("update calls = %d after re-reconcile; want unchanged (%d) — "+
			"a non-converging mirror must not loop on writes", grew, writes)
	}
}

// TestAccessGroupSynced_MirrorHealthy — the no-op guard. Both sides agree,
// so the reconciler must stay read-only. Without this the mirror check
// could "repair" a healthy binding on every 5-minute pass forever.
func TestAccessGroupSynced_MirrorHealthy(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("run", "t-run")
	accessGroupFake.SeedTeamMirror("t-run", "ag-uuid-test-env-mirror-ok")
	// The deny-all shell team must ALSO be pre-seeded as already-created and
	// healthy (Task 4) — otherwise ensureShellTeam creates it fresh on this
	// pass, its brand-new empty mirror legitimately triggers one repair
	// sequence, and this test's whole point (a fully healthy Environment
	// writes nothing) no longer holds.
	shellID := accessGroupFake.SeedShellTeam("test-env-mirror-ok")
	accessGroupFake.SeedTeamMirror(shellID, "ag-uuid-test-env-mirror-ok")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   "ag-uuid-test-env-mirror-ok",
		AccessGroupName: "ach-env-test-env-mirror-ok",
		AssignedTeamIDs: []string{"t-run", shellID},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-mirror-ok",
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
		t.Fatalf("healthy mirror did NOT reach True/Synced")
	}
	if n := accessGroupFake.UpdateCallsFor("ach-env-test-env-mirror-ok"); n != 0 {
		t.Errorf("healthy mirror must not write: update calls = %d; want 0", n)
	}
}

// A pre-rename group named "<env>" (no ach- prefix) is adopted by id and
// renamed to "ach-env-<env>" in place — id and assigned_team_ids preserved, no
// second group created.
func TestAccessGroupSynced_MigratesLegacyName(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	// Legacy group already exists under the bare env name with the SAME
	// bindings the reconciler will compute, so ONLY the name drifts.
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      "ag-legacy-uuid",
		AccessGroupName:    "test-env-ag-migrate",
		AccessModelNames:   []string{},
		AccessMCPServerIDs: []string{},
		AccessAgentIDs:     []string{},
		AssignedTeamIDs:    []string{"t-uuid-default", "id-ach-env-test-env-ag-migrate"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env-ag-migrate", Namespace: WatchNamespace},
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
		g, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-env-test-env-ag-migrate")
		return g != nil && g.AccessGroupID == "ag-legacy-uuid"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("legacy group was not renamed in place to ach-env-test-env-ag-migrate")
	}
	// No second group under the bare name, and no CREATE was issued.
	if g, _ := accessGroupFake.GetAccessGroupByName(ctx, "test-env-ag-migrate"); g != nil {
		t.Error("bare-name group should be gone after in-place rename")
	}
	if n := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-migrate"); n != 0 {
		t.Errorf("expected 0 CREATE calls (rename in place); got %d", n)
	}
}

// TestAccessGroupSynced_MigratesV0619Name covers the middle generation: a group
// already named ach-<env> (minted by v0.6.19) is adopted by id and renamed in
// place to the canonical ach-env-<env>, with no CREATE and no second group.
func TestAccessGroupSynced_MigratesV0619Name(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      "ag-midgen-uuid",
		AccessGroupName:    "ach-test-env-ag-midgen",
		AccessModelNames:   []string{},
		AccessMCPServerIDs: []string{},
		AccessAgentIDs:     []string{},
		AssignedTeamIDs:    []string{"t-uuid-default", "id-ach-env-test-env-ag-midgen"},
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env-ag-midgen", Namespace: WatchNamespace},
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
		g, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-env-test-env-ag-midgen")
		return g != nil && g.AccessGroupID == "ag-midgen-uuid"
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatal("v0.6.19 group was not renamed in place to ach-env-test-env-ag-midgen")
	}
	if g, _ := accessGroupFake.GetAccessGroupByName(ctx, "ach-test-env-ag-midgen"); g != nil {
		t.Error("v0.6.19-name group should be gone after in-place rename")
	}
	if n := accessGroupFake.CreateCallsFor("ach-env-test-env-ag-midgen"); n != 0 {
		t.Errorf("expected 0 CREATE calls (rename in place); got %d", n)
	}
}

// createEnvAwaitingAG creates an Environment and waits for AccessGroupSynced
// to report status + reason, returning that condition.
func createEnvAwaitingAG(
	t *testing.T,
	name string,
	teams []string,
	runtime achv1alpha1.RuntimeBlock,
	status metav1.ConditionStatus,
	reason string,
) *metav1.Condition {
	t.Helper()
	ctx := context.Background()
	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: teams,
			Runtime:         runtime,
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	var got achv1alpha1.Environment
	if !Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		c := agCondition(&got)
		return c != nil && c.Status == status && c.Reason == reason
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("expected %s/%s, conditions = %+v", status, reason, got.Status.Conditions)
	}
	return agCondition(&got)
}

// storedGroup reads the fake's access group back.
func storedGroup(t *testing.T, name string) *litellm.AccessGroupResponse {
	t.Helper()
	g, err := accessGroupFake.GetAccessGroupByName(context.Background(), name)
	if err != nil || g == nil {
		t.Fatalf("stored group %s: %v, %v", name, g, err)
	}
	return g
}

// A team taken out of the spec leaves the group while another reference is
// unresolved, its members' user shells go with it, and the shells still
// entitled stay. An unresolved team goes too: LiteLLM answered and does not
// know it.
func TestAccessGroupUnresolved_RemovedTeamLeaves(t *testing.T) {
	const (
		env   = "test-env-unres-removed"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeamMembers("t-kept", "kept", litellm.TeamMemberRole{UserID: "kept@x.com"})
	accessGroupFake.SeedTeamMembers("t-removed", "removed", litellm.TeamMemberRole{UserID: "gone@x.com"})
	accessGroupFake.SeedUserShellPresent("kept@x.com")
	accessGroupFake.SeedUserShellPresent("gone@x.com")
	shell := accessGroupFake.SeedShellTeam(env)
	assigned := []string{"t-kept", "t-removed", "t-unknown", shell, "ach-user-kept@x.com", "ach-user-gone@x.com"}
	for _, id := range assigned {
		accessGroupFake.SeedTeamMirror(id, gid)
	}
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: group,
		AssignedTeamIDs: assigned,
	})

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-unknown"}
	c := createEnvAwaitingAG(t, env, []string{"kept", "unknown"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	want := "unresolved: mcpServers=[mcp-unknown] a2aAgents=[] authorizedTeams=[unknown] guardrails=[]"
	if c.Message != want {
		t.Errorf("message = %q; want %q", c.Message, want)
	}
	wantTeams := []string{"t-kept", shell, "ach-user-kept@x.com"}
	if got := storedGroup(t, group).AssignedTeamIDs; !sameSet(got, wantTeams) {
		t.Errorf("assigned_team_ids = %v; want %v", got, wantTeams)
	}
	if m := accessGroupFake.Mirror("t-removed"); slices.Contains(m, gid) {
		t.Errorf("t-removed mirror = %v; want the group gone", m)
	}
}

// What the spec dropped goes, what the spec added waits, even when it resolves.
func TestAccessGroupUnresolved_GainsNothing(t *testing.T) {
	const (
		env   = "test-env-unres-gains"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedTeam("added", "t-added")
	accessGroupFake.SeedMCP("mcp-kept", "m-kept")
	accessGroupFake.SeedMCP("mcp-added", "m-added")
	accessGroupFake.SeedMCP("mcp-dropped", "m-dropped")
	accessGroupFake.SeedAgent("agent-kept", "a-kept")
	accessGroupFake.SeedAgent("agent-added", "a-added")
	accessGroupFake.SeedAgent("agent-dropped", "a-dropped")
	shell := accessGroupFake.SeedShellTeam(env)
	accessGroupFake.SeedTeamMirror("t-kept", gid)
	accessGroupFake.SeedTeamMirror(shell, gid)
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      gid,
		AccessGroupName:    group,
		AccessModelNames:   []string{"model-kept", "model-dropped"},
		AccessMCPServerIDs: []string{"m-kept", "m-dropped"},
		AccessAgentIDs:     []string{"a-kept", "a-dropped"},
		AssignedTeamIDs:    []string{"t-kept", shell},
	})

	rt := achv1alpha1.RuntimeBlock{
		Models:     []string{"model-kept", "model-added"},
		MCPServers: []string{"mcp-kept", "mcp-added", "mcp-unknown"},
		A2AAgents:  []string{"agent-kept", "agent-added"},
	}
	createEnvAwaitingAG(t, env, []string{"kept", "added"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	g := storedGroup(t, group)
	if !sameSet(g.AccessModelNames, []string{"model-kept"}) {
		t.Errorf("models = %v", g.AccessModelNames)
	}
	if !sameSet(g.AccessMCPServerIDs, []string{"m-kept"}) {
		t.Errorf("mcp = %v", g.AccessMCPServerIDs)
	}
	if !sameSet(g.AccessAgentIDs, []string{"a-kept"}) {
		t.Errorf("agents = %v", g.AccessAgentIDs)
	}
	if !sameSet(g.AssignedTeamIDs, []string{"t-kept", shell}) {
		t.Errorf("assigned_team_ids = %v", g.AssignedTeamIDs)
	}
	if m := accessGroupFake.Mirror("t-added"); len(m) != 0 {
		t.Errorf("t-added mirror = %v; want empty", m)
	}
}

// LiteLLM answers 200 [] while its registry loads: an empty list removes no
// MCP or agent grant, even though every name is unresolved.
func TestAccessGroupUnresolved_EmptyLiveListsKeepGrants(t *testing.T) {
	const (
		env   = "test-env-unres-emptylive"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	shell := accessGroupFake.SeedShellTeam(env)
	accessGroupFake.SeedTeamMirror("t-kept", gid)
	accessGroupFake.SeedTeamMirror(shell, gid)
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      gid,
		AccessGroupName:    group,
		AccessMCPServerIDs: []string{"m-wanted", "m-other"},
		AccessAgentIDs:     []string{"a-wanted", "a-other"},
		AssignedTeamIDs:    []string{"t-kept", shell},
	})

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-wanted"}
	rt.A2AAgents = []string{"agent-wanted"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if n := accessGroupFake.UpdateCallsFor(group); n != 0 {
		t.Errorf("update calls = %d; want 0", n)
	}
	g := storedGroup(t, group)
	if !sameSet(g.AccessMCPServerIDs, []string{"m-wanted", "m-other"}) {
		t.Errorf("mcp = %v", g.AccessMCPServerIDs)
	}
	if !sameSet(g.AccessAgentIDs, []string{"a-wanted", "a-other"}) {
		t.Errorf("agents = %v", g.AccessAgentIDs)
	}
}

// A partial list keeps every id it does not show.
func TestAccessGroupUnresolved_PartialLiveListKeepsUnseen(t *testing.T) {
	const (
		env   = "test-env-unres-partial"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedMCP("mcp-seen", "m-seen")
	accessGroupFake.SeedAgent("agent-seen", "a-seen")
	shell := accessGroupFake.SeedShellTeam(env)
	accessGroupFake.SeedTeamMirror("t-kept", gid)
	accessGroupFake.SeedTeamMirror(shell, gid)
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      gid,
		AccessGroupName:    group,
		AccessMCPServerIDs: []string{"m-seen", "m-unseen"},
		AccessAgentIDs:     []string{"a-seen", "a-unseen"},
		AssignedTeamIDs:    []string{"t-kept", shell},
	})

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-seen", "mcp-unseen"}
	rt.A2AAgents = []string{"agent-seen", "agent-unseen"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if n := accessGroupFake.UpdateCallsFor(group); n != 0 {
		t.Errorf("update calls = %d; want 0", n)
	}
}

// An id LiteLLM lists and the spec no longer wants leaves, in the same PUT that
// keeps an id LiteLLM does not list.
func TestAccessGroupUnresolved_ListedSpecRemovalLeaves(t *testing.T) {
	const (
		env   = "test-env-unres-listed"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedMCP("mcp-removed", "m-removed")
	accessGroupFake.SeedAgent("agent-removed", "a-removed")
	shell := accessGroupFake.SeedShellTeam(env)
	accessGroupFake.SeedTeamMirror("t-kept", gid)
	accessGroupFake.SeedTeamMirror(shell, gid)
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      gid,
		AccessGroupName:    group,
		AccessMCPServerIDs: []string{"m-removed", "m-unseen"},
		AccessAgentIDs:     []string{"a-removed", "a-unseen"},
		AssignedTeamIDs:    []string{"t-kept", shell},
	})

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	g := storedGroup(t, group)
	if !sameSet(g.AccessMCPServerIDs, []string{"m-unseen"}) {
		t.Errorf("mcp = %v", g.AccessMCPServerIDs)
	}
	if !sameSet(g.AccessAgentIDs, []string{"a-unseen"}) {
		t.Errorf("agents = %v", g.AccessAgentIDs)
	}
}

// Clearing a list needs `[]` on the wire: absent keeps it.
func TestAccessGroupUnresolved_ClearsEmptiedList(t *testing.T) {
	const (
		env   = "test-env-unres-clear"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedMCP("mcp-dropped", "m-dropped")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:      gid,
		AccessGroupName:    group,
		AccessMCPServerIDs: []string{"m-dropped"},
		AssignedTeamIDs:    []string{"t-kept"},
	})

	rt := emptyRuntimeBlock()
	rt.A2AAgents = []string{"agent-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if got := storedGroup(t, group).AccessMCPServerIDs; len(got) != 0 {
		t.Errorf("mcp = %v; want cleared", got)
	}
}

// With nothing to take away the group is left alone, whether it matches the
// spec or the spec only wants to add.
func TestAccessGroupUnresolved_NoPutWhenNothingToRemove(t *testing.T) {
	const (
		env   = "test-env-unres-noput"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedTeam("added", "t-added")
	shell := accessGroupFake.SeedShellTeam(env)
	accessGroupFake.SeedTeamMirror("t-kept", gid)
	accessGroupFake.SeedTeamMirror(shell, gid)
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: group,
		AssignedTeamIDs: []string{"t-kept", shell},
	})

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept", "added"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if n := accessGroupFake.UpdateCallsFor(group); n != 0 {
		t.Errorf("update calls = %d; want 0", n)
	}
}

// A group that does not exist stays that way, and the shell team, which
// carries the spec's guardrails, is not written either.
func TestAccessGroupUnresolved_NoGroupNoWrites(t *testing.T) {
	const env = "test-env-unres-nogroup"
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")

	rt := emptyRuntimeBlock()
	rt.Guardrails = []string{"guardrail-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if n := accessGroupFake.CreateCallsFor("ach-env-" + env); n != 0 {
		t.Errorf("create calls = %d; want 0", n)
	}
	accessGroupFake.mu.Lock()
	defer accessGroupFake.mu.Unlock()
	if n := accessGroupFake.teamCreateCalls["ach-env-"+env]; n != 0 {
		t.Errorf("shell team create calls = %d; want 0", n)
	}
}

// An unresolved guardrail must not reach the shell team, and removals still
// land.
func TestAccessGroupUnresolved_GuardrailLeavesShellAlone(t *testing.T) {
	const (
		env   = "test-env-unres-guardrail"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedTeam("removed", "t-removed")
	shell := accessGroupFake.SeedShellTeam(env)
	for _, id := range []string{"t-kept", "t-removed", shell} {
		accessGroupFake.SeedTeamMirror(id, gid)
	}
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: group,
		AssignedTeamIDs: []string{"t-kept", "t-removed", shell},
	})

	rt := emptyRuntimeBlock()
	rt.Guardrails = []string{"guardrail-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if got := storedGroup(t, group).AssignedTeamIDs; !sameSet(got, []string{"t-kept", shell}) {
		t.Errorf("assigned_team_ids = %v", got)
	}
	accessGroupFake.mu.Lock()
	defer accessGroupFake.mu.Unlock()
	if n := accessGroupFake.teamUpdateCalls[shell]; n != 0 {
		t.Errorf("shell team update calls = %d; want 0", n)
	}
}

// A failed LiteLLM read is not an answer: nothing is removed on it.
func TestAccessGroupUnresolved_FailedReadNoPut(t *testing.T) {
	for name, inject := range map[string]func(){
		"team info": func() { accessGroupFake.InjectTeamInfoErr("t-kept", errors.New("litellm down")) },
		"group":     func() { accessGroupFake.InjectListErr(errors.New("litellm down")) },
	} {
		t.Run(name, func(t *testing.T) {
			env := "test-env-unres-readfail-" + strings.ReplaceAll(name, " ", "")
			group := "ach-env-" + env
			accessGroupFake.Reset()
			accessGroupFake.SeedTeamMembers("t-kept", "kept")
			accessGroupFake.SeedTeam("removed", "t-removed")
			shell := accessGroupFake.SeedShellTeam(env)
			accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
				AccessGroupID:   "ag-uuid-" + group,
				AccessGroupName: group,
				AssignedTeamIDs: []string{"t-kept", "t-removed", shell},
			})
			inject()

			rt := emptyRuntimeBlock()
			rt.MCPServers = []string{"mcp-unknown"}
			createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "ResolveFailed")

			if n := accessGroupFake.UpdateCallsFor(group); n != 0 {
				t.Errorf("update calls = %d; want 0", n)
			}
		})
	}
}

// A removal LiteLLM refused is reported as that, not as unresolved names.
func TestAccessGroupUnresolved_PutFailureReported(t *testing.T) {
	const (
		env   = "test-env-unres-putfail"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedTeam("removed", "t-removed")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: group,
		AssignedTeamIDs: []string{"t-kept", "t-removed"},
	})
	accessGroupFake.InjectUpdateErr(group, errors.New("litellm refused"))

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "AccessGroupUpdateFailed")
}

// Removals reach a group still under a legacy name; the rename stays with the
// fully resolved write.
func TestAccessGroupUnresolved_LegacyGroupShrinksUnrenamed(t *testing.T) {
	const (
		env = "test-env-unres-legacy"
		gid = "ag-uuid-legacy-" + env
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedTeam("removed", "t-removed")
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: env,
		AssignedTeamIDs: []string{"t-kept", "t-removed"},
	})

	rt := emptyRuntimeBlock()
	rt.MCPServers = []string{"mcp-unknown"}
	createEnvAwaitingAG(t, env, []string{"kept"}, rt, metav1.ConditionFalse, "UnresolvedReferences")

	if got := storedGroup(t, env).AssignedTeamIDs; !sameSet(got, []string{"t-kept"}) {
		t.Errorf("assigned_team_ids = %v", got)
	}
}

// With every reference resolved the write is the whole desired state: an
// added team joins.
func TestAccessGroupResolved_AddsAndRemoves(t *testing.T) {
	const (
		env   = "test-env-resolved-both"
		group = "ach-env-" + env
		gid   = "ag-uuid-" + group
	)
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("kept", "t-kept")
	accessGroupFake.SeedTeam("added", "t-added")
	accessGroupFake.SeedTeam("removed", "t-removed")
	shell := accessGroupFake.SeedShellTeam(env)
	for _, id := range []string{"t-kept", "t-removed", shell} {
		accessGroupFake.SeedTeamMirror(id, gid)
	}
	accessGroupFake.SeedExisting(&litellm.AccessGroupResponse{
		AccessGroupID:   gid,
		AccessGroupName: group,
		AssignedTeamIDs: []string{"t-kept", "t-removed", shell},
	})

	createEnvAwaitingAG(t, env, []string{"kept", "added"}, emptyRuntimeBlock(), metav1.ConditionTrue, "Synced")

	if got := storedGroup(t, group).AssignedTeamIDs; !sameSet(got, []string{"t-kept", "t-added", shell}) {
		t.Errorf("assigned_team_ids = %v", got)
	}
}

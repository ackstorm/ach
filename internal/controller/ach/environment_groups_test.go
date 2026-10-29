// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/snapshot"
)

func TestExpandGroups(t *testing.T) {
	byTag := map[string][]string{"default": {"a", "b"}, "ro": {"b", "c"}}
	cases := []struct {
		name     string
		groups   []string
		explicit []string
		want     []string
	}{
		{"no groups", nil, nil, nil},
		{"unknown tag grants nothing", []string{"nope"}, nil, nil},
		{"overlapping tags dedup", []string{"ro", "default"}, nil, []string{"a", "b", "c"}},
		{"explicit names excluded", []string{"default"}, []string{"a"}, []string{"b"}},
	}
	for _, tc := range cases {
		if got := expandGroups(tc.groups, byTag, tc.explicit); !slices.Equal(got, tc.want) {
			t.Errorf("%s: expandGroups = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := unionSorted(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("unionSorted(nil, nil) = %#v, want non-nil empty", got)
	}
	if got := unionSorted([]string{"m2", "m1"}, []string{"openai", "m1"}); !slices.Equal(got, []string{"m1", "m2", "openai"}) {
		t.Errorf("unionSorted = %v", got)
	}
}

// TestExpandRuntimeGroups_ColdSnapshotWaits: groups declared against a
// never-refreshed snapshot must not expand to empty (a revocation).
func TestExpandRuntimeGroups_ColdSnapshotWaits(t *testing.T) {
	env := &achv1alpha1.Environment{Spec: achv1alpha1.EnvironmentSpec{
		Runtime: achv1alpha1.RuntimeBlock{MCPServerGroups: []string{"grp"}},
	}}
	if _, _, ok := expandRuntimeGroups(env, snapshot.LiteLLMSnapshot{}); ok {
		t.Fatal("cold snapshot: ok = true, want false")
	}
	warm := snapshot.LiteLLMSnapshot{MCPServerGroups: map[string][]string{"grp": {"s"}}, A2AAgentGroups: map[string][]string{}}
	mcp, _, ok := expandRuntimeGroups(env, warm)
	if !ok || !slices.Equal(mcp, []string{"s"}) || env.Status.ExpandedRuntime == nil {
		t.Fatalf("warm snapshot: mcp=%v ok=%v status=%+v", mcp, ok, env.Status.ExpandedRuntime)
	}
	noGroups := &achv1alpha1.Environment{}
	if _, _, ok := expandRuntimeGroups(noGroups, snapshot.LiteLLMSnapshot{}); !ok {
		t.Fatal("no groups declared: a cold snapshot must not block")
	}
}

// TestRuntimeGroups_ExpandIntoAccessGroup drives the full Reconcile: MCP and
// agent group tags expand (from the snapshot) into access-group ids and
// status.expandedRuntime; model group tags pass through untouched.
func TestRuntimeGroups_ExpandIntoAccessGroup(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.SeedMCP("srv-a", "id-a")
	accessGroupFake.SeedMCP("srv-b", "id-b")
	accessGroupFake.SeedMCP("srv-x", "id-x")
	accessGroupFake.SeedTags("srv-a", "grp")
	accessGroupFake.SeedTags("srv-b", "other")
	accessGroupFake.SeedTags("srv-x", "grp")
	accessGroupFake.SeedAgent("ag-1", "id-ag-1")
	accessGroupFake.SeedTags("ag-1", "agents")
	envSnapshotter.RefreshForTest(ctx)
	t.Cleanup(func() {
		accessGroupFake.Reset()
		envSnapshotter.RefreshForTest(context.Background())
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env-groups", Namespace: WatchNamespace},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime: achv1alpha1.RuntimeBlock{
				MCPServers:      []string{"srv-x"},
				MCPServerGroups: []string{"grp", "unknown-tag"},
				A2AAgentGroups:  []string{"agents"},
				ModelGroups:     []string{"openai"},
			},
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
		return c != nil && c.Status == metav1.ConditionTrue && got.Status.ExpandedRuntime != nil
	}, 15*time.Second, 250*time.Millisecond) {
		t.Fatalf("expected AccessGroupSynced=True with expandedRuntime; status = %+v", got.Status)
	}

	exp := got.Status.ExpandedRuntime
	if !slices.Equal(exp.MCPServers, []string{"srv-a"}) || !slices.Equal(exp.A2AAgents, []string{"ag-1"}) {
		t.Errorf("expandedRuntime = %+v, want mcpServers=[srv-a] (srv-x is explicit) a2aAgents=[ag-1]", exp)
	}
	last := accessGroupFake.LastCreate("ach-env-test-env-groups")
	ids := slices.Sorted(slices.Values(last.AccessMCPServerIDs))
	if !slices.Equal(ids, []string{"id-a", "id-x"}) {
		t.Errorf("AccessMCPServerIDs = %v, want [id-a id-x] (srv-b carries another tag)", last.AccessMCPServerIDs)
	}
	if !slices.Equal(last.AccessAgentIDs, []string{"id-ag-1"}) {
		t.Errorf("AccessAgentIDs = %v, want [id-ag-1]", last.AccessAgentIDs)
	}
	if !slices.Equal(last.AccessModelNames, []string{"openai"}) {
		t.Errorf("AccessModelNames = %v, want the model tag passed through", last.AccessModelNames)
	}
}

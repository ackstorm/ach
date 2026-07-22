//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// E2E coverage for issue #17 — Access Group /v1 migration.
//
// Two specs:
//   - HappyPath: examples/04-environment-demo.yaml reaches
//     AccessGroupSynced=True/Synced within 30s (relies on
//     hydrate_litellm seeding demo-model / demo-mcp / demo-agent).
//   - UnresolvedReferences: examples/04b-environment-unresolved.yaml
//     reaches AccessGroupSynced=False/UnresolvedReferences within 30s.
//
// Both opt out via ACH_SKIP_PHASE4=1, matching the existing phase4
// promotion e2e gate.

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

// TestAccessGroupSynced_Demo_HappyPath asserts the demo fixture flips
// to True/Synced once the operator reconciles. Requires hydrate_litellm
// to have seeded the demo Model / MCP / A2A.
//
// Fix 1 regression gate: every other test on this branch asserts the shell
// team's sentinels against a Go fake or httptest — nothing checks that a REAL
// LiteLLM accepts object_permission on POST /team/new and stores it. This has
// already been confirmed by hand against this cluster (2026-07-21); the
// assertion below codifies that as a PASS-on-first-run regression gate, not a
// discovery test.
func TestAccessGroupSynced_Demo_HappyPath(t *testing.T) {
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skip("§17 e2e (phase4); opt out via ACH_SKIP_PHASE4=1")
	}
	// The "demo" Environment is pre-synced by cluster.sh (reconcile_examples).
	// Tests assert against the live, already-synced cluster — they do NOT apply
	// fixtures (that would mutate shared state other specs depend on).
	if !waitForConditionTriple(t, "environment", "demo", "ach-system",
		"AccessGroupSynced", "True", "Synced", 30*time.Second) {
		dumpAGConditions(t, "demo")
		t.Fatalf("demo Environment did NOT reach AccessGroupSynced=True/Synced within 30s")
	}

	assertDemoShellTeamWiredInLiteLLM(t)
}

// assertDemoShellTeamWiredInLiteLLM reads the REAL in-cluster LiteLLM (not a
// Go fake — see startPortForward / sc5MasterKey, shared with the SC#5 e2e)
// and confirms the "demo" Environment's deny-all shell team
// (litellm.ShellTeamAlias("demo")) carries the exact sentinels ACH writes:
//
//   - models == [ShellTeamDenyAllModel]
//   - object_permission.agents == [ShellTeamDenyAllAgent], mcp_servers empty
//   - access_group_ids contains the demo access group's id — the LiteLLM-side
//     mirror that actually enforces (references/litellm-permission-model.md
//     §2), so its presence is the proof the shell is wired to the
//     Environment's grants and not merely present.
//
// GET /team/info?team_id= is the only LiteLLM read that resolves
// object_permission (references/litellm-permission-model.md §9); GET
// /v2/team/list serialises it as null, so this deliberately does not use
// that endpoint for the sentinel assertions.
func assertDemoShellTeamWiredInLiteLLM(t *testing.T) {
	t.Helper()
	llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)
	client := litellm.NewRESTClient(llURL, sc5MasterKey, logr.Discard())
	ctx := context.Background()

	alias := litellm.ShellTeamAlias("demo")
	teams, err := client.ListTeamsByAlias(ctx, alias)
	if err != nil {
		t.Fatalf("ListTeamsByAlias(%s): %v", alias, err)
	}
	if len(teams) != 1 {
		t.Fatalf("ListTeamsByAlias(%s) = %d teams, want exactly 1: %+v", alias, len(teams), teams)
	}
	teamID := teams[0].TeamID

	info, err := client.GetTeamInfo(ctx, teamID)
	if err != nil {
		t.Fatalf("GetTeamInfo(%s) (team=%s): %v", teamID, alias, err)
	}
	if info == nil {
		t.Fatalf("GetTeamInfo(%s) (team=%s) returned nil", teamID, alias)
	}
	if !slices.Equal(info.Models, []string{litellm.ShellTeamDenyAllModel}) {
		t.Fatalf("shell team %s models = %v, want [%s]", alias, info.Models, litellm.ShellTeamDenyAllModel)
	}
	if info.ObjectPermission == nil {
		t.Fatalf("shell team %s: GET /team/info did not resolve object_permission", alias)
	}
	if !slices.Equal(info.ObjectPermission.Agents, []string{litellm.ShellTeamDenyAllAgent}) {
		t.Fatalf("shell team %s object_permission.agents = %v, want [%s]",
			alias, info.ObjectPermission.Agents, litellm.ShellTeamDenyAllAgent)
	}
	if len(info.ObjectPermission.MCPServers) != 0 {
		t.Fatalf("shell team %s object_permission.mcp_servers = %v, want empty", alias, info.ObjectPermission.MCPServers)
	}

	ag, err := client.GetAccessGroupByName(ctx, "demo")
	if err != nil {
		t.Fatalf("GetAccessGroupByName(demo): %v", err)
	}
	if ag == nil {
		t.Fatal("GetAccessGroupByName(demo) returned nil — access group not found")
	}
	if !slices.Contains(info.AccessGroupIDs, ag.AccessGroupID) {
		t.Fatalf("shell team %s access_group_ids = %v, want to contain the demo access group id %s",
			alias, info.AccessGroupIDs, ag.AccessGroupID)
	}
}

// TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences
// asserts the negative-path fixture flips to False/UnresolvedReferences.
func TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences(t *testing.T) {
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skip("§17 e2e (phase4); opt out via ACH_SKIP_PHASE4=1")
	}
	// The "demo-unresolved" Environment is pre-synced by cluster.sh
	// (reconcile_examples). Assert against the live cluster; do not apply.
	if !waitForConditionTriple(t, "environment", "demo-unresolved", "ach-system",
		"AccessGroupSynced", "False", "UnresolvedReferences", 30*time.Second) {
		dumpAGConditions(t, "demo-unresolved")
		t.Fatalf("demo-unresolved Environment did NOT reach AccessGroupSynced=False/UnresolvedReferences within 30s")
	}
}

// waitForConditionTriple polls until a condition matches (type, status,
// reason) all three, or the deadline expires. Returns true on success.
func waitForConditionTriple(t *testing.T, kind, name, ns, condType, status, reason string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("kubectl", "-n", ns, "get", kind, name, "-o", "json").Output()
		if err == nil {
			var obj struct {
				Status struct {
					Conditions []struct {
						Type, Status, Reason string
					}
				}
			}
			if jerr := json.Unmarshal(out, &obj); jerr == nil {
				for _, c := range obj.Status.Conditions {
					if c.Type == condType && c.Status == status && c.Reason == reason {
						return true
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// dumpAGConditions prints the Environment's status.conditions for debug.
func dumpAGConditions(t *testing.T, name string) {
	t.Helper()
	out, _ := exec.Command("kubectl", "-n", "ach-system", "get",
		"environment", name, "-o", "jsonpath={.status.conditions}").Output()
	t.Logf("environment/%s conditions: %s", name, out)
}

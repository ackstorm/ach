// SPDX-License-Identifier: Apache-2.0

package litellm

import "testing"

func TestShellTeamAlias(t *testing.T) {
	if got := ShellTeamAlias("platform"); got != "ach-env-platform" {
		t.Fatalf("ShellTeamAlias = %q, want ach-env-platform", got)
	}
}

// TestNewShellTeamRequestSentinels is the load-bearing test of this change:
// empty model/agent lists fail OPEN in LiteLLM, so the request must carry
// both sentinels and explicit (non-nil) empty MCP lists.
func TestNewShellTeamRequestSentinels(t *testing.T) {
	req := NewShellTeamRequest("demo")
	if req.TeamAlias != "ach-env-demo" {
		t.Fatalf("TeamAlias = %q", req.TeamAlias)
	}
	if len(req.Models) != 1 || req.Models[0] != ShellTeamDenyAllModel {
		t.Fatalf("Models = %v, want [%s]", req.Models, ShellTeamDenyAllModel)
	}
	op := req.ObjectPermission
	if op == nil {
		t.Fatal("ObjectPermission is nil — the team would allow every agent")
	}
	if len(op.Agents) != 1 || op.Agents[0] != ShellTeamDenyAllAgent {
		t.Fatalf("Agents = %v, want [%s]", op.Agents, ShellTeamDenyAllAgent)
	}
	if op.MCPServers == nil || len(op.MCPServers) != 0 {
		t.Fatalf("MCPServers = %v, want an explicit empty list", op.MCPServers)
	}
	if op.MCPAccessGroups == nil || len(op.MCPAccessGroups) != 0 {
		t.Fatalf("MCPAccessGroups = %v, want an explicit empty list", op.MCPAccessGroups)
	}
	if op.AgentAccessGroups == nil || len(op.AgentAccessGroups) != 0 {
		t.Fatalf("AgentAccessGroups = %v, want an explicit empty list", op.AgentAccessGroups)
	}
}

func TestShellTeamDrifted(t *testing.T) {
	healthy := TeamListEntry{
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
	}
	if ShellTeamDrifted(healthy) {
		t.Fatal("healthy shell reported as drifted")
	}

	cases := map[string]TeamListEntry{
		"models absent on a resolved read (fail-open on every model)": {
			Models:           nil,
			ObjectPermission: ShellTeamPermissions(),
		},
		"models cleared (fail-open on every model)": {
			Models:           []string{},
			ObjectPermission: ShellTeamPermissions(),
		},
		"a real model was granted directly": {
			Models:           []string{"gemini.gemini-flash-latest"},
			ObjectPermission: ShellTeamPermissions(),
		},
		"agents cleared (fail-open on every agent)": {
			Models:           []string{ShellTeamDenyAllModel},
			ObjectPermission: &TeamObjectPermission{Agents: []string{}},
		},
		"an mcp server was granted directly": {
			Models: []string{ShellTeamDenyAllModel},
			ObjectPermission: &TeamObjectPermission{
				MCPServers: []string{"mcp-slack"},
				Agents:     []string{ShellTeamDenyAllAgent},
			},
		},
	}
	for name, e := range cases {
		if !ShellTeamDrifted(e) {
			t.Errorf("%s: reported as healthy, want drifted", name)
		}
	}

	// A read-back that carries NEITHER field is unverifiable, not drifted —
	// reporting drift there would make the operator write on every reconcile
	// forever against a LiteLLM whose list endpoint omits object_permission.
	if ShellTeamDrifted(TeamListEntry{TeamID: "t-1", TeamAlias: "ach-env-demo"}) {
		t.Fatal("unverifiable read-back reported as drifted")
	}

	// A /v2/team/list-shaped read: models resolved (and healthy), the
	// object_permission relation not. That single unresolved dimension must
	// not be reported as drift.
	if ShellTeamDrifted(TeamListEntry{
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: nil,
	}) {
		t.Fatal("healthy models with unresolved object_permission reported as drifted")
	}
}

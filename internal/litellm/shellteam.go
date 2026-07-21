// SPDX-License-Identifier: Apache-2.0

package litellm

import "slices"

// The per-Environment deny-all SHELL TEAM is the ceiling on an Environment
// Key. LiteLLM access groups only ever ADD permissions — a team is the only
// thing that reliably caps a key (references/litellm-permission-model.md).
//
// The shell carries NO grants of its own. The Environment's real grants stay
// in its access group, which is attached to the shell exactly like any
// authorized team; the key inherits them through the group→team mirror. That
// keeps one copy of the three lists and one drift surface.
const (
	// ShellTeamPrefix namespaces ACH-owned shell teams inside LiteLLM's flat
	// team-alias space.
	ShellTeamPrefix = "ach-env-"

	// ShellTeamDenyAllModel is a model name that must never exist upstream.
	// An empty `models` list means EVERY model, so "deny all" has to be
	// spelled as "allow exactly this one impossible model".
	ShellTeamDenyAllModel = "__deny_all__"

	// ShellTeamDenyAllAgent is the same trick for agents: an empty or absent
	// `agents` list means every agent, so the list carries the nil UUID.
	// alitellm-operator applies the same sentinel to its teams.
	ShellTeamDenyAllAgent = "00000000-0000-0000-0000-000000000000"
)

// ShellTeamAlias is the LiteLLM team alias for an Environment's shell team.
func ShellTeamAlias(env string) string { return ShellTeamPrefix + env }

// ShellTeamPermissions is the deny-all object_permission block. MCP lists are
// explicit empties (mcp_servers is the one dimension that fails CLOSED when
// empty); the agent list carries the sentinel because empty fails OPEN.
func ShellTeamPermissions() *TeamObjectPermission {
	return &TeamObjectPermission{
		MCPServers:        []string{},
		MCPAccessGroups:   []string{},
		Agents:            []string{ShellTeamDenyAllAgent},
		AgentAccessGroups: []string{},
	}
}

// NewShellTeamRequest is the POST /team/new body for an Environment's shell.
func NewShellTeamRequest(env string) *NewTeamRequest {
	return &NewTeamRequest{
		TeamAlias:        ShellTeamAlias(env),
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
	}
}

// ShellTeamDrifted reports whether a shell team read back from LiteLLM has
// lost its sentinels (someone edited the team by hand, or a LiteLLM upgrade
// rewrote it). Any drift is a fail-OPEN condition, so the caller repairs it.
//
// The check is deliberately asymmetric between Models and ObjectPermission.
// `GET /v2/team/list` and `GET /team/list` never resolve the object_permission
// relation and serialise it as null (references/litellm-permission-model.md
// §9) — that null means "the endpoint did not tell us", not "no permissions",
// so a nil ObjectPermission alone is UNVERIFIABLE and must not be reported as
// drift. Models carries no such documented ambiguity: whenever the read
// resolved ObjectPermission, it resolved the team row, so a nil Models in that
// case is a genuine fail-open state. The only unverifiable read-back is
// therefore the one where BOTH fields are absent (a bare team-list row);
// everywhere else Models is checked unconditionally.
func ShellTeamDrifted(e TeamListEntry) bool {
	if e.Models == nil && e.ObjectPermission == nil {
		return false
	}
	if !slices.Equal(e.Models, []string{ShellTeamDenyAllModel}) {
		return true
	}
	op := e.ObjectPermission
	if op == nil {
		return false
	}
	return len(op.MCPServers) != 0 ||
		len(op.MCPAccessGroups) != 0 ||
		len(op.AgentAccessGroups) != 0 ||
		!slices.Equal(op.Agents, []string{ShellTeamDenyAllAgent})
}

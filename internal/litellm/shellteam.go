// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"slices"
)

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

	// ShellTeamManagedMetadataKey / ShellTeamManagedMetadataValue mark a
	// team as an ACH-owned shell in its `metadata` bag. Without this, ANY
	// team that happens to carry a shell's alias looks like ACH's own —
	// ensureShellTeam would UpdateTeam it (overwriting models/
	// object_permission) and deleteShellTeam would later DeleteTeam it,
	// which CASCADES to that team's keys. The marker is the proof of
	// ownership those two functions require before touching a team.
	ShellTeamManagedMetadataKey   = "ach_managed"
	ShellTeamManagedMetadataValue = "env-shell"
	// ShellTeamManagedEnvKey names the companion metadata entry carrying
	// which Environment a shell team belongs to.
	ShellTeamManagedEnvKey = "ach_environment"
)

// ShellTeamMetadata is the ownership metadata bag stamped on an
// Environment's shell team at create time (NewShellTeamRequest) and
// re-asserted on every repair (environment_shellteam.go's ensureShellTeam),
// so a shell can always be told apart from a same-alias team ACH did not
// create.
func ShellTeamMetadata(env string) map[string]any {
	return map[string]any{
		ShellTeamManagedMetadataKey: ShellTeamManagedMetadataValue,
		ShellTeamManagedEnvKey:      env,
	}
}

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

// denyAllTeamRequest builds a POST /team/new body for a deny-all shell, shared
// by the env shell (ach-env-<name>) and the user shell (ach-user-<email>).
// team_id is set == alias so the id is deterministic and creation is idempotent.
func denyAllTeamRequest(alias string, metadata map[string]any) *NewTeamRequest {
	return &NewTeamRequest{
		TeamID:           alias,
		TeamAlias:        alias,
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
		Metadata:         metadata,
	}
}

// NewShellTeamRequest is the POST /team/new body for an Environment's shell.
func NewShellTeamRequest(env string) *NewTeamRequest {
	return denyAllTeamRequest(ShellTeamAlias(env), ShellTeamMetadata(env))
}

// teamMetadataStrings decodes a LiteLLM team metadata blob and keeps only its
// string-valued entries. Absent or unparseable metadata yields a nil map, whose
// zero-value lookups make every ownership check fail safe.
//
// The blob must NOT be decoded as map[string]string. LiteLLM keeps its own
// management fields in the SAME object as ACH's ownership markers, and most are
// not strings: saving a team in the LiteLLM UI writes the full default block —
// guardrails ([]), model_rpm_limit ({}), disable_global_guardrails (false) —
// even when no enterprise feature is configured (LiteLLM issue #20304).
// encoding/json fails the WHOLE document on the first type mismatch, so a
// map[string]string decode would drop the ACH markers along with it and the
// operator would disown its own shell teams.
func teamMetadataStrings(raw json.RawMessage) map[string]string {
	var meta map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &meta) != nil {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// IsShellTeamManaged reports whether e's metadata carries the ACH shell-team
// ownership marker for env. Absent or unparseable metadata is NOT managed —
// fail safe, so callers refuse to touch a team they cannot prove they own.
func IsShellTeamManaged(e TeamListEntry, env string) bool {
	meta := teamMetadataStrings(e.Metadata)
	return meta[ShellTeamManagedMetadataKey] == ShellTeamManagedMetadataValue &&
		meta[ShellTeamManagedEnvKey] == env
}

// IsShellTeamShaped reports whether e already carries a shell team's alias
// and deny-all Models sentinel for env, independent of ownership metadata.
//
// This is the migration path for shells created before
// ShellTeamManagedMetadataKey existed: they carry no metadata, but landing
// on exactly this alias with exactly the deny-all sentinel is not a state an
// unrelated hand-made team would plausibly be in. ensureShellTeam treats a
// shell-shaped-but-unmarked team as adoptable (repairs it, which also stamps
// the metadata) instead of refusing it forever.
func IsShellTeamShaped(e TeamListEntry, env string) bool {
	return e.TeamAlias == ShellTeamAlias(env) && slices.Equal(e.Models, []string{ShellTeamDenyAllModel})
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

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

	// ShellTeamDenyAllModel is the value ACH writes to deny every model. An
	// empty `models` list means EVERY model, so "deny all" has to be spelled
	// as a list of exactly one name that grants nothing.
	//
	// It is LiteLLM's OWN recognised value, not an invented one, and that is
	// the whole point: LiteLLM filters "no-default-models" out of
	// GET /v1/models, while an arbitrary impossible name is echoed back as a
	// phantom model row. ACH used "__deny_all__" until 2026-09-22 and every
	// caller in a shell team saw a bogus model by that name (measured table
	// in references/litellm-permission-model.md §5). Both deny identically;
	// only this one stays out of the catalog.
	ShellTeamDenyAllModel = "no-default-models"

	// ShellTeamDenyAllModelLegacy is the pre-2026-09-22 sentinel. It is never
	// WRITTEN any more — it is only still RECOGNISED, so that a shell created
	// by an older ACH is adopted and repaired rather than mistaken for a team
	// ACH does not own. ShellTeamDrifted deliberately does NOT accept it:
	// reporting it as drift is what makes the next reconcile migrate it.
	ShellTeamDenyAllModelLegacy = "__deny_all__"

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
// mcp_tool_permissions is an empty map for the reason on the field itself:
// LiteLLM counts its KEYS as granted servers, so leaving it unmanaged left a
// hand-written entry granting a server the Environment never listed.
func ShellTeamPermissions() *TeamObjectPermission {
	return &TeamObjectPermission{
		MCPServers:         []string{},
		MCPAccessGroups:    []string{},
		MCPToolPermissions: map[string][]string{},
		Agents:             []string{ShellTeamDenyAllAgent},
		AgentAccessGroups:  []string{},
	}
}

// denyAllTeamRequest builds a POST /team/new body for a deny-all shell, shared
// by the env shell (ach-env-<name>) and the user shell (ach-user-<email>).
// team_id is set == alias so the id is deterministic and creation is idempotent.
//
// guardrails is non-nil ONLY for the env shell: coverage is EK-only (D2), and a
// user shell spans every Environment its owner is entitled to, so it has no
// per-Environment attachment point.
func denyAllTeamRequest(alias string, metadata map[string]any, guardrails []string) *NewTeamRequest {
	return &NewTeamRequest{
		TeamID:           alias,
		TeamAlias:        alias,
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
		Metadata:         metadata,
		Guardrails:       guardrails,
	}
}

// NewShellTeamRequest is the POST /team/new body for an Environment's shell.
func NewShellTeamRequest(env string, guardrails []string) *NewTeamRequest {
	return denyAllTeamRequest(ShellTeamAlias(env), ShellTeamMetadata(env), guardrails)
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
	meta := decodeTeamMetadata(raw)
	if meta == nil {
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

// decodeTeamMetadata unmarshals a LiteLLM team metadata blob into its raw
// map form, or nil if absent/unparseable. Shared by teamMetadataStrings and
// TeamGuardrails so IsShellTeamManaged + ShellTeamDrifted's back-to-back
// calls on the same TeamListEntry don't each decode the blob independently.
func decodeTeamMetadata(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var meta map[string]any
	if json.Unmarshal(raw, &meta) != nil {
		return nil
	}
	return meta
}

// TeamGuardrails reads the guardrail names LiteLLM stores under
// metadata.guardrails. Absent, unparseable, or wrongly-typed metadata yields
// nil — which ShellTeamDrifted reads as "none attached", so the repair writes
// the desired set. Non-string members are skipped rather than failing the whole
// read, matching teamMetadataStrings' tolerance of LiteLLM's mixed blob.
func TeamGuardrails(raw json.RawMessage) []string {
	meta := decodeTeamMetadata(raw)
	if meta == nil {
		return nil
	}
	arr, ok := meta["guardrails"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// sameGuardrailSet compares two guardrail lists as DEDUPLICATED, order-
// insensitive sets.
//
// Both properties are required. Order: LiteLLM's enforcement path builds the
// effective list from a Python set, so ordering is not stable. Deduplication:
// its storage path keeps what it was sent verbatim while enforcement dedupes,
// so ["a","a"] upstream and ["a"] in spec describe the same state — comparing
// lengths would report permanent phantom drift and rewrite the team on every
// reconcile, forever.
func sameGuardrailSet(a, b []string) bool {
	norm := func(in []string) []string {
		out := slices.Clone(in)
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(norm(a), norm(b))
}

// IsShellTeamManaged reports whether e's metadata carries the ACH shell-team
// ownership marker for env. Absent or unparseable metadata is NOT managed —
// fail safe, so callers refuse to touch a team they cannot prove they own.
func IsShellTeamManaged(e TeamListEntry, env string) bool {
	meta := teamMetadataStrings(e.Metadata)
	return meta[ShellTeamManagedMetadataKey] == ShellTeamManagedMetadataValue &&
		meta[ShellTeamManagedEnvKey] == env
}

// IsDenyAllModel reports whether name is a deny-all models sentinel ACH
// recognises — the current one or the legacy one. Reading accepts both;
// writing only ever emits ShellTeamDenyAllModel.
func IsDenyAllModel(name string) bool {
	return name == ShellTeamDenyAllModel || name == ShellTeamDenyAllModelLegacy
}

// isDenyAllModelList reports whether models is exactly one deny-all sentinel.
func isDenyAllModelList(models []string) bool {
	return len(models) == 1 && IsDenyAllModel(models[0])
}

// IsShellTeamShaped reports whether e carries a shell team's alias and a
// deny-all Models list (either sentinel) for env, independent of ownership
// metadata.
//
// It is the ADOPTION path, not a proof of ownership: the metadata marker
// (IsShellTeamManaged) is the proof, and this exists only so two kinds of
// unmarked shell are repaired instead of refused forever — those created
// before ShellTeamManagedMetadataKey existed, and those whose metadata a
// LiteLLM UI save wiped. What bounds it is the ALIAS: "ach-env-<env>" is
// ACH's own namespace and must match an Environment ACH reconciles. The
// Models value bounds nothing much on its own — "no-default-models" is a
// value LiteLLM documents, so a human could plausibly set it (that was less
// true of the invented "__deny_all__", and the comment that used to lean on
// it was wrong to). ensureShellTeam repairs an adopted team, which stamps
// the metadata, so every later pass sees it as managed.
func IsShellTeamShaped(e TeamListEntry, env string) bool {
	return e.TeamAlias == ShellTeamAlias(env) && isDenyAllModelList(e.Models)
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
//
// Guardrails are compared as a deduplicated, order-insensitive set read from
// metadata.guardrails, checked before the unverifiable-read-back early
// return so removal converges.
func ShellTeamDrifted(e TeamListEntry, wantGuardrails []string) bool {
	// Guardrails are checked FIRST and unconditionally. Unlike models and
	// object_permission they live in metadata, which every read path returns,
	// so there is no unverifiable-read-back ambiguity here — and checking
	// before the early return below is what makes REMOVAL converge (S2).
	if !sameGuardrailSet(TeamGuardrails(e.Metadata), wantGuardrails) {
		return true
	}
	if e.Models == nil && e.ObjectPermission == nil {
		return false
	}
	// Exact match against the CURRENT sentinel, deliberately: a shell still
	// carrying the legacy "__deny_all__" is reported as drifted so the repair
	// below rewrites it and the phantom model leaves the caller's catalog.
	if !slices.Equal(e.Models, []string{ShellTeamDenyAllModel}) {
		return true
	}
	op := e.ObjectPermission
	if op == nil {
		return false
	}
	return len(op.MCPServers) != 0 ||
		len(op.MCPAccessGroups) != 0 ||
		len(op.MCPToolPermissions) != 0 ||
		len(op.AgentAccessGroups) != 0 ||
		!slices.Equal(op.Agents, []string{ShellTeamDenyAllAgent})
}

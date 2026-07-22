// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"slices"
	"strings"
)

// The per-user deny-all SHELL TEAM caps a Personal Key exactly as the
// per-Environment shell caps an Environment Key (references/litellm-permission-model.md).
// The shell holds NO grants; the operator attaches every entitled Environment's
// access group onto it, and the pk_ inherits their union through the group→team
// mirror. A pk_ has one team_id, so this per-user shell is what lets a single
// key cover the union of a user's entitlements.
const (
	UserShellPrefix = "ach-user-"

	// UserShellManagedMetadataValue marks a team as an ACH-owned user shell,
	// distinct from the env-shell value so the two ownership checks never
	// cross-adopt. ShellTeamManagedMetadataKey / ShellTeamManagedEnvKey are
	// reused; the companion key here carries the email.
	UserShellManagedMetadataValue = "user-shell"
	UserShellManagedUserKey       = "ach_user"
)

// NormalizeEmail lower-cases and trims so ach-user-<email> is stable across
// casing/whitespace variants of the same identity.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// UserShellAlias is the LiteLLM team alias (== team_id) for a user's shell.
func UserShellAlias(email string) string { return UserShellPrefix + NormalizeEmail(email) }

// UserShellMetadata is the ownership bag stamped at create time.
func UserShellMetadata(email string) map[string]any {
	return map[string]any{
		ShellTeamManagedMetadataKey: UserShellManagedMetadataValue,
		UserShellManagedUserKey:     NormalizeEmail(email),
	}
}

// NewUserShellRequest is the POST /team/new body for a user's shell.
func NewUserShellRequest(email string) *NewTeamRequest {
	return denyAllTeamRequest(UserShellAlias(email), UserShellMetadata(email))
}

// IsUserShellManaged reports whether e carries the ACH user-shell ownership
// marker for email. Absent/unparseable metadata is NOT managed (fail safe).
func IsUserShellManaged(e TeamListEntry, email string) bool {
	if len(e.Metadata) == 0 {
		return false
	}
	var meta map[string]string
	if err := json.Unmarshal(e.Metadata, &meta); err != nil {
		return false
	}
	return meta[ShellTeamManagedMetadataKey] == UserShellManagedMetadataValue &&
		meta[UserShellManagedUserKey] == NormalizeEmail(email)
}

// IsUserShellShaped reports whether e already carries a user shell's alias and
// deny-all Models sentinel for email, independent of ownership metadata (the
// migration path for shells created before the metadata existed — mirrors
// IsShellTeamShaped).
func IsUserShellShaped(e TeamListEntry, email string) bool {
	return e.TeamAlias == UserShellAlias(email) && slices.Equal(e.Models, []string{ShellTeamDenyAllModel})
}

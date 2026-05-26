// SPDX-License-Identifier: Apache-2.0

//go:build phase5_orphan
// +build phase5_orphan

// Build tag temporarily excludes this test from default builds while
// internal/orphan (its cross-package import) lands in Phase 5 of the
// domain port. Tag removed in Phase 5.1 alongside the orphan port.

package audit_test

import (
	"testing"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/orphan"
)

// TestEventConstantsAreStable locks the string value of every
// Action* and Outcome* constant declared in events.go. Downstream
// log filters (fluent-bit / Loki / etc.) match on these literals, so
// any value drift is a wire-format break — this test is the canary.
//
// Source of truth: Hub §18.2 outcome enum (16 values) + Hub §18.2
// action enum (9 values) + OutcomeStateInvalid (Phase-3-internal
// additive extension per BLK-05 for SSO state-mismatch — total 17
// outcomes).
func TestEventConstantsAreStable(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		// Actions — Hub §18.2 (9 total)
		{"ActionSSOLogin", audit.ActionSSOLogin, "platform.sso.login"},
		{"ActionEkCreate", audit.ActionEkCreate, "platform.ek.create"},
		{"ActionEkRevoke", audit.ActionEkRevoke, "platform.ek.revoke"},
		{"ActionPkRevoke", audit.ActionPkRevoke, "platform.pk.revoke"},
		{"ActionHydrate", audit.ActionHydrate, "platform.hydrate"},
		{"ActionAdminRefresh", audit.ActionAdminRefresh, "platform.admin.refresh"},
		{"ActionAdminKeysRevoke", audit.ActionAdminKeysRevoke, "platform.admin.keys.revoke"},
		{"ActionAdminUsersRevokeKeys", audit.ActionAdminUsersRevokeKeys, "platform.admin.users.revoke_keys"},
		{"ActionEnvironmentLifecycle", audit.ActionEnvironmentLifecycle, "platform.environment.lifecycle"},

		// Outcomes — Hub §18.2 (16 verbatim)
		{"OutcomeCreated", audit.OutcomeCreated, "created"},
		{"OutcomeRevoked", audit.OutcomeRevoked, "revoked"},
		{"OutcomeUnauthorizedTeam", audit.OutcomeUnauthorizedTeam, "unauthorized_team"},
		{"OutcomeWrongEnvironment", audit.OutcomeWrongEnvironment, "wrong_environment"},
		{"OutcomeMissingEnvironment", audit.OutcomeMissingEnvironment, "missing_environment"},
		{"OutcomeEnvironmentNotFound", audit.OutcomeEnvironmentNotFound, "environment_not_found"},
		{"OutcomeNotReady", audit.OutcomeNotReady, "not_ready"},
		{"OutcomeDefaultTeamMissing", audit.OutcomeDefaultTeamMissing, "default_team_missing"},
		{"OutcomeInvalidKeyType", audit.OutcomeInvalidKeyType, "invalid_key_type"},
		{"OutcomeNotAdmin", audit.OutcomeNotAdmin, "not_admin"},
		{"OutcomeNotKeyOwner", audit.OutcomeNotKeyOwner, "not_key_owner"},
		{"OutcomeInvalidKeyFormat", audit.OutcomeInvalidKeyFormat, "invalid_key_format"},
		{"OutcomeExpiredOrRevoked", audit.OutcomeExpiredOrRevoked, "expired_or_revoked"},
		{"OutcomeLitellmUnreachable", audit.OutcomeLitellmUnreachable, "litellm_unreachable"},
		{"OutcomeDbInsertFailed", audit.OutcomeDbInsertFailed, "db_insert_failed"},
		{"OutcomeInternalError", audit.OutcomeInternalError, "internal_error"},

		// Outcomes — Phase-3-internal additive extension per BLK-05 (1 value).
		// Emitted by the SSO callback on cookie-state vs URL-state mismatch
		// or missing URL state. Documented in 03-02-SUMMARY.md so future
		// Hub-spec revisions can adopt it back into §18.2 if desired.
		{"OutcomeStateInvalid", audit.OutcomeStateInvalid, "state_invalid"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("audit.%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestEventConstantsMatchOrphan asserts the cross-phase string-value
// equality contract: the two outcomes that overlap between Phase 2's
// orphan-cleanup loop and the Phase 3 platform-handler enum carry the
// SAME literal so a single grep predicate matches both eras of audit
// records.
func TestEventConstantsMatchOrphan(t *testing.T) {
	if audit.OutcomeRevoked != orphan.OutcomeRevoked {
		t.Fatalf("audit.OutcomeRevoked (%q) != orphan.OutcomeRevoked (%q) — log-filter break",
			audit.OutcomeRevoked, orphan.OutcomeRevoked)
	}
	if audit.OutcomeLitellmUnreachable != orphan.OutcomeLiteLLMUnreachable {
		t.Fatalf("audit.OutcomeLitellmUnreachable (%q) != orphan.OutcomeLiteLLMUnreachable (%q) — log-filter break",
			audit.OutcomeLitellmUnreachable, orphan.OutcomeLiteLLMUnreachable)
	}
}

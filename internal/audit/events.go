// SPDX-License-Identifier: Apache-2.0

// events.go declares the closed-enum string constants that bind the
// audit event schema (Hub §18.2). Every Phase 3 handler emits one
// Action* value as the `action` attribute (and the slog message — see
// emit.go) and one Outcome* value as the `outcome` attribute. These
// constants are package-level by design: handler plans (03-07..03-10)
// import them directly rather than re-deriving the literal strings, so
// a single rename here propagates everywhere.
//
// Naming discipline:
//
//   - Actions follow the `platform.<area>.<verb>` convention so
//     downstream log filters can grep on `action=platform.*` to scope
//     to Platform-API-emitted records (vs Operator records like
//     `operator.orphan-cleanup` from Phase 2).
//   - Outcomes are lowercase snake_case Hub §18.2 verbatim — they are
//     wire format on the HTTP error envelope too (render.Error uses the
//     same vocabulary, see internal/platformapi/render/json.go).
//
// Cross-phase compatibility:
//
//   - OutcomeRevoked / OutcomeLitellmUnreachable carry the SAME string
//     values as internal/orphan.OutcomeRevoked /
//     internal/orphan.OutcomeLiteLLMUnreachable so a single log-filter
//     predicate matches Phase 2 orphan-cleanup audit lines and Phase 3
//     handler emissions. TestEventConstantsMatchOrphan enforces this
//     contract.
//
// Extension policy (Hub §18.5):
//
//   - Future phases MAY extend the Outcome enum ADDITIVELY (new
//     constants here, never renaming or removing). OutcomeStateInvalid
//     is one such extension — it is Phase-3-internal (added per BLK-05
//     for SSO callback state-mismatch / missing URL state), not in the
//     original Hub §18.2 enum, and documented in
//     .planning/phases/03-hub-identity-platform-api/03-02-SUMMARY.md so
//     a future Hub-spec revision can adopt it back into §18.2.

package audit

// Action* — the closed set of `action` values a Platform API or
// Operator audit emission may carry. Each value is also used as the
// slog message (`logger.Info(e.Action, ...)`); the double-coding is
// intentional (msg + action attribute) so both message-based and
// attribute-based log filters work.
//
// 9 constants — Hub §18.2 action enum verbatim.
const (
	ActionSSOLogin             = "platform.sso.login"
	ActionEkCreate             = "platform.ek.create"
	ActionEkRevoke             = "platform.ek.revoke"
	ActionPkRevoke             = "platform.pk.revoke"
	ActionHydrate              = "platform.hydrate"
	ActionAdminRefresh         = "platform.admin.refresh"
	ActionAdminKeysRevoke      = "platform.admin.keys.revoke"
	ActionAdminUsersRevokeKeys = "platform.admin.users.revoke_keys"
	// ActionEnvironmentLifecycle is reserved for the Operator's
	// Environment-CR reconciliation emission point (Phase 3 wires it via
	// the existing controller; handlers do NOT emit this action).
	ActionEnvironmentLifecycle = "platform.environment.lifecycle"
)

// Outcome* — the closed set of `outcome` values a Platform API or
// Operator audit emission may carry. The first 16 constants are Hub
// §18.2 verbatim. OutcomeStateInvalid is a Phase-3-internal additive
// extension per BLK-05 (see file-level docstring).
//
// 17 constants — 16 from Hub §18.2 + OutcomeStateInvalid.
const (
	OutcomeCreated             = "created"
	OutcomeRevoked             = "revoked" // matches orphan.OutcomeRevoked
	OutcomeUnauthorizedTeam    = "unauthorized_team"
	OutcomeWrongEnvironment    = "wrong_environment"
	OutcomeMissingEnvironment  = "missing_environment"
	OutcomeEnvironmentNotFound = "environment_not_found"
	OutcomeNotReady            = "not_ready"
	OutcomeDefaultTeamMissing  = "default_team_missing"
	OutcomeInvalidKeyType      = "invalid_key_type"
	OutcomeNotAdmin            = "not_admin"
	OutcomeNotKeyOwner         = "not_key_owner"
	OutcomeInvalidKeyFormat    = "invalid_key_format"
	OutcomeExpiredOrRevoked    = "expired_or_revoked"
	OutcomeLitellmUnreachable  = "litellm_unreachable" // matches orphan.OutcomeLiteLLMUnreachable
	OutcomeDbInsertFailed      = "db_insert_failed"
	OutcomeInternalError       = "internal_error"

	// OutcomeStateInvalid is emitted by the SSO callback (Plan 03-07)
	// on cookie-state vs URL-state mismatch or missing URL state (per
	// BLK-05). Phase-3-internal additive extension to Hub §18.2 — not
	// in the original enum.
	OutcomeStateInvalid = "state_invalid"
)

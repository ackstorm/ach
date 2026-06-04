// SPDX-License-Identifier: Apache-2.0

package litellm

import "context"

// Client is the LiteLLM-side contract every domain reconciler accepts
// (D-11 carry-forward from Phase 1; widened in Phase 2 per D-01, D-13, D-16).
//
// Reconcilers MUST type their litellm dependency as Client (interface),
// never as a concrete type. This mirrors the sister project's
// connection.ConnectionCache discipline — the production swap from
// NoopClient to RESTClient is a wiring change in cmd/operator/main.go
// only (Plan 09), no reconciler edit. The compile-time assertions
//
//	var _ Client = (*NoopClient)(nil)   // noop.go
//	var _ Client = (*RESTClient)(nil)   // bottom of this file
//
// are the canaries that break the build if either implementation drifts
// from the interface method set.
//
// Method semantics:
//
//   - DeleteAccessGroup is called from EnvironmentReconciler at Hub §6.5
//     step 2 — the runtime barrier. Once the LiteLLM access group named
//     <environment> is deleted, every ek_ still bound to the Environment
//     fails forwarding at LiteLLM regardless of ACH cache state. The
//     RESTClient implementation propagates upstream errors back, which
//     keep the finalizer in place via the reconciler's return-on-err
//     sequencing; NoopClient logs and returns nil.
//
//   - DeleteTag is called at Hub §6.5 step 3 — clears the budget tag
//     used by LiteLLM for spend attribution. Same contract.
//
//   - ListModels / ListMCPServers / ListA2AAgents are called by the
//     LiteLLM-snapshot manager.Runnable (D-13, Plan 07). The snapshotter
//     wraps errors.Is(err, ErrNotFound) → empty slice: an Environment
//     listing a model against a LiteLLM with zero models is the empty
//     intersection (a real `unresolvedRuntime` result), NOT an error.
//
//   - ListUserKeys / RevokeKey are called by the orphan-cleanup
//     manager.Runnable (D-16, Plan 08). Audit emission is the caller's
//     responsibility — neither method emits audit events itself.
//
// All methods accept context.Context so the RESTClient transport can
// honor cancellation/deadline propagation from the caller's ctx.
type Client interface {
	// Phase 1 — preserved verbatim.

	// DeleteAccessGroup invokes LiteLLM DELETE /access-groups/<name>.
	// NoopClient logs and returns nil; RESTClient propagates upstream errors.
	DeleteAccessGroup(ctx context.Context, name string) error

	// DeleteTag invokes LiteLLM DELETE /tags/<name>.
	// NoopClient logs and returns nil; RESTClient propagates upstream errors.
	DeleteTag(ctx context.Context, name string) error

	// Phase 2 — added per D-01, D-13, D-16. Consumed by the LiteLLM-snapshot
	// Runnable (Plan 07) and the orphan-cleanup Runnable (Plan 08).

	// ListModels issues GET /v1/model/info and returns one entry per
	// LiteLLM-registered model. Returns ErrNotFound on empty result —
	// callers (Plan 07 snapshotter) wrap this into an empty slice.
	ListModels(ctx context.Context) ([]ModelInfoResponse, error)

	// ListMCPServers issues GET /v1/mcp/server and returns one entry per
	// LiteLLM-registered MCP server. Returns ErrNotFound on empty result.
	ListMCPServers(ctx context.Context) ([]MCPServerEntry, error)

	// ListA2AAgents issues GET /v1/agents?health_check=false and returns
	// one entry per LiteLLM-registered A2A agent. The wrapper name reflects
	// ACH's A2A-agent terminology (D-13); the LiteLLM endpoint name is
	// unchanged. Returns ErrNotFound on empty result.
	ListA2AAgents(ctx context.Context) ([]AgentEntry, error)

	// ListUserKeys issues
	// GET /key/list?user_id=<userID>&return_full_object=true&include_team_keys=false
	// and returns one entry per key owned by the specified user. Used by
	// orphan-cleanup to enumerate keys per ACH-managed user. The response
	// `token` field is the LiteLLM-internal opaque hex (NOT ACH's
	// `pkid_*` / `ekid_*` prefix) — see Phase 02.2 Plan 1 (Gap G1 fix).
	ListUserKeys(ctx context.Context, userID string) ([]UserKeyInfo, error)

	// RevokeKey issues POST /key/delete with body {"keys": [keyID]} —
	// used by orphan-cleanup to revoke a single LiteLLM key by its
	// LiteLLM-internal key_id (NOT the plaintext bearer key).
	RevokeKey(ctx context.Context, keyID string) error

	// Phase 3 — added per D-25. Consumed by the Platform API SSO handler
	// (Plan 03-07) and the env-keys create handler (Plan 03-08).

	// UserNew issues POST /user/new. ACH NEVER sets max_budget on
	// first-SSO LiteLLM user creation (KEY-10); UserNewRequest reflects
	// that at the type level. Returns the canonical *UserInfo so callers
	// read user_id without a second round-trip.
	UserNew(ctx context.Context, req *UserNewRequest) (*UserInfo, error)

	// UserInfoByEmail issues GET /user/info?user_email=<email>. The 404
	// response is NOT translated to ErrNotFound — callers
	// (Plan 03-07 SSO handler) use strings.Contains(err.Error(), "404")
	// to branch into the UserNew first-time-SSO path. Returned UserInfo
	// carries Teams []string — the BLK-01 contract consumed by
	// Plans 03-08 + 03-09 for the §8.2 step-4 team-intersection check (KEY-11).
	UserInfoByEmail(ctx context.Context, email string) (*UserInfo, error)

	// TeamMemberAdd issues POST /team/member_add. LiteLLM treats
	// duplicate-add as 4xx; callers (Plan 03-07) decide whether to swallow.
	// The error propagates verbatim with no special-case wrapping here.
	TeamMemberAdd(ctx context.Context, teamID, userID, role string) error

	// ListTeamsByAlias issues GET /v2/team/list?team_alias=<alias>
	// and returns the matching team entries. Used by the SSO handler
	// (Plan 03-07) to resolve the LiteLLM-assigned team_id UUID from
	// the well-known alias "default" before the TeamMemberAdd call.
	ListTeamsByAlias(ctx context.Context, alias string) ([]TeamListEntry, error)

	// ListAllTeams issues GET /v2/team/list (no alias filter) and returns
	// every team's id+alias. Used by platformapi/teams to resolve caller
	// team IDs → aliases for the authorized_teams intersection.
	ListAllTeams(ctx context.Context) ([]TeamListEntry, error)

	// KeyGenerate issues POST /key/generate.
	//
	// ACH does NOT control or persist the LiteLLM virtual-key plaintext
	// material (FIX01 §A.6 — supersedes the obsolete D-13 design where
	// ACH supplied req.Key). LiteLLM v1.83 enforces an `sk-` prefix on
	// virtual keys; ACH callers leave req.Key empty so LiteLLM mints
	// its own plaintext, and ACH stores only the opaque keyResp.Token
	// — the stable LiteLLM-side identifier used for revoke + forwarder
	// attribution.
	//
	// Response semantics:
	//   - KeyGenerateResponse.Key   plaintext `sk-…`, one-time, MUST
	//                               NOT be persisted by ACH. Returned
	//                               here only because LiteLLM emits
	//                               it; callers discard it.
	//   - KeyGenerateResponse.Token opaque LiteLLM token id, STORE
	//                               THIS as `litellm_token` for revoke
	//                               + orphan-cleanup attribution.
	//
	// KEY-10 enforced at the type level: MaxBudget is *float64 with
	// omitempty so nil pointer drops the field from the wire payload.
	KeyGenerate(ctx context.Context, req *KeyGenerateRequest) (*KeyGenerateResponse, error)

	// Phase 4 (TODO §7) — Environment AccessGroupSynced reconciler.

	// CreateAccessGroup issues POST /v1/access_group. The reconciler
	// must call GetAccessGroupByName first; only POST when nil is
	// returned (list-first-then-create per issue #17 plan §4).
	CreateAccessGroup(ctx context.Context, req AccessGroupCreateRequest) (*AccessGroupResponse, error)

	// GetAccessGroupByName performs GET /v1/access_group and returns
	// the matching entry by access_group_name. (nil, nil) when not
	// found.
	GetAccessGroupByName(ctx context.Context, name string) (*AccessGroupResponse, error)

	// UpdateAccessGroup issues PUT /v1/access_group/{id}. Used by the
	// reconciler's drift-correction branch.
	UpdateAccessGroup(ctx context.Context, id string, req AccessGroupUpdateRequest) (*AccessGroupResponse, error)

	// DeleteAccessGroupByID issues DELETE /v1/access_group/{id}. 404
	// is treated as success (§7.7 idempotent-delete contract).
	DeleteAccessGroupByID(ctx context.Context, id string) error

	// EnsureDefaultTeam is the operator-side bootstrap call that
	// guarantees LiteLLM has at least one Team with alias=default
	// before any SSO callback fires. Idempotent: list by alias first,
	// only POST /team/new on empty. Called by the LiteLLMConnection
	// reconciler after a successful probe so we never need the deployer
	// to hand-seed the team via cluster.sh / curl.
	//
	// Returns nil on success (team already present OR newly created).
	// Returns wrapped error on LiteLLM unreachable / 5xx / unauthorized
	// — caller logs and continues; the next reconcile retries.
	EnsureDefaultTeam(ctx context.Context) error
}

// Compile-time interface assertion: RESTClient satisfies Client. If a
// future edit to the Client interface adds or changes a method, the
// build breaks here until RESTClient catches up.
var _ Client = (*RESTClient)(nil)

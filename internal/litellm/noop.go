// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"

	"github.com/go-logr/logr"
)

// NoopClient is the Phase 1 Client implementation (D-11). Every method
// logs the intended LiteLLM operation and returns nil. No HTTP traffic
// is generated; the operator has no LiteLLM connectivity in Phase 1.
//
// Phase 2 swaps in a real implementation via dependency-injection at
// EnvironmentReconciler construction time in cmd/operator/main.go.
// Reconcilers are typed against the Client interface so the swap is a
// wiring-only change.
type NoopClient struct {
	// Log is the structured logger used for the "stub: would …" lines
	// emitted by each method. Setting Log to a zero-value logr.Logger
	// is safe — logr.Logger is a value type and its methods no-op when
	// no sink is set.
	Log logr.Logger
}

// NewNoopClient returns a *NoopClient that logs through the supplied
// logger. The constructor exists for symmetry with the Phase 2
// constructor it will be paired with; callers MAY also construct a
// NoopClient by struct literal in tests.
func NewNoopClient(log logr.Logger) *NoopClient {
	return &NoopClient{Log: log}
}

// DeleteAccessGroup is the §6.5 step 2 LiteLLM call. Phase 1 logs and
// returns nil — the deletion will happen for real in Phase 2.
func (c *NoopClient) DeleteAccessGroup(_ context.Context, name string) error {
	c.Log.Info("stub: would delete LiteLLM access group", "name", name)
	return nil
}

// DeleteTag is the §6.5 step 3 LiteLLM call. Phase 1 logs and returns
// nil — the deletion will happen for real in Phase 2.
func (c *NoopClient) DeleteTag(_ context.Context, name string) error {
	c.Log.Info("stub: would delete LiteLLM tag", "name", name)
	return nil
}

// ListModels is the Plan 07 snapshot-Runnable call. NoopClient returns
// (nil, nil) — an empty registered-model set, NOT an error — so Plan 07
// tests against NoopClient compute the empty intersection consistently.
func (c *NoopClient) ListModels(_ context.Context) ([]ModelInfoResponse, error) {
	c.Log.Info("stub: would list LiteLLM models")
	return nil, nil
}

// ListMCPServers is the Plan 07 snapshot-Runnable call. NoopClient
// returns (nil, nil) — empty MCP-server set, NOT an error.
func (c *NoopClient) ListMCPServers(_ context.Context) ([]MCPServerEntry, error) {
	c.Log.Info("stub: would list LiteLLM MCP servers")
	return nil, nil
}

// ListA2AAgents is the Plan 07 snapshot-Runnable call. NoopClient
// returns (nil, nil) — empty A2A-agent set, NOT an error.
func (c *NoopClient) ListA2AAgents(_ context.Context) ([]AgentEntry, error) {
	c.Log.Info("stub: would list LiteLLM A2A agents")
	return nil, nil
}

// ListUserKeys is the Plan 08 orphan-cleanup call. NoopClient returns
// (nil, nil) — no keys reported, the orphan loop is a no-op against
// NoopClient (which is the desired behavior for unit tests).
func (c *NoopClient) ListUserKeys(_ context.Context, userID string) ([]UserKeyInfo, error) {
	c.Log.Info("stub: would list LiteLLM user keys", "userID", userID)
	return nil, nil
}

// RevokeKey is the Plan 08 orphan-cleanup call. NoopClient logs and
// returns nil — no wire traffic in unit tests.
func (c *NoopClient) RevokeKey(_ context.Context, keyID string) error {
	c.Log.Info("stub: would revoke LiteLLM key", "keyID", keyID)
	return nil
}

// UserNew is the Phase 3 Plan 03-07 SSO-callback call. NoopClient echoes
// the supplied email and synthesizes a stable "noop-<email>" user_id so
// unit-test assertions can detect a stubbed value vs a real LiteLLM-
// assigned id. Returns nil error.
func (c *NoopClient) UserNew(_ context.Context, req *UserNewRequest) (*UserInfo, error) {
	c.Log.Info("stub: would create LiteLLM user", "user_email", req.UserEmail)
	return &UserInfo{
		UserID:    "noop-" + req.UserEmail,
		UserEmail: req.UserEmail,
	}, nil
}

// UserInfoByEmail is the Phase 3 Plan 03-07 SSO-callback call. NoopClient
// returns (nil, ErrNotFound) by default so unit tests deterministically
// drive the SSO first-time-user branch (UserInfoByEmail → 404 → UserNew).
// Tests that need the existing-user branch construct a stand-in *UserInfo
// directly rather than fooling around with stub state.
func (c *NoopClient) UserInfoByEmail(_ context.Context, email string) (*UserInfo, error) {
	c.Log.Info("stub: would look up LiteLLM user by email", "user_email", email)
	return nil, ErrNotFound
}

// TeamMemberAdd is the Phase 3 Plan 03-07 SSO-callback call. NoopClient
// logs and returns nil unconditionally — no duplicate-add detection.
func (c *NoopClient) TeamMemberAdd(_ context.Context, teamID, userID, role string) error {
	c.Log.Info("stub: would add LiteLLM team member",
		"team_id", teamID, "user_id", userID, "role", role)
	return nil
}

// KeyGenerate is the Phase 3 Plan 03-07 + Plan 03-08 call. NoopClient
// echoes the caller-supplied req.Key (D-13 contract) and synthesizes a
// deterministic "noop-token-<user_id>" Token so the SSO + env-keys
// handler tests can assert the persisted litellm_token value without
// stub state leakage between tests. Returns nil error.
func (c *NoopClient) KeyGenerate(_ context.Context, req *KeyGenerateRequest) (*KeyGenerateResponse, error) {
	c.Log.Info("stub: would generate LiteLLM key", "user_id", req.UserID)
	return &KeyGenerateResponse{
		Key:    req.Key,
		Token:  "noop-token-" + req.UserID,
		UserID: req.UserID,
	}, nil
}

// ListTeamsByAlias returns an empty slice — the noop client has no
// team state. Production SSO callers must run against a RESTClient
// against a real LiteLLM with the "default" Team pre-provisioned.
func (c *NoopClient) ListTeamsByAlias(_ context.Context, alias string) ([]TeamListEntry, error) {
	c.Log.Info("stub: would list LiteLLM teams by alias", "alias", alias)
	return nil, nil
}

// EnsureDefaultTeam is a no-op for the stub client. Production
// operator-bootstrap uses RESTClient against a real LiteLLM; envtest
// pinning to the stub gets a free pass — the SSO path branches on
// presence/absence at runtime, not on this call's outcome.
func (c *NoopClient) EnsureDefaultTeam(_ context.Context) error {
	c.Log.Info("stub: would ensure LiteLLM default team")
	return nil
}

// CreateAccessGroup is the §7 LiteLLM call (issue #17: /v1/access_group).
// NoopClient logs and returns a synthetic response with a deterministic
// UUID so envtests that don't override the LiteLLM client can still
// progress through reconcileAccessGroup without ID-resolution surprises.
func (c *NoopClient) CreateAccessGroup(_ context.Context, req AccessGroupCreateRequest) (*AccessGroupResponse, error) {
	c.Log.Info("stub: would create LiteLLM access group", "name", req.AccessGroupName, "modelNames", req.AccessModelNames)
	return &AccessGroupResponse{
		AccessGroupID:      "noop-" + req.AccessGroupName,
		AccessGroupName:    req.AccessGroupName,
		AccessModelNames:   req.AccessModelNames,
		AccessMCPServerIDs: req.AccessMCPServerIDs,
		AccessAgentIDs:     req.AccessAgentIDs,
		AssignedTeamIDs:    req.AssignedTeamIDs,
		AssignedKeyIDs:     req.AssignedKeyIDs,
	}, nil
}

// GetAccessGroupByName always returns (nil, nil) — the reconciler will
// take the POST branch on every reconcile in noop mode, which is
// harmless (the noop POST returns synthetic success).
func (c *NoopClient) GetAccessGroupByName(_ context.Context, name string) (*AccessGroupResponse, error) {
	c.Log.V(2).Info("stub: would lookup LiteLLM access group by name", "name", name)
	return nil, nil
}

// UpdateAccessGroup logs and echoes the request back.
func (c *NoopClient) UpdateAccessGroup(_ context.Context, id string, req AccessGroupUpdateRequest) (*AccessGroupResponse, error) {
	c.Log.Info("stub: would update LiteLLM access group", "id", id)
	resp := &AccessGroupResponse{AccessGroupID: id}
	if req.AccessGroupName != nil {
		resp.AccessGroupName = *req.AccessGroupName
	}
	if req.AccessModelNames != nil {
		resp.AccessModelNames = req.AccessModelNames
	}
	if req.AccessMCPServerIDs != nil {
		resp.AccessMCPServerIDs = req.AccessMCPServerIDs
	}
	if req.AccessAgentIDs != nil {
		resp.AccessAgentIDs = req.AccessAgentIDs
	}
	if req.AssignedTeamIDs != nil {
		resp.AssignedTeamIDs = req.AssignedTeamIDs
	}
	if req.AssignedKeyIDs != nil {
		resp.AssignedKeyIDs = req.AssignedKeyIDs
	}
	return resp, nil
}

// DeleteAccessGroupByID is a no-op.
func (c *NoopClient) DeleteAccessGroupByID(_ context.Context, id string) error {
	c.Log.Info("stub: would delete LiteLLM access group by id", "id", id)
	return nil
}

// Compile-time interface satisfaction. If a future edit to the Client
// interface adds or changes a method, the build breaks here until
// NoopClient catches up — matching the sister project's discipline
// (ach_litellm/internal/controller/noop_controller.go line 188).
var _ Client = (*NoopClient)(nil)

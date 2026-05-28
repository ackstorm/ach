// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"time"
)

// LiteLLMParams is the pass-through bag the operator marshals into POST
// /model/new and POST /model/update bodies. The OpenAPI schema declares
// `additionalProperties: true` (truly freeform), so a map preserves
// forward-compat with future LiteLLM fields the operator does not need
// to know about.
type LiteLLMParams map[string]any

// ModelInfo is the model_info sub-block carried by POST /model/new and
// the inverse responses.
type ModelInfo struct {
	ID                  string         `json:"id"`
	DBModel             bool           `json:"db_model,omitempty"`
	UpdatedAt           string         `json:"updated_at,omitempty"`
	UpdatedBy           string         `json:"updated_by,omitempty"`
	CreatedAt           string         `json:"created_at,omitempty"`
	CreatedBy           string         `json:"created_by,omitempty"`
	BaseModel           string         `json:"base_model,omitempty"`
	Tier                string         `json:"tier,omitempty"`
	TeamID              string         `json:"team_id,omitempty"`
	TeamPublicModelName string         `json:"team_public_model_name,omitempty"`
	Extra               map[string]any `json:"-"`
}

// Deployment is the POST /model/new request body.
type Deployment struct {
	ModelName     string        `json:"model_name"`
	LiteLLMParams LiteLLMParams `json:"litellm_params"`
	ModelInfo     ModelInfo     `json:"model_info"`
}

// updateDeployment is the POST /model/update request body (note the
// lowercase 'u' — matches the OpenAPI operationId / schema name verbatim).
// The model id lives in ModelInfo.ID, NOT in the URL — see Pitfall 2.
//
//nolint:revive // OpenAPI schema name is lowercase
type updateDeployment struct {
	ModelName     string        `json:"model_name,omitempty"`
	LiteLLMParams LiteLLMParams `json:"litellm_params,omitempty"`
	ModelInfo     ModelInfo     `json:"model_info"`
}

// UpdateDeployment is the exported alias callers in later phases use to
// construct the body. The lowercase name above stays for OpenAPI parity
// in internal code review.
type UpdateDeployment = updateDeployment

// ModelInfoResponse is one entry in a GET /model/info response Data
// array. The OpenAPI doc is sparse here ({}); shape inferred from spike
// Probe 2 + bbdsoftware/litellm-operator's known mapping.
type ModelInfoResponse struct {
	ModelID       string         `json:"model_id"`
	ModelName     string         `json:"model_name"`
	LiteLLMParams LiteLLMParams  `json:"litellm_params"`
	ModelInfo     ModelInfo      `json:"model_info"`
	Extra         map[string]any `json:"-"`
}

// ModelListResponse is the envelope returned by GET /model/info.
type ModelListResponse struct {
	Data []ModelInfoResponse `json:"data"`
}

// ModelDeleteRequest is the POST /model/delete request body.
type ModelDeleteRequest struct {
	ID string `json:"id"`
}

// NewTeamRequest is the POST /team/new request body. Mirrors the
// OpenAPI schema's optional fields; callers populate only what they need.
type NewTeamRequest struct {
	TeamAlias      string         `json:"team_alias,omitempty"`
	TeamID         string         `json:"team_id,omitempty"`
	OrganizationID string         `json:"organization_id,omitempty"`
	Admins         []string       `json:"admins,omitempty"`
	Members        []string       `json:"members,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	TPMLimit       *int           `json:"tpm_limit,omitempty"`
	RPMLimit       *int           `json:"rpm_limit,omitempty"`
	MaxBudget      *float64       `json:"max_budget,omitempty"`
	BudgetDuration string         `json:"budget_duration,omitempty"`
	Models         []string       `json:"models,omitempty"`
	Blocked        *bool          `json:"blocked,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Extra          map[string]any `json:"-"`
}

// UpdateTeamRequest is the POST /team/update request body. team_id is
// required by the OpenAPI schema.
type UpdateTeamRequest struct {
	TeamID         string         `json:"team_id"`
	TeamAlias      string         `json:"team_alias,omitempty"`
	OrganizationID string         `json:"organization_id,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	TPMLimit       *int           `json:"tpm_limit,omitempty"`
	RPMLimit       *int           `json:"rpm_limit,omitempty"`
	MaxBudget      *float64       `json:"max_budget,omitempty"`
	BudgetDuration string         `json:"budget_duration,omitempty"`
	Models         []string       `json:"models,omitempty"`
	Blocked        *bool          `json:"blocked,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Extra          map[string]any `json:"-"`
}

// DeleteTeamRequest is the POST /team/delete request body.
type DeleteTeamRequest struct {
	TeamIDs []string `json:"team_ids"`
}

// TeamListEntry is one row of a GET /v2/team/list response. Shape
// loosely typed — only fields the operator uses are explicit.
type TeamListEntry struct {
	TeamID         string          `json:"team_id"`
	TeamAlias      string          `json:"team_alias"`
	OrganizationID string          `json:"organization_id"`
	Models         []string        `json:"models,omitempty"`
	Blocked        *bool           `json:"blocked,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Raw            json.RawMessage `json:"-"`
}

// TeamListResponse is the GET /v2/team/list envelope.
type TeamListResponse struct {
	Teams      []TeamListEntry `json:"teams"`
	Total      int             `json:"total"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalPages int             `json:"total_pages"`
}

// MCPServerRequest is the POST /v1/mcp/server request body. Mirrors the
// OpenAPI schema's optional fields.
type MCPServerRequest struct {
	ServerID                  string         `json:"server_id,omitempty"`
	ServerName                string         `json:"server_name,omitempty"`
	Alias                     string         `json:"alias,omitempty"`
	Description               string         `json:"description,omitempty"`
	Transport                 string         `json:"transport,omitempty"`
	AuthType                  string         `json:"auth_type,omitempty"`
	Credentials               map[string]any `json:"credentials,omitempty"`
	URL                       string         `json:"url,omitempty"`
	SpecPath                  string         `json:"spec_path,omitempty"`
	MCPInfo                   map[string]any `json:"mcp_info,omitempty"`
	MCPAccessGroups           []string       `json:"mcp_access_groups,omitempty"`
	AllowedTools              []string       `json:"allowed_tools,omitempty"`
	ToolNameToDisplayName     map[string]any `json:"tool_name_to_display_name,omitempty"`
	ToolNameToDescription     map[string]any `json:"tool_name_to_description,omitempty"`
	ExtraHeaders              map[string]any `json:"extra_headers,omitempty"`
	StaticHeaders             map[string]any `json:"static_headers,omitempty"`
	Command                   string         `json:"command,omitempty"`
	Args                      []string       `json:"args,omitempty"`
	Env                       map[string]any `json:"env,omitempty"`
	AuthorizationURL          string         `json:"authorization_url,omitempty"`
	TokenURL                  string         `json:"token_url,omitempty"`
	RegistrationURL           string         `json:"registration_url,omitempty"`
	OAuth2Flow                string         `json:"oauth2_flow,omitempty"`
	AllowAllKeys              *bool          `json:"allow_all_keys,omitempty"`
	AvailableOnPublicInternet *bool          `json:"available_on_public_internet,omitempty"`
	Extra                     map[string]any `json:"-"`
}

// MCPServerUpdateRequest is the PUT /v1/mcp/server request body. The
// OpenAPI schema marks server_id as required.
type MCPServerUpdateRequest struct {
	ServerID                  string         `json:"server_id"`
	ServerName                string         `json:"server_name,omitempty"`
	Alias                     string         `json:"alias,omitempty"`
	Description               string         `json:"description,omitempty"`
	Transport                 string         `json:"transport,omitempty"`
	AuthType                  string         `json:"auth_type,omitempty"`
	Credentials               map[string]any `json:"credentials,omitempty"`
	URL                       string         `json:"url,omitempty"`
	SpecPath                  string         `json:"spec_path,omitempty"`
	MCPInfo                   map[string]any `json:"mcp_info,omitempty"`
	MCPAccessGroups           []string       `json:"mcp_access_groups,omitempty"`
	AllowedTools              []string       `json:"allowed_tools,omitempty"`
	ToolNameToDisplayName     map[string]any `json:"tool_name_to_display_name,omitempty"`
	ToolNameToDescription     map[string]any `json:"tool_name_to_description,omitempty"`
	ExtraHeaders              map[string]any `json:"extra_headers,omitempty"`
	StaticHeaders             map[string]any `json:"static_headers,omitempty"`
	Command                   string         `json:"command,omitempty"`
	Args                      []string       `json:"args,omitempty"`
	Env                       map[string]any `json:"env,omitempty"`
	AuthorizationURL          string         `json:"authorization_url,omitempty"`
	TokenURL                  string         `json:"token_url,omitempty"`
	RegistrationURL           string         `json:"registration_url,omitempty"`
	AllowAllKeys              *bool          `json:"allow_all_keys,omitempty"`
	AvailableOnPublicInternet *bool          `json:"available_on_public_internet,omitempty"`
	IsBYOK                    *bool          `json:"is_byok,omitempty"`
	Extra                     map[string]any `json:"-"`
}

// MCPServerEntry is one row of GET /v1/mcp/server (bare array; the
// operator wraps it in MCPServerListResponse for length-check uniformity
// with the model and agent helpers — see REL-05).
type MCPServerEntry struct {
	ServerID       string          `json:"server_id"`
	ServerName     string          `json:"server_name,omitempty"`
	Alias          string          `json:"alias,omitempty"`
	Description    string          `json:"description,omitempty"`
	URL            string          `json:"url,omitempty"`
	SpecPath       string          `json:"spec_path,omitempty"`
	Transport      string          `json:"transport"`
	AuthType       string          `json:"auth_type,omitempty"`
	Status         string          `json:"status,omitempty"`
	ApprovalStatus string          `json:"approval_status,omitempty"`
	Raw            json.RawMessage `json:"-"`
}

// MCPServerListResponse wraps the bare-array GET /v1/mcp/server response
// in a Data envelope so the per-domain length-check pattern is uniform.
type MCPServerListResponse struct {
	Data []MCPServerEntry `json:"data"`
}

// AgentConfig is the POST /v1/agents and PUT /v1/agents/{id} request
// body. agent_name and agent_card_params are required by the OpenAPI
// schema; LiteLLMParams + ObjectPermission etc. are optional.
type AgentConfig struct {
	AgentName        string         `json:"agent_name"`
	AgentCardParams  map[string]any `json:"agent_card_params"`
	LiteLLMParams    LiteLLMParams  `json:"litellm_params,omitempty"`
	ObjectPermission map[string]any `json:"object_permission,omitempty"`
	TPMLimit         *int           `json:"tpm_limit,omitempty"`
	RPMLimit         *int           `json:"rpm_limit,omitempty"`
	SessionTPMLimit  *int           `json:"session_tpm_limit,omitempty"`
	SessionRPMLimit  *int           `json:"session_rpm_limit,omitempty"`
	StaticHeaders    map[string]any `json:"static_headers,omitempty"`
	ExtraHeaders     map[string]any `json:"extra_headers,omitempty"`
	Extra            map[string]any `json:"-"`
}

// AgentEntry is one row of GET /v1/agents (bare-array response wrapped
// in AgentListResponse).
type AgentEntry struct {
	AgentID         string          `json:"agent_id"`
	AgentName       string          `json:"agent_name"`
	AgentCardParams map[string]any  `json:"agent_card_params,omitempty"`
	LiteLLMParams   LiteLLMParams   `json:"litellm_params,omitempty"`
	CreatedAt       string          `json:"created_at,omitempty"`
	UpdatedAt       string          `json:"updated_at,omitempty"`
	Raw             json.RawMessage `json:"-"`
}

// AgentListResponse wraps the bare-array GET /v1/agents response in a
// Data envelope (uniform length-check pattern).
type AgentListResponse struct {
	Data []AgentEntry `json:"data"`
}

// UserKeyInfo is one row from
// GET /key/list?user_id=<id>&return_full_object=true&include_team_keys=false.
// Used by orphan-cleanup (D-16) to enumerate all LiteLLM keys owned by an
// ACH-managed user, then cross-reference against active personal_keys
// / environment_keys rows per Hub §18.4.
//
// The Token field carries the LiteLLM-internal opaque hex token returned
// by /key/list (NOT ACH's `pkid_*` / `ekid_*` prefix; that distinction
// is the Gap G1 namespace mismatch that Phase 02.2 Plan 1 resolved).
type UserKeyInfo struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	KeyAlias  string    `json:"key_alias,omitempty"`
}

// ListUserKeysResponse is the envelope returned by
// GET /key/list?user_id=<id>&return_full_object=true&include_team_keys=false.
type ListUserKeysResponse struct {
	Keys []UserKeyInfo `json:"keys"`
}

// --- Phase 3 (Plan 03-01) ---
//
// The five request/response shapes below back the four new Client
// methods Phase 3 SSO + env-keys flows require (per Phase 3 D-25):
// UserNew, UserInfoByEmail, TeamMemberAdd, KeyGenerate.
//
// Wire-shape source: LiteLLM v1.83.10 OpenAPI document
// (spec/litellm_api.json) plus Phase 3 D-25 contract notes.

// UserNewRequest is the POST /user/new request body.
//
// LiteLLM's /user/new schema is wide; ACH only ever populates these three
// fields (Phase 3 D-25). NEVER sets max_budget — KEY-10 invariant — so
// max_budget is intentionally absent from the type. If a future plan
// needs additional fields, extend this struct rather than dropping into
// a freeform map (so the field is grep-able and CR-reviewable).
type UserNewRequest struct {
	UserEmail string   `json:"user_email"`
	UserID    string   `json:"user_id,omitempty"`
	Teams     []string `json:"teams,omitempty"`
}

// UserInfo is the /user/new response and the /user/info GET response.
//
// The Teams field is the BLK-01 contract — Plans 03-08 + 03-09 consume
// info.Teams for the §8.2 step-4 team-intersection check (KEY-11). LiteLLM
// v1.83.10 returns `teams` as a JSON array of strings on /user/info;
// `omitempty` keeps Phase 3's "nil/empty means no info" signal expressible
// (Phase 3 D-25).
type UserInfo struct {
	UserID    string   `json:"user_id"`
	UserEmail string   `json:"user_email"`
	Teams     []string `json:"teams,omitempty"`
}

// AccessGroupCreateRequest is the POST /v1/access_group request body
// (ackstorm OpenAPI schema: AccessGroupCreateRequest). access_group_name
// is the only required field; every other slice may be nil/empty. The
// endpoint returns AccessGroupResponse with the assigned UUID.
//
// Migration note (issue #17): replaces NewAccessGroupRequest. The legacy
// POST /access_group/new endpoint required at least one model_name OR
// model_id; the /v1 endpoint accepts an empty-resource creation, so the
// controller no longer needs an AwaitingModels short-circuit. Unresolved
// MCP/A2A/Team names are still surfaced via the resolver layer in
// reconcileAccessGroup (AccessGroupSynced=False reason=UnresolvedReferences).
type AccessGroupCreateRequest struct {
	AccessGroupName    string   `json:"access_group_name"`
	Description        string   `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names,omitempty"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids,omitempty"`
	AccessAgentIDs     []string `json:"access_agent_ids,omitempty"`
	AssignedTeamIDs    []string `json:"assigned_team_ids,omitempty"`
	AssignedKeyIDs     []string `json:"assigned_key_ids,omitempty"`
}

// AccessGroupUpdateRequest is the PUT /v1/access_group/{id} request body
// (ackstorm OpenAPI schema: AccessGroupUpdateRequest). Every field is
// optional; nil values are omitted, instructing the upstream to keep
// the corresponding stored value. To CLEAR a list the caller must send
// an explicit empty []string — use the json tag's non-omitempty form by
// passing a non-nil zero-length slice through the marshaler. The
// reconciler's desired-state sync always sends the full computed set
// for every dimension, so the clear-via-empty semantics rarely matter
// in practice.
type AccessGroupUpdateRequest struct {
	AccessGroupName    *string  `json:"access_group_name,omitempty"`
	Description        *string  `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names,omitempty"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids,omitempty"`
	AccessAgentIDs     []string `json:"access_agent_ids,omitempty"`
	AssignedTeamIDs    []string `json:"assigned_team_ids,omitempty"`
	AssignedKeyIDs     []string `json:"assigned_key_ids,omitempty"`
}

// AccessGroupResponse is the body returned by POST /v1/access_group,
// GET /v1/access_group, GET /v1/access_group/{id}, and PUT /v1/access_group/{id}.
// access_group_id is the stable UUID the reconciler resolves by name on
// each reconcile (per issue #17 decision: no status field, list-by-name).
type AccessGroupResponse struct {
	AccessGroupID      string   `json:"access_group_id"`
	AccessGroupName    string   `json:"access_group_name"`
	Description        string   `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids"`
	AccessAgentIDs     []string `json:"access_agent_ids"`
	AssignedTeamIDs    []string `json:"assigned_team_ids"`
	AssignedKeyIDs     []string `json:"assigned_key_ids"`
	CreatedAt          string   `json:"created_at,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
	UpdatedBy          string   `json:"updated_by,omitempty"`
}

// TeamMember is the inner object on POST /team/member_add. LiteLLM uses
// a nested {"member": {...}} shape (NOT a top-level user_id field) so
// the wire payload mirrors this struct.
type TeamMember struct {
	UserID string `json:"user_id"`
	Role   string `json:"role,omitempty"`
}

// TeamMemberAddRequest is the POST /team/member_add request body. The
// Phase 3 SSO handler (Plan 03-07) calls TeamMemberAdd("default", userID,
// "user") to enroll the SSO-resolved user in the deployment's `default`
// Team — missing-team failure on LiteLLM side bubbles as audit
// outcome=default_team_missing.
type TeamMemberAddRequest struct {
	TeamID string     `json:"team_id"`
	Member TeamMember `json:"member"`
}

// KeyGenerateRequest is the POST /key/generate request body.
//
// The Key field is the caller-supplied bearer plaintext: Phase 3 D-13
// generates `pk_*` / `ek_*` plaintext ACH-side via crypto/rand and passes
// it here so LiteLLM stores ACH's prefix in its key column (NOT a
// LiteLLM-generated `sk-*`).
//
// MaxBudget MUST be *float64 (NOT float64). The pointer encodes
// "JSON null vs not present" so Phase 3 callers can pass nil to enforce
// KEY-10 (ACH NEVER sets max_budget on first-SSO LiteLLM user creation).
// With omitempty on a nil *float64, the JSON output drops the key
// entirely — the deployer-side LiteLLM keeps its built-in default.
//
// AccessGroups carries the LiteLLM access-group name list (Phase 3 ek_
// creation passes []string{"<environment>"} — single-element slice).
//
// Metadata is a free-form pass-through bag stored verbatim on the
// LiteLLM-side key row. ACH populates it with `ach_key_id`,
// `ach_key_type`, `ach_owner_email`, and (for ek_) `ach_environment`
// so the orphan-cleanup reconciler can validate ACH ↔ LiteLLM mapping
// deterministically via `/key/list` without relying on user_id +
// creation-time heuristics.
type KeyGenerateRequest struct {
	UserID       string            `json:"user_id,omitempty"`
	Key          string            `json:"key,omitempty"`
	KeyAlias     string            `json:"key_alias,omitempty"`
	Models       []string          `json:"models,omitempty"`
	MaxBudget    *float64          `json:"max_budget,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	AccessGroups []string          `json:"access_groups,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// KeyGenerateResponse is the POST /key/generate response.
//
// FIX01 §A.6 contract: ACH does NOT supply or persist the LiteLLM
// plaintext. LiteLLM mints `Key` (sk-…) and returns it ONCE in this
// response; ACH callers discard it. Only `Token` — the stable opaque
// LiteLLM identifier — is persisted (personal_keys.litellm_token /
// environment_keys.litellm_token), and only it is used for revoke +
// orphan-cleanup attribution.
type KeyGenerateResponse struct {
	// Key — plaintext virtual key (sk-…). ONE-TIME, DO NOT STORE.
	Key string `json:"key"`
	// Token — opaque LiteLLM identifier. STORE THIS.
	Token     string `json:"token"`
	UserID    string `json:"user_id,omitempty"`
	KeyAlias  string `json:"key_alias,omitempty"`
	ExpiresAt string `json:"expires,omitempty"`
}

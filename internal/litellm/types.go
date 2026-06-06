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

// ModelInfo is the model_info sub-block embedded in GET /v1/model/info
// response entries (ModelInfoResponse).
type ModelInfo struct {
	ID                  string `json:"id"`
	DBModel             bool   `json:"db_model,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	UpdatedBy           string `json:"updated_by,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	CreatedBy           string `json:"created_by,omitempty"`
	BaseModel           string `json:"base_model,omitempty"`
	Tier                string `json:"tier,omitempty"`
	TeamID              string `json:"team_id,omitempty"`
	TeamPublicModelName string `json:"team_public_model_name,omitempty"`
}

// ModelInfoResponse is one entry in a GET /model/info response Data
// array. The OpenAPI doc is sparse here ({}); shape inferred from spike
// Probe 2 + bbdsoftware/litellm-operator's known mapping.
type ModelInfoResponse struct {
	ModelID       string        `json:"model_id"`
	ModelName     string        `json:"model_name"`
	LiteLLMParams LiteLLMParams `json:"litellm_params"`
	ModelInfo     ModelInfo     `json:"model_info"`
}

// ModelListResponse is the envelope returned by GET /model/info.
type ModelListResponse struct {
	Data []ModelInfoResponse `json:"data"`
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
}

// TeamListResponse is the GET /v2/team/list envelope.
type TeamListResponse struct {
	Teams      []TeamListEntry `json:"teams"`
	Total      int             `json:"total"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalPages int             `json:"total_pages"`
}

// MCPServerEntry is one row of GET /v1/mcp/server (bare array; the
// operator wraps it in MCPServerListResponse for length-check uniformity
// with the model and agent helpers — see REL-05).
type MCPServerEntry struct {
	ServerID       string `json:"server_id"`
	ServerName     string `json:"server_name,omitempty"`
	Alias          string `json:"alias,omitempty"`
	Description    string `json:"description,omitempty"`
	URL            string `json:"url,omitempty"`
	SpecPath       string `json:"spec_path,omitempty"`
	Transport      string `json:"transport"`
	AuthType       string `json:"auth_type,omitempty"`
	Status         string `json:"status,omitempty"`
	ApprovalStatus string `json:"approval_status,omitempty"`
}

// MCPServerListResponse wraps the bare-array GET /v1/mcp/server response
// in a Data envelope so the per-domain length-check pattern is uniform.
type MCPServerListResponse struct {
	Data []MCPServerEntry `json:"data"`
}

// AgentEntry is one row of GET /v1/agents (bare-array response wrapped
// in AgentListResponse).
type AgentEntry struct {
	AgentID         string         `json:"agent_id"`
	AgentName       string         `json:"agent_name"`
	AgentCardParams map[string]any `json:"agent_card_params,omitempty"`
	LiteLLMParams   LiteLLMParams  `json:"litellm_params,omitempty"`
	CreatedAt       string         `json:"created_at,omitempty"`
	UpdatedAt       string         `json:"updated_at,omitempty"`
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
//
// The Metadata field IS consumed by the orphan loop: return_full_object=true
// echoes LiteLLM's per-key metadata bag, and ACH-minted keys carry
// ach_key_id / ach_key_type / ach_owner_email (set at mint in sso.go +
// envkeys/handler.go). The orphan loop reads metadata.ach_key_id as the
// ownership gate — a key WITHOUT it is foreign (manual dashboard / tf-* /
// token-factory) and is NEVER revoked. The ach_key_id value is in the
// key_id namespace (pkid_* / ekid_*), so it joins against
// db.ListActiveACHKeyIDs; the opaque Token remains the revoke handle only.
type UserKeyInfo struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	KeyAlias  string    `json:"key_alias,omitempty"`
	// Metadata is LiteLLM's per-key metadata bag. LiteLLM may return
	// scalar values as strings, so this stays map[string]any and the
	// loop coerces ach_key_id via a string type assertion.
	Metadata map[string]any `json:"metadata,omitempty"`
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
	// AutoCreateKey controls LiteLLM's /user/new default-key minting.
	// nil → field omitted (LiteLLM default auto_create_key=true). ACH
	// callers pass BoolPtr(false) so the user is created WITHOUT an
	// untracked default key — the only key is the pk_/ek_ minted via
	// /key/generate. *bool keeps the tri-state (omit vs explicit false).
	AutoCreateKey *bool `json:"auto_create_key,omitempty"`
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
// (ackstorm OpenAPI schema: AccessGroupUpdateRequest).
//
// LiteLLM PUT contract (prod-verified against the running ackstorm
// LiteLLM): the update is partial — a field ABSENT from the body keeps
// the stored value, a field sent as an explicit `[]` CLEARS that
// dimension.
//
// The four managed lists (AccessModelNames, AccessMCPServerIDs,
// AccessAgentIDs, AssignedTeamIDs) deliberately have NO `omitempty`: the
// reconciler is the sole writer and always sends the full computed set
// for all four dimensions, so every reconcile is authoritative. They are
// therefore ALWAYS serialized — an empty list marshals to `[]`, which
// clears that dimension upstream. `omitempty` would drop ANY zero-length
// slice (nil OR []string{}), silently turning a "clear" into a "keep" and
// wedging convergence; callers MUST pass a non-nil `[]string{}` (not nil)
// for the empty case, since only `[]` — not `null` — is a proven clear.
//
// AccessGroupName/Description are pointers the reconciler never sends
// (genuine keep-on-absent), and AssignedKeyIDs keeps `omitempty` because
// the reconciler does NOT manage key assignment — absent must mean keep,
// never clear.
type AccessGroupUpdateRequest struct {
	AccessGroupName    *string  `json:"access_group_name,omitempty"`
	Description        *string  `json:"description,omitempty"`
	AccessModelNames   []string `json:"access_model_names"`
	AccessMCPServerIDs []string `json:"access_mcp_server_ids"`
	AccessAgentIDs     []string `json:"access_agent_ids"`
	AssignedTeamIDs    []string `json:"assigned_team_ids"`
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
// generates `pk-*` / `ek-*` plaintext ACH-side via crypto/rand and passes
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

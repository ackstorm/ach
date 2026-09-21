// SPDX-License-Identifier: Apache-2.0

package console

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/environments"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

// UserCatalog is what the Personal view reads with the user's own key
// (production: *litellm.UserView).
type UserCatalog interface {
	ListModelGroups(ctx context.Context) ([]litellm.ModelGroupInfo, error)
	ListMCPServers(ctx context.Context) ([]litellm.MCPServerEntry, error)
	ListA2AAgents(ctx context.Context) ([]litellm.AgentEntry, error)
}

type envStore interface {
	GetEnvironment(ctx context.Context, name string) (*db.EnvironmentRow, error)
}

// Deps for the console API. AsUser binds a decrypted sk- to LiteLLM
// (production: (*litellm.RESTClient).AsUser); KeyEncryptionKey opens the
// KeyContext's sealed material.
type Deps struct {
	Store            envStore
	LiteLLM          litellm.Client // environments.CallerMayRead (teams lookup)
	AsUser           func(key string) UserCatalog
	KeyEncryptionKey []byte
	OpenWorkEnabled  bool
	Audit            *slog.Logger
	Logger           *slog.Logger
}

// SuspendPropagationSeconds is the UI notice bound (spec §8.2): the
// resolver cache ceiling.
const SuspendPropagationSeconds = 60

// Mount registers /platform/console/{bootstrap,capabilities} — inside the
// Authn group, so both the cookie and a header credential reach them.
func Mount(r chi.Router, d Deps) {
	r.Get("/platform/console/bootstrap", d.bootstrap)
	r.Get("/platform/console/capabilities", d.capabilities)
}

// bootstrap: identity + console options, the first call the SPA makes.
func (d Deps) bootstrap(w http.ResponseWriter, r *http.Request) {
	kc, _ := middleware.KeyContextFromCtx(r.Context())
	render.JSON(w, http.StatusOK, map[string]any{
		"email":                       kc.OwnerEmail,
		"is_admin":                    kc.IsAdmin,
		"openwork_enabled":            d.OpenWorkEnabled,
		"suspend_propagation_seconds": SuspendPropagationSeconds,
	})
}

func (d Deps) capabilities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.RequestIDFromCtx(ctx)
	kc, _ := middleware.KeyContextFromCtx(ctx)
	if kc.KeyType != keys.PrefixPk {
		render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "console endpoints require a personal identity", reqID)
		return
	}
	switch r.URL.Query().Get("scope") {
	case "personal":
		d.personal(w, r, kc)
	case "environment":
		d.environment(w, r, kc, r.URL.Query().Get("name"))
	default:
		render.Error(w, http.StatusBadRequest, "invalid_argument", "scope must be personal or environment", reqID)
	}
}

// Public projections — explicit allow-lists (alitellm-auth
// _project_model_group / _project_mcp_server / _project_a2a_agent): no
// litellm_params, credentials, headers, OAuth URLs or *_by fields ever
// leave the server.
type mcpServerRow struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	URL          string   `json:"url"`
	Transport    string   `json:"transport"`
	AuthType     string   `json:"auth_type"`
	Status       string   `json:"status"`
	Tools        []string `json:"tools"`
	ToolCount    int      `json:"tool_count"`
	AccessGroups []string `json:"access_groups"`
}

type a2aAgentRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Transport   string   `json:"transport"`
	Version     string   `json:"version"`
	Skills      []string `json:"skills"`
	SkillCount  int      `json:"skill_count"`
	Streaming   bool     `json:"streaming"`
}

func projectMCP(e litellm.MCPServerEntry) mcpServerRow {
	name := e.Alias
	if name == "" {
		name = e.ServerName
	}
	return mcpServerRow{ID: e.ServerID, Name: name, Description: e.Description, URL: e.URL,
		Transport: e.Transport, AuthType: e.AuthType, Status: e.Status,
		Tools: orEmpty(e.AllowedTools), ToolCount: len(e.AllowedTools), AccessGroups: orEmpty(e.MCPAccessGroups)}
}

func projectA2A(e litellm.AgentEntry) a2aAgentRow {
	card := e.AgentCardParams
	str := func(k string) string { s, _ := card[k].(string); return s }
	var skills []string
	if raw, ok := card["skills"].([]any); ok {
		for _, s := range raw {
			switch v := s.(type) {
			case map[string]any:
				if n, _ := v["name"].(string); n != "" {
					skills = append(skills, n)
				} else if id, _ := v["id"].(string); id != "" {
					skills = append(skills, id)
				}
			case string:
				if v != "" {
					skills = append(skills, v)
				}
			}
		}
	}
	streaming := false
	if caps, ok := card["capabilities"].(map[string]any); ok {
		streaming, _ = caps["streaming"].(bool)
	}
	name := e.AgentName
	if name == "" {
		name = str("name")
	}
	return a2aAgentRow{ID: e.AgentID, Name: name, Description: str("description"), URL: str("url"),
		Transport: str("preferredTransport"), Version: str("version"),
		Skills: orEmpty(skills), SkillCount: len(skills), Streaming: streaming}
}

// personal: effective access as LiteLLM sees the user's own key (D-05).
// "__deny_all__" is the shell-team sentinel: alone, it means the operator
// has not yet attached the fresh ach-user shell to any access group
// (§10.2 fact 3) — reported as provisioning, never as "no models".
func (d Deps) personal(w http.ResponseWriter, r *http.Request, kc middleware.KeyContext) {
	ctx := r.Context()
	reqID := middleware.RequestIDFromCtx(ctx)
	if kc.LiteLLMKeyMaterial == nil {
		render.Error(w, http.StatusServiceUnavailable, "not_ready", "personal credential is being provisioned", reqID)
		return
	}
	sk, err := keycrypt.Open(d.KeyEncryptionKey, *kc.LiteLLMKeyMaterial)
	if err != nil {
		d.Logger.Error("console: open key material failed", "key_id", kc.KeyID, "err", err)
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
		return
	}
	u := d.AsUser(string(sk))
	groups, err := u.ListModelGroups(ctx)
	if err != nil {
		d.upstreamError(w, r, err)
		return
	}
	provisioning := true
	models := make([]litellm.ModelGroupInfo, 0, len(groups))
	for _, g := range groups {
		if g.Name == "__deny_all__" {
			continue
		}
		provisioning = false
		models = append(models, g)
	}
	mcp, err := u.ListMCPServers(ctx)
	if err != nil && !litellm.IsHTTPNotFound(err) {
		d.upstreamError(w, r, err)
		return
	}
	a2a, err := u.ListA2AAgents(ctx)
	if err != nil && !litellm.IsHTTPNotFound(err) {
		d.upstreamError(w, r, err)
		return
	}
	mcpRows := make([]mcpServerRow, 0, len(mcp))
	for _, e := range mcp {
		mcpRows = append(mcpRows, projectMCP(e))
	}
	a2aRows := make([]a2aAgentRow, 0, len(a2a))
	for _, e := range a2a {
		a2aRows = append(a2aRows, projectA2A(e))
	}
	render.JSON(w, http.StatusOK, map[string]any{
		"scope": "personal", "models": models, "mcp_servers": mcpRows, "a2a_agents": a2aRows,
		"provisioning": provisioning,
	})
}

// environment: declared runtime/context + sync status from the projection
// (D-05), behind the same read rule as GET /platform/environments/{name}.
func (d Deps) environment(w http.ResponseWriter, r *http.Request, kc middleware.KeyContext, name string) {
	ctx := r.Context()
	reqID := middleware.RequestIDFromCtx(ctx)
	if name == "" {
		render.Error(w, http.StatusBadRequest, "invalid_argument", "name is required", reqID)
		return
	}
	env, err := d.Store.GetEnvironment(ctx, name)
	if err != nil {
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "failed to read environment", reqID)
		return
	}
	if env == nil {
		render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "environment not found", reqID)
		return
	}
	ok, err := environments.CallerMayRead(ctx, environments.Deps{LiteLLM: d.LiteLLM, Audit: d.Audit}, kc, env)
	if err != nil {
		render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "upstream LiteLLM unreachable", reqID)
		return
	}
	if !ok {
		render.Error(w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam, "caller is not a member of any authorized team", reqID)
		return
	}
	v := store.RowToView(*env)
	render.JSON(w, http.StatusOK, map[string]any{
		"scope": "environment", "name": v.Name, "status": v.Status, "description": v.Description,
		"runtime": v.Runtime, "context": v.Context, "conditions": v.Conditions,
	})
}

// upstreamError: a LiteLLM refusal of the USER key is 502 litellm_rejected,
// a transport failure 503 — and never a retry with the master key (§10.1).
func (d Deps) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	var a *litellm.Auth401Error
	var api *litellm.APIError
	if errors.As(err, &a) || (errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode < 500) {
		render.Error(w, http.StatusBadGateway, audit.OutcomeLitellmRejected, "LiteLLM refused the personal credential", reqID)
		return
	}
	render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "upstream LiteLLM unreachable", reqID)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

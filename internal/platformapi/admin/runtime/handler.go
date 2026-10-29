// SPDX-License-Identifier: Apache-2.0

// Package runtime serves the admin-only runtime catalog (models / MCP servers
// / A2A agents / teams / guardrails) projected from LiteLLM into
// runtime_catalog_entries. All routes mount under /platform/admin and inherit
// admin.AdminOnly (pk_ + allowlist).
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// CatalogReader is the read surface this package needs. The concrete impl is
// poolCatalog (backed by *pgxpool.Pool); tests inject a fake.
type CatalogReader interface {
	List(ctx context.Context, ns, connector, kind string) ([]db.RuntimeCatalogRow, error)
	MaxSync(ctx context.Context, ns, connector string) (time.Time, bool, error)
}

// Deps configures the runtime catalog handlers.
type Deps struct {
	Catalog   CatalogReader
	Namespace string
	Connector string
}

// NewPoolCatalog adapts a pgxpool.Pool to CatalogReader.
func NewPoolCatalog(pool *pgxpool.Pool) CatalogReader { return poolCatalog{pool: pool} }

type poolCatalog struct{ pool *pgxpool.Pool }

func (p poolCatalog) List(ctx context.Context, ns, connector, kind string) ([]db.RuntimeCatalogRow, error) {
	return db.ListRuntimeCatalog(ctx, p.pool, ns, connector, kind)
}
func (p poolCatalog) MaxSync(ctx context.Context, ns, connector string) (time.Time, bool, error) {
	return db.MaxRuntimeCatalogSync(ctx, p.pool, ns, connector)
}

type itemView struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// Attributes is kind-specific JSON, present for guardrails only today
	// (mode, defaultOn). omitempty keeps the four pre-existing kinds'
	// responses byte-identical, so no consumer breaks.
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

type connectorView struct {
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	Status             string  `json:"status"`
	LastSuccessfulSync *string `json:"lastSuccessfulSync"`
}

func (d Deps) connector(ctx context.Context) connectorView {
	cv := connectorView{Name: d.Connector, Type: "litellm", Status: "missing"}
	if ts, ok, err := d.Catalog.MaxSync(ctx, d.Namespace, d.Connector); err == nil && ok {
		cv.Status = "active"
		s := ts.UTC().Format(time.RFC3339)
		cv.LastSuccessfulSync = &s
	}
	return cv
}

func toItems(rows []db.RuntimeCatalogRow) []itemView {
	out := make([]itemView, 0, len(rows))
	for _, r := range rows {
		out = append(out, itemView{
			Name: r.Name, Kind: r.Kind, Status: r.Status,
			Attributes: json.RawMessage(r.Attributes),
		})
	}
	return out
}

func kindHandler(d Deps, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		rows, err := d.Catalog.List(ctx, d.Namespace, d.Connector, kind)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read runtime catalog", reqID)
			return
		}
		render.JSON(w, http.StatusOK, map[string]any{
			"connector":   d.connector(ctx),
			"items":       toItems(rows),
			"next_cursor": nil,
		})
	}
}

// ModelsHandler serves GET /platform/admin/runtime/models.
func ModelsHandler(d Deps) http.HandlerFunc { return kindHandler(d, "model") }

// MCPServersHandler serves GET /platform/admin/runtime/mcp-servers.
func MCPServersHandler(d Deps) http.HandlerFunc { return kindHandler(d, "mcp_server") }

// A2AAgentsHandler serves GET /platform/admin/runtime/a2a-agents.
func A2AAgentsHandler(d Deps) http.HandlerFunc { return kindHandler(d, "a2a_agent") }

// TeamsHandler serves GET /platform/admin/runtime/teams.
func TeamsHandler(d Deps) http.HandlerFunc { return kindHandler(d, "team") }

// GuardrailsHandler serves GET /platform/admin/runtime/guardrails.
func GuardrailsHandler(d Deps) http.HandlerFunc { return kindHandler(d, "guardrail") }

// CatalogHandler serves GET /platform/admin/runtime/catalog (all kinds).
func CatalogHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		all, err := d.Catalog.List(ctx, d.Namespace, d.Connector, "")
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read runtime catalog", reqID)
			return
		}
		models, mcps, agents, teams, guardrails :=
			make([]itemView, 0), make([]itemView, 0), make([]itemView, 0), make([]itemView, 0), make([]itemView, 0)
		for _, it := range toItems(all) {
			switch it.Kind {
			case "model":
				models = append(models, it)
			case "mcp_server":
				mcps = append(mcps, it)
			case "a2a_agent":
				agents = append(agents, it)
			case "team":
				teams = append(teams, it)
			case "guardrail":
				guardrails = append(guardrails, it)
			}
		}
		render.JSON(w, http.StatusOK, map[string]any{
			"connector":  d.connector(ctx),
			"models":     models,
			"mcpServers": mcps,
			"a2aAgents":  agents,
			"teams":      teams,
			"guardrails": guardrails,
		})
	}
}

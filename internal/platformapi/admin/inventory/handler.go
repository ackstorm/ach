// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// Pagination bounds mirror internal/platformapi/environments (Hub §15.5): an
// opaque base64-of-decimal offset cursor over the in-memory slice.
const (
	defaultLimit = 100
	maxLimit     = 500
)

// Lister is the read seam the inventory handlers consume. Production wires the
// internal/db List* helpers (NewLister); tests inject a fake. Defining the seam
// in this package — rather than depending on admin.Deps — also breaks the
// admin → inventory → admin import cycle (admin.Mount constructs a Lister and
// hands it back in).
type Lister interface {
	Plugins(ctx context.Context) ([]db.PluginRow, error)
	Prompts(ctx context.Context) ([]db.PromptRow, error)
	Artifacts(ctx context.Context) ([]db.ArtifactRow, error)
	Marketplaces(ctx context.Context) ([]db.MarketplaceRow, error)
	MarketplacePlugins(ctx context.Context) ([]db.MarketplacePlugin, error)
	BIPs(ctx context.Context) ([]db.BIPRow, error)
	LitellmConnections(ctx context.Context) ([]db.LiteLLMConnectionRow, error)
	ExternalRefs(ctx context.Context) ([]db.ExternalRef, error)
}

// Deps is the inventory handler dependency bag. Kept deliberately narrow (just
// the read seam) so it carries no k8s/LiteLLM surface — these endpoints are a
// pure Postgres projection read.
type Deps struct {
	Lister Lister
}

// dbLister is the production Lister: each method binds the operator namespace
// (POD_NAMESPACE) and pool into the matching internal/db List helper.
type dbLister struct {
	pool *pgxpool.Pool
	ns   string
}

// NewLister returns the production Lister bound to pool + namespace.
func NewLister(pool *pgxpool.Pool, ns string) Lister { return dbLister{pool: pool, ns: ns} }

func (l dbLister) Plugins(ctx context.Context) ([]db.PluginRow, error) {
	return db.ListPlugins(ctx, l.pool, l.ns)
}
func (l dbLister) Prompts(ctx context.Context) ([]db.PromptRow, error) {
	return db.ListPrompts(ctx, l.pool, l.ns)
}
func (l dbLister) Artifacts(ctx context.Context) ([]db.ArtifactRow, error) {
	return db.ListArtifacts(ctx, l.pool, l.ns)
}
func (l dbLister) Marketplaces(ctx context.Context) ([]db.MarketplaceRow, error) {
	return db.ListMarketplaces(ctx, l.pool, l.ns)
}
func (l dbLister) MarketplacePlugins(ctx context.Context) ([]db.MarketplacePlugin, error) {
	return db.ListAllMarketplacePlugins(ctx, l.pool)
}
func (l dbLister) BIPs(ctx context.Context) ([]db.BIPRow, error) {
	return db.ListAllBIPs(ctx, l.pool, l.ns)
}
func (l dbLister) LitellmConnections(ctx context.Context) ([]db.LiteLLMConnectionRow, error) {
	return db.ListLitellmConnections(ctx, l.pool, l.ns)
}
func (l dbLister) ExternalRefs(ctx context.Context) ([]db.ExternalRef, error) {
	return db.ListExternalRefs(ctx, l.pool)
}

// PluginsHandler serves GET /platform/admin/plugins. It MERGES standalone
// Plugin CRs (source=plugin) with the plugins discovered inside marketplaces
// (source=marketplace, name scoped as <plugin>@<marketplace>).
func PluginsHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		plugins, err := deps.Lister.Plugins(ctx)
		if err != nil {
			return nil, err
		}
		mktPlugins, err := deps.Lister.MarketplacePlugins(ctx)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		out := make([]AdminObjectView, 0, len(plugins)+len(mktPlugins))
		for _, r := range plugins {
			out = append(out, pluginRowToView(r, now))
		}
		for _, r := range mktPlugins {
			out = append(out, marketplacePluginAsPluginView(r, now))
		}
		return out, nil
	})
}

// PromptsHandler serves GET /platform/admin/prompts.
func PromptsHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		rows, err := deps.Lister.Prompts(ctx)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		out := make([]AdminObjectView, 0, len(rows))
		for _, r := range rows {
			out = append(out, promptRowToView(r, now))
		}
		return out, nil
	})
}

// ArtifactsHandler serves GET /platform/admin/artifacts.
func ArtifactsHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		rows, err := deps.Lister.Artifacts(ctx)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		out := make([]AdminObjectView, 0, len(rows))
		for _, r := range rows {
			out = append(out, artifactRowToView(r, now))
		}
		return out, nil
	})
}

// MarketplacesHandler serves GET /platform/admin/marketplaces — the marketplace
// OBJECTS (and their Synced status), NOT their contained plugins (those are in
// PluginsHandler's merge).
func MarketplacesHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		rows, err := deps.Lister.Marketplaces(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]AdminObjectView, 0, len(rows))
		for _, r := range rows {
			out = append(out, marketplaceRowToView(r))
		}
		return out, nil
	})
}

// BIPsHandler serves GET /platform/admin/bips.
func BIPsHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		rows, err := deps.Lister.BIPs(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]AdminObjectView, 0, len(rows))
		for _, r := range rows {
			out = append(out, bipRowToView(r))
		}
		return out, nil
	})
}

// LitellmConnectionsHandler serves GET /platform/admin/litellm-connections.
func LitellmConnectionsHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		rows, err := deps.Lister.LitellmConnections(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]AdminObjectView, 0, len(rows))
		for _, r := range rows {
			out = append(out, litellmConnToView(r))
		}
		return out, nil
	})
}

// ExternalRefsHandler serves GET /platform/admin/external-refs.
func ExternalRefsHandler(deps Deps) http.HandlerFunc {
	return listHandler(func(ctx context.Context) ([]AdminObjectView, error) {
		rows, err := deps.Lister.ExternalRefs(ctx)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		out := make([]AdminObjectView, 0, len(rows))
		for _, r := range rows {
			out = append(out, externalRefToView(r, now))
		}
		return out, nil
	})
}

// listHandler is the shared glue every per-kind handler wraps: run the
// row-fetch+map closure, surface a fetch error as 500 internal_error, else
// paginate the views and render the {items, next_cursor} envelope.
func listHandler(build func(ctx context.Context) ([]AdminObjectView, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)

		items, err := build(ctx)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to list inventory", reqID)
			return
		}
		paginate(w, r, reqID, items)
	}
}

// paginate slices items by the ?limit (default 100, cap 500) + ?cursor
// (base64-of-decimal offset) query parameters and renders the standard
// envelope. Mirrors internal/platformapi/environments.ListHandler verbatim so
// the CLI cursor loop is uniform across endpoints.
func paginate(w http.ResponseWriter, r *http.Request, reqID string, items []AdminObjectView) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxLimit {
			render.Error(w, http.StatusBadRequest, "invalid_argument",
				"limit must be a positive integer no greater than "+strconv.Itoa(maxLimit), reqID)
			return
		}
		limit = n
	}

	offset := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			render.Error(w, http.StatusBadRequest, "invalid_argument",
				"cursor must be a valid base64-encoded value", reqID)
			return
		}
		n, err := strconv.Atoi(string(decoded))
		if err != nil || n < 0 {
			render.Error(w, http.StatusBadRequest, "invalid_argument",
				"cursor must decode to a non-negative integer", reqID)
			return
		}
		offset = n
	}

	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := items[offset:end]

	var nextCursor any
	if end < total {
		nextCursor = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	} else {
		nextCursor = nil
	}

	render.JSON(w, http.StatusOK, map[string]any{
		"items":       page,
		"next_cursor": nextCursor,
	})
}

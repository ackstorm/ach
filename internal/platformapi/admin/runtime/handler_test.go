// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

type fakeCatalog struct {
	rows    []db.RuntimeCatalogRow
	maxSync time.Time
	hasSync bool
	err     error
}

func (f fakeCatalog) List(_ context.Context, _, _, kind string) ([]db.RuntimeCatalogRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []db.RuntimeCatalogRow
	for _, r := range f.rows {
		if kind == "" || r.Kind == kind {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f fakeCatalog) MaxSync(_ context.Context, _, _ string) (time.Time, bool, error) {
	return f.maxSync, f.hasSync, f.err
}

func TestModelsHandler_ShapesEnvelope(t *testing.T) {
	now := time.Now()
	cat := fakeCatalog{
		rows: []db.RuntimeCatalogRow{
			{Kind: "model", Name: "gpt-4o", Status: "active"},
			{Kind: "model", Name: "old-vision", Status: "missing"},
			{Kind: "mcp_server", Name: "github", Status: "active"},
		},
		maxSync: now, hasSync: true,
	}
	h := ModelsHandler(Deps{Catalog: cat, Namespace: "ach-system", Connector: "default"})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/platform/admin/runtime/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}

	var body struct {
		Connector struct {
			Name, Type, Status string `json:"-"`
		} `json:"connector"`
		Items []struct {
			Name, Kind, Status string
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items: got %d want 2 (models only)", len(body.Items))
	}
}

func TestCatalogHandler_EmptyConnectorIsActiveEmpty(t *testing.T) {
	h := CatalogHandler(Deps{Catalog: fakeCatalog{hasSync: false}, Namespace: "ach-system", Connector: "default"})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/platform/admin/runtime/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	var body struct {
		Models     []map[string]any `json:"models"`
		MCPServers []map[string]any `json:"mcpServers"`
		A2AAgents  []map[string]any `json:"a2aAgents"`
		Teams      []map[string]any `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Models == nil || body.MCPServers == nil || body.A2AAgents == nil || body.Teams == nil {
		t.Fatalf("empty categories must serialize as [] not null: %s", rec.Body.String())
	}
}

func TestTeamsHandler_ShapesEnvelope(t *testing.T) {
	now := time.Now()
	cat := fakeCatalog{
		rows: []db.RuntimeCatalogRow{
			{Kind: "team", Name: "default", Status: "active"},
			{Kind: "team", Name: "old-team", Status: "missing"},
			{Kind: "model", Name: "gpt-4o", Status: "active"},
		},
		maxSync: now, hasSync: true,
	}
	h := TeamsHandler(Deps{Catalog: cat, Namespace: "ach-system", Connector: "default"})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/platform/admin/runtime/teams", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}

	var body struct {
		Items []struct {
			Name, Kind, Status string
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items: got %d want 2 (teams only)", len(body.Items))
	}
}

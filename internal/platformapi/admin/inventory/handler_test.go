// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// fakeLister implements Lister for httptest handler coverage without Postgres.
type fakeLister struct {
	plugins            []db.PluginRow
	marketplaces       []db.MarketplaceRow
	marketplacePlugins []db.MarketplacePlugin
	bips               []db.BIPRow
	err                error
}

func (f fakeLister) Plugins(context.Context) ([]db.PluginRow, error) { return f.plugins, f.err }
func (f fakeLister) Prompts(context.Context) ([]db.PromptRow, error) { return nil, f.err }
func (f fakeLister) Artifacts(context.Context) ([]db.ArtifactRow, error) {
	return nil, f.err
}
func (f fakeLister) Marketplaces(context.Context) ([]db.MarketplaceRow, error) {
	return f.marketplaces, f.err
}
func (f fakeLister) MarketplacePlugins(context.Context) ([]db.MarketplacePlugin, error) {
	return f.marketplacePlugins, f.err
}
func (f fakeLister) BIPs(context.Context) ([]db.BIPRow, error) { return f.bips, f.err }
func (f fakeLister) LitellmConnections(context.Context) ([]db.LiteLLMConnectionRow, error) {
	return nil, f.err
}
func (f fakeLister) ExternalRefs(context.Context) ([]db.ExternalRef, error) {
	return nil, f.err
}

type envelope struct {
	Items      []AdminObjectView `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func doReq(t *testing.T, h http.HandlerFunc, target string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	var env envelope
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode body: %v (body=%s)", err, rec.Body.String())
		}
	}
	return rec, env
}

// TestPluginsHandler_MapsAndEnvelopes: rows map to views and the standard
// envelope is emitted with next_cursor null on the last page.
func TestPluginsHandler_MapsAndEnvelopes(t *testing.T) {
	now := time.Now()
	deps := Deps{Lister: fakeLister{plugins: []db.PluginRow{
		{Namespace: "ach", Name: "alpha", ResourceVersion: "1",
			LastSuccessfulRefresh: ptrTime(now.Add(-time.Minute)), MaxStalenessSeconds: 600, UpdatedAt: now},
		{Namespace: "ach", Name: "bravo", ResourceVersion: "2", MaxStalenessSeconds: 600, UpdatedAt: now},
	}}}

	rec, env := doReq(t, PluginsHandler(deps), "/platform/admin/plugins")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(env.Items) != 2 {
		t.Fatalf("items: got %d, want 2", len(env.Items))
	}
	if env.Items[0].Name != "alpha" || env.Items[0].Kind != "plugin" {
		t.Errorf("item0: %+v", env.Items[0])
	}
	if env.Items[0].Sync != syncFresh {
		t.Errorf("item0 sync: got %q, want fresh", env.Items[0].Sync)
	}
	if env.Items[1].Sync != syncNever {
		t.Errorf("item1 sync: got %q, want never (nil last refresh)", env.Items[1].Sync)
	}
	if env.NextCursor != nil {
		t.Errorf("next_cursor: got %v, want nil", *env.NextCursor)
	}
}

// TestPaginate_CursorWalk: limit=1 yields a next_cursor; following it returns
// the next item; the final page nulls the cursor.
func TestPaginate_CursorWalk(t *testing.T) {
	now := time.Now()
	deps := Deps{Lister: fakeLister{plugins: []db.PluginRow{
		{Namespace: "ach", Name: "a", ResourceVersion: "1", MaxStalenessSeconds: 600, UpdatedAt: now},
		{Namespace: "ach", Name: "b", ResourceVersion: "2", MaxStalenessSeconds: 600, UpdatedAt: now},
		{Namespace: "ach", Name: "c", ResourceVersion: "3", MaxStalenessSeconds: 600, UpdatedAt: now},
	}}}
	h := PluginsHandler(deps)

	rec, env := doReq(t, h, "/platform/admin/plugins?limit=1")
	if rec.Code != http.StatusOK || len(env.Items) != 1 || env.Items[0].Name != "a" {
		t.Fatalf("page1 wrong: code=%d items=%+v", rec.Code, env.Items)
	}
	if env.NextCursor == nil {
		t.Fatal("page1 next_cursor: got nil, want offset cursor")
	}
	// cursor should decode to offset 1.
	dec, _ := base64.StdEncoding.DecodeString(*env.NextCursor)
	if string(dec) != "1" {
		t.Errorf("cursor decode: got %q, want 1", string(dec))
	}

	_, env2 := doReq(t, h, "/platform/admin/plugins?limit=1&cursor="+*env.NextCursor)
	if len(env2.Items) != 1 || env2.Items[0].Name != "b" {
		t.Fatalf("page2 wrong: %+v", env2.Items)
	}

	// Walk to the last page.
	_, env3 := doReq(t, h, "/platform/admin/plugins?limit=2&cursor="+base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(1))))
	if len(env3.Items) != 2 || env3.NextCursor != nil {
		t.Errorf("last page wrong: items=%d cursor=%v", len(env3.Items), env3.NextCursor)
	}
}

// TestPaginate_BadLimit: non-numeric / out-of-range limit → 400.
func TestPaginate_BadLimit(t *testing.T) {
	deps := Deps{Lister: fakeLister{}}
	for _, q := range []string{"limit=0", "limit=-1", "limit=abc", "limit=99999"} {
		rec, _ := doReq(t, PluginsHandler(deps), "/platform/admin/plugins?"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, rec.Code)
		}
	}
}

// TestPaginate_BadCursor: undecodable cursor → 400.
func TestPaginate_BadCursor(t *testing.T) {
	deps := Deps{Lister: fakeLister{}}
	for _, q := range []string{"cursor=!!notbase64", "cursor=" + base64.StdEncoding.EncodeToString([]byte("-5"))} {
		rec, _ := doReq(t, PluginsHandler(deps), "/platform/admin/plugins?"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, rec.Code)
		}
	}
}

// TestListError_500: a Lister error surfaces as 500 internal_error.
func TestListError_500(t *testing.T) {
	deps := Deps{Lister: fakeLister{err: errors.New("db down")}}
	rec, _ := doReq(t, PluginsHandler(deps), "/platform/admin/plugins")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}

// TestBIPsHandler_Projected: a projected kind maps Extra + sync=projected.
func TestBIPsHandler_Projected(t *testing.T) {
	deps := Deps{Lister: fakeLister{bips: []db.BIPRow{
		{Namespace: "ach", Name: "p1", TargetKind: "MCPServer", TargetName: "echo",
			ForwardIdentityJWT: true, ObservedGeneration: 2, Origin: "cr", Locked: true},
	}}}
	rec, env := doReq(t, BIPsHandler(deps), "/platform/admin/bips")
	if rec.Code != http.StatusOK || len(env.Items) != 1 {
		t.Fatalf("code=%d items=%+v", rec.Code, env.Items)
	}
	got := env.Items[0]
	if got.Sync != syncProjected || got.Version != "2" || got.Extra["target"] != "MCPServer/echo" {
		t.Errorf("bip view wrong: %+v", got)
	}
}

func TestPluginsHandler_MergesStandaloneAndMarketplacePlugins(t *testing.T) {
	deps := Deps{Lister: fakeLister{
		plugins: []db.PluginRow{{Namespace: "ach", Name: "frontend-design", ResourceVersion: "5", MaxStalenessSeconds: 600}},
		marketplacePlugins: []db.MarketplacePlugin{
			{MarketplaceName: "ackstorm", Name: "branding", UpstreamRev: "b899e89", MaxStalenessSeconds: 600},
		},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/platform/admin/plugins", nil)
	PluginsHandler(deps).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "frontend-design") || !strings.Contains(body, "branding@ackstorm") {
		t.Errorf("merged plugins body missing expected names: %s", body)
	}
}

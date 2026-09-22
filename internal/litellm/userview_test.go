// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
)

func TestUserView_UsesTheUserKeyNotTheMaster(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/model_group/info":
			_, _ = w.Write([]byte(`{"data":[
			  {"model_group":"no-default-models","providers":[]},
			  {"model_group":"demo-model","providers":["openai"],"mode":"chat","max_input_tokens":128000.0,
			   "input_cost_per_token":1e-06,"supports_vision":true,"litellm_params":{"api_key":"SECRET"}}]}`))
		case "/v1/mcp/server":
			_, _ = w.Write([]byte(`[{"server_id":"s1","server_name":"demo-mcp","alias":"demo","transport":"http","allowed_tools":["a","b"],"credentials":{"x":"SECRET"}}]`))
		case "/v1/agents":
			_, _ = w.Write([]byte(`{"data":[{"agent_id":"a1","agent_name":"demo-agent","agent_card_params":{"url":"http://x","skills":[{"name":"s"}],"capabilities":{"streaming":true}}}]}`))
		case "/v1/model/info":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"m"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
	u := c.AsUser("sk-user")

	groups, err := u.ListModelGroups(context.Background())
	if err != nil || len(groups) != 2 || groups[1].Name != "demo-model" || !groups[1].SupportsVision || groups[1].Mode == nil || *groups[1].Mode != "chat" {
		t.Fatalf("%+v %v", groups, err)
	}
	if groups[0].Providers == nil {
		t.Fatal("providers must be [] not null")
	}
	mcp, err := u.ListMCPServers(context.Background())
	if err != nil || len(mcp) != 1 || mcp[0].Alias != "demo" || len(mcp[0].AllowedTools) != 2 {
		t.Fatalf("%+v %v", mcp, err)
	}
	a2a, err := u.ListA2AAgents(context.Background())
	if err != nil || len(a2a) != 1 || a2a[0].AgentName != "demo-agent" {
		t.Fatalf("%+v %v", a2a, err)
	}
	for _, h := range seen {
		if h != "Bearer sk-user" {
			t.Fatalf("user read sent %q", h)
		}
	}
	// The shared client still speaks as the master.
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen[len(seen)-1] != "Bearer sk-master" {
		t.Fatalf("master call sent %q", seen[len(seen)-1])
	}
}

func TestUserView_EmptyCatalogsAreEmptyNotErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/mcp/server":
			_, _ = w.Write([]byte(`[]`))
		case "/v1/agents":
			_, _ = w.Write([]byte(`{"agents":[]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")
	if g, err := u.ListModelGroups(context.Background()); err != nil || len(g) != 0 {
		t.Fatalf("%v %v", g, err)
	}
	if m, err := u.ListMCPServers(context.Background()); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
	if a, err := u.ListA2AAgents(context.Background()); err != nil || len(a) != 0 {
		t.Fatalf("%v %v", a, err)
	}
}

func TestUserView_RefusalIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid proxy server token passed"}}`))
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")
	_, err := u.ListModelGroups(context.Background())
	var a *Auth401Error
	if err == nil || !errors.As(err, &a) {
		t.Fatalf("want *Auth401Error, got %v", err)
	}
}

func TestUserView_DailyActivity_PagesAndSumsExceptTotalPages(t *testing.T) {
	var reqs []*http.Request
	pages := []string{
		`{"results":[{"date":"2026-09-19"},{"date":"2026-09-20"}],
		  "metadata":{"total_api_requests":5,"total_pages":1,"has_more":true}}`,
		`{"results":[{"date":"2026-09-20"}],
		  "metadata":{"total_api_requests":3,"total_pages":2,"has_more":true}}`,
		`{"results":[{"date":"2026-09-21"}],
		  "metadata":{"total_api_requests":2,"total_pages":3,"has_more":false}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r)
		page := r.URL.Query().Get("page")
		idx := map[string]int{"1": 0, "2": 1, "3": 2}[page]
		_, _ = w.Write([]byte(pages[idx]))
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")

	got, err := u.DailyActivity(context.Background(), "2026-09-19", "2026-09-21")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 4 {
		t.Fatalf("results = %d, want 4 (day split across pages 1+2)", len(got.Results))
	}
	total, ok := got.Metadata["total_api_requests"].(json.Number)
	if !ok {
		t.Fatalf("total_api_requests type = %T", got.Metadata["total_api_requests"])
	}
	if f, _ := total.Float64(); f != 10 {
		t.Fatalf("total_api_requests = %v, want 10 (5+3+2 summed across pages)", f)
	}
	if tp, _ := got.Metadata["total_pages"].(json.Number).Float64(); tp != 3 {
		t.Fatalf("total_pages = %v, want 3 (last-page value, NOT summed)", tp)
	}
	if hm, ok := got.Metadata["has_more"].(bool); !ok || hm {
		t.Fatalf("has_more = %v, want false (last page, not summed/coerced)", got.Metadata["has_more"])
	}
	if len(reqs) != 3 {
		t.Fatalf("issued %d requests, want 3 (stopped at has_more=false)", len(reqs))
	}
	for i, r := range reqs {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-user" {
			t.Fatalf("request %d auth = %q, want Bearer sk-user", i, got)
		}
		q := r.URL.Query()
		if q.Get("start_date") != "2026-09-19" || q.Get("end_date") != "2026-09-21" || q.Get("page_size") != "100" {
			t.Fatalf("request %d params = %v", i, q)
		}
	}
}

func TestUserView_SpendLogsV2_CapsPagesAndReportsTruncated(t *testing.T) {
	var reqs []*http.Request
	row := func(i int) map[string]any {
		return map[string]any{"status": "success", "model": "demo-model", "startTime": fmt.Sprintf("2026-09-2%dT00:00:00Z", i%10)}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r)
		rows := make([]map[string]any, 100)
		for i := range rows {
			rows[i] = row(i)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "total_pages": 7})
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")

	rows, truncated, err := u.SpendLogsV2(context.Background(), "2026-09-20", "2026-09-23", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 500 {
		t.Fatalf("rows = %d, want 500 (5 pages x 100)", len(rows))
	}
	if !truncated {
		t.Fatal("truncated = false, want true (total_pages=7 > maxPages=5)")
	}
	if len(reqs) != 5 {
		t.Fatalf("issued %d requests, want 5 (capped at maxPages)", len(reqs))
	}
	for i, r := range reqs {
		q := r.URL.Query()
		if q.Get("sort_by") != "startTime" || q.Get("sort_order") != "desc" {
			t.Fatalf("request %d sort params = %v", i, q)
		}
		if q.Get("end_date") != "2026-09-23" {
			t.Fatalf("request %d end_date = %q, want the caller's value verbatim (no +1 day inside this method)", i, q.Get("end_date"))
		}
	}
}

func TestUserView_SpendLogsV2_NoDataListIsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")
	rows, truncated, err := u.SpendLogsV2(context.Background(), "2026-09-20", "2026-09-23", 5)
	if err != nil || len(rows) != 0 || truncated {
		t.Fatalf("rows=%v truncated=%v err=%v", rows, truncated, err)
	}
}

func TestUserView_SpendLogsV2_401IsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Only proxy admin ... Your role=unknown"}}`))
	}))
	defer srv.Close()
	u := NewRESTClient(srv.URL, "sk-master", logr.Discard()).AsUser("sk-user")
	_, _, err := u.SpendLogsV2(context.Background(), "2026-09-20", "2026-09-23", 5)
	var a *Auth401Error
	if err == nil || !errors.As(err, &a) {
		t.Fatalf("want *Auth401Error, got %v", err)
	}
}

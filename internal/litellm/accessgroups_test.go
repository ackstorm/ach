// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// TestCreateAccessGroup_HappyPath asserts the POST /access_group/new
// wire shape: path, method, body.access_group, body.model_names.
func TestCreateAccessGroup_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody NewAccessGroupRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_group":"demo"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.CreateAccessGroup(context.Background(), "demo", []string{"gpt-4"}); err != nil {
		t.Fatalf("CreateAccessGroup: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/access_group/new" {
		t.Errorf("wire: want POST /access_group/new, got %s %s", gotMethod, gotPath)
	}
	if gotBody.AccessGroup != "demo" {
		t.Errorf("body.access_group = %q; want demo", gotBody.AccessGroup)
	}
	if len(gotBody.ModelNames) != 1 || gotBody.ModelNames[0] != "gpt-4" {
		t.Errorf("body.model_names = %v; want [gpt-4]", gotBody.ModelNames)
	}
}

// TestCreateAccessGroup_AlreadyExists_ReturnsSentinel asserts the
// idempotent-success branch: LiteLLM 400 "already exists" → ErrAlreadyExists.
func TestCreateAccessGroup_AlreadyExists_ReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"access group already exists","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	err := c.CreateAccessGroup(context.Background(), "demo", nil)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateAccessGroup err = %v; want ErrAlreadyExists", err)
	}
}

// TestBindTeamToAccessGroup_Idempotent_NoUpstreamUpdate asserts that
// when the team's models[] already contains "access_group/<name>", no
// POST /team/update fires.
func TestBindTeamToAccessGroup_Idempotent_NoUpstreamUpdate(t *testing.T) {
	var updateCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/team/info"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"team_info":{"team_id":"t-1","models":["access_group/demo","gpt-4"]}}`))
		case r.Method == "POST" && r.URL.Path == "/team/update":
			updateCalls++
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.BindTeamToAccessGroup(context.Background(), "demo", "t-1"); err != nil {
		t.Fatalf("BindTeamToAccessGroup: %v", err)
	}
	if updateCalls != 0 {
		t.Errorf("/team/update calls = %d; want 0 (idempotent — already bound)", updateCalls)
	}
}

// TestBindTeamToAccessGroup_AppendsToExistingModels asserts the
// "team has other models, add the access_group/ entry" path.
func TestBindTeamToAccessGroup_AppendsToExistingModels(t *testing.T) {
	var updBody UpdateTeamRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/team/info"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"team_info":{"team_id":"t-1","models":["gpt-4","claude-3"]}}`))
		case r.Method == "POST" && r.URL.Path == "/team/update":
			_ = json.NewDecoder(r.Body).Decode(&updBody)
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.BindTeamToAccessGroup(context.Background(), "demo", "t-1"); err != nil {
		t.Fatalf("BindTeamToAccessGroup: %v", err)
	}
	if updBody.TeamID != "t-1" {
		t.Errorf("update.team_id = %q; want t-1", updBody.TeamID)
	}
	if len(updBody.Models) != 3 {
		t.Fatalf("update.models length = %d; want 3 (gpt-4, claude-3, access_group/demo)", len(updBody.Models))
	}
	want := "access_group/demo"
	found := false
	for _, m := range updBody.Models {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Errorf("update.models = %v; missing %q", updBody.Models, want)
	}
}

// TestListAccessGroupBindings_FiltersByPrefix asserts pagination + the
// "access_group/<name>" filter.
func TestListAccessGroupBindings_FiltersByPrefix(t *testing.T) {
	page1 := `{
		"teams":[
			{"team_id":"t-1","models":["access_group/demo"]},
			{"team_id":"t-2","models":["gpt-4"]},
			{"team_id":"t-3","models":["access_group/other","access_group/demo"]}
		],
		"total":3,"page":1,"page_size":200,"total_pages":1
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v2/team/list") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, page1)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.ListAccessGroupBindings(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListAccessGroupBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d bindings; want 2 (t-1, t-3)", len(got))
	}
	wantSet := map[string]bool{"t-1": true, "t-3": true}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("unexpected team_id %q", id)
		}
	}
}

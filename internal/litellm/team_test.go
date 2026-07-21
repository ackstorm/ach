// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// TestListTeamsByAliasExactMatchFilter — §6.7 client-side exact-match
// filter. LiteLLM's server-side filter is partial; the operator MUST
// drop non-exact matches.
func TestListTeamsByAliasExactMatchFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// Partial server-side match — includes alpha, alpha-beta, alpha-prod.
		_, _ = w.Write([]byte(`{"teams":[
			{"team_id":"t1","team_alias":"alpha"},
			{"team_id":"t2","team_alias":"alpha-beta"},
			{"team_id":"t3","team_alias":"alpha-prod"}
		],"total":3,"page":1,"page_size":100,"total_pages":1}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListTeamsByAlias(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ListTeamsByAlias: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("exact-match filter failed: want 1, got %d (%+v)", len(got), got)
	}
	if got[0].TeamID != "t1" || got[0].TeamAlias != "alpha" {
		t.Errorf("wrong entry kept: %+v", got[0])
	}
}

// TestListTeamsByAliasEmptyOK — empty list is NOT ErrNotFound for the
// team helper; callers decide whether absence is an error (per §6.7).
func TestListTeamsByAliasEmptyOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"teams":[],"total":0,"page":1,"page_size":100,"total_pages":0}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListTeamsByAlias(context.Background(), "missing")
	if err != nil {
		t.Errorf("ListTeamsByAlias empty: want nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListTeamsByAlias empty: want empty slice, got %+v", got)
	}
}

// TestListTeamsByAliasPath — path-string assertion. /v2/team/list (NOT /v1).
func TestListTeamsByAliasPath(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"teams":[],"total":0,"page":1,"page_size":100,"total_pages":0}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, _ = c.ListTeamsByAlias(context.Background(), "x")
	if len(captured) != 1 || captured[0].Method != "GET" {
		t.Fatalf("ListTeamsByAlias: want GET, got %+v", captured)
	}
	if !strings.HasPrefix(captured[0].Path, "/v2/team/list?") {
		t.Errorf("path: want prefix /v2/team/list?, got %q", captured[0].Path)
	}
	if !strings.Contains(captured[0].Path, "page_size=100") {
		t.Errorf("path: want page_size=100, got %q", captured[0].Path)
	}
}

// TestListAllTeamsUsesPageSize100 — ListAllTeams MUST request page_size=100,
// never page_size=500. The deployed LiteLLM 422s page_size=500, which
// makeRequest maps to a 4xx error → teams.LookupCallerTeams (nil,err) →
// hydrate 503 litellm_unreachable (#113 regression, 2026-06-05). 100 is the
// value ListTeamsByAlias already uses successfully.
func TestListAllTeamsUsesPageSize100(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"teams":[
			{"team_id":"t1","team_alias":"alpha"},
			{"team_id":"t2","team_alias":"beta"}
		],"total":2,"page":1,"page_size":100,"total_pages":1}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListAllTeams(context.Background())
	if err != nil {
		t.Fatalf("ListAllTeams: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 teams, got %d (%+v)", len(got), got)
	}
	if len(captured) != 1 {
		t.Fatalf("single page → want 1 request, got %d", len(captured))
	}
	if !strings.Contains(captured[0].Path, "page_size=100") {
		t.Errorf("path: want page_size=100, got %q", captured[0].Path)
	}
	if strings.Contains(captured[0].Path, "page_size=500") {
		t.Errorf("path: must NOT use page_size=500 (422s on deployed LiteLLM), got %q", captured[0].Path)
	}
}

// TestListAllTeamsPaginates — when total_pages>1 ListAllTeams MUST page through
// (page=1,2,…) and accumulate every team, not truncate at page 1. Silent
// truncation would drop alias resolutions and re-introduce the team
// false-negative the alias map closes.
func TestListAllTeamsPaginates(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		switch i {
		case 0:
			_, _ = w.Write([]byte(`{"teams":[
				{"team_id":"t1","team_alias":"a"},
				{"team_id":"t2","team_alias":"b"}
			],"total":3,"page":1,"page_size":100,"total_pages":2}`))
		default:
			_, _ = w.Write([]byte(`{"teams":[
				{"team_id":"t3","team_alias":"c"}
			],"total":3,"page":2,"page_size":100,"total_pages":2}`))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListAllTeams(context.Background())
	if err != nil {
		t.Fatalf("ListAllTeams: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 teams across 2 pages, got %d (%+v)", len(got), got)
	}
	if len(captured) != 2 {
		t.Fatalf("want 2 paged requests, got %d", len(captured))
	}
	if !strings.Contains(captured[0].Path, "page=1") || !strings.Contains(captured[1].Path, "page=2") {
		t.Errorf("want page=1 then page=2, got %q then %q", captured[0].Path, captured[1].Path)
	}
}

// TestListAllTeamsCarriesAccessGroupIDs — /v2/team/list returns the
// team-side mirror of the access-group binding (access_group_ids). The
// operator compares it against access_group.assigned_team_ids to detect
// half-applied bindings, so the field MUST survive the decode.
func TestListAllTeamsCarriesAccessGroupIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"teams":[
			{"team_id":"t-run","team_alias":"run",
			 "access_group_ids":["210f1ff1-c2eb-4fcd-8511-a309ae466d15"]}
		],"total":1,"page":1,"page_size":100,"total_pages":1}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListAllTeams(context.Background())
	if err != nil {
		t.Fatalf("ListAllTeams: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("teams: want 1, got %d", len(got))
	}
	if len(got[0].AccessGroupIDs) != 1 ||
		got[0].AccessGroupIDs[0] != "210f1ff1-c2eb-4fcd-8511-a309ae466d15" {
		t.Errorf("AccessGroupIDs: want [210f1ff1-c2eb-4fcd-8511-a309ae466d15], got %v",
			got[0].AccessGroupIDs)
	}
}

// TestTeamHelpers401Propagation — REL-06 propagation through team helpers.
func TestTeamHelpers401Propagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(litellmAuth401Body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	check := func(name string, err error) {
		t.Helper()
		var a *Auth401Error
		if !errors.As(err, &a) {
			t.Errorf("%s: want *Auth401Error, got %T: %v", name, err, err)
		}
	}

	_, err := c.CreateTeam(context.Background(), &NewTeamRequest{TeamAlias: "x"})
	check("CreateTeam", err)
	_, err = c.ListTeamsByAlias(context.Background(), "x")
	check("ListTeamsByAlias", err)
}

// TestUpdateTeamRequestBody asserts POST /team/update carries the team_id,
// the models sentinel, and the full object_permission block — the deny-all
// shell-team contract. A dropped object_permission key would silently leave
// the team fail-open on agents.
func TestUpdateTeamRequestBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"team_id":"t-1","team_alias":"ach-env-demo"}`))
	}))
	defer srv.Close()

	c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
	out, err := c.UpdateTeam(context.Background(), &TeamUpdateRequest{
		TeamID:           "t-1",
		Models:           []string{"__deny_all__"},
		ObjectPermission: &TeamObjectPermission{Agents: []string{"00000000-0000-0000-0000-000000000000"}},
	})
	if err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	if out.TeamID != "t-1" {
		t.Fatalf("TeamID = %q, want t-1", out.TeamID)
	}
	if gotPath != "/team/update" {
		t.Fatalf("path = %q, want /team/update", gotPath)
	}
	if gotBody["team_id"] != "t-1" {
		t.Fatalf("body team_id = %v, want t-1", gotBody["team_id"])
	}
	models, _ := gotBody["models"].([]any)
	if len(models) != 1 || models[0] != "__deny_all__" {
		t.Fatalf("body models = %v, want the deny-all sentinel", gotBody["models"])
	}
	op, ok := gotBody["object_permission"].(map[string]any)
	if !ok {
		t.Fatalf("body has no object_permission object: %v", gotBody)
	}
	agents, _ := op["agents"].([]any)
	if len(agents) != 1 || agents[0] != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("object_permission.agents = %v, want the nil-UUID sentinel", op["agents"])
	}
	// mcp_servers must serialise even when empty — absent means "every server"
	// on some LiteLLM paths, and we always want the explicit closed list.
	if _, present := op["mcp_servers"]; !present {
		t.Fatalf("object_permission.mcp_servers missing from body: %v", op)
	}
}

// TestDeleteTeamRequestBody asserts POST /team/delete sends {"team_ids":[id]}.
func TestDeleteTeamRequestBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
	if err := c.DeleteTeam(context.Background(), "t-9"); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if gotPath != "/team/delete" {
		t.Fatalf("path = %q, want /team/delete", gotPath)
	}
	ids, _ := gotBody["team_ids"].([]any)
	if len(ids) != 1 || ids[0] != "t-9" {
		t.Fatalf("team_ids = %v, want [t-9]", gotBody["team_ids"])
	}
}

// TestGetTeamInfoDecodesEnvelopeAndFlat asserts both response shapes decode:
// LiteLLM wraps the team under "team_info", but a flat body must not break us.
// GET /team/info is the ONLY read that carries object_permission — the team
// LIST endpoints serialise it as null — so shell-team drift detection depends
// on this decoding correctly.
func TestGetTeamInfoDecodesEnvelopeAndFlat(t *testing.T) {
	bodies := map[string]string{
		"envelope": `{"team_id":"t-1","team_info":{"team_id":"t-1","team_alias":"ach-env-demo","models":["__deny_all__"],"object_permission":{"mcp_servers":[],"agents":["00000000-0000-0000-0000-000000000000"]}}}`,
		"flat":     `{"team_id":"t-1","team_alias":"ach-env-demo","models":["__deny_all__"],"object_permission":{"mcp_servers":[],"agents":["00000000-0000-0000-0000-000000000000"]}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query().Get("team_id")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			c := NewRESTClient(srv.URL, "sk-master", logr.Discard())
			got, err := c.GetTeamInfo(context.Background(), "t-1")
			if err != nil {
				t.Fatalf("GetTeamInfo: %v", err)
			}
			if gotQuery != "t-1" {
				t.Fatalf("team_id query = %q, want t-1", gotQuery)
			}
			if got.TeamAlias != "ach-env-demo" {
				t.Fatalf("TeamAlias = %q", got.TeamAlias)
			}
			if got.ObjectPermission == nil || len(got.ObjectPermission.Agents) != 1 {
				t.Fatalf("ObjectPermission = %+v, want the agent sentinel", got.ObjectPermission)
			}
		})
	}
}

// TestDeleteTeamRejectsEmptyID guards the "delete every team" footgun.
func TestDeleteTeamRejectsEmptyID(t *testing.T) {
	c := NewRESTClient("http://127.0.0.1:1", "sk-master", logr.Discard())
	if err := c.DeleteTeam(context.Background(), ""); err == nil {
		t.Fatal("DeleteTeam(\"\") = nil, want error")
	}
}

// TestTeamObjectPermissionMarshalNormalizesNilSlices asserts a zero-value
// TeamObjectPermission marshals every field as `[]`, never `null` — a nil
// slice reads as "every agent"/"every model" to LiteLLM, defeating the
// deny-all shell team's whole purpose.
func TestTeamObjectPermissionMarshalNormalizesNilSlices(t *testing.T) {
	b, err := json.Marshal(TeamObjectPermission{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"mcp_servers", "mcp_access_groups", "agents", "agent_access_groups"} {
		raw, ok := got[key]
		if !ok {
			t.Fatalf("key %q missing from marshaled body: %s", key, b)
		}
		if string(raw) != "[]" {
			t.Errorf("%s = %s, want []", key, raw)
		}
	}
}

// TestTeamObjectPermissionMarshalThroughPointerField confirms the
// value-receiver MarshalJSON still fires when TeamObjectPermission is
// reached through TeamUpdateRequest.ObjectPermission (*TeamObjectPermission)
// — the actual shape UpdateTeam callers send.
func TestTeamObjectPermissionMarshalThroughPointerField(t *testing.T) {
	req := TeamUpdateRequest{TeamID: "t-1", ObjectPermission: &TeamObjectPermission{}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var op map[string]json.RawMessage
	if err := json.Unmarshal(got["object_permission"], &op); err != nil {
		t.Fatalf("unmarshal object_permission: %v", err)
	}
	for _, key := range []string{"mcp_servers", "mcp_access_groups", "agents", "agent_access_groups"} {
		if string(op[key]) != "[]" {
			t.Errorf("object_permission.%s = %s, want [] (nil field through *TeamObjectPermission)", key, op[key])
		}
	}
}

// TestKeyGenerateRequestCarriesTeamID asserts the ek_ mint shape: team_id
// present, access_groups gone (LiteLLM never accepted that field).
func TestKeyGenerateRequestCarriesTeamID(t *testing.T) {
	b, err := json.Marshal(&KeyGenerateRequest{UserID: "u@example.com", TeamID: "t-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["team_id"] != "t-1" {
		t.Fatalf("team_id = %v, want t-1", got["team_id"])
	}
	if _, present := got["access_groups"]; present {
		t.Fatalf("access_groups must not be sent: %v", got)
	}
}

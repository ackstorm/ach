// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

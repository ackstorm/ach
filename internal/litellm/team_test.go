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

// TestUpdateTeamUsesPostNotPatch — same Pitfall 2 enforcement at the
// team.go layer. POST /team/update — never PATCH.
func TestUpdateTeamUsesPostNotPatch(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"team_id":"t1","team_alias":"alpha"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.UpdateTeam(context.Background(), &UpdateTeamRequest{TeamID: "t1", TeamAlias: "alpha"})
	if err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	if len(captured) != 1 || captured[0].Method != "POST" || captured[0].Path != "/team/update" {
		t.Errorf("UpdateTeam: want POST /team/update, got %+v", captured)
	}
}

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
	_, err = c.UpdateTeam(context.Background(), &UpdateTeamRequest{TeamID: "x"})
	check("UpdateTeam", err)
	err = c.DeleteTeam(context.Background(), []string{"x"})
	check("DeleteTeam", err)
	_, err = c.ListTeamsByAlias(context.Background(), "x")
	check("ListTeamsByAlias", err)
}

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
)

// TestUserNewHappyPath asserts that UserNew issues POST /user/new with the
// supplied body and decodes the response into *UserInfo (Phase 3 D-25).
func TestUserNewHappyPath(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"user_id":"u-1","user_email":"a@b.c","teams":["default"]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.UserNew(context.Background(), &UserNewRequest{
		UserEmail: "a@b.c",
		Teams:     []string{"default"},
	})
	if err != nil {
		t.Fatalf("UserNew: %v", err)
	}
	if got == nil {
		t.Fatal("UserNew: nil response")
	}
	if got.UserID != "u-1" || got.UserEmail != "a@b.c" {
		t.Errorf("UserNew: want {u-1, a@b.c}, got %+v", got)
	}
	if len(got.Teams) != 1 || got.Teams[0] != "default" {
		t.Errorf("UserNew: want Teams=[default], got %+v", got.Teams)
	}
	if len(captured) != 1 || captured[0].Method != "POST" || captured[0].Path != "/user/new" {
		t.Errorf("wire: want POST /user/new, got %+v", captured)
	}
}

// TestUserInfoByEmailNotFound asserts that UserInfoByEmail returns a
// non-nil error when LiteLLM responds 404. The exact wrapping is the
// makeRequest convention (generic 4xx error string with the status code) —
// caller-side (Plan 03-07 SSO handler) uses strings.Contains("404") to
// branch into the UserNew path per Phase 3 D-25.
func TestUserInfoByEmailNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"message":"User not found","type":"not_found","param":"user_email","code":"404"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.UserInfoByEmail(context.Background(), "missing@x")
	if err == nil {
		t.Fatalf("want error on 404, got nil; result=%+v", got)
	}
	if got != nil {
		t.Errorf("want nil *UserInfo on 404, got %+v", got)
	}
	// makeRequest formats 4xx as "litellm: 404 on GET <path> (code=404)".
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404 status, got: %v", err)
	}
}

// TestUserInfoByEmailHappyPath asserts that UserInfoByEmail issues GET
// /user/info?user_email=<email> and decodes the body into *UserInfo with
// the Teams field populated (BLK-01).
func TestUserInfoByEmailHappyPath(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"user_id":"u-1","user_email":"a@b.c","teams":["default","ops"]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.UserInfoByEmail(context.Background(), "a@b.c")
	if err != nil {
		t.Fatalf("UserInfoByEmail: %v", err)
	}
	if got == nil {
		t.Fatal("UserInfoByEmail: nil response")
	}
	if got.UserID != "u-1" || got.UserEmail != "a@b.c" {
		t.Errorf("UserInfoByEmail: want {u-1, a@b.c}, got %+v", got)
	}
	if len(got.Teams) != 2 || got.Teams[0] != "default" || got.Teams[1] != "ops" {
		t.Errorf("UserInfoByEmail: want Teams=[default ops], got %+v", got.Teams)
	}
	if len(captured) != 1 || captured[0].Method != "GET" {
		t.Fatalf("wire method: want GET, got %+v", captured)
	}
	// Path must be /user/info with url-escaped user_email parameter.
	if !strings.HasPrefix(captured[0].Path, "/user/info?") {
		t.Errorf("path prefix: want /user/info?, got %q", captured[0].Path)
	}
	if !strings.Contains(captured[0].Path, "user_email=a%40b.c") {
		t.Errorf("path must url-escape email (@ → %%40), got %q", captured[0].Path)
	}
}

// TestUserInfoByEmailEscapesPlus asserts that the email url-escape uses
// QueryEscape semantics — the `+` in `a+tag@b.c` MUST become `%2B`, not
// stay literal (literal `+` in a query string decodes as space).
func TestUserInfoByEmailEscapesPlus(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"user_id":"u-1","user_email":"a+tag@b.c"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, _ = c.UserInfoByEmail(context.Background(), "a+tag@b.c")
	if len(captured) != 1 {
		t.Fatalf("want 1 request, got %d", len(captured))
	}
	if !strings.Contains(captured[0].Path, "user_email=a%2Btag%40b.c") {
		t.Errorf("want url.QueryEscape result (a%%2Btag%%40b.c), got %q", captured[0].Path)
	}
}

// TestTeamMemberAddHappyPath asserts wire shape (POST /team/member_add)
// and body content (team_id + nested member).
func TestTeamMemberAddHappyPath(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"team_id":"default"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.TeamMemberAdd(context.Background(), "default", "u-1", "user"); err != nil {
		t.Fatalf("TeamMemberAdd: %v", err)
	}
	if len(captured) != 1 || captured[0].Method != "POST" || captured[0].Path != "/team/member_add" {
		t.Errorf("wire: want POST /team/member_add, got %+v", captured)
	}

	// Decode and assert the body shape.
	var body TeamMemberAddRequest
	if err := json.Unmarshal(captured[0].Body, &body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if body.TeamID != "default" {
		t.Errorf("TeamID: want default, got %q", body.TeamID)
	}
	if body.Member.UserID != "u-1" || body.Member.Role != "user" {
		t.Errorf("Member: want {u-1, user}, got %+v", body.Member)
	}
}

// TestTeamMemberAddDuplicate4xx asserts that TeamMemberAdd propagates a
// 4xx error from the upstream (LiteLLM treats duplicate-add as 4xx).
// Caller-side (Plan 03-07 SSO handler) decides whether to swallow.
func TestTeamMemberAddDuplicate4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"already a member","type":"already_exists","param":null,"code":"400"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.TeamMemberAdd(context.Background(), "default", "u-1", "user")
	if err == nil {
		t.Fatalf("want error on 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention 400 status, got: %v", err)
	}
}

// TestUserHelpers401Propagation — REL-06 typed-error propagation through
// the three new user-helper methods. Same shape as TestTeamHelpers401Propagation.
func TestUserHelpers401Propagation(t *testing.T) {
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

	_, err := c.UserNew(context.Background(), &UserNewRequest{UserEmail: "x@y"})
	check("UserNew", err)
	_, err = c.UserInfoByEmail(context.Background(), "x@y")
	check("UserInfoByEmail", err)
	err = c.TeamMemberAdd(context.Background(), "default", "u", "user")
	check("TeamMemberAdd", err)
}

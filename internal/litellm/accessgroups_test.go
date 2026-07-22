// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// TestCreateAccessGroup_PostsV1Endpoint asserts the migrated wire shape:
// POST /v1/access_group with access_group_name in the body, returning the
// AccessGroupResponse (UUID + name + lists).
func TestCreateAccessGroup_PostsV1Endpoint(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody AccessGroupCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"access_group_id":"ag-uuid-1",
			"access_group_name":"demo",
			"access_model_names":["gpt-4"],
			"access_mcp_server_ids":["mcp-1"],
			"access_agent_ids":[],
			"assigned_team_ids":["t-1"],
			"assigned_key_ids":[]
		}`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	resp, err := c.CreateAccessGroup(context.Background(), AccessGroupCreateRequest{
		AccessGroupName:    "demo",
		AccessModelNames:   []string{"gpt-4"},
		AccessMCPServerIDs: []string{"mcp-1"},
		AssignedTeamIDs:    []string{"t-1"},
	})
	if err != nil {
		t.Fatalf("CreateAccessGroup: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/v1/access_group" {
		t.Errorf("wire: want POST /v1/access_group, got %s %s", gotMethod, gotPath)
	}
	if gotBody.AccessGroupName != "demo" {
		t.Errorf("body.access_group_name = %q; want demo", gotBody.AccessGroupName)
	}
	if len(gotBody.AccessModelNames) != 1 || gotBody.AccessModelNames[0] != "gpt-4" {
		t.Errorf("body.access_model_names = %v; want [gpt-4]", gotBody.AccessModelNames)
	}
	if resp == nil || resp.AccessGroupID != "ag-uuid-1" {
		t.Fatalf("response access_group_id = %q; want ag-uuid-1", resp.AccessGroupID)
	}
}

// TestGetAccessGroupByName_ListsAndFilters asserts the helper that
// resolves a UUID by name. Used by reconcileAccessGroup to discover
// whether to POST (create) or PUT (drift correction), and by the §6.5
// finalizer to find the UUID to DELETE.
func TestGetAccessGroupByName_ListsAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/access_group" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[
			{"access_group_id":"ag-uuid-a","access_group_name":"alpha","access_model_names":[],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":[],"assigned_key_ids":[]},
			{"access_group_id":"ag-uuid-d","access_group_name":"demo","access_model_names":["gpt-4"],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":[],"assigned_key_ids":[]}
		]`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.GetAccessGroupByName(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetAccessGroupByName: %v", err)
	}
	if got == nil || got.AccessGroupID != "ag-uuid-d" {
		t.Fatalf("got = %+v; want access_group_id=ag-uuid-d", got)
	}
}

// TestGetAccessGroupByName_AbsentReturnsNilNil asserts the "no row found"
// contract: nil response, nil error. The reconciler treats this as "must
// POST to create".
func TestGetAccessGroupByName_AbsentReturnsNilNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	got, err := c.GetAccessGroupByName(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %+v; want nil", got)
	}
}

// TestUpdateAccessGroup_PutsByID asserts PUT /v1/access_group/{id} with
// the AccessGroupUpdateRequest body.
func TestUpdateAccessGroup_PutsByID(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody AccessGroupUpdateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"access_group_id":"ag-uuid-1","access_group_name":"demo","access_model_names":["gpt-4","claude-3"],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":["t-1"],"assigned_key_ids":[]}`)
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	_, err := c.UpdateAccessGroup(context.Background(), "ag-uuid-1", AccessGroupUpdateRequest{
		AccessModelNames: []string{"gpt-4", "claude-3"},
		AssignedTeamIDs:  []string{"t-1"},
	})
	if err != nil {
		t.Fatalf("UpdateAccessGroup: %v", err)
	}
	if gotMethod != "PUT" || gotPath != "/v1/access_group/ag-uuid-1" {
		t.Errorf("wire: want PUT /v1/access_group/ag-uuid-1, got %s %s", gotMethod, gotPath)
	}
	if len(gotBody.AccessModelNames) != 2 {
		t.Errorf("body.access_model_names = %v; want 2 entries", gotBody.AccessModelNames)
	}
}

// TestAccessGroupUpdateRequest_EmptyManagedListsMarshalToBrackets is the
// regression guard for the omitempty-drops-the-clear bug: the four
// reconciler-managed lists MUST always serialize (empty → `[]`, which
// clears the dimension on LiteLLM's partial-update PUT), while
// AssignedKeyIDs MUST stay absent (still omitempty — the reconciler does
// not manage keys, so absent=keep). It fails the instant anyone re-adds
// `omitempty` to a managed list.
func TestAccessGroupUpdateRequest_EmptyManagedListsMarshalToBrackets(t *testing.T) {
	b, err := json.Marshal(AccessGroupUpdateRequest{
		AccessModelNames:   []string{},
		AccessMCPServerIDs: []string{},
		AccessAgentIDs:     []string{},
		AssignedTeamIDs:    []string{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"access_model_names":[]`,
		`"access_mcp_server_ids":[]`,
		`"access_agent_ids":[]`,
		`"assigned_team_ids":[]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("marshaled body must contain %s (empty managed list must clear, not be dropped); got %s", want, got)
		}
	}
	// AssignedKeyIDs is still omitempty → absent when empty (absent=keep).
	if strings.Contains(got, "assigned_key_ids") {
		t.Errorf("assigned_key_ids must be ABSENT when empty (still omitempty); got %s", got)
	}
}

// TestAccessGroupUpdateRequest_NilManagedListMarshalsToNull documents WHY
// the controller normalizes nil → []string{} (env_controller.go
// nonNilStrings): without omitempty a nil managed slice marshals to JSON
// `null`, which is NOT a proven LiteLLM clear — only `[]` is. So the
// controller must never let a nil reach the wire for a managed dimension.
func TestAccessGroupUpdateRequest_NilManagedListMarshalsToNull(t *testing.T) {
	b, err := json.Marshal(AccessGroupUpdateRequest{}) // all managed lists nil
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"access_mcp_server_ids":null`) {
		t.Errorf("nil managed list serializes to null (not omitted) — this is why nonNilStrings is required; got %s", got)
	}
}

// TestDeleteAccessGroupByID_DeletesByID asserts DELETE /v1/access_group/{id}.
// 204 → nil. 404 → nil (idempotent §7.7 contract).
func TestDeleteAccessGroupByID_DeletesByID(t *testing.T) {
	cases := []int{204, 404}
	for _, code := range cases {
		t.Run("status"+strings.ReplaceAll(http.StatusText(code), " ", ""), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "DELETE" || r.URL.Path != "/v1/access_group/ag-uuid-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)

			c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
			if err := c.DeleteAccessGroupByID(context.Background(), "ag-uuid-1"); err != nil {
				t.Fatalf("DeleteAccessGroupByID (status %d): %v", code, err)
			}
		})
	}
}

// TestDeleteAccessGroup_LooksUpThenDeletes asserts the high-level helper
// the §6.5 finalizer calls: GET /v1/access_group → find by name → DELETE
// /v1/access_group/{id}. Absent name = idempotent success.
func TestDeleteAccessGroup_LooksUpThenDeletes(t *testing.T) {
	var deleteHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/access_group":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `[{"access_group_id":"ag-uuid-1","access_group_name":"demo","access_model_names":[],"access_mcp_server_ids":[],"access_agent_ids":[],"assigned_team_ids":[],"assigned_key_ids":[]}]`)
		case r.Method == "DELETE" && r.URL.Path == "/v1/access_group/ag-uuid-1":
			deleteHit = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.DeleteAccessGroup(context.Background(), "demo"); err != nil {
		t.Fatalf("DeleteAccessGroup: %v", err)
	}
	if !deleteHit {
		t.Errorf("expected DELETE /v1/access_group/ag-uuid-1 to fire")
	}
}

// TestDeleteAccessGroup_AbsentName_NoDelete asserts the §7.7 idempotent
// branch: a §6.5 finalizer running after a partially-completed prior
// delete must NOT error if the access group is already gone.
func TestDeleteAccessGroup_AbsentName_NoDelete(t *testing.T) {
	var deleteHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/access_group":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `[]`)
		case r.Method == "DELETE":
			deleteHit = true
		}
	}))
	t.Cleanup(srv.Close)

	c := NewRESTClient(srv.URL, "sk-test", logr.Discard())
	if err := c.DeleteAccessGroup(context.Background(), "missing"); err != nil {
		t.Fatalf("DeleteAccessGroup (missing): %v", err)
	}
	if deleteHit {
		t.Errorf("DELETE must NOT fire when name is absent")
	}
}

func TestAccessGroupName(t *testing.T) {
	if got := AccessGroupName("platform"); got != "ach-env-platform" {
		t.Errorf("AccessGroupName(platform) = %q; want ach-env-platform", got)
	}
	if AccessGroupPrefix != "ach-env-" {
		t.Errorf("AccessGroupPrefix = %q; want ach-env-", AccessGroupPrefix)
	}
}

func TestAccessGroupNameGenerations(t *testing.T) {
	got := AccessGroupNameGenerations("platform")
	want := []string{"ach-env-platform", "ach-platform", "platform"}
	if !slices.Equal(got, want) {
		t.Errorf("AccessGroupNameGenerations(platform) = %v; want %v", got, want)
	}
}

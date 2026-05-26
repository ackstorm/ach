// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package litellm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateMCPServerPathIsAdminImmediate — POST /v1/mcp/server. The
// admin-immediate path; the user-submission path is intentionally
// NOT used by this operator.
func TestCreateMCPServerPathIsAdminImmediate(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(202) // LiteLLM 1.83.10 returns 202 on POST /v1/mcp/server
		_, _ = w.Write([]byte(`{"server_id":"mcp-1","transport":"sse"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.CreateMCPServer(context.Background(), &MCPServerRequest{ServerName: "test"})
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	if len(captured) != 1 || captured[0].Method != "POST" || captured[0].Path != "/v1/mcp/server" {
		t.Errorf("CreateMCPServer: want POST /v1/mcp/server (admin-immediate), got %+v", captured)
	}
}

// TestUpdateMCPServerUsesPUT — PUT /v1/mcp/server (§5.1 wholesale-replace).
func TestUpdateMCPServerUsesPUT(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"server_id":"mcp-1","transport":"sse"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.UpdateMCPServer(context.Background(), &MCPServerUpdateRequest{ServerID: "mcp-1"})
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	if len(captured) != 1 || captured[0].Method != "PUT" || captured[0].Path != "/v1/mcp/server" {
		t.Errorf("UpdateMCPServer: want PUT /v1/mcp/server, got %+v", captured)
	}
}

// TestDeleteMCPServerPath — DELETE /v1/mcp/server/{id}.
func TestDeleteMCPServerPath(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.DeleteMCPServer(context.Background(), "mcp-xyz"); err != nil {
		t.Fatalf("DeleteMCPServer: %v", err)
	}
	if len(captured) != 1 || captured[0].Method != "DELETE" || captured[0].Path != "/v1/mcp/server/mcp-xyz" {
		t.Errorf("DeleteMCPServer: want DELETE /v1/mcp/server/mcp-xyz, got %+v", captured)
	}
}

// TestListMCPServersLengthCheck — REL-05 on the bare-array list shape.
// LiteLLM returns a bare JSON array; the helper wraps into
// MCPServerListResponse{Data: ...} for the length check.
func TestListMCPServersLengthCheck(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty_array", `[]`},
		{"null_response", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			got, err := c.ListMCPServers(context.Background())
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("err: want ErrNotFound, got %v", err)
			}
			if got != nil {
				t.Errorf("result: want nil on empty, got %+v", got)
			}
		})
	}
}

// TestListMCPServersOK — happy path: non-empty array returns entries.
func TestListMCPServersOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`[{"server_id":"a","transport":"sse"},{"server_id":"b","transport":"sse"}]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListMCPServers(context.Background())
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 entries, got %d", len(got))
	}
}

// TestMCPHelpers401Propagation — REL-06 propagation through MCP helpers.
func TestMCPHelpers401Propagation(t *testing.T) {
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

	_, err := c.CreateMCPServer(context.Background(), &MCPServerRequest{ServerName: "x"})
	check("CreateMCPServer", err)
	_, err = c.UpdateMCPServer(context.Background(), &MCPServerUpdateRequest{ServerID: "x"})
	check("UpdateMCPServer", err)
	err = c.DeleteMCPServer(context.Background(), "x")
	check("DeleteMCPServer", err)
	_, err = c.ListMCPServers(context.Background())
	check("ListMCPServers", err)
}

// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

	_, err := c.ListMCPServers(context.Background())
	check("ListMCPServers", err)
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestA2ASearch(t *testing.T) {
	envTestEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/platform/hydrate/version":
			fmt.Fprint(w, `{"items":[{"kind":"a2a","name":"accounting"},{"kind":"a2a","name":"unavailable"}]}`)
		case "/v1/agents":
			if r.URL.Query().Get("health_check") != "false" {
				t.Error("catalog must not probe agent health")
			}
			fmt.Fprint(w, `[{"agent_name":"accounting","agent_card_params":{"description":"Ledger",
				"skills":[{"id":"forecasts","name":"Forecasts"}]}},{"agent_name":"other"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("ACH_KEY", testEK)
	t.Setenv("ACH_URL", srv.URL)
	out, _, _, err := executeCommand(t, newA2ACmd(), "search", "LEDGER", "foreCAST", "-o", "json")
	var got []runtimeTarget
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 || got[0].Name != "accounting" {
		t.Fatalf("search: %q, %v", out, err)
	}
	out, _, _, err = executeCommand(t, newA2ACmd(), "list")
	if err != nil || !strings.Contains(out, "accounting") ||
		strings.Contains(out, "other") || strings.Contains(out, "unavailable") {
		t.Fatalf("list: %q, %v", out, err)
	}
}

func TestA2ACallUsesBridge(t *testing.T) {
	envTestEnv(t)
	m := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	type args struct {
		Message   string `json:"message"`
		ContextID string `json:"context_id,omitempty"`
	}
	send := func(_ context.Context, _ *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if a.Message != "next step" || a.ContextID != "thread" {
			t.Errorf("arguments = %+v", a)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "response"}}}, nil, nil
	}
	mcp.AddTool(m, &mcp.Tool{Name: "send_message"}, send)
	mux := http.NewServeMux()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m },
		&mcp.StreamableHTTPOptions{Stateless: true})
	mux.Handle("/a2a/accounting/mcp", handler)
	mux.HandleFunc("/a2a/private/mcp", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("ACH_KEY", testEK)
	t.Setenv("ACH_URL", srv.URL)
	out, _, _, err := executeCommand(t, newA2ACmd(), "call", "accounting", "next step",
		"--context-id", "thread", "-o", "json")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "response") {
		t.Fatalf("call: %q, %v", out, err)
	}
	if _, _, code, _ := executeCommand(t, newA2ACmd(), "call", "private", "hello"); code == 0 {
		t.Error("calling an ungranted agent succeeded")
	}
}

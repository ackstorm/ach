// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCommandReferences(t *testing.T) {
	credTestEnv(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/hydrate/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"environment":"sales","items":[{"kind":"mcp","name":"crm"},{"kind":"mcp","name":"crm.eu"}]}`))
	})
	mux.HandleFunc("/v1/mcp/server", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"server_name":"crm"},{"server_name":"crm.eu"},
			{"server_name":"hidden"},{"server_name":"crm.other"}]`))
	})
	server := mcp.NewServer(&mcp.Implementation{Name: "crm", Version: "1"}, nil)
	for _, name := range []string{"auth_status", "crm.eu.contacts", "sales.lookup", "team.lookup", "null"} {
		tool := &mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}
		server.AddTool(tool, func(_ context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			reply := r.Params.Name + " " + string(r.Params.Arguments)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: reply}}}, nil
		})
	}
	mux.Handle("/mcp/crm.eu", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true}))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("ACH_URL", ts.URL)
	t.Setenv("ACH_KEY", testEK)
	for _, tc := range []struct {
		args   []string
		stdin  string
		output string
		fails  bool
	}{
		{[]string{"describe", "crm.eu"}, "", "auth_status", false},
		{[]string{"describe", "crm.eu.auth_status"}, "", "Input schema", false},
		{[]string{"call", "crm.eu.auth_status"}, "", "auth_status {}", false},
		{[]string{"call", "crm.eu.contacts", `{"n":9007199254740993}`}, "", `crm.eu.contacts {"n":9007199254740993}`, false},
		{[]string{"call", "crm.eu.sales.lookup", "-"}, "{\"n\":1}\n", `sales.lookup {"n":1}`, false},
		{[]string{"call", "crm.eu", "sales.lookup", "-"}, "{}", "sales.lookup {}", false},
		{[]string{"call", "crm.eu", "lookup"}, "", "ambiguous", true},
		{[]string{"call", "crm.eu.auth_status", "-"}, "[]", "valid JSON object", true},
		{[]string{"call", "crm.eu", "null"}, "", "null {}", false},
		{[]string{"call", "crm.eu.auth_status", "[]"}, "", "valid JSON object", true},
		{[]string{"call", "crm.eu.auth_status", "null"}, "", "valid JSON object", true},
		{[]string{"describe", "crm.other", "lookup"}, "", `MCP server "crm.other" is unavailable to this credential`, true},
		{[]string{"call", "crm.other", "lookup"}, "", `MCP server "crm.other" is unavailable to this credential`, true},
		{[]string{"describe", "hidden"}, "", `MCP server "hidden" is unavailable to this credential`, true},
		{[]string{"call", "hidden", "tool"}, "", `MCP server "hidden" is unavailable to this credential`, true},
		{[]string{"call", "hidden.tool", "{}"}, "", `MCP server "hidden.tool" is unavailable to this credential`, true},
		{[]string{"call", "nonexistent", "tool"}, "", `MCP server "nonexistent" is unavailable to this credential`, true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := newMCPCmd()
			cmd.SetIn(strings.NewReader(tc.stdin))
			out, _, _, err := executeCommand(t, cmd, tc.args...)
			if tc.fails {
				if err == nil || !strings.Contains(err.Error(), tc.output) {
					t.Fatalf("expected %q in error, got %v", tc.output, err)
				}
			} else if err != nil || !strings.Contains(out, tc.output) {
				t.Fatalf("expected %q, got %q, %v", tc.output, out, err)
			}
		})
	}
}

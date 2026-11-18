// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPSearchRanksToolsAndServers(t *testing.T) {
	credTestEnv(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/hydrate/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
 {"kind":"mcp","name":"aws-ec2"},{"kind":"mcp","name":"zoho"},{"kind":"mcp","name":"billing"}]}`))
	})
	mux.HandleFunc("/v1/mcp/server", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"server_name":"billing"},{"server_name":"zoho"},{"server_name":"aws-ec2"}]`))
	})
	for server, tools := range map[string]map[string]string{
		"aws-ec2": {
			"instance_price": "", "a_rates": "Instance price", "instance_details": "",
			"z_rates": "Instance price", "aws-ec2.reboot": "Restart a node",
		},
		"billing": {"price": "Look up a bill"},
		"zoho":    {"search_contacts": "Search by company", "auth_revoke": "Clear the session", "find_company": ""},
	} {
		m := mcp.NewServer(&mcp.Implementation{Name: server, Version: "1"}, nil)
		for name, description := range tools {
			m.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: map[string]string{"type": "object"}},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return &mcp.CallToolResult{}, nil
				})
		}
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m },
			&mcp.StreamableHTTPOptions{Stateless: true})
		mux.Handle("/mcp/"+server, handler)
	}
	ts := httptest.NewServer(mux)
	defer ts.Close()
	t.Setenv("ACH_URL", ts.URL)
	t.Setenv("ACH_KEY", testEK)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"Ec2 instance PRICE price", []string{
			"aws-ec2", "instance_price", "a_rates", "z_rates", "instance_details", "billing", "price",
		}},
		{"who is the contact at a company", []string{"zoho", "search_contacts", "find_company"}},
		{"auth revoke", []string{"zoho", "auth_revoke"}},
		{"unfindable", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			args := append([]string{"search", "-o", "json"}, strings.Fields(tc.query)...)
			out, _, _, err := executeCommand(t, newMCPCmd(), args...)
			var inventory mcpInventory
			if err != nil || json.Unmarshal([]byte(out), &inventory) != nil {
				t.Fatalf("search: %v, output %s", err, out)
			}
			var names []string
			for _, server := range inventory.Servers {
				names = append(names, server.Name)
				for _, tool := range server.Tools {
					names = append(names, tool.Name)
				}
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("matches = %v, want %v", names, tc.want)
			}
		})
	}
	out, stderr, _, err := executeCommand(t, newMCPCmd(), "search", "unfindable")
	if err != nil || !strings.Contains(out+stderr, "fewer words") {
		t.Errorf("no-match hint: %v, stdout %q, stderr %q", err, out, stderr)
	}
}

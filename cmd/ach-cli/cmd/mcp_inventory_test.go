// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func TestMCPInventorySkippedServers(t *testing.T) {
	signIn := mcpQueryFailure{Server: "calendar", Error: mcpAuthRequired("calendar").Error(), Reason: "auth_required"}
	outage := mcpQueryFailure{Server: "books", Error: "connection refused"}
	for _, tc := range []struct {
		name      string
		attempted int
		failures  []mcpQueryFailure
		wantErr   bool
	}{
		{"no grants", 0, nil, false},
		{"one server succeeded", 2, []mcpQueryFailure{signIn}, false},
		{"all need sign-in", 1, []mcpQueryFailure{signIn}, true},
		{"sign-in and outage", 3, []mcpQueryFailure{signIn, outage}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "search"}
			var out, diagnostic bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostic)
			inventory := mcpInventory{Servers: []mcpServerSummary{}, Errors: tc.failures, attempted: tc.attempted}
			err := inventory.write(cmd, outputFlag{v: outputJSON})
			if (err != nil) != tc.wantErr || !json.Valid(out.Bytes()) {
				t.Fatalf("write = %v, output %s", err, out.String())
			}
			if len(tc.failures) == 0 {
				return
			}
			message := diagnostic.String()
			if err != nil {
				message = err.Error()
			}
			if !strings.Contains(message, "1 server skipped, sign-in needed: calendar") ||
				!strings.Contains(out.String(), "auth_required") || strings.Count(strings.TrimSpace(message), "\n") != 0 {
				t.Fatalf("diagnostic %q, output %s", message, out.String())
			}
			if len(tc.failures) > 1 && !strings.Contains(message, "books: connection refused") {
				t.Fatalf("missing outage in %q", message)
			}
		})
	}
}

func TestMCPDisplayedReferencesDisambiguateTargets(t *testing.T) {
	servers := []runtimeTarget{{Name: "crm"}, {Name: "crm.eu"}}
	tools := []*mcp.Tool{{Name: "eu.lookup"}, {Name: "eu"}, {Name: "read"}, {Name: "crm.read"}, {Name: "contacts"}}
	want := []string{"crm eu.lookup", "crm eu", "crm read", "crm.read", "crm.contacts"}
	summary := mcpServerSummary{Name: "crm"}
	for _, tool := range tools {
		summary.Tools = append(summary.Tools, summarizeTool(tool))
	}
	summary.setToolReferences(servers, tools)
	for i, tool := range summary.Tools {
		if tool.reference != want[i] {
			t.Errorf("%s displayed as %q, want %q", tool.Name, tool.reference, want[i])
		}
	}
}

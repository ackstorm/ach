// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestToRuntimeBlock_EscapesNamesInEndpoints asserts that resource names
// containing URL metacharacters are PathEscaped before being appended to
// the endpoint URL (security finding S2).
func TestToRuntimeBlock_EscapesNamesInEndpoints(t *testing.T) {
	row := &db.EnvironmentRow{
		RuntimeMCPServers: []string{"key?admin=true"},
		RuntimeA2AAgents:  []string{"../admin"},
	}
	got := toRuntimeBlockFromRow(row, "https://ach.example.com")
	if ep := got.MCPServers[0].Endpoint; ep != "https://ach.example.com/mcp/key%3Fadmin=true" {
		t.Errorf("mcp endpoint not escaped: %q", ep)
	}
	if ep := got.A2AAgents[0].Endpoint; ep != "https://ach.example.com/a2a/..%2Fadmin" {
		t.Errorf("a2a endpoint not escaped: %q", ep)
	}
}

// TestToContextBlock_EscapesNamesInDownloadURLs asserts that context resource
// names are PathEscaped in the generated download URLs (security finding S2).
func TestToContextBlock_EscapesNamesInDownloadURLs(t *testing.T) {
	row := &db.EnvironmentRow{ContextInstructions: []string{"a?b"}}
	got := toContextBlockFromRow(row, "https://ach.example.com")
	if u := got.Instructions[0].DownloadURL; u != "https://ach.example.com/content/instruction/a%3Fb" {
		t.Errorf("download url not escaped: %q", u)
	}
}

func TestContextBlock_OmitsEmptyInstructions(t *testing.T) {
	for name, cb := range map[string]ContextBlock{
		"empty context":            emptyContext(),
		"row without instructions": toContextBlockFromRow(&db.EnvironmentRow{}, ""),
	} {
		b, err := json.Marshal(cb)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"instructions"`) || strings.Contains(string(b), `"prompts"`) {
			t.Fatalf("%s must omit instructions and prompts: %s", name, b)
		}
	}
}

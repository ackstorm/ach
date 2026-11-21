// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"slices"
	"testing"

	"github.com/ackstorm/ach/internal/cli/manifest"
)

func TestRenderMCPServersJSON(t *testing.T) {
	m := &manifest.Manifest{
		Environment: "dev",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{{ID: "mcp-echo", Endpoint: "https://ach.example/mcp/mcp-echo"}},
			A2AAgents:  []manifest.ContentRef{{ID: "helper", Endpoint: "https://ach.example/a2a/helper"}},
		},
	}
	out, keys, err := RenderMCPServersJSON(m, "ek-test", "serverUrl")
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "mcpServers": {
    "a2a-helper": {
      "headers": {
        "x-ach-environment": "dev",
        "x-ach-key": "ek-test"
      },
      "serverUrl": "https://ach.example/a2a/helper/mcp"
    },
    "mcp-echo": {
      "headers": {
        "x-ach-environment": "dev",
        "x-ach-key": "ek-test"
      },
      "serverUrl": "https://ach.example/mcp/mcp-echo"
    }
  }
}
`
	if string(out) != want {
		t.Fatalf("got:\n%s", out)
	}
	if !slices.Equal(keys, []string{"mcpServers.a2a-helper", "mcpServers.mcp-echo"}) {
		t.Fatalf("keys: %v", keys)
	}
}

func TestRenderMCPServersJSON_Empty(t *testing.T) {
	out, keys, err := RenderMCPServersJSON(&manifest.Manifest{Runtime: &manifest.RuntimeBlock{}}, "", "url")
	if err != nil || len(keys) != 0 || string(out) != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("out=%q keys=%v err=%v", out, keys, err)
	}
}

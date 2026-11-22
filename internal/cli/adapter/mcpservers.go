// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ackstorm/ach/internal/cli/manifest"
)

// OpencodeMCPKeyPrefix is the dotted-key prefix of every MCP server ACH
// contributes to OpenCode's opencode.json (`mcp.servers.<id>`).
const OpencodeMCPKeyPrefix = "mcp.servers."

// RenderMCPServersJSON encodes the Environment's runtime MCP servers as
// {"mcpServers": {"<id>": {"<urlField>": <endpoint>, "headers": {…}}}} and
// returns the contributed dotted keys, sorted. urlField is the tool's name
// for a remote server's address. Output is deterministic.
func RenderMCPServersJSON(m *manifest.Manifest, credential, urlField string) ([]byte, []string, error) {
	servers := RuntimeMCPServers(m)
	entries := make(map[string]any, len(servers))
	keys := make([]string, 0, len(servers))
	for _, s := range servers {
		e := map[string]any{urlField: s.Endpoint}
		if h := HeadersWithCredential(credential, m.Environment); h != nil {
			e["headers"] = h
		}
		entries[s.ID] = e
		keys = append(keys, "mcpServers."+s.ID)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"mcpServers": entries}); err != nil {
		return nil, nil, fmt.Errorf("encode mcpServers: %w", err)
	}
	return buf.Bytes(), keys, nil
}

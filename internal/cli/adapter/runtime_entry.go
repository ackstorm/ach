// SPDX-License-Identifier: Apache-2.0

package adapter

// MCPServerEntry is the per-server JSON shape the JSON-rendering adapters
// (claude-code, gemini-cli, opencode) emit for each MCP server. The
// containing document shape and its top-level key (mcpServers / mcp)
// stay per-adapter; only this leaf entry is shared. The TOML-flavored
// codex adapter carries its own variant and does NOT use this type.
type MCPServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// A2AAgentEntry mirrors the MCP server shape for A2A agents in the
// JSON-rendering adapters. Structurally identical to MCPServerEntry;
// kept as a distinct named type so the two roles read clearly at the
// call sites (and so a future divergence in the A2A contract does not
// silently change the MCP shape).
type A2AAgentEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

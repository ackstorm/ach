// SPDX-License-Identifier: Apache-2.0

package adapter

// MCPServerEntry is the per-server JSON shape for an MCP server in the
// claude-code {type:"http", url, headers} format. It is now used ONLY by
// claude-code: gemini-cli (httpUrl), opencode (type:"remote"), pimono
// (no type), and codex (TOML) each carry their own per-adapter entry
// shape, because each tool's real MCP schema differs and copying claude's
// shape produced non-loadable config.
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

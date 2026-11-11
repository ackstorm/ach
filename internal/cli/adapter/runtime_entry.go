// SPDX-License-Identifier: Apache-2.0

package adapter

// MCPServerEntry is the per-server JSON shape for an MCP server in the
// claude-code {type:"http", url, headers} format. It is now used ONLY by
// claude-code: gemini-cli (httpUrl), opencode (type:"remote"), pi
// (no type), and codex (TOML) each carry their own per-adapter entry
// shape, because each tool's real MCP schema differs and copying claude's
// shape produced non-loadable config.
type MCPServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

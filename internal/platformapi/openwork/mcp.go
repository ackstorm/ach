// SPDX-License-Identifier: Apache-2.0

package openwork

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/platformapi/auth"
)

// Connect MCP (alitellm-auth app/openwork.py @ 7605a9c). The desktop mints
// a token at /v1/mcp/token whenever it is signed in, registers
// <resource>/agent as the `openwork-cloud` remote MCP in every workspace
// and probes it; Connect health turns green only when tools/list carries
// search_capabilities + execute_capability. The catalog is EMPTY: this
// exists for the green badge, nothing is connected through it.
const (
	mcpTokenKind = "openwork_mcp_token" //nolint:gosec // a store key prefix, not a credential
	// The hosted Den's lifetime; the desktop re-mints 24h before expiry.
	mcpTokenTTL = 7 * 24 * time.Hour
)

var mcpProtocolVersions = []string{"2025-06-18", "2025-03-26"}

var mcpTools = []any{
	map[string]any{
		"name":        "search_capabilities",
		"description": "Search this organization's Connect catalog. It is currently empty.",
		"annotations": map[string]any{"readOnlyHint": true},
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":  map[string]any{"type": "string"},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"type":   map[string]any{"type": "string"},
				"intent": map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
	},
	map[string]any{
		"name":        "execute_capability",
		"description": "Run a capability by the exact name search_capabilities returned.",
		"annotations": map[string]any{"destructiveHint": true},
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":  map[string]any{"type": "string"},
				"body":  map[string]any{"type": "object", "additionalProperties": true},
				"path":  map[string]any{"type": "object", "additionalProperties": true},
				"query": map[string]any{"type": "object", "additionalProperties": true},
			},
			"required": []string{"name"},
		},
	},
}

// mcpResources are read by the local server on every prompt; strict v1
// shape (connect-skill-catalog.ts).
var mcpResources = map[string]string{
	"skill://index.json":      `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[]}`,
	"automation://index.json": `{"fetchedAt":0,"total":0,"omitted":0,"automations":[]}`,
}

// mcpToken mints the Connect token. organizationId must equal the active
// org id or health fails cloud_token_org_mismatch; the desktop appends
// /agent to resource and requires the /mcp suffix.
func (d Deps) mcpToken(w http.ResponseWriter, r *http.Request) {
	s, ok := d.requireToken(w, r)
	if !ok {
		return
	}
	tok, err := auth.NewSessionID()
	if err != nil {
		denError(w, 500, "server_error", "")
		return
	}
	if err := d.Store.Put(r.Context(), mcpTokenKind, tok, denSession{Email: s.Email}, mcpTokenTTL); err != nil {
		denError(w, 500, "server_error", "store unavailable")
		return
	}
	denJSON(w, map[string]any{
		"token":          tok,
		"expiresAt":      time.Now().UTC().Add(mcpTokenTTL).Format(time.RFC3339),
		"organizationId": d.organization()["id"],
		"scopes":         []string{"mcp:read", "mcp:write"},
		"resource":       d.denAPIBase() + "/mcp",
	})
}

// mcpAgent is the Streamable-HTTP MCP endpoint. Traps
// (connect-mcp-transport.ts): a notification-only request gets 202 with an
// EMPTY body; GET is 405 like the hosted Den (a 204 confuses the SDK); a
// 401 makes the desktop re-mint silently.
func (d Deps) mcpAgent(w http.ResponseWriter, r *http.Request) {
	var s denSession
	tok := bearer(r)
	if ok, err := d.Store.Get(r.Context(), mcpTokenKind, tok, &s); tok == "" || err != nil || !ok {
		denError(w, 401, "invalid_mcp_token", "Missing or unknown MCP token.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent) // nothing is kept per session
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&raw); err != nil {
		denJSON(w, rpcError(nil, -32700, "Parse error"))
		return
	}
	var batch []json.RawMessage
	isBatch := json.Unmarshal(raw, &batch) == nil
	if !isBatch {
		batch = []json.RawMessage{raw}
	}
	var replies []any
	for _, m := range batch {
		if rep := mcpReply(m); rep != nil {
			replies = append(replies, rep)
		}
	}
	switch {
	case len(replies) == 0:
		w.WriteHeader(http.StatusAccepted)
	case isBatch:
		denJSON(w, replies)
	default:
		denJSON(w, replies[0])
	}
}

func rpcError(id any, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

// mcpReply answers one JSON-RPC message; nil for a notification.
func mcpReply(raw json.RawMessage) any {
	var m struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			ProtocolVersion string `json:"protocolVersion"`
			Name            string `json:"name"`
			URI             string `json:"uri"`
			Arguments       struct {
				Name any `json:"name"`
			} `json:"arguments"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &m) != nil || m.JSONRPC != "2.0" {
		return rpcError(nil, -32600, "Invalid request")
	}
	if m.ID == nil {
		return nil // notification (e.g. notifications/initialized)
	}
	id := m.ID
	ok := func(result any) any { return map[string]any{"jsonrpc": "2.0", "id": id, "result": result} }
	switch m.Method {
	case "initialize":
		v := mcpProtocolVersions[0]
		for _, p := range mcpProtocolVersions {
			if p == m.Params.ProtocolVersion {
				v = p
			}
		}
		return ok(map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "resources": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "ach-den", "version": "1"},
			"instructions": "This organization's Connect catalog is empty: search_capabilities returns no matches and " +
				"there are no connected services, remote skills, workflows or automations. Do not suggest connecting services.",
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": mcpTools})
	case "tools/call":
		switch m.Params.Name {
		case "search_capabilities":
			payload := map[string]any{"matches": []any{}, "hint": "The Connect catalog is empty."}
			text, _ := json.Marshal(payload)
			return ok(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}},
				"structuredContent": payload, "isError": false})
		case "execute_capability":
			text, _ := json.Marshal(map[string]any{"error": "unknown_capability", "name": m.Params.Arguments.Name})
			return ok(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "isError": true})
		}
		return rpcError(id, -32602, "Unknown tool: "+m.Params.Name)
	case "resources/list":
		res := []any{}
		for _, uri := range []string{"skill://index.json", "automation://index.json"} {
			res = append(res, map[string]any{"uri": uri, "name": uri, "mimeType": "application/json"})
		}
		return ok(map[string]any{"resources": res})
	case "resources/read":
		text, found := mcpResources[m.Params.URI]
		if !found {
			return rpcError(id, -32002, "Resource not found: "+m.Params.URI)
		}
		// The reader matches on uri, so it must come back verbatim.
		return ok(map[string]any{"contents": []any{map[string]any{"uri": m.Params.URI, "mimeType": "application/json", "text": text}}})
	case "prompts/list":
		return ok(map[string]any{"prompts": []any{}})
	}
	return rpcError(id, -32601, "Method not found: "+m.Method)
}

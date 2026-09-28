// SPDX-License-Identifier: Apache-2.0

package openwork

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// mcpTok mints a Connect token through the Den route.
func mcpTok(t *testing.T, f *fixture) (string, map[string]any) {
	t.Helper()
	w := f.do(t, "POST", "/api/den/v1/mcp/token", nil, bearerHdr(token(t, f)))
	if w.Code != 200 {
		t.Fatalf("mcp/token: %d %s", w.Code, w.Body)
	}
	m := decode(t, w)
	return m["token"].(string), m
}

func rpc(t *testing.T, f *fixture, tok string, body any) map[string]any {
	t.Helper()
	w := f.do(t, "POST", "/api/den/mcp/agent", body, bearerHdr(tok))
	if w.Code != 200 {
		t.Fatalf("rpc: %d %s", w.Code, w.Body)
	}
	return decode(t, w)
}

func TestMCP_TokenCarriesActiveOrgAndResource(t *testing.T) {
	f := newDen(t, true)
	_, m := mcpTok(t, f)
	exp, err := time.Parse(time.RFC3339, m["expiresAt"].(string))
	if err != nil || !strings.HasSuffix(m["expiresAt"].(string), "Z") || time.Until(exp) < 6*24*time.Hour {
		t.Fatalf("expiresAt: %v", m["expiresAt"])
	}
	if m["organizationId"] != f.deps.organization()["id"] || m["resource"] != "https://ach.test/openwork/api/den/mcp" {
		t.Fatalf("%v", m)
	}
	if w := f.do(t, "POST", "/api/den/v1/mcp/token", nil, nil); w.Code != 401 {
		t.Fatalf("unauthenticated mint: %d", w.Code)
	}
}

func TestMCP_AgentAuthAndMethods(t *testing.T) {
	f := newDen(t, true)
	if w := f.do(t, "POST", "/api/den/mcp/agent", map[string]any{}, bearerHdr("nope")); w.Code != 401 || decode(t, w)["error"] != "invalid_mcp_token" {
		t.Fatalf("unknown token: %d %s", w.Code, w.Body)
	}
	// A Den session token is not a Connect token.
	if w := f.do(t, "POST", "/api/den/mcp/agent", map[string]any{}, bearerHdr(token(t, f))); w.Code != 401 {
		t.Fatalf("den token accepted: %d", w.Code)
	}
	tok, _ := mcpTok(t, f)
	if w := f.do(t, "GET", "/openwork/api/den/mcp/agent", nil, bearerHdr(tok)); w.Code != 405 || w.Header().Get("Allow") != "POST, DELETE" {
		t.Fatalf("GET: %d %v", w.Code, w.Header())
	}
	if w := f.do(t, "DELETE", "/api/den/mcp/agent", nil, bearerHdr(tok)); w.Code != 204 {
		t.Fatalf("DELETE: %d", w.Code)
	}
}

func TestMCP_InitializeNegotiatesProtocol(t *testing.T) {
	f := newDen(t, true)
	tok, _ := mcpTok(t, f)
	for req, want := range map[string]string{"2025-03-26": "2025-03-26", "2025-06-18": "2025-06-18", "1999-01-01": "2025-06-18"} {
		m := rpc(t, f, tok, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": req}})
		res := m["result"].(map[string]any)
		if res["protocolVersion"] != want || res["capabilities"].(map[string]any)["tools"] == nil || res["instructions"] == "" {
			t.Fatalf("%s: %v", req, res)
		}
	}
}

func TestMCP_NotificationsOnlyIs202Empty(t *testing.T) {
	f := newDen(t, true)
	tok, _ := mcpTok(t, f)
	for _, body := range []any{
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		[]any{map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}},
	} {
		if w := f.do(t, "POST", "/api/den/mcp/agent", body, bearerHdr(tok)); w.Code != 202 || w.Body.Len() != 0 {
			t.Fatalf("%v: %d %q", body, w.Code, w.Body)
		}
	}
}

func TestMCP_ToolsResourcesAndErrors(t *testing.T) {
	f := newDen(t, true)
	tok, _ := mcpTok(t, f)
	call := func(method string, params any) map[string]any {
		return rpc(t, f, tok, map[string]any{"jsonrpc": "2.0", "id": "x", "method": method, "params": params})
	}
	tools := call("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "search_capabilities" || tools[1].(map[string]any)["name"] != "execute_capability" {
		t.Fatalf("tools: %v", tools)
	}
	s := call("tools/call", map[string]any{"name": "search_capabilities", "arguments": map[string]any{"query": "x"}})["result"].(map[string]any)
	if s["isError"] != false || len(s["structuredContent"].(map[string]any)["matches"].([]any)) != 0 {
		t.Fatalf("search: %v", s)
	}
	e := call("tools/call", map[string]any{"name": "execute_capability", "arguments": map[string]any{"name": "foo"}})["result"].(map[string]any)
	text := e["content"].([]any)[0].(map[string]any)["text"].(string)
	if e["isError"] != true || !strings.Contains(text, `"unknown_capability"`) || !strings.Contains(text, `"foo"`) {
		t.Fatalf("execute: %v", e)
	}
	for uri, key := range map[string]string{"skill://index.json": "skills", "automation://index.json": "automations"} {
		c := call("resources/read", map[string]any{"uri": uri})["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
		var body map[string]any
		if c["uri"] != uri || json.Unmarshal([]byte(c["text"].(string)), &body) != nil || body[key] == nil {
			t.Fatalf("%s: %v", uri, c)
		}
	}
	if n := len(call("resources/list", nil)["result"].(map[string]any)["resources"].([]any)); n != 2 {
		t.Fatalf("resources/list: %d", n)
	}
	if call("ping", nil)["result"] == nil || call("prompts/list", nil)["result"] == nil {
		t.Fatal("ping/prompts")
	}
	for method, code := range map[string]float64{"tools/call": -32602, "resources/read": -32002, "nope": -32601} {
		params := map[string]any{"name": "other", "uri": "x://y"}
		if got := call(method, params)["error"].(map[string]any)["code"]; got != code {
			t.Fatalf("%s: %v", method, got)
		}
	}
	if m := rpc(t, f, tok, map[string]any{"jsonrpc": "1.0", "id": 1, "method": "ping"}); m["error"].(map[string]any)["code"] != float64(-32600) {
		t.Fatalf("non-2.0: %v", m)
	}
	if m := rpc(t, f, tok, "{not json"); m["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatalf("parse: %v", m)
	}
}

func TestMCP_BatchKeepsOrderAndDropsNotifications(t *testing.T) {
	f := newDen(t, true)
	tok, _ := mcpTok(t, f)
	w := f.do(t, "POST", "/api/den/mcp/agent", []any{
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"},
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "prompts/list"},
	}, bearerHdr(tok))
	var out []map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != 2 || out[0]["id"] != float64(1) || out[1]["id"] != float64(2) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestInstallConfig(t *testing.T) {
	f := newDen(t, true)
	w := f.do(t, "GET", "/api/den/v1/install-config?token=join-acme-1", nil, nil)
	m := decode(t, w)
	if w.Code != 200 || m["appName"] != "Acme AI" || m["clientName"] != "Acme" || m["webUrl"] != "https://ach.test" ||
		m["apiUrl"] != "https://ach.test" || m["requireSignin"] != true || m["logoUrl"] != nil || m["iconUrl"] != nil {
		t.Fatalf("%d %v", w.Code, m)
	}
	if _, ok := m["logoUrl"]; !ok {
		t.Fatal("logoUrl must be present as null")
	}
	for _, tok := range []string{"unknown-token", "", "short", "join-acme-2"} {
		if w := f.do(t, "GET", "/api/den/v1/install-config?token="+tok, nil, nil); w.Code != 404 || decode(t, w)["error"] == nil {
			t.Fatalf("%q: %d", tok, w.Code)
		}
	}
}

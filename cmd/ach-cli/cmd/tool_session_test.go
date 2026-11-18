// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const legacyGatewayKey = "example-session-key"

func legacyToolGateway(t *testing.T, lists *atomic.Int32, status *atomic.Value) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-ach-key") != legacyGatewayKey {
			t.Error("missing caller credential")
		}
		if r.URL.Path == "/mcp/crm" {
			http.Redirect(w, r, "/mcp/crm/", http.StatusTemporaryRedirect)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			ID     json.RawMessage
			Method string
			Params struct {
				Cursor    string
				Arguments json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "server/discover" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"unknown method"}}`, req.ID)
			return
		}
		result := ""
		if req.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "example-session")
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1"}}`
		} else {
			if r.Header.Get("Mcp-Protocol-Version") != "2025-06-18" || r.Header.Get("Mcp-Session-Id") != "example-session" {
				t.Error("missing negotiated version or session ID")
			}
			switch req.Method {
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
				return
			case "tools/list":
				lists.Add(1)
				if state := status.Load(); state != nil && req.Params.Cursor == "page-two" {
					if state == "transport" {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					result = fmt.Sprintf(`{"tools":[],"_meta":{"litellm.ai/server_outcomes":{"crm":{"status":%q}}}}`, state)
					break
				}
				result = `{"tools":[{"name":"second","inputSchema":{"type":"object"}}]}`
				if req.Params.Cursor == "" {
					result = `{"nextCursor":"page-two","tools":[null,{"name":"first","inputSchema":{
						"type":"object","properties":{"id":{"type":"integer","default":9007199254740993}}},
						"outputSchema":{"type":"object","properties":{"id":{"const":9007199254740997}}}}]}`
				} else if req.Params.Cursor != "page-two" {
					t.Errorf("unexpected cursor %q", req.Params.Cursor)
				}
			case "tools/call":
				if string(req.Params.Arguments) == "{}" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,
						"error":{"code":-32602,"message":"example-session-key example-session"}}`, req.ID)
					return
				}
				if !strings.Contains(string(req.Params.Arguments), "9007199254740993") {
					t.Error("call lost the integer argument")
				}
				result = `{"content":[{"type":"text","text":"record"}],
					"structuredContent":{"id":9007199254740993},"extension":true}`
			default:
				t.Errorf("unexpected method %s", req.Method)
				return
			}
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, result)
	}
}

func TestToolSessionLegacyGateway(t *testing.T) {
	var lists atomic.Int32
	var status atomic.Value
	srv := httptest.NewServer(legacyToolGateway(t, &lists, &status))
	defer srv.Close()
	c := cred{BaseURL: srv.URL, Bearer: legacyGatewayKey}
	cs, err := openToolSession(t.Context(), c, false, "mcp", "crm")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	tools, err := listRuntimeTools(t.Context(), cs, "crm")
	if err != nil || len(tools) != 2 || tools[1].Name != "second" || lists.Load() != 2 {
		t.Fatalf("tools %v, lists %d, error %v", tools, lists.Load(), err)
	}
	schema, err := json.Marshal(tools[0].InputSchema)
	if err != nil || !strings.Contains(string(schema), "9007199254740993") {
		t.Fatalf("schema %s, error %v", schema, err)
	}
	schema, err = json.Marshal(tools[0].OutputSchema)
	if err != nil || !strings.Contains(string(schema), "9007199254740997") {
		t.Fatalf("output schema %s, error %v", schema, err)
	}
	r, err := callRuntimeTool(t.Context(), cs, "first", json.RawMessage(`{"id":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(b), "9007199254740993") || !strings.Contains(string(b), `"extension":true`) {
		t.Fatalf("result %s, error %v", b, err)
	}
	_, err = callRuntimeTool(t.Context(), cs, "first", json.RawMessage(`{}`))
	if err == nil || strings.Contains(err.Error(), "example-session") {
		t.Errorf("RPC error did not redact secrets: %v", err)
	}
	for _, check := range []struct{ status, message string }{
		{"auth_required", "ach-cli mcp login crm"},
		{"unreachable", "unreachable"},
		{"transport", "Service Unavailable"},
	} {
		status.Store(check.status)
		partial, err := listRuntimeTools(t.Context(), cs, "crm")
		if len(partial) != 1 || partial[0].Name != "first" {
			t.Errorf("gateway outcome %q: first page missing from %v", check.status, partial)
		}
		if err == nil || !strings.Contains(err.Error(), check.message) {
			t.Errorf("gateway outcome %q: %v", check.status, err)
		}
	}
}

func TestToolSessionRefusesRedirectOutsideEndpoint(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request reached redirect destination")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path+"/", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := cred{BaseURL: srv.URL, Bearer: "example-key"}
	if _, err := openToolSession(t.Context(), c, false, "mcp", "crm"); err == nil {
		t.Error("redirect succeeded")
	}
}

func TestToolSessionRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../another", "%2e%2e", "a?b", "a#b", "a\\b"} {
		_, err := openToolSession(t.Context(), cred{}, false, "a2a", name)
		if err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("name %q: error %v", name, err)
		}
	}
}

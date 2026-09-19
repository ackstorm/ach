// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWellKnown_ASMetadataAndPRMDocuments(t *testing.T) {
	h := WellKnownHandler("https://ach.example.com/")
	cases := map[string]map[string]any{
		"/.well-known/oauth-authorization-server": {
			"issuer": "https://ach.example.com", "registration_endpoint": "https://ach.example.com/platform/oauth/register",
		},
		"/.well-known/oauth-protected-resource":               {"resource": "https://ach.example.com"},
		"/.well-known/oauth-protected-resource/mcp/my-server": {"resource": "https://ach.example.com/mcp/my-server"},
		"/.well-known/oauth-protected-resource/a2a/agent-1":   {"resource": "https://ach.example.com/a2a/agent-1"},
	}
	for path, want := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		var got map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %v, want %v", path, k, got[k], v)
			}
		}
		if path != "/.well-known/oauth-authorization-server" {
			as, _ := got["authorization_servers"].([]any)
			if len(as) != 1 || as[0] != "https://ach.example.com" {
				t.Errorf("%s: authorization_servers = %v", path, got["authorization_servers"])
			}
		}
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource/v1", "/.well-known/oauth-protected-resource/mcp/", "/.well-known/oauth-protected-resource/mcp/a/b"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestChallengeFor_ServiceRootNeverTheDialledPath(t *testing.T) {
	base := "https://ach.example.com"
	cases := map[string]string{
		"/v1/chat/completions":    base + "/.well-known/oauth-protected-resource",
		"/mcp/my-server":          base + "/.well-known/oauth-protected-resource/mcp/my-server",
		"/mcp/my-server/messages": base + "/.well-known/oauth-protected-resource/mcp/my-server",
		"/a2a/agent-1/tasks/send": base + "/.well-known/oauth-protected-resource/a2a/agent-1",
	}
	for path, doc := range cases {
		got := ChallengeFor(base)(httptest.NewRequest("GET", path, nil))
		if got != `Bearer resource_metadata="`+doc+`"` {
			t.Errorf("%s: %q", path, got)
		}
	}
}

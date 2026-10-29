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
		"/.well-known/oauth-protected-resource/v1":            {"resource": "https://ach.example.com/v1"},
		"/.well-known/oauth-protected-resource/gemini":        {"resource": "https://ach.example.com/gemini"},
		"/.well-known/oauth-protected-resource/mcp/my-server": {"resource": "https://ach.example.com/mcp/my-server"},
		"/.well-known/oauth-protected-resource/a2a/agent-1":   {"resource": "https://ach.example.com/a2a/agent-1"},
		// DELIBERATE, not an oversight: no Environment declares these names
		// and the document is served anyway. The handler holds no resolver
		// (WellKnownHandler takes only a base URL) precisely so this endpoint
		// cannot become an existence oracle for anonymous callers — D-15
		// applied to the anonymous surface. If you are here to "fix" this by
		// 404-ing unknown names, read the WellKnownHandler doc comment first:
		// the cost of that fix is letting anyone on the internet enumerate
		// every MCP server and a2a agent configured in the deployment.
		"/.well-known/oauth-protected-resource/mcp/no-such-server-xyz": {"resource": "https://ach.example.com/mcp/no-such-server-xyz"},
		"/.well-known/oauth-protected-resource/a2a/no-such-agent-xyz":  {"resource": "https://ach.example.com/a2a/no-such-agent-xyz"},
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
	// Malformed shapes 404. "/mcp/foo@bar" is here because ChallengeFor must
	// agree with this handler on it: "@" is outside the name class, so neither
	// side may treat the input as the service root "/mcp/foo".
	for _, p := range []string{"/.well-known/oauth-protected-resource/v1/chat", "/.well-known/oauth-protected-resource/mcp/", "/.well-known/oauth-protected-resource/mcp/a/b", "/.well-known/oauth-protected-resource/mcp/foo@bar"} {
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
		"/v1/chat/completions":    base + "/.well-known/oauth-protected-resource/v1",
		"/ui":                     base + "/.well-known/oauth-protected-resource",
		"/mcp/my-server":          base + "/.well-known/oauth-protected-resource/mcp/my-server",
		"/mcp/my-server/messages": base + "/.well-known/oauth-protected-resource/mcp/my-server",
		"/a2a/agent-1/tasks/send": base + "/.well-known/oauth-protected-resource/a2a/agent-1",
		// A malformed name falls back to the API-root document rather than
		// truncating to "/mcp/foo" and pointing at a service the caller never
		// dialled. Same verdict the PRM handler reaches above.
		"/mcp/foo@bar": base + "/.well-known/oauth-protected-resource",
	}
	for path, doc := range cases {
		got := ChallengeFor(base)(httptest.NewRequest("GET", path, nil))
		if got != `Bearer resource_metadata="`+doc+`"` {
			t.Errorf("%s: %q", path, got)
		}
	}
}

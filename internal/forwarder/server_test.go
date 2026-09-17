// SPDX-License-Identifier: Apache-2.0

package forwarder_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder"
)

// TestRouteAcceptsBareAndSubpathNames pins the routing contract that a bare
// "/mcp/<name>" (no trailing slash) reaches the authenticated handler chain,
// not chi's 404. Regression guard for the hydrate-emitted endpoint
// (platformapi/hydrate/handler.go writes "/mcp/<name>" verbatim) being dropped
// by a "/{name}/*"-only route table.
//
// The probe sends NO x-ach-key header. Authn (the group's first middleware)
// then short-circuits with 401 "missing_key" BEFORE touching the nil resolver
// or any upstream — so the status discriminates cleanly:
//   - 401  => the route matched and the request entered the Authn group (PASS)
//   - 404  => chi found no matching route (the bug this test guards against)
func TestRouteAcceptsBareAndSubpathNames(t *testing.T) {
	upstream, err := url.Parse("http://litellm.invalid")
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	h := forwarder.New(forwarder.Deps{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		LiteLLMUpstream: upstream,
	})

	paths := []string{
		"/mcp/vmcp-zoho",       // bare — the previously-404ing form
		"/mcp/vmcp-zoho/",      // trailing slash
		"/mcp/vmcp-zoho/tools", // subpath
		"/a2a/agent-x",         // bare a2a
		"/a2a/agent-x/",        // trailing slash a2a
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, p, nil)
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("POST %s: route did not match (404) — expected to reach Authn", p)
			continue
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s: got %d, want 401 (reached Authn group)", p, rec.Code)
		}
	}
}

// TestV2RegisteredInsideAuthnGroup pins B.3.1: /v2/* sits INSIDE the same
// pamw.Authn group as /v1/*, so an unauthenticated request is rejected
// identically (401), never 404 (route missing) and never 200 (bypass).
func TestV2RegisteredInsideAuthnGroup(t *testing.T) {
	upstream, err := url.Parse("http://litellm.invalid")
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	h := forwarder.New(forwarder.Deps{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		LiteLLMUpstream: upstream,
	})
	for _, p := range []string{"/v1/model/info", "/v2/model/info"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s: got %d, want 401 (inside Authn group)", p, rec.Code)
		}
	}
}

// TestProtectedResourceMetadataIsAnonymous pins issue #177: the RFC 9728
// protected-resource document must be reachable WITHOUT a credential (§5
// assumes an unauthenticated GET — a client fetches it precisely because it
// holds none yet) and is now COMPOSED BY ACH: `resource` is the service root
// under ACH_BASE_URL and `authorization_servers` names ACH's own AS, never
// LiteLLM's. LiteLLM is not contacted.
//
// Status discriminates: 404 => not mounted; 401 => wrongly inside the Authn
// group; 200 => served anonymously (PASS).
func TestProtectedResourceMetadataIsAnonymous(t *testing.T) {
	litellmCalls := 0
	litellm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		litellmCalls++
		_, _ = io.WriteString(w, `{"resource":"ok"}`)
	}))
	defer litellm.Close()

	upstream, err := url.Parse(litellm.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	h := forwarder.New(forwarder.Deps{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		LiteLLMUpstream: upstream,
		BaseURL:         "https://ach.example.com",
	})

	for path, resource := range map[string]string{
		"/.well-known/oauth-protected-resource/mcp/mcp-gitlab-ro": "https://ach.example.com/mcp/mcp-gitlab-ro",
		"/.well-known/oauth-protected-resource":                   "https://ach.example.com",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200 (mounted, anonymous)", path, rec.Code)
		}
		var doc struct {
			Resource string   `json:"resource"`
			AS       []string `json:"authorization_servers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Resource != resource || len(doc.AS) != 1 || doc.AS[0] != "https://ach.example.com" {
			t.Errorf("%s: %+v", path, doc)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"registration_endpoint":"https://ach.example.com/platform/oauth/register"`) {
		t.Errorf("AS metadata: %d %s", rec.Code, rec.Body)
	}
	if litellmCalls != 0 {
		t.Errorf("LiteLLM must not be consulted for discovery documents; calls=%d", litellmCalls)
	}
}

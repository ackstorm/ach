// SPDX-License-Identifier: Apache-2.0

package forwarder_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

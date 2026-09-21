// SPDX-License-Identifier: Apache-2.0

package forwarder_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
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

// TestNoCatchAll pins D-18: the forwarder proxies ONLY its four families
// (+ the anonymous /.well-known documents). Every other path LiteLLM serves
// (/ui, /key/*, /health, /model/*, /v2/*, …) is a 404 here — with or
// without a credential — and never reaches the upstream. LiteLLM's own
// surface is reached on LiteLLM's own host, not through ACH.
func TestNoCatchAll(t *testing.T) {
	upstreamHits := 0
	litellm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusTeapot)
	}))
	defer litellm.Close()
	upstream, _ := url.Parse(litellm.URL)
	h := forwarder.New(forwarder.Deps{
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		LiteLLMUpstream:  upstream,
		Resolver:         ekOnlyResolver{},
		KeyEncryptionKey: make([]byte, 32),
		AuthnOptions: pamw.AuthnOptions{Headers: []pamw.CredentialHeader{
			{Name: "x-genai-api-key", Mode: pamw.ModePassthrough}, {Name: "x-ach-key", Mode: pamw.ModeResolve}}},
	})
	do := func(path string, hdr map[string]string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/", "/ui", "/ui/", "/key/info", "/health/liveliness", "/model/info", "/v2/anything", "/sso/key/generate"} {
		for name, hdr := range map[string]map[string]string{
			"anonymous":       nil,
			"raw backend key": {"x-genai-api-key": "cust-1"},
			"ach credential":  {"x-ach-key": "pk-whatever"},
			"foreign bearer":  {"Authorization": "Bearer ui-token"},
		} {
			if c := do(path, hdr); c != http.StatusNotFound {
				t.Fatalf("%s (%s): %d, want 404 — no catch-all (D-18)", path, name, c)
			}
		}
	}
	if upstreamHits != 0 {
		t.Fatalf("an unowned path reached the upstream %d times", upstreamHits)
	}
	// The owned families keep their contract: a credential is required.
	// /v2/model/info is the one D-18 exception — ach-agent prices its usage
	// there with its ek_ (litellm_usage cost source); it is an OWNED route
	// (credential required, never anonymous), not a catch-all.
	for _, p := range []string{"/v1/models", "/v2/model/info"} {
		if c := do(p, nil); c != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d, want 401", p, c)
		}
	}
	if c := do("/v2/model/info?model=x", map[string]string{"x-ach-key": "ek_live"}); c != http.StatusTeapot {
		t.Fatalf("ek_ on /v2/model/info: %d, want the upstream's 418", c)
	}
	// A foreign Authorization on an owned family is not ours to judge: it is
	// forwarded untouched with no ACH identity, and LiteLLM decides.
	if c := do("/v1/models", map[string]string{"Authorization": "Bearer ui-token"}); c != http.StatusTeapot {
		t.Fatalf("foreign Authorization on /v1: %d, want the upstream's 418", c)
	}
}

// nilResolver answers "unknown credential" for everything.
type nilResolver struct{}

// ekOnlyResolver knows exactly one live ek_ ("ek_live"); everything else is
// unknown.
type ekOnlyResolver struct{}

func (ekOnlyResolver) Resolve(_ context.Context, plaintext string) (*keystore.KeyInfo, error) {
	if plaintext == "ek_live" {
		return &keystore.KeyInfo{KeyID: "ekid_live", KeyType: keys.PrefixEk, OwnerEmail: "u@x.com", Environment: "demo"}, nil
	}
	return nil, nil
}

func (nilResolver) Resolve(context.Context, string) (*keystore.KeyInfo, error) { return nil, nil }

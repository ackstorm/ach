// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceRoutes(t *testing.T) {
	routes := ServiceRoutes("ach-system")

	want := map[string]string{
		"/platform/":     "http://ach-platform-api.ach-system.svc.cluster.local:80",
		"/content/":      "http://ach-content-service.ach-system.svc.cluster.local:8082",
		"/v1/":           "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/gemini/":       "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/mcp/":          "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/a2a/":          "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/.well-known/":  "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/v2/model/info": "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/":              "http://ach-platform-api.ach-system.svc.cluster.local:80",
	}

	if len(routes) != len(want) {
		t.Fatalf("got %d routes, want %d", len(routes), len(want))
	}
	for _, r := range routes {
		wantUpstream, ok := want[r.Prefix]
		if !ok {
			t.Errorf("unexpected prefix %q", r.Prefix)
			continue
		}
		if r.Upstream != wantUpstream {
			t.Errorf("prefix %q: got upstream %q, want %q", r.Prefix, r.Upstream, wantUpstream)
		}
	}
}

func TestServiceRoutesHonorsNamespace(t *testing.T) {
	routes := ServiceRoutes("prod-ns")
	for _, r := range routes {
		if r.Prefix == "/platform/" {
			if r.Upstream != "http://ach-platform-api.prod-ns.svc.cluster.local:80" {
				t.Fatalf("namespace not honored: %q", r.Upstream)
			}
			return
		}
	}
	t.Fatal("/platform/ route missing")
}

// TestRootRouteIsLastMatch pins that the "/" route (the console, D-26)
// never shadows a service prefix or the gateway's own /healthz: net/http's
// ServeMux picks the longest registered pattern.
func TestRootRouteIsLastMatch(t *testing.T) {
	h, err := Handler(ServiceRoutes("ns"), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := h.(*http.ServeMux)
	for path, want := range map[string]string{
		"/": "/", "/index.html": "/", "/assets/app.js": "/", "/openwork": "/", "/api/den/v1/me": "/", "/ui": "/",
		"/v2/model/info": "/v2/model/info", "/v2/other": "/",
		"/platform/keys": "/platform/", "/content/x": "/content/", "/v1/models": "/v1/", "/healthz": "/healthz",
		"/metrics": "/metrics", "/metrics/": "/metrics/", "/metrics/x": "/metrics/",
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern != want {
			t.Errorf("%s matched %q, want %q", path, pattern, want)
		}
	}
}

// TestRootGoesToPlatformAPI: "/" is the console served by platform-api
// (D-26), never the forwarder — LiteLLM's surface is not reachable through
// ACH (D-18).
func TestRootGoesToPlatformAPI(t *testing.T) {
	routes := ServiceRoutes("ns")
	last := routes[len(routes)-1]
	if last.Prefix != "/" || !strings.Contains(last.Upstream, "ach-platform-api") {
		t.Fatalf("last route = %+v, want / -> ach-platform-api (D-26)", last)
	}
	for _, r := range routes[:len(routes)-1] {
		if r.Prefix == "/" {
			t.Fatalf("duplicate / route: %+v", r)
		}
	}
}

// TestMetricsNeverProxied: the "/" route must not carry /metrics out
// through the public Ingress — platform-api serves its own Prometheus
// handler on the same port (cmd/ach/cmd/platform_api.go composes
// /metrics next to the API), so without the pin the console route would
// export it.
func TestMetricsNeverProxied(t *testing.T) {
	h, err := Handler(ServiceRoutes("ns"), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/metrics", "/metrics/", "/metrics/anything"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, w.Code)
		}
	}
}

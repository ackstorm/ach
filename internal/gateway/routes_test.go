// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceRoutes(t *testing.T) {
	routes := ServiceRoutes("ach-system")

	want := map[string]string{
		"/platform/":    "http://ach-platform-api.ach-system.svc.cluster.local:80",
		"/content/":     "http://ach-content-service.ach-system.svc.cluster.local:8082",
		"/v1/":          "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/gemini/":      "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/mcp/":         "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/a2a/":         "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/.well-known/": "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/":             "http://ach-forwarder.ach-system.svc.cluster.local:80",
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

// TestCatchAllIsLastMatch pins that the "/" route never shadows a service
// prefix or the gateway's own /healthz: net/http's ServeMux picks the
// longest registered pattern.
func TestCatchAllIsLastMatch(t *testing.T) {
	h, err := Handler(ServiceRoutes("ns"), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := h.(*http.ServeMux)
	for path, want := range map[string]string{
		"/ui": "/", "/key/info": "/", "/health/liveliness": "/",
		"/platform/keys": "/platform/", "/content/x": "/content/", "/v1/models": "/v1/", "/healthz": "/healthz",
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern != want {
			t.Errorf("%s matched %q, want %q", path, pattern, want)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0

package gateway

import "testing"

func TestServiceRoutes(t *testing.T) {
	routes := ServiceRoutes("ach-system")

	want := map[string]string{
		"/platform/":    "http://ach-platform-api.ach-system.svc.cluster.local:80",
		"/content/":     "http://ach-content-service.ach-system.svc.cluster.local:8082",
		"/v1/":          "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/v2/":          "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/gemini/":      "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/mcp/":         "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/a2a/":         "http://ach-forwarder.ach-system.svc.cluster.local:80",
		"/.well-known/": "http://ach-forwarder.ach-system.svc.cluster.local:80",
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

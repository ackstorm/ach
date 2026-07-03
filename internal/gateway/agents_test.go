// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeResolver map[string]string

func (f fakeResolver) Upstream(ns, serviceName string) (string, bool) {
	u, ok := f[ns+"/"+serviceName]
	return u, ok
}

func TestAgentsHandler_StripsPrefixAndProxies(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	resolver := fakeResolver{"ach-system/achagent-gh": backend.URL}
	h, err := Handler(nil, resolver, slog.Default())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	// Tail is opaque — forwarded verbatim, whatever it is.
	for _, tc := range []struct{ reqPath, wantPath string }{
		{"/agents/ach-system/achagent-gh/channels/github-review/events", "/channels/github-review/events"},
		{"/agents/ach-system/achagent-gh/a2a/anything", "/a2a/anything"},
		{"/agents/ach-system/achagent-gh", "/"},  // no tail → "/"
		{"/agents/ach-system/achagent-gh/", "/"}, // trailing slash → "/"
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://gw"+tc.reqPath, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", tc.reqPath, rec.Code)
		}
		if gotPath != tc.wantPath {
			t.Fatalf("%s: upstream path = %q, want %q", tc.reqPath, gotPath, tc.wantPath)
		}
	}
}

func TestAgentsHandler_UnknownAgentIs404(t *testing.T) {
	h, err := Handler(nil, fakeResolver{}, slog.Default())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://gw/agents/ns/achagent-missing/channels/x/events", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAgentsHandler_MalformedPathIs404(t *testing.T) {
	h, _ := Handler(nil, fakeResolver{"ns/achagent-n": "http://x"}, slog.Default())
	for _, p := range []string{"http://gw/agents/ns", "http://gw/agents/ns/"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, p, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", p, rec.Code)
		}
	}
}

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

func (f fakeResolver) Upstream(ns, name string) (string, bool) {
	u, ok := f[ns+"/"+name]
	return u, ok
}

func TestHookHandler_StripsPrefixAndProxies(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	resolver := fakeResolver{"ach-system/gh": backend.URL}
	h, err := Handler(nil, resolver, slog.Default())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"http://gw/hook/ach-system/gh/channels/github-review/events", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPath != "/channels/github-review/events" {
		t.Fatalf("upstream path = %q, want /channels/github-review/events", gotPath)
	}
}

func TestHookHandler_UnknownAgentIs404(t *testing.T) {
	h, err := Handler(nil, fakeResolver{}, slog.Default())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://gw/hook/ns/missing/channels/x/events", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHookHandler_MalformedPathIs404(t *testing.T) {
	h, _ := Handler(nil, fakeResolver{"ns/n": "http://x"}, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://gw/hook/ns", nil) // no name, no tail
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

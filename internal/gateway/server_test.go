// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerRoutesByPrefix(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "platform:"+r.URL.Path)
	}))
	defer platform.Close()
	forwarder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "forwarder:"+r.URL.Path)
	}))
	defer forwarder.Close()

	routes := []Route{
		{Prefix: "/platform/", Upstream: platform.URL},
		{Prefix: "/v1/", Upstream: forwarder.URL},
	}
	h, err := Handler(routes, nil, slog.Default())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	cases := []struct {
		path, wantBody string
		wantStatus     int
	}{
		{"/platform/auth/login", "platform:/platform/auth/login", http.StatusOK},
		{"/v1/chat/completions", "forwarder:/v1/chat/completions", http.StatusOK},
		{"/healthz", "ok\n", http.StatusOK},
		{"/metrics", "", http.StatusNotFound},
		{"/unknown", "", http.StatusNotFound},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://gw"+tc.path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != tc.wantStatus {
			t.Errorf("%s: status got %d want %d", tc.path, rec.Code, tc.wantStatus)
		}
		if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
			t.Errorf("%s: body got %q want %q", tc.path, rec.Body.String(), tc.wantBody)
		}
	}
}

func TestHandlerRejectsBadUpstream(t *testing.T) {
	_, err := Handler([]Route{{Prefix: "/x/", Upstream: "://not a url"}}, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for malformed upstream, got nil")
	}
}

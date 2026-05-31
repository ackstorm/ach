// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestNewReverseProxyPreservesPathAndClearsHost(t *testing.T) {
	var gotPath, gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHost = r.Host
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	target, _ := url.Parse(backend.URL)
	rp := newReverseProxy(target, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "http://gateway.example/v1/chat/completions", nil)
	req.Host = "gateway.example"
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path: got %q want /v1/chat/completions", gotPath)
	}
	// req.Host cleared => upstream Host is the backend's own host, never
	// the client-supplied "gateway.example".
	if gotHost == "gateway.example" {
		t.Errorf("client Host leaked to upstream: %q", gotHost)
	}
}

func TestNewReverseProxyFlushIntervalForStreaming(t *testing.T) {
	target, _ := url.Parse("http://example.invalid:80")
	rp := newReverseProxy(target, slog.Default())
	if rp.FlushInterval != -1 {
		t.Fatalf("FlushInterval: got %d want -1 (immediate flush for SSE)", rp.FlushInterval)
	}
}

func TestNewReverseProxyDeadUpstreamReturns502(t *testing.T) {
	// Port 1 on loopback is reliably unconnectable.
	target, _ := url.Parse("http://127.0.0.1:1")
	rp := newReverseProxy(target, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "http://gateway.example/v1/x", nil)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d want 502", rec.Code)
	}
}

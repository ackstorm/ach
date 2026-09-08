// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder"
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

// TestGateway_PreservesAtInPluginPath verifies that the reverse proxy forwards
// plugin paths containing a literal "@" or its percent-encoded form "%40" to
// the upstream without mangling the separator. Scoped plugin refs such as
// /content/plugin/code-review@mkt must reach content-service intact.
//
// Go's httputil.NewSingleHostReverseProxy preserves req.URL.RawPath when it is
// set (populated by the HTTP server for any percent-encoded char), so %40
// round-trips as %40. For a literal @, url.EscapedPath() leaves it unescaped
// because @ is a valid pchar per RFC 3986 — so it also passes through as @.
func TestNewReverseProxyClientCancelNo502(t *testing.T) {
	// A caller that disconnects mid-proxy (an /mcp SSE stream closed by
	// opencode) cancels the inbound request context. That is a client
	// disconnect, not an upstream fault: the gateway must NOT emit a 502
	// (the client is already gone) and must NOT log an error.
	target, _ := url.Parse("http://127.0.0.1:1")
	rp := newReverseProxy(target, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client already gone
	req := httptest.NewRequest(http.MethodGet, "http://gateway.example/mcp/mcp-gitlab", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadGateway {
		t.Fatalf("client cancel must not yield 502; got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("client cancel must write no body; got %q", rec.Body.String())
	}
}

func TestGateway_PreservesAtInPluginPath(t *testing.T) {
	var gotEscapedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	rp := newReverseProxy(target, slog.Default())

	cases := []struct {
		name string
		path string
		want string // substring that must appear in the upstream-observed escaped path
	}{
		{
			name: "literal @",
			path: "/content/plugin/code-review@mkt",
			want: "@",
		},
		{
			name: "percent-encoded %40",
			// Build the request URL explicitly so the percent-encoding survives
			// URL parsing: set RawPath to preserve %40 rather than decoding it.
			path: "/content/plugin/code-review%40mkt",
			want: "%40",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotEscapedPath = ""

			// Parse the path into a url.URL preserving any percent-encoding in
			// RawPath (required so the HTTP client ships the wire form intact).
			u, err := url.ParseRequestURI(tc.path)
			if err != nil {
				t.Fatalf("ParseRequestURI(%q): %v", tc.path, err)
			}
			req := &http.Request{
				Method:     http.MethodGet,
				URL:        u,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
			}
			rec := httptest.NewRecorder()
			rp.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d want 200", rec.Code)
			}
			if !strings.Contains(gotEscapedPath, tc.want) {
				t.Errorf("upstream saw escaped path %q; want it to contain %q (separator was mangled)", gotEscapedPath, tc.want)
			}
		})
	}
}

// Issue #177: the public hostname the client dialled must survive the hop in
// X-Forwarded-Host, and a client-supplied value must NOT (it is overwritten,
// not appended). Without this a backend behind ACH serves RFC 9728 metadata
// naming its own canonical URL, which every spec-compliant MCP client rejects.
func TestNewReverseProxySetsXForwardedHost(t *testing.T) {
	var got []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("X-Forwarded-Host")
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	target, _ := url.Parse(backend.URL)
	rp := newReverseProxy(target, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "http://ach.example.com/.well-known/oauth-protected-resource/mcp/demo", nil)
	req.Host = "ach.example.com"
	req.Header.Set("X-Forwarded-Host", "spoofed.evil")
	rp.ServeHTTP(httptest.NewRecorder(), req)

	if len(got) != 1 || got[0] != "ach.example.com" {
		t.Fatalf("X-Forwarded-Host: got %v want [ach.example.com]", got)
	}
}

// TestXForwardedHostSurvivesBothHops composes the real gateway hop onto the
// real forwarder hop and asserts the public hostname reaches the upstream.
//
// Issue #177 asks for exactly this: "Worth a test asserting the header
// survives both hops, since a regression there is invisible — the backend
// silently falls back to its canonical URL and the client failure looks
// identical to this bug being unfixed." Each hop is covered in isolation
// elsewhere (this file for the gateway, headers.StripAndRewrite's table for
// the forwarder); only a composed test catches a strip introduced BETWEEN them.
//
// The anonymous /.well-known route is used as the vehicle because it is the
// one forwarder route that needs no key resolver — the header handling it
// exercises is the shared Director path every route uses.
func TestXForwardedHostSurvivesBothHops(t *testing.T) {
	var gotFwdHost, gotFwdProto string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFwdHost = r.Header.Get("X-Forwarded-Host")
		gotFwdProto = r.Header.Get("X-Forwarded-Proto")
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	fwd := httptest.NewServer(forwarder.New(forwarder.Deps{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		LiteLLMUpstream: upstreamURL,
	}))
	defer fwd.Close()

	fwdURL, _ := url.Parse(fwd.URL)
	gw := newReverseProxy(fwdURL, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "http://ach.example.com/.well-known/oauth-protected-resource/mcp/demo", nil)
	req.Host = "ach.example.com"
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if gotFwdHost != "ach.example.com" {
		t.Errorf("X-Forwarded-Host after gateway+forwarder: got %q want ach.example.com", gotFwdHost)
	}
	if gotFwdProto != "http" {
		t.Errorf("X-Forwarded-Proto after gateway+forwarder: got %q want http", gotFwdProto)
	}
}

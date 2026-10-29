// SPDX-License-Identifier: Apache-2.0

package extract_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// TestFetchContent_Plugin asserts the GET URL path is exactly
// /content/plugin/<name> and that the request method is GET (no
// POST/HEAD/etc.). The fixture sends back a fixed body; the test
// only inspects the request shape.
func TestFetchContent_Plugin(t *testing.T) {
	const name = "demo-plugin"
	const wantPath = "/content/plugin/demo-plugin"

	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()

	client := &httpclient.Client{
		BaseURL: srv.URL,
		APIKey:  "pk_test",
	}
	resp, err := extract.FetchContent(context.Background(), client, extract.KindPlugin, name)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodGet)
	}
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
}

// TestFetchContent_Prompt asserts the kind path segment routes
// distinctly for KindPrompt — the Content Service URL discriminates
// per-kind.
func TestFetchContent_Prompt(t *testing.T) {
	const name = "leak-claude-code"
	const wantPath = "/content/prompt/leak-claude-code"

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("prompt body"))
	}))
	defer srv.Close()

	client := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	resp, err := extract.FetchContent(context.Background(), client, extract.KindPrompt, name)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
}

// TestFetchContent_Artifact asserts KindArtifact routing.
func TestFetchContent_Artifact(t *testing.T) {
	const name = "openclaw-templates"
	const wantPath = "/content/artifact/openclaw-templates"

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("art body"))
	}))
	defer srv.Close()

	client := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	resp, err := extract.FetchContent(context.Background(), client, extract.KindArtifact, name)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
}

// TestFetchContent_ResponseBodyVerbatim asserts the response body is
// delivered byte-for-byte from the upstream — no re-encoding, no
// transformation. The 100-byte deterministic body is consumed by the
// test and compared against the server's source.
func TestFetchContent_ResponseBodyVerbatim(t *testing.T) {
	body := make([]byte, 100)
	for i := range body {
		body[i] = byte(i)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	resp, err := extract.FetchContent(context.Background(), client, extract.KindPlugin, "x")
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body bytes differ: got len=%d, want len=%d", len(got), len(body))
	}
}

// TestFetchContent_NoConditionalHeaders is the D-15 invariant gate:
// the request MUST NOT carry If-None-Match, If-Modified-Since, or
// Range. Phase 7 deliberately ships an unconditional GET so the
// disk-write short-circuit (sha256 compare) lives in StageAndPublish,
// not in HTTP semantics. A future regression that adds conditional
// fetch behavior would silently violate STATE-11; this test catches it.
func TestFetchContent_NoConditionalHeaders(t *testing.T) {
	var ifNoneMatch, ifModifiedSince, rangeHdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ifNoneMatch = r.Header.Get("If-None-Match")
		ifModifiedSince = r.Header.Get("If-Modified-Since")
		rangeHdr = r.Header.Get("Range")
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	client := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	resp, err := extract.FetchContent(context.Background(), client, extract.KindPlugin, "x")
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ifNoneMatch != "" {
		t.Errorf("If-None-Match = %q, want empty (D-15 unconditional)", ifNoneMatch)
	}
	if ifModifiedSince != "" {
		t.Errorf("If-Modified-Since = %q, want empty (D-15 unconditional)", ifModifiedSince)
	}
	if rangeHdr != "" {
		t.Errorf("Range = %q, want empty (D-15 unconditional)", rangeHdr)
	}
}

// TestFetchContent_PreservesExtraHeaders asserts that
// client.ExtraHeaders flow through to the request — confirms the
// pk_ + x-ach-environment routing path the W3-05 cobra wiring will
// use.
func TestFetchContent_PreservesExtraHeaders(t *testing.T) {
	const envName = "demo"
	var gotEnv string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnv = r.Header.Get("x-ach-environment")
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	client := &httpclient.Client{
		BaseURL:      srv.URL,
		APIKey:       "pk_test",
		ExtraHeaders: http.Header{"x-ach-environment": []string{envName}},
	}
	resp, err := extract.FetchContent(context.Background(), client, extract.KindPlugin, "x")
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotEnv != envName {
		t.Errorf("x-ach-environment = %q, want %q", gotEnv, envName)
	}
}

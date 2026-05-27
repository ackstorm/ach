// SPDX-License-Identifier: Apache-2.0

//go:build integration

// LEGACY TESTS — Plan 05-05 Task 3b rewrites the handler. These tests
// assume the pre-rewrite §8 stub semantics (no authn, no envcache, no
// projection-row lookups). They remain compilable but are gated by
// the `integration` build tag so `make unit` does not run them.
// Plan 05-05 Task 4 owns the end-to-end rewrite of this file using
// testcontainers Postgres + miniredis + httptest LiteLLM mocks; once
// that rewrite lands these legacy tests are deleted and the new
// integration suite replaces them.

package contentservice

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// seedCache writes the canonical example fixtures into a fresh tempdir
// laid out per cachefs.SubDirs. Returns the cache root.
func seedCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"prompt", "plugin", "artifact", "marketplace", ".tmp"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(p string, b []byte) {
		t.Helper()
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "prompt", "claude-code-system-prompt"),
		[]byte("# Claude Code\n\nYou are a helpful assistant.\n"))
	must(filepath.Join(root, "plugin", "caveman.tar.gz"),
		// Magic bytes are gzip's; body is irrelevant — handler does
		// not inspect content, only file metadata.
		[]byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00})
	must(filepath.Join(root, "artifact", "openclaw-templates.tar.gz"),
		[]byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00})
	must(filepath.Join(root, "artifact", "single-file"),
		[]byte("raw bytes\n"))
	return root
}

// staticPromptLookup serves as the test double for the production
// k8s-cached lookup. Returns "" (handler default text/markdown) when
// name absent.
func staticPromptLookup(m map[string]string) PromptContentTypeLookup {
	return func(_ context.Context, name string) (string, error) {
		if ct, ok := m[name]; ok {
			return ct, nil
		}
		return "", nil
	}
}

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := seedCache(t)
	r := chi.NewRouter()
	RegisterRoutes(r, Deps{
		CacheRoot:           root,
		PromptContentTypeFn: staticPromptLookup(map[string]string{"claude-code-system-prompt": "text/markdown"}),
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, root
}

func TestHandler_PromptBody(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/prompt/claude-code-system-prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Errorf("Content-Type=%q, want text/markdown*", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control=%q, want public, max-age=300", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), "# Claude Code") {
		t.Errorf("body=%q, want prefix '# Claude Code'", string(body))
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Error("Content-Length empty")
	}
}

func TestHandler_PluginGzip(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/plugin/caveman")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type=%q, want application/gzip", got)
	}
}

func TestHandler_ArtifactDirectoryScope(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/artifact/openclaw-templates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type=%q, want application/gzip", got)
	}
}

func TestHandler_ArtifactObjectScope(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/artifact/single-file")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type=%q, want application/octet-stream", got)
	}
}

func TestHandler_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/prompt/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}

func TestHandler_InvalidName(t *testing.T) {
	srv, _ := newTestServer(t)
	// chi may treat path-traversal differently; we check the routed
	// path (no slash in name) only.
	resp, err := http.Get(srv.URL + "/content/prompt/.hidden")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 400 or 404", resp.StatusCode)
	}
}

func TestHandler_HealthZ(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status=%d, want 200", resp.StatusCode)
	}
}

func TestHandler_RangeReturns206(t *testing.T) {
	srv, _ := newTestServer(t)
	req, _ := http.NewRequest("GET",
		srv.URL+"/content/prompt/claude-code-system-prompt", nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status=%d, want 206", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 10 {
		t.Errorf("body length=%d, want 10", len(body))
	}
	if got := resp.Header.Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-9/") {
		t.Errorf("Content-Range=%q, want 'bytes 0-9/*' prefix", got)
	}
}

func TestHandler_ContentLengthMatchesBody(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/content/prompt/claude-code-system-prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	clHeader := resp.Header.Get("Content-Length")
	cl, err := strconv.Atoi(clHeader)
	if err != nil {
		t.Fatalf("Content-Length %q not int: %v", clHeader, err)
	}
	body, _ := io.ReadAll(resp.Body)
	if cl != len(body) {
		t.Errorf("Content-Length=%d but body=%d bytes", cl, len(body))
	}
	if te := resp.Header.Get("Transfer-Encoding"); te != "" {
		t.Errorf("Transfer-Encoding=%q, want empty (identity)", te)
	}
}

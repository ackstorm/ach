// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeTempFile creates a tempfile under t.TempDir with the supplied
// bytes and returns the open *os.File ready for read (seek-0). The file
// is closed by t.Cleanup.
func writeTempFile(t *testing.T, content []byte) *os.File {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write tempfile: %v", err)
	}
	f, err := os.Open(path) // #nosec G304 — test fixture path under t.TempDir
	if err != nil {
		t.Fatalf("open tempfile: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestStream_SetsHeaders(t *testing.T) {
	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i % 256)
	}
	f := writeTempFile(t, body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/content/plugin/whatever", nil)

	n, err := stream(rec, req, f, "application/gzip", int64(len(body)))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if n != int64(len(body)) {
		t.Errorf("bytes written=%d, want %d", n, len(body))
	}

	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type=%q, want application/gzip", got)
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length=%q, want %d", got, len(body))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
	if rec.Body.Len() != len(body) {
		t.Errorf("body length=%d, want %d", rec.Body.Len(), len(body))
	}
}

func TestStream_IgnoresRangeHeader(t *testing.T) {
	body := []byte("0123456789ABCDEF") // 16 bytes
	f := writeTempFile(t, body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/content/prompt/x", nil)
	req.Header.Set("Range", "bytes=0-3")

	if _, err := stream(rec, req, f, "text/markdown", int64(len(body))); err != nil {
		t.Fatalf("stream: %v", err)
	}
	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200 (NOT 206 — Range MUST be ignored)", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length=%q, want full size %d", got, len(body))
	}
	if rec.Body.Len() != len(body) {
		t.Errorf("body length=%d, want full body %d", rec.Body.Len(), len(body))
	}
}

func TestStream_IgnoresIfNoneMatch(t *testing.T) {
	body := []byte("hello")
	f := writeTempFile(t, body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/content/prompt/x", nil)
	req.Header.Set("If-None-Match", `"deadbeef"`)

	if _, err := stream(rec, req, f, "text/markdown", int64(len(body))); err != nil {
		t.Fatalf("stream: %v", err)
	}
	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200 (NOT 304 — If-None-Match MUST be ignored)", resp.StatusCode)
	}
	if rec.Body.Len() != len(body) {
		t.Errorf("body length=%d, want full body", rec.Body.Len())
	}
}

func TestStream_IgnoresIfModifiedSince(t *testing.T) {
	body := []byte("hello world")
	f := writeTempFile(t, body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/content/prompt/x", nil)
	// A far-future date so a Last-Modified-aware handler would have returned 304.
	req.Header.Set("If-Modified-Since", "Fri, 31 Dec 2100 23:59:59 GMT")

	if _, err := stream(rec, req, f, "text/markdown", int64(len(body))); err != nil {
		t.Fatalf("stream: %v", err)
	}
	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200 (NOT 304 — If-Modified-Since MUST be ignored)", resp.StatusCode)
	}
	if rec.Body.Len() != len(body) {
		t.Errorf("body length=%d, want full body", rec.Body.Len())
	}
}

func TestStream_NoTransferEncoding(t *testing.T) {
	body := make([]byte, 4096)
	f := writeTempFile(t, body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/content/artifact/x", nil)

	if _, err := stream(rec, req, f, "application/octet-stream", int64(len(body))); err != nil {
		t.Fatalf("stream: %v", err)
	}
	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if te := resp.Header.Get("Transfer-Encoding"); te != "" {
		t.Errorf("Transfer-Encoding=%q, want empty (identity transfer; Content-Length is set)", te)
	}
	// net/http httptest.ResponseRecorder does not synthesize Transfer-Encoding;
	// guard explicitly that we never set it ourselves either.
	for _, v := range resp.Header.Values("Transfer-Encoding") {
		if strings.EqualFold(v, "chunked") {
			t.Errorf("Transfer-Encoding contains chunked: %v", resp.Header.Values("Transfer-Encoding"))
		}
	}
}

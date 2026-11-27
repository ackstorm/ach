// SPDX-License-Identifier: Apache-2.0

package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<html>console</html>")},
		"assets/app.1.js": {Data: []byte("js")},
	}
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSPA_DefaultTheme(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte(htmlOpen + "console</html>")}}
	if body := get(SPA(dist, "red"), "/console/").Body.String(); !strings.Contains(body, `class="red" data-default-theme="red"`) {
		t.Fatalf("themed index: %q", body)
	}
	if body := get(SPA(dist, ""), "/console/").Body.String(); !strings.HasPrefix(body, htmlOpen) {
		t.Fatalf("unthemed index: %q", body)
	}
}

func TestSPA_ServesIndexAndAssets(t *testing.T) {
	h := SPA(testDist(), "")
	if rec := get(h, "/console/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "console") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("/console/: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "/console/assets/app.1.js"); rec.Code != 200 || rec.Body.String() != "js" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestSPA_RootRedirectsToConsole(t *testing.T) {
	h := SPA(testDist(), "")
	for _, p := range []string{"/", "/console"} {
		if rec := get(h, p); rec.Code != 302 || rec.Header().Get("Location") != "/console/" {
			t.Fatalf("%s: %d %q, want 302 /console/", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestSPA_UnknownPathIs404(t *testing.T) {
	h := SPA(testDist(), "")
	for _, p := range []string{
		"/pepeluissssd", "/env/demo", "/keys", "/stats?range=7d", "/assets/app.1.js", "/index.html",
		"/console/nope", "/console/assets/missing.js", "/console/assets/", "/consolex",
		"/ui", "/key/list", "/platform/nope", "/platform/", "/healthz", "/livez", "/readyz",
	} {
		if rec := get(h, p); rec.Code != 404 || !strings.Contains(rec.Header().Get("Content-Type"), "json") {
			t.Fatalf("%s: %d %q — must be a JSON 404, not index.html", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("POST /nope: %d, want 404", rec.Code)
	}
}

func TestSPA_ConsoleNotBuilt(t *testing.T) {
	if rec := get(SPA(fstest.MapFS{}, ""), "/console/"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not built") {
		t.Fatalf("empty dist: %d %q", rec.Code, rec.Body.String())
	}
}

func TestSPA_MethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	SPA(testDist(), "").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/console/", nil))
	if rec.Code != 405 {
		t.Fatalf("POST /console/: %d", rec.Code)
	}
}

// Dist is the embedded build: with only .gitkeep tracked it is a valid,
// index-less filesystem — the binary builds without a UI build.
func TestDist_EmbedsWithoutABuild(t *testing.T) {
	if _, err := Dist().Open("."); err != nil {
		t.Fatal(err)
	}
}

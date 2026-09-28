// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func servePlugin(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	PluginHandler("https://ach.test/", "acme")(rec, httptest.NewRequest(http.MethodGet, "/clients/opencode/plugin", nil))
	return rec
}

func untar(t *testing.T, body io.Reader) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(body)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = b
	}
}

// The served bytes are an npm tarball: gzip, entries under package/, a
// package.json whose main is shipped beside it.
func TestPluginHandler_NpmTarball(t *testing.T) {
	rec := servePlugin(t)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("status %d content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	files := untar(t, rec.Body)
	var pkg struct {
		Name string `json:"name"`
		Main string `json:"main"`
	}
	if err := json.Unmarshal(files["package/package.json"], &pkg); err != nil {
		t.Fatalf("package/package.json: %v (entries %v)", err, keys(files))
	}
	if pkg.Name == "" || len(files["package/"+pkg.Main]) == 0 {
		t.Fatalf("main %q missing from %v", pkg.Main, keys(files))
	}
}

func TestPluginHandler_PlatformJSON(t *testing.T) {
	a, b := servePlugin(t), servePlugin(t)
	if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
		t.Fatal("tarball bytes must be deterministic")
	}
	files := untar(t, a.Body)
	if got := string(files["package/platform.json"]); got != `{"api":"https://ach.test/v1","platform":"https://ach.test","provider":"acme"}` {
		t.Fatalf("platform.json = %s", got)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

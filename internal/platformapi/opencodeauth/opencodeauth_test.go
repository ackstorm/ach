// SPDX-License-Identifier: Apache-2.0

package opencodeauth

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The served bytes are an npm tarball: gzip, entries under package/, a
// package.json whose main is shipped beside it.
func TestHandler_NpmTarball(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/platform/opencode-auth", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("status %d content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = b
	}
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

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// tinyPluginTarGz builds a minimal gzip-compressed tar carrying one regular
// file, so the extractor's gzip dispatch lands a DIRECTORY at the final path
// (the W6-01 Bug E shape — re-extracting over a pre-existing directory).
func tinyPluginTarGz(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "manifest.txt",
		Mode: 0o644,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestExtractorImpl_ReHydrate_NoOpAndReplace is the W6-01 Bug E proof. A
// plugin tar.gz extracts to a DIRECTORY; before the fix the SECOND hydrate
// crashed (`read <dir>: is a directory` in the step-3 sha256 short-circuit,
// then a rename over a non-empty dir). It asserts:
//
//   - 1st ExtractContent (prev=nil): extracts, non-empty WrittenFiles.
//   - 2nd ExtractContent with prior state recording the same SourceHash and
//     the SAME upstream bytes: NO-OP — zero WrittenFiles (FilesWritten==0 at
//     the orchestrator) and the directory is preserved.
//   - 3rd ExtractContent with prior state but CHANGED upstream bytes:
//     delete-before-replace re-extracts cleanly (no crash, non-empty
//     WrittenFiles, new content on disk).
func TestExtractorImpl_ReHydrate_NoOpAndReplace(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()

	// A mutable body so the third call can serve changed bytes.
	body := tinyPluginTarGz(t, "v1 plugin payload")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)

	hc := &httpclient.Client{BaseURL: ts.URL, APIKey: "pk_test"}
	ext, _ := hydrate.NewWiring(hc, "claude-code", extract.DefaultLimits(), false, false, false, hydrate.ConflictNamespace)
	ref := manifest.ContentRef{
		ID:          "caveman",
		Name:        "caveman.tar.gz",
		DownloadURL: ts.URL + "/content/plugin/caveman.tar.gz",
	}
	finalDir := filepath.Join(achDir, "plugin", "caveman.tar.gz")

	// 1st hydrate — fresh (no prior state).
	res1, err := ext.ExtractContent(context.Background(), ref, achDir, nil)
	if err != nil {
		t.Fatalf("first ExtractContent: %v", err)
	}
	if len(res1.WrittenFiles) == 0 {
		t.Fatalf("first hydrate WrittenFiles empty; want >=1")
	}
	if info, serr := os.Stat(finalDir); serr != nil || !info.IsDir() {
		t.Fatalf("first hydrate: expected directory at %s (err=%v)", finalDir, serr)
	}

	// Build prior state mirroring what step-12 composition records for this
	// content: one entry per written file, all sharing the archive SourceHash.
	prev := &state.File{SchemaVersion: "3"}
	for _, fw := range res1.WrittenFiles {
		prev.Plugins = append(prev.Plugins, state.FileEntry{
			Target:     fw.Target,
			Hash:       fw.Hash,
			SourceHash: res1.SourceHash,
		})
	}

	// 2nd hydrate — identical upstream bytes → no-op skip.
	res2, err := ext.ExtractContent(context.Background(), ref, achDir, prev)
	if err != nil {
		t.Fatalf("second ExtractContent (no-op): %v", err)
	}
	if len(res2.WrittenFiles) != 0 {
		t.Errorf("second hydrate WrittenFiles = %d; want 0 (no-op skip)", len(res2.WrittenFiles))
	}
	if res2.SourceHash != res1.SourceHash {
		t.Errorf("second hydrate SourceHash = %q; want %q (unchanged)", res2.SourceHash, res1.SourceHash)
	}
	if info, serr := os.Stat(finalDir); serr != nil || !info.IsDir() {
		t.Errorf("second hydrate: directory not preserved at %s (err=%v)", finalDir, serr)
	}

	// 3rd hydrate — changed upstream bytes → delete-before-replace re-extract.
	body = tinyPluginTarGz(t, "v2 plugin payload CHANGED")
	res3, err := ext.ExtractContent(context.Background(), ref, achDir, prev)
	if err != nil {
		t.Fatalf("third ExtractContent (replace): %v", err)
	}
	if len(res3.WrittenFiles) == 0 {
		t.Errorf("third hydrate WrittenFiles empty; want >=1 (re-extract on change)")
	}
	if res3.SourceHash == res1.SourceHash {
		t.Errorf("third hydrate SourceHash unchanged (%q); want a different hash for changed bytes", res3.SourceHash)
	}
	got, rerr := os.ReadFile(filepath.Join(finalDir, "manifest.txt"))
	if rerr != nil {
		t.Fatalf("third hydrate: read replaced file: %v", rerr)
	}
	if string(got) != "v2 plugin payload CHANGED" {
		t.Errorf("third hydrate: on-disk content = %q; want the v2 payload (delete-before-replace)", got)
	}
}

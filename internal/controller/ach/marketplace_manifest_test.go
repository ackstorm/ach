// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// buildTarGz produces an in-memory tar.gz with the given path→content map.
// Used to synthesize the staged tarball verifyPluginManifest walks.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("Write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

func TestVerifyPluginManifest_PresentAtRoot(t *testing.T) {
	// Whole-repo tar (subtree=""): manifest at .claude-plugin/plugin.json.
	tgz := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"x"}`,
		"README.md":                  "hi",
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz), ""); err != nil {
		t.Errorf("verify: %v; want nil", err)
	}
}

func TestVerifyPluginManifest_PresentInSubtree(t *testing.T) {
	// Subtree tar: manifest at plugins/x/.claude-plugin/plugin.json.
	tgz := buildTarGz(t, map[string]string{
		"plugins/x/.claude-plugin/plugin.json": `{"name":"x"}`,
		"plugins/x/README.md":                  "hi",
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz), "plugins/x"); err != nil {
		t.Errorf("verify: %v; want nil", err)
	}
}

func TestVerifyPluginManifest_Missing(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{
		"README.md": "no manifest here",
	})
	err := verifyPluginManifest(bytes.NewReader(tgz), "")
	if err == nil {
		t.Fatal("expected error on missing manifest")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of sources.ErrUpstreamInvalid", err)
	}
	if !strings.Contains(err.Error(), "plugin.json") {
		t.Errorf("err message should mention plugin.json; got %q", err.Error())
	}
}

func TestVerifyPluginManifest_SubtreeButOnlyRootManifest(t *testing.T) {
	// Subtree=plugins/x; manifest only at top-level — should fail.
	tgz := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"top"}`,
		"plugins/x/README.md":        "no manifest in this subtree",
	})
	err := verifyPluginManifest(bytes.NewReader(tgz), "plugins/x")
	if err == nil {
		t.Fatal("expected error: manifest in wrong location")
	}
}

func TestVerifyPluginManifest_LeadingDotSlashSubtreeTolerated(t *testing.T) {
	// local-path entries arrive with "./plugins/x" style paths — the
	// verifier must normalize ./ prefixes so it doesn't double up.
	tgz := buildTarGz(t, map[string]string{
		"plugins/x/.claude-plugin/plugin.json": `{}`,
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz), "./plugins/x"); err != nil {
		t.Errorf("verify: %v; want nil with ./ prefix", err)
	}
}

func TestVerifyPluginManifest_CorruptGzip(t *testing.T) {
	// Non-gzip input must surface as wrapped ErrUpstreamInvalid (the
	// gzip.NewReader failure branch).
	err := verifyPluginManifest(bytes.NewReader([]byte("not gzip")), "")
	if err == nil {
		t.Fatal("expected error on corrupt gzip")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of ErrUpstreamInvalid", err)
	}
}

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
	// Real-world layout: git.tarSubtree always strips the subtree
	// prefix, so the manifest, when present, is always at the tar root
	// — regardless of whether the entry was git-subdir/url with a
	// `path:` field or a whole-repo github/url with no path.
	tgz := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"x"}`,
		"README.md":                  "hi",
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil", err)
	}
}

func TestVerifyPluginManifest_LeadingDotSlashTolerated(t *testing.T) {
	// Some tar writers prefix entries with "./". The verifier must
	// normalize that prefix before comparing.
	tgz := buildTarGz(t, map[string]string{
		"./.claude-plugin/plugin.json": `{}`,
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil with leading ./", err)
	}
}

func TestVerifyPluginManifest_Missing(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{
		"README.md": "no manifest here",
	})
	err := verifyPluginManifest(bytes.NewReader(tgz))
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

func TestVerifyPluginManifest_BuriedInSubdirRejected(t *testing.T) {
	// Manifest must be at the tar root — a tar that buries it under a
	// subdirectory (which would happen if git.tarSubtree ever stopped
	// stripping the subtree, or if the upstream repo nested the
	// manifest one level too deep) MUST be rejected. This test pins
	// the contract: only the tar-root location counts.
	tgz := buildTarGz(t, map[string]string{
		"plugins/x/.claude-plugin/plugin.json": `{"name":"x"}`,
	})
	err := verifyPluginManifest(bytes.NewReader(tgz))
	if err == nil {
		t.Fatal("expected error: manifest must be at tar root")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of ErrUpstreamInvalid", err)
	}
}

func TestVerifyPluginManifest_CorruptGzip(t *testing.T) {
	// Non-gzip input must surface as wrapped ErrUpstreamInvalid (the
	// gzip.NewReader failure branch).
	err := verifyPluginManifest(bytes.NewReader([]byte("not gzip")))
	if err == nil {
		t.Fatal("expected error on corrupt gzip")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of ErrUpstreamInvalid", err)
	}
}

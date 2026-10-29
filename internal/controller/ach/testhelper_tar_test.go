// SPDX-License-Identifier: Apache-2.0

// Shared tar-building helper for controller/ach tests. The same helper
// exists in internal/contentkit (package contentkit) for contentkit unit
// tests; this copy lives in package ach so envtest tests can build
// synthetic tarballs without crossing package boundaries.

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// buildTarGz produces an in-memory tar.gz with the given path→content map.
// Used by pluginmarketplace_envtest_test.go to synthesize staged tarballs
// that contentkit.VerifyPluginContents / contentkit.VerifySkillContents walk.
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

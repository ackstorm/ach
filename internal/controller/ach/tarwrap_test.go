// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

func TestWrapSingleFileTarGz_RoundTrip(t *testing.T) {
	payload := []byte("# Example\n\npirate caveman prompt\n")
	got, err := wrapSingleFileTarGz(payload, "example1.md")
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !isGzipMagic(got) {
		t.Fatalf("output is not gzip (magic %x)", got[:2])
	}
	gz, err := gzip.NewReader(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar next: %v", err)
	}
	if hdr.Name != "example1.md" {
		t.Errorf("entry name=%q, want example1.md", hdr.Name)
	}
	body, _ := io.ReadAll(tr)
	if !bytes.Equal(body, payload) {
		t.Errorf("entry body=%q, want %q", body, payload)
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("expected single entry, got more (err=%v)", err)
	}
}

func TestSourceBasename(t *testing.T) {
	cases := []struct {
		name string
		spec sources.SourceSpec
		want string
	}{
		{"github file", sources.SourceSpec{Type: "github", GitHub: &achv1alpha1.GitHubSource{Path: "ANTHROPIC/CLAUDE-FABLE-5.md"}}, "CLAUDE-FABLE-5.md"},
		{"github empty path", sources.SourceSpec{Type: "github", GitHub: &achv1alpha1.GitHubSource{Path: ""}}, ""},
		{"s3 key", sources.SourceSpec{Type: "s3", S3: &achv1alpha1.S3Source{Key: "prompts/sys.txt"}}, "sys.txt"},
		{"http url", sources.SourceSpec{Type: "http", HTTP: &achv1alpha1.HTTPSource{URL: "https://x.example/a/b/p.md?ref=1"}}, "p.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceBasename(tc.spec); got != tc.want {
				t.Errorf("sourceBasename=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsGzipMagic(t *testing.T) {
	if !isGzipMagic([]byte{0x1f, 0x8b, 0x08}) {
		t.Error("gzip magic not detected")
	}
	if isGzipMagic([]byte("# markdown")) {
		t.Error("markdown wrongly detected as gzip")
	}
	if isGzipMagic([]byte{0x1f}) {
		t.Error("1-byte slice must be false")
	}
}

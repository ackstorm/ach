// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/url"
	"path"

	"github.com/ackstorm/ach/internal/sources"
)

// wrapSingleFileTarGz returns a gzip-compressed tar archive containing exactly
// one regular-file entry named entryName with the given bytes. Used at
// ingestion to give single-file context (Prompt, Artifact scope=object) the
// same gzip-tar shape as skills/plugins/directory-artifacts, so every context
// kind decompresses uniformly and the real source filename survives inside the
// archive.
func wrapSingleFileTarGz(data []byte, entryName string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     entryName,
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sourceBasename returns the basename of the upstream file a single-file source
// points at (used as the tar entry name). Returns "" when the source names no
// concrete file (empty/degenerate path), in which case the caller falls back
// to the CR name.
func sourceBasename(spec sources.SourceSpec) string {
	var p string
	switch spec.Type {
	case "github":
		if spec.GitHub != nil {
			p = spec.GitHub.Path
		}
	case "gitlab":
		if spec.GitLab != nil {
			p = spec.GitLab.Path
		}
	case "bitbucket":
		if spec.Bitbucket != nil {
			p = spec.Bitbucket.Path
		}
	case "s3":
		if spec.S3 != nil {
			p = spec.S3.Key
		}
	case "gcs":
		if spec.GCS != nil {
			p = spec.GCS.Object
		}
	case "http":
		if spec.HTTP != nil {
			p = spec.HTTP.URL
			if u, err := url.Parse(p); err == nil {
				p = u.Path
			}
		}
	}
	base := path.Base(p)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

// isGzipMagic reports whether b begins with the gzip magic header (0x1f 0x8b).
// A body that is already gzip is an upstream tarball (directory fetch) and must
// NOT be re-wrapped.
func isGzipMagic(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

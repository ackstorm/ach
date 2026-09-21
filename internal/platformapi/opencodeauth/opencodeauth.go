// SPDX-License-Identifier: Apache-2.0

// Package opencodeauth serves the OpenCode auth plugin as an npm tarball at
// GET /platform/opencode-auth, so a user installs it from the same origin
// that runs the authorization server it logs into:
//
//	opencode plugin https://<ach>/platform/opencode-auth -g
//
// The plugin source lives beside this file (plugin/) and is embedded into the
// binary; the tarball is built once at process start.
package opencodeauth

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"net/http"
	"sort"
)

//go:embed plugin
var plugin embed.FS

// tarball is the npm layout: every file under a top-level "package/" dir.
var tarball = build()

func build() []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var names []string
	_ = fs.WalkDir(plugin, "plugin", func(p string, d fs.DirEntry, _ error) error {
		if !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names) // deterministic bytes
	for _, p := range names {
		b, err := plugin.ReadFile(p)
		if err != nil {
			panic(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "package/" + p[len("plugin/"):], Mode: 0o644, Size: int64(len(b))}); err != nil {
			panic(err)
		}
		if _, err := tw.Write(b); err != nil {
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Handler serves the tarball. Anonymous by nature: it is fetched by a user
// who holds no credential yet.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="opencode-auth.tgz"`)
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(tarball)
	}
}

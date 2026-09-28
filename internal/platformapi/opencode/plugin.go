// SPDX-License-Identifier: Apache-2.0

// Package opencode serves ACH's OpenCode client surface under /clients/opencode:
// the auth plugin as an npm tarball (plugin.go) and the per-user config the
// plugin's config hook pulls at every start (config.go). A user installs the
// plugin from the same origin that runs the authorization server it logs into:
//
//	opencode plugin https://<ach>/clients/opencode/plugin -g
//
// The plugin source lives beside this file (plugin/) and is embedded into the
// binary; the tarball is built once, when the route is mounted.
package opencode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

//go:embed plugin
var plugin embed.FS

// build tars plugin/ plus package/platform.json under package/ (npm layout).
// Deterministic bytes: sorted names, zero mtimes, no gzip header time.
func build(platformJSON []byte) []byte {
	files := map[string][]byte{"package/platform.json": platformJSON}
	_ = fs.WalkDir(plugin, "plugin", func(p string, d fs.DirEntry, _ error) error {
		if !d.IsDir() && p != "plugin/platform.json" {
			b, err := plugin.ReadFile(p)
			if err != nil {
				panic(err)
			}
			files["package/"+p[len("plugin/"):]] = b
		}
		return nil
	})
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		b := files[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(b))}); err != nil {
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

// PluginHandler serves the tarball for this deployment. platform.json tells
// the product-neutral plugin where its backend is: api for OAuth discovery
// and the fallback provider, platform (the origin serving /clients/*) for its
// config, provider for the provider id it signs in. Anonymous by nature: it
// is fetched by a user who holds no credential yet.
func PluginHandler(baseURL, provider string) http.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	u, err := url.Parse(base)
	if err != nil {
		panic(err) // ACH_BASE_URL is validated at startup
	}
	doc, _ := json.Marshal(map[string]string{"api": base + "/v1", "platform": u.Scheme + "://" + u.Host, "provider": provider})
	tarball := build(doc)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="opencode-auth.tgz"`)
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(tarball)
	}
}

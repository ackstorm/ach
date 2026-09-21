// SPDX-License-Identifier: Apache-2.0

// Package console serves the React console at "/" and the
// /platform/console/* contracts the console calls (session bootstrap and
// capability views). The React build lands in dist/ (make ui-build) and is
// embedded into the binary — the same shape as opencodeauth's plugin/.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// dist is the Vite build output. "all:" keeps .gitkeep embeddable so a
// checkout without a UI build still compiles; SPA then answers 404
// "console not built" instead of failing at go build.
//
//go:embed all:dist
var dist embed.FS

// Dist is the embedded build as a plain fs.FS rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the directory is embedded above; cannot fail
	}
	return sub
}

// apiPrefixes are never served by the SPA fallback: an unknown API path
// stays a JSON 404 (API-01's carve-out plus everything under /platform).
var apiPrefixes = []string{"/platform/", "/healthz", "/livez", "/readyz"}

// SPA returns the "/" handler: a real file is served as is (hashed assets
// immutable), anything else gets index.html (client-side routing) with
// no-store. openworkHandoff turns "/?desktopAuth=1" into a redirect to
// /openwork (D-26: the origin-only OpenWork sign-in URL is rescued on "/"
// only).
func SPA(fsys fs.FS, openworkHandoff bool) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for _, pre := range apiPrefixes {
			if strings.HasPrefix(p, pre) {
				render.Error(w, http.StatusNotFound, "not_found", "no such route", middleware.RequestIDFromCtx(r.Context()))
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if p == "/" && openworkHandoff && r.URL.Query().Get("desktopAuth") == "1" {
			http.Redirect(w, r, "/openwork?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		name := strings.TrimPrefix(p, "/")
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("console not built: run make ui-build\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(index)
	})
}

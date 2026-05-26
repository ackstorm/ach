// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// PromptContentTypeLookup returns the Content-Type override for a
// Prompt by metadata.name, or empty string when not set (caller falls
// back to the §8 default text/markdown). Implementations close over a
// k8s cached client in production; tests use a static map.
type PromptContentTypeLookup func(ctx context.Context, name string) (string, error)

// Deps bundles the handler's runtime collaborators. ZeroValue.Logger
// falls back to slog.Default(); ZeroValue.PromptContentTypeFn falls
// back to "always return empty" (handler default content-type).
type Deps struct {
	CacheRoot           string
	PromptContentTypeFn PromptContentTypeLookup
	Logger              *slog.Logger
}

// RegisterRoutes wires the four routes onto r:
//
//	GET /healthz
//	GET /content/prompt/{name}
//	GET /content/plugin/{name}
//	GET /content/artifact/{name}
//
// Routes are registered explicitly per kind (not via {kind} URL param)
// so chi can return 404 for /content/marketplace/<name> without the
// handler ever running — keeping the kind allow-list at the routing
// layer rather than inside the handler body.
func RegisterRoutes(r chi.Router, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.PromptContentTypeFn == nil {
		d.PromptContentTypeFn = func(context.Context, string) (string, error) { return "", nil }
	}
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/content/prompt/{name}", d.serve(kindPrompt))
	r.Get("/content/plugin/{name}", d.serve(kindPlugin))
	r.Get("/content/artifact/{name}", d.serve(kindArtifact))
}

func (d Deps) serve(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")

		candidates, err := ResolvePath(d.CacheRoot, kind, name)
		if err != nil {
			if errors.Is(err, ErrInvalidName) {
				http.Error(w, "invalid name", http.StatusBadRequest)
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Walk candidates in order (artifact has 2; others have 1).
		// First os.Open success wins.
		var (
			f       *os.File
			fi      os.FileInfo
			openErr error
			path    string
		)
		for _, p := range candidates {
			f, openErr = os.Open(p) // #nosec G304 — path is filepath.Join(cacheRoot, kind, validatedName)
			if openErr == nil {
				fi, openErr = f.Stat()
				if openErr == nil {
					path = p
					break
				}
				_ = f.Close()
				f = nil
			}
		}
		if f == nil {
			if errors.Is(openErr, fs.ErrNotExist) || os.IsNotExist(openErr) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			d.Logger.Error("open cache file", "kind", kind, "name", name, "err", openErr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()

		// Content-Type: kind-specific policy; prompts may carry an
		// explicit CR-level override.
		var override string
		if kind == kindPrompt {
			ct, lookupErr := d.PromptContentTypeFn(r.Context(), name)
			if lookupErr != nil {
				// Lookup failure must not block serving the body —
				// fall through to the default content-type. The
				// reason: the cache file IS authoritative; the CR
				// only carries the content-type hint.
				d.Logger.Warn("prompt lookup failed", "name", name, "err", lookupErr)
			}
			override = ct
		}
		w.Header().Set("Content-Type", ContentTypeForFile(kind, path, override))
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))

		// http.ServeContent handles Range, If-Modified-Since,
		// If-None-Match. It also calls io.CopyBuffer which on Linux
		// engages sendfile(2) via *os.File.WriteTo when w's
		// underlying conn is *net.TCPConn (the production case).
		http.ServeContent(w, r, path, fi.ModTime(), f)
	}
}

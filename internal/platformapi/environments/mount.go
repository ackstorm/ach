// SPDX-License-Identifier: Apache-2.0

package environments

import (
	"github.com/go-chi/chi/v5"
)

// Mount returns a chi.Router subtree wiring the two read endpoints. The
// caller mounts the subtree at "/platform/environments" inside the
// authenticated chi.Group (Plan 03-11's server.go composes this).
//
// Routes:
//
//	GET /             — ListHandler (filtered list with pagination)
//	GET /{name}       — GetHandler  (single environment)
func Mount(deps Deps) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/", ListHandler(deps))
		r.Get("/{name}", GetHandler(deps))
	}
}

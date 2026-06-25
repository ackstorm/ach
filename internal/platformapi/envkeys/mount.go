// SPDX-License-Identifier: Apache-2.0

package envkeys

import "github.com/go-chi/chi/v5"

// Mount returns the chi.Router subtree constructor for the
// /platform/env-keys endpoint family. Plan 03-11's cmd/platform-api
// server.go wires the Authn-gated group like this:
//
//	r.Group(func(r chi.Router) {
//	    r.Use(middleware.Authn(deps.Resolver, deps.Allowlist, deps.Audit))
//	    r.Route("/platform/env-keys", envkeys.Mount(deps))
//	    envkeys.MountKeys(r, deps)
//	})
//
// The four registered routes correspond to the §15.5 endpoint quartet:
//
//   - POST   /                — CreateHandler (§8.2 8-step flow).
//   - GET    /                — ListHandler   (paginated read).
//   - GET    /{key_id}        — GetHandler    (single-row read).
//   - DELETE /{key_id}        — RevokeHandler (§8.5 LiteLLM-first).
//
// chi.URLParam("key_id") extracts the path parameter inside GetHandler
// and RevokeHandler.
func Mount(deps Deps) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", CreateHandler(deps))
		r.Get("/", ListHandler(deps))
		r.Get("/{key_id}", GetHandler(deps))
		r.Delete("/{key_id}", RevokeHandler(deps))
	}
}

// MountKeys registers the /platform/keys routes on the router r at the same
// level as /platform/env-keys (sibling, not child). server.go calls this
// alongside r.Route("/platform/env-keys", Mount(deps)).
//
// Routes:
//
//   - GET    /platform/keys           — ListAllHandler  (caller-scoped list).
//   - DELETE /platform/keys/{key_id}  — RevokePersonalHandler (owner-scoped pk_ revoke).
func MountKeys(r chi.Router, deps Deps) {
	r.Route("/platform/keys", func(r chi.Router) {
		r.Get("/", ListAllHandler(deps))
		r.Delete("/{key_id}", RevokePersonalHandler(deps))
	})
}

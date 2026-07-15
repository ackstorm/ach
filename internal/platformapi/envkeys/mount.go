// SPDX-License-Identifier: Apache-2.0

package envkeys

import "github.com/go-chi/chi/v5"

// MountKeys registers the /platform/keys endpoint family (the sole key-management
// surface; the former /platform/env-keys family was folded in 2026-06).
//
//   - POST   /platform/keys           — CreateHandler  (§8.2 ek_ create flow).
//   - GET    /platform/keys           — ListAllHandler (caller-scoped pk_ + ek_).
//   - DELETE /platform/keys/{key_id}  — RevokeHandler  (prefix-dispatched:
//     ekid_ → LiteLLM-first 204; pkid_ → DB-first 200, active-key 409 guard).
func MountKeys(r chi.Router, deps Deps) {
	r.Route("/platform/keys", func(r chi.Router) {
		r.Post("/", CreateHandler(deps))
		r.Get("/", ListAllHandler(deps))
		r.Delete("/{key_id}", RevokeHandler(deps))
	})
}

// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/platformapi/admin/inventory"
)

// Mount returns a chi.Router subtree configurator that:
//
//  1. Applies the AdminOnly middleware to the entire subtree so every
//     descendant route is gated by the allowlist + key-type checks per
//     Hub §15.5 + §18 + API-12.
//  2. Registers the three admin endpoints:
//     - POST /keys/revoke              → RevokeKeyHandler
//     - POST /users/{email}/revoke-keys → RevokeUserKeysHandler
//     - POST /refresh                  → ForceRefreshHandler
//
// The caller wires this under the authenticated chi.Group (Plan 03-11
// cmd/platform-api/main.go):
//
//	r.Group(func(r chi.Router) {
//	    r.Use(middleware.Authn(deps.Resolver, deps.Allowlist, deps.Audit))
//	    r.Route("/platform/admin", admin.Mount(deps))
//	})
//
// Authn populates KeyContext (including IsAdmin per BLK-02) so
// AdminOnly's allowlist + key-type checks here see the resolved
// identity. AdminOnly is intentionally re-applied inside Mount rather
// than relying on IsAdmin so the rejection paths surface uniform
// outcomes (`invalid_key_type` for ek_, `not_admin` for missing-
// allowlist pk_) and emit the right audit event.
func Mount(deps Deps) func(r chi.Router) {
	return func(r chi.Router) {
		r.Use(AdminOnly(deps.Allowlist, deps.Audit, deps.Namespace))
		r.Post("/keys/revoke", RevokeKeyHandler(deps))
		r.Post("/users/{email}/revoke-keys", RevokeUserKeysHandler(deps))
		r.Post("/refresh", ForceRefreshHandler(deps))

		// Read-only object inventory (GET) — projection reads from Postgres,
		// gated by the same AdminOnly middleware. environments has no route
		// here: the CLI uses the existing GET /platform/environments (admin
		// sees all rows via that handler's admin bypass).
		inv := inventory.Deps{Lister: inventory.NewLister(deps.Pool, deps.Namespace)}
		r.Get("/plugins", inventory.PluginsHandler(inv))
		r.Get("/prompts", inventory.PromptsHandler(inv))
		r.Get("/artifacts", inventory.ArtifactsHandler(inv))
		r.Get("/marketplaces", inventory.MarketplacesHandler(inv))
		r.Get("/bips", inventory.BIPsHandler(inv))
		r.Get("/litellm-connections", inventory.LitellmConnectionsHandler(inv))
		r.Get("/external-refs", inventory.ExternalRefsHandler(inv))
	}
}

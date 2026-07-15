// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/platformapi/admin/inventory"
	runtimecatalog "github.com/ackstorm/ach/internal/platformapi/admin/runtime"
)

// Mount returns a chi.Router subtree configurator that:
//
//  1. Applies the AdminOnly middleware to the entire subtree so every
//     descendant route is gated by the allowlist + key-type checks per
//     Hub §15.5 + §18 + API-12.
//  2. Registers key-management endpoints:
//     - GET  /keys                     → ListKeysHandler
//     - POST /keys/revoke              → RevokeKeyHandler
//     - POST /users/{email}/revoke-keys → RevokeUserKeysHandler
//     - POST /refresh                  → ForceRefreshHandler
//  3. Registers read-only object inventory endpoints (GET):
//     /plugins, /prompts, /artifacts, /skills, /marketplaces,
//     /skill-marketplaces, /bips.
//  4. Registers read-only runtime catalog endpoints (GET):
//     /runtime/models, /runtime/mcp-servers, /runtime/a2a-agents,
//     /runtime/teams, /runtime/catalog.
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
		r.Get("/keys", ListKeysHandler(deps))
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
		r.Get("/skills", inventory.SkillsHandler(inv))
		r.Get("/marketplaces", inventory.MarketplacesHandler(inv))
		r.Get("/skill-marketplaces", inventory.SkillMarketplacesHandler(inv))
		r.Get("/bips", inventory.BIPsHandler(inv))

		rcDeps := runtimecatalog.Deps{
			Catalog:   runtimecatalog.NewPoolCatalog(deps.Pool),
			Namespace: deps.Namespace,
			Connector: "default",
		}
		r.Get("/runtime/models", runtimecatalog.ModelsHandler(rcDeps))
		r.Get("/runtime/mcp-servers", runtimecatalog.MCPServersHandler(rcDeps))
		r.Get("/runtime/a2a-agents", runtimecatalog.A2AAgentsHandler(rcDeps))
		r.Get("/runtime/teams", runtimecatalog.TeamsHandler(rcDeps))
		r.Get("/runtime/catalog", runtimecatalog.CatalogHandler(rcDeps))
	}
}

// SPDX-License-Identifier: Apache-2.0

package openwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/platformapi/auth"
)

// Store kinds. Grants are single-use and short-lived (Take = GETDEL);
// tokens are the desktop's long-lived session credential.
const (
	grantKind = "openwork_grant"
	tokenKind = "openwork_token"
)

// Deps for the Den. Session resolves the console cookie for the handoff
// page; Store is the AS's Valkey store.
type Deps struct {
	Config
	Store      *auth.OAuthStore
	BaseURL    string
	CookieName string
	Session    func(ctx context.Context, sid string) (email string, ok bool, err error)
}

type denSession struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// denError is the envelope the desktop reads (den.ts: payload.error +
// payload.message); a bare {"detail": …} collapses into a generic failure
// string with no actionable code.
func denError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}

func denJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// userIDFor is a stable, opaque per-user id; the desktop only needs a string.
func userIDFor(email string) string {
	sum := sha256.Sum256([]byte(email))
	return "user_" + hex.EncodeToString(sum[:])[:24]
}

func (d Deps) organization() map[string]string {
	sum := sha256.Sum256([]byte(d.OrgSlug))
	return map[string]string{"id": "organization_" + hex.EncodeToString(sum[:])[:16], "slug": d.OrgSlug, "name": d.OrgName}
}

// denAPIBase is the API base the desktop must use — what the deep link hands it.
func (d Deps) denAPIBase() string { return strings.TrimRight(d.BaseURL, "/") + "/openwork/api/den" }

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// requireToken resolves the desktop's bearer or writes the Den 401.
func (d Deps) requireToken(w http.ResponseWriter, r *http.Request) (denSession, bool) {
	tok := bearer(r)
	if tok == "" {
		denError(w, 401, "unauthorized", "This request needs a session token.")
		return denSession{}, false
	}
	var s denSession
	ok, err := d.Store.Get(r.Context(), tokenKind, tok, &s)
	if err != nil || !ok {
		denError(w, 401, "unauthorized", "This session token is unknown or expired.")
		return denSession{}, false
	}
	return s, true
}

const allowedHeaders = "authorization,content-type,accept,x-organization-id,x-openwork-org-id,x-openwork-legacy-org-id"

// cors reflects the caller's origin on Den routes only: the desktop is not
// a page on our origin and sends credentials: include, so "*" is unusable.
// Scoped to the Den prefixes, so the cookie-authenticated API keeps its
// same-origin posture.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
			h.Set("Access-Control-Allow-Headers", allowedHeaders)
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Mount registers the handoff page + brand marks under /openwork and the
// Den API under both /api/den and /openwork/api/den. Nothing is mounted
// unless Config.Enabled — in the composed platform-api server those paths
// then fall to the console SPA fallback (index.html), not a 404.
func Mount(r chi.Router, d Deps) {
	if !d.Enabled {
		return
	}
	r.Get("/openwork", d.handoff)
	r.Get("/openwork/", d.handoff)
	r.Get("/openwork/brand/{name}", d.brand)
	for _, prefix := range []string{"/api/den", "/openwork/api/den"} {
		r.Route(prefix, func(r chi.Router) {
			r.Use(cors)
			r.Post("/v1/auth/desktop-handoff/exchange", d.exchange)
			r.Get("/v1/me", d.me)
			r.Get("/v1/me/orgs", d.orgs)
			r.Post("/v1/me/active-organization", d.activeOrg)
			r.Get("/v1/resources", d.resources)
			r.Get("/v1/me/desktop-config", d.desktopConfig)
			r.Post("/api/auth/sign-out", d.signOut)
			r.Post("/v1/telemetry/ingest", d.telemetry)
			r.HandleFunc("/v1/*", d.catalog) // the explicit routes above win
		})
	}
}

func (d Deps) me(w http.ResponseWriter, r *http.Request) {
	s, ok := d.requireToken(w, r)
	if !ok {
		return
	}
	denJSON(w, map[string]any{"user": map[string]string{"id": userIDFor(s.Email), "email": s.Email, "name": s.Name}})
}

// exchange trades a one-time grant for the desktop token. Public: the
// grant IS the credential — minted only for a signed-in browser, minutes
// of life, consumed atomically (Take = GETDEL) so a captured value cannot
// be replayed.
func (d Deps) exchange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Grant string `json:"grant"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	grant := strings.TrimSpace(body.Grant)
	if grant == "" {
		denError(w, 400, "invalid_request", "A grant is required.")
		return
	}
	var s denSession
	ok, err := d.Store.Take(r.Context(), grantKind, grant, &s)
	if err != nil || !ok {
		denError(w, 404, "grant_not_found", "This desktop sign-in link is missing, expired, or already used.")
		return
	}
	tok, err := auth.NewSessionID()
	if err != nil {
		denError(w, 500, "server_error", "")
		return
	}
	if err := d.Store.Put(r.Context(), tokenKind, tok, s, d.TokenTTL); err != nil {
		denError(w, 500, "server_error", "store unavailable")
		return
	}
	denJSON(w, map[string]any{
		"token":          tok,
		"user":           map[string]string{"id": userIDFor(s.Email), "email": s.Email, "name": s.Name},
		"organization":   d.organization(),
		"connectEnabled": false, // Cloud MCP is excluded (spec §12)
	})
}

func (d Deps) orgs(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireToken(w, r); !ok {
		return
	}
	org := d.organization()
	denJSON(w, map[string]any{
		"orgs":          []map[string]string{{"id": org["id"], "slug": org["slug"], "name": org["name"], "role": "member"}},
		"activeOrgId":   org["id"],
		"activeOrgSlug": org["slug"],
	})
}

// activeOrg: single-org deployment — acknowledge the choice, nothing to switch.
func (d Deps) activeOrg(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireToken(w, r); !ok {
		return
	}
	org := d.organization()
	denJSON(w, map[string]string{"activeOrgId": org["id"], "activeOrgSlug": org["slug"]})
}

// resources: change-detection snapshot — must be byte-identical while
// nothing changed. We ship no resources, so both maps stay empty.
func (d Deps) resources(w http.ResponseWriter, r *http.Request) {
	s, ok := d.requireToken(w, r)
	if !ok {
		return
	}
	denJSON(w, map[string]any{
		"organizationId": d.organization()["id"],
		"orgMemberId":    "orgmember_" + userIDFor(s.Email)[5:],
		"teamIds":        []string{},
		"resources":      map[string]any{"llmProviders": map[string]any{}, "marketplaces": []any{}},
	})
}

// desktopConfig: branding + enforced desktop policy (OpenWork's local
// server persists it and then denies engine/HTTP actions with 403
// organization_policy_denied — real limits, not UI preferences).
// Deliberately permissive: allowCustomProviders=false would hide the
// provider OpenCode loads from its own config; allowManageExtensions=false
// would block installing the auth plugin.
func (d Deps) desktopConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireToken(w, r); !ok {
		return
	}
	blocked := d.BlockedCommands
	if blocked == nil {
		blocked = []string{}
	}
	p := map[string]any{
		"brandAppName": d.BrandAppName, "brandAccentColor": d.AccentColor,
		"allowCustomProviders": true, "allowManageExtensions": true, "allowControlSettings": true,
		"allowBuiltInExtensions": true, "allowMultipleWorkspaces": true, "allowZenModel": true,
		"allowAlphaUpdates": false, "showWelcomePage": false,
		// A non-empty blockedCommands disables interactive terminals outright
		// (managed-policy-rules.ts:77-78).
		"execution":          map[string]any{"commands": "allow", "blockedCommands": blocked, "blockBrowserUploads": d.BlockBrowserUploads},
		"automationsEnabled": false, "dashboardEnabled": false,
		"connectEnabled": false,
	}
	// A non-URL value is dropped by the client normalizer: omit, don't send "".
	if d.BrandLogoURL != "" {
		p["brandLogoUrl"] = d.BrandLogoURL
	}
	if d.BrandIconURL != "" {
		p["brandIconUrl"] = d.BrandIconURL
	}
	denJSON(w, p)
}

// signOut revokes the desktop token; an unknown token is fine (idempotent).
func (d Deps) signOut(w http.ResponseWriter, r *http.Request) {
	if tok := bearer(r); tok != "" {
		_ = d.Store.Del(r.Context(), tokenKind, tok)
	}
	denJSON(w, map[string]any{})
}

// telemetry: accept and discard — nothing is forwarded anywhere.
func (d Deps) telemetry(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireToken(w, r); !ok {
		return
	}
	denJSON(w, map[string]any{})
}

// emptyCatalogs: empty-but-valid payloads for an organization that ships
// no resources. The key names are what each desktop parser looks for
// (den.ts); a wrong name parses as "no data" and hides real breakage.
var emptyCatalogs = map[string]any{
	"llm-providers":                      map[string]any{"llmProviders": []any{}},
	"inference-providers":                map[string]any{"inferenceProviders": []any{}},
	"marketplaces":                       map[string]any{"items": []any{}},
	"resources/marketplace-capabilities": map[string]any{"items": []any{}},
	"me/library":                         map[string]any{"items": []any{}},
	"me/dashboards":                      map[string]any{"items": []any{}},
	"mcp-connections":                    map[string]any{"connections": []any{}},
	"mcp-connections/presets":            map[string]any{"presets": []any{}},
	"apps":                               map[string]any{"enabled": false, "sharingEnabled": false, "items": []any{}},
	"automations":                        map[string]any{"items": []any{}, "nextCursor": nil},
	"cloud-automations":                  map[string]any{"items": []any{}, "nextCursor": nil},
	"plugins":                            map[string]any{"items": []any{}},
	"inference/analytics/settings": map[string]any{"available": false, "subscribed": false, "modelsEnabled": false,
		"enabled": false, "consentedAt": nil, "consentVersion": nil, "exportEnabled": false, "langfuseHost": nil, "langfuseConfigured": false},
}

// catalog is the /v1/{path} catch-all: known empty catalogs on GET, the
// Den 404 envelope otherwise (never chi's bare 404/405) — including every
// former Cloud MCP route.
func (d Deps) catalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireToken(w, r); !ok {
		return
	}
	res := strings.TrimSuffix(chi.URLParam(r, "*"), "/")
	if r.Method == http.MethodGet {
		if p, ok := emptyCatalogs[res]; ok {
			denJSON(w, p)
			return
		}
	}
	denError(w, 404, "not_implemented", "No handler for "+r.Method+" /v1/"+res)
}

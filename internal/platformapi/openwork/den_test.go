// SPDX-License-Identifier: Apache-2.0

package openwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/platformapi/auth"
)

type fixture struct {
	r     chi.Router
	deps  Deps
	store *auth.OAuthStore
}

func newDen(t *testing.T, enabled bool) *fixture {
	t.Helper()
	mr := miniredis.RunT(t)
	store := &auth.OAuthStore{RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	f := &fixture{store: store, deps: Deps{
		Config: Config{Enabled: enabled, OrgName: "Acme", OrgSlug: "acme", BrandAppName: "Acme AI",
			BlockedCommands: []string{"rm"}, GrantTTL: 5 * time.Minute, TokenTTL: time.Hour},
		Store: store, BaseURL: "https://ach.test/", CookieName: "ach_console",
		Session: func(_ context.Context, sid string) (string, bool, error) { return "u@x.com", sid == "sid1", nil },
	}}
	f.r = chi.NewRouter()
	Mount(f.r, f.deps)
	return f
}

func (f *fixture) do(t *testing.T, method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func bearerHdr(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %s", w.Body)
	}
	return m
}

// handoffGrant runs the signed-in handoff and extracts the grant from the
// deep link on the page.
func handoffGrant(t *testing.T, f *fixture) string {
	t.Helper()
	w := f.do(t, "GET", "/openwork?desktopAuth=1", nil, map[string]string{"Cookie": "ach_console=sid1"})
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("handoff: %d %s", w.Code, w.Body)
	}
	m := regexp.MustCompile(`href="openwork://den-auth\?([^"]+)"`).FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no openwork:// deep link in href (html/template must not neuter the scheme): %s", w.Body)
	}
	q := m[1]
	if !strings.Contains(q, "denBaseUrl=https%3A%2F%2Fach.test%2Fopenwork%2Fapi%2Fden") {
		t.Fatalf("deep link base: %s", q)
	}
	grant := regexp.MustCompile(`grant=([^&]+)`).FindStringSubmatch(q)[1]
	if !strings.Contains(w.Body.String(), "u@x.com") || !strings.Contains(w.Body.String(), "expires in <b>5 min</b>") {
		t.Fatalf("page copy: %s", w.Body)
	}
	return grant
}

func token(t *testing.T, f *fixture) string {
	t.Helper()
	w := f.do(t, "POST", "/api/den/v1/auth/desktop-handoff/exchange", map[string]string{"grant": handoffGrant(t, f)}, nil)
	if w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body)
	}
	return decode(t, w)["token"].(string)
}

func TestDen_RoutesAbsentWhenDisabled(t *testing.T) {
	f := newDen(t, false)
	for _, p := range []string{"/openwork", "/api/den/v1/me", "/openwork/brand/logo.svg"} {
		if w := f.do(t, "GET", p, nil, nil); w.Code != 404 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

func TestDen_UnauthenticatedUsesTheDenErrorShape(t *testing.T) {
	f := newDen(t, true)
	w := f.do(t, "GET", "/api/den/v1/me", nil, nil)
	if w.Code != 401 || decode(t, w)["error"] != "unauthorized" || decode(t, w)["message"] == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", "/api/den/v1/me", nil, bearerHdr("nope")); w.Code != 401 || decode(t, w)["error"] != "unauthorized" {
		t.Fatalf("unknown token: %d %s", w.Code, w.Body)
	}
}

func TestDen_MeReturnsTheTokenOwner(t *testing.T) {
	f := newDen(t, true)
	w := f.do(t, "GET", "/api/den/v1/me", nil, bearerHdr(token(t, f)))
	sum := sha256.Sum256([]byte("u@x.com"))
	user := decode(t, w)["user"].(map[string]any)
	if w.Code != 200 || user["email"] != "u@x.com" || user["name"] != "u@x.com" || user["id"] != "user_"+hex.EncodeToString(sum[:])[:24] {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestDen_CorsReflectsOriginOnDenRoutesOnly(t *testing.T) {
	f := newDen(t, true)
	w := f.do(t, "OPTIONS", "/api/den/v1/me", nil, map[string]string{"Origin": "https://app"})
	h := w.Header()
	if w.Code != 204 || h.Get("Access-Control-Allow-Origin") != "https://app" || h.Get("Access-Control-Allow-Credentials") != "true" ||
		h.Get("Vary") != "Origin" || h.Get("Access-Control-Allow-Methods") != "GET,POST,DELETE,OPTIONS" || h.Get("Access-Control-Allow-Headers") != allowedHeaders {
		t.Fatalf("preflight: %d %v", w.Code, h)
	}
	// Real responses carry it too; the handoff page (not a Den route) does not.
	if w := f.do(t, "GET", "/openwork/api/den/v1/me", nil, map[string]string{"Origin": "https://app"}); w.Header().Get("Access-Control-Allow-Origin") != "https://app" {
		t.Fatalf("real response: %v", w.Header())
	}
	if w := f.do(t, "GET", "/openwork", nil, map[string]string{"Origin": "https://app"}); w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("handoff must not reflect: %v", w.Header())
	}
}

func TestDen_GrantExchangeIsSingleUse(t *testing.T) {
	f := newDen(t, true)
	grant := handoffGrant(t, f)
	w := f.do(t, "POST", "/api/den/v1/auth/desktop-handoff/exchange", map[string]string{"grant": grant}, nil)
	m := decode(t, w)
	sum := sha256.Sum256([]byte("acme"))
	org := m["organization"].(map[string]any)
	if w.Code != 200 || m["token"] == "" || m["connectEnabled"] != false ||
		org["id"] != "organization_"+hex.EncodeToString(sum[:])[:16] || org["slug"] != "acme" || org["name"] != "Acme" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do(t, "POST", "/api/den/v1/auth/desktop-handoff/exchange", map[string]string{"grant": grant}, nil); w.Code != 404 || decode(t, w)["error"] != "grant_not_found" {
		t.Fatalf("second exchange: %d %s", w.Code, w.Body)
	}
}

func TestDen_ExchangeRejectsMissingOrMalformed(t *testing.T) {
	f := newDen(t, true)
	for _, body := range []any{map[string]string{}, "not json", map[string]string{"grant": "  "}} {
		if w := f.do(t, "POST", "/api/den/v1/auth/desktop-handoff/exchange", body, nil); w.Code != 400 || decode(t, w)["error"] != "invalid_request" {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body)
		}
	}
	if w := f.do(t, "POST", "/api/den/v1/auth/desktop-handoff/exchange", map[string]string{"grant": "unknown"}, nil); w.Code != 404 {
		t.Fatalf("unknown: %d", w.Code)
	}
}

func TestDen_HandoffRedirectsToLoginWhenSignedOut(t *testing.T) {
	f := newDen(t, true)
	w := f.do(t, "GET", "/openwork?desktopAuth=1", nil, nil)
	if w.Code != 302 || w.Header().Get("Location") != "/platform/console/session/login?next=%2Fopenwork%3FdesktopAuth%3D1" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	if w := f.do(t, "GET", "/openwork/", nil, map[string]string{"Cookie": "ach_console=stale"}); w.Code != 302 {
		t.Fatalf("stale cookie: %d", w.Code)
	}
}

func TestDen_OrgsAndActiveOrg(t *testing.T) {
	f := newDen(t, true)
	tok := token(t, f)
	m := decode(t, f.do(t, "GET", "/api/den/v1/me/orgs", nil, bearerHdr(tok)))
	orgs := m["orgs"].([]any)
	if len(orgs) != 1 || orgs[0].(map[string]any)["role"] != "member" || m["activeOrgSlug"] != "acme" || m["activeOrgId"] != orgs[0].(map[string]any)["id"] {
		t.Fatalf("%v", m)
	}
	a := decode(t, f.do(t, "POST", "/api/den/v1/me/active-organization", map[string]string{"orgId": "x"}, bearerHdr(tok)))
	if a["activeOrgSlug"] != "acme" || a["activeOrgId"] != m["activeOrgId"] {
		t.Fatalf("%v", a)
	}
}

func TestDen_ResourcesSnapshotIsByteStable(t *testing.T) {
	f := newDen(t, true)
	tok := token(t, f)
	a := f.do(t, "GET", "/api/den/v1/resources", nil, bearerHdr(tok))
	b := f.do(t, "GET", "/api/den/v1/resources", nil, bearerHdr(tok))
	if a.Code != 200 || a.Body.String() != b.Body.String() {
		t.Fatalf("%d %s vs %s", a.Code, a.Body, b.Body)
	}
	m := decode(t, a)
	sum := sha256.Sum256([]byte("u@x.com"))
	if m["orgMemberId"] != "orgmember_"+hex.EncodeToString(sum[:])[:24] || len(m["teamIds"].([]any)) != 0 {
		t.Fatalf("%v", m)
	}
	res := m["resources"].(map[string]any)
	if len(res["llmProviders"].(map[string]any)) != 0 || len(res["marketplaces"].([]any)) != 0 {
		t.Fatalf("%v", res)
	}
}

func TestDen_DesktopConfigCarriesBrandingAndPolicy(t *testing.T) {
	f := newDen(t, true)
	m := decode(t, f.do(t, "GET", "/api/den/v1/me/desktop-config", nil, bearerHdr(token(t, f))))
	if m["brandAppName"] != "Acme AI" || m["allowCustomProviders"] != true || m["allowManageExtensions"] != true ||
		m["allowAlphaUpdates"] != false || m["connectEnabled"] != false || m["dashboardEnabled"] != false {
		t.Fatalf("%v", m)
	}
	ex := m["execution"].(map[string]any)
	if ex["commands"] != "allow" || ex["blockBrowserUploads"] != false || len(ex["blockedCommands"].([]any)) != 1 {
		t.Fatalf("%v", ex)
	}
	if _, ok := m["brandLogoUrl"]; ok {
		t.Fatal("empty brand URLs must be omitted, not sent as \"\"")
	}
	f.deps.BrandLogoURL, f.deps.BrandIconURL = "https://x/logo.svg", "https://x/icon.svg"
	f.r = chi.NewRouter()
	Mount(f.r, f.deps)
	m = decode(t, f.do(t, "GET", "/api/den/v1/me/desktop-config", nil, bearerHdr(token(t, f))))
	if m["brandLogoUrl"] != "https://x/logo.svg" || m["brandIconUrl"] != "https://x/icon.svg" {
		t.Fatalf("%v", m)
	}
}

func TestDen_BrandMarksServedWithoutAuth(t *testing.T) {
	f := newDen(t, true)
	for _, n := range []string{"logo.svg", "icon.svg"} {
		w := f.do(t, "GET", "/openwork/brand/"+n, nil, nil)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(w.Body.String(), "<svg") {
			t.Fatalf("%s: %d %q", n, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := f.do(t, "GET", "/openwork/brand/x.svg", nil, nil); w.Code != 404 || decode(t, w)["error"] != "not_found" {
		t.Fatalf("unknown asset: %d %s", w.Code, w.Body)
	}
}

func TestDen_CatalogStubsEmptyButWellFormed(t *testing.T) {
	f := newDen(t, true)
	tok := token(t, f)
	for path, want := range map[string]string{
		"llm-providers":                      `{"llmProviders":[]}`,
		"inference-providers":                `{"inferenceProviders":[]}`,
		"marketplaces":                       `{"items":[]}`,
		"resources/marketplace-capabilities": `{"items":[]}`,
		"me/library":                         `{"items":[]}`,
		"me/dashboards":                      `{"items":[]}`,
		"mcp-connections":                    `{"connections":[]}`,
		"mcp-connections/presets":            `{"presets":[]}`,
		"apps":                               `{"enabled":false,"items":[],"sharingEnabled":false}`,
		"automations":                        `{"items":[],"nextCursor":null}`,
		"cloud-automations":                  `{"items":[],"nextCursor":null}`,
		"plugins":                            `{"items":[]}`,
	} {
		w := f.do(t, "GET", "/api/den/v1/"+path, nil, bearerHdr(tok))
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
			t.Fatalf("%s: %d %s (want %s)", path, w.Code, w.Body, want)
		}
		if w := f.do(t, "GET", "/api/den/v1/"+path+"/", nil, bearerHdr(tok)); w.Code != 200 {
			t.Fatalf("%s/ (trailing slash): %d", path, w.Code)
		}
	}
	m := decode(t, f.do(t, "GET", "/api/den/v1/inference/analytics/settings", nil, bearerHdr(tok)))
	if m["available"] != false || m["langfuseHost"] != nil || m["consentedAt"] != nil {
		t.Fatalf("%v", m)
	}
	if w := f.do(t, "GET", "/api/den/v1/llm-providers", nil, nil); w.Code != 401 {
		t.Fatalf("catalog needs a token: %d", w.Code)
	}
}

func TestDen_UnknownCatalogUsesTheDen404(t *testing.T) {
	f := newDen(t, true)
	tok := token(t, f)
	for _, c := range []struct{ method, path string }{{"GET", "whatever"}, {"POST", "llm-providers"}, {"PUT", "plugins"}, {"POST", "mcp/token"}} {
		w := f.do(t, c.method, "/api/den/v1/"+c.path, nil, bearerHdr(tok))
		if w.Code != 404 || decode(t, w)["error"] != "not_implemented" {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	// Cloud MCP is gone entirely.
	if w := f.do(t, "POST", "/api/den/mcp/agent", nil, bearerHdr(tok)); w.Code != 404 {
		t.Fatalf("mcp/agent: %d", w.Code)
	}
}

func TestDen_SignOutRevokesAndIsIdempotent(t *testing.T) {
	f := newDen(t, true)
	tok := token(t, f)
	if w := f.do(t, "POST", "/api/den/api/auth/sign-out", nil, bearerHdr(tok)); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", "/api/den/v1/me", nil, bearerHdr(tok)); w.Code != 401 {
		t.Fatalf("after sign-out: %d", w.Code)
	}
	if w := f.do(t, "POST", "/api/den/api/auth/sign-out", nil, bearerHdr(tok)); w.Code != 200 {
		t.Fatalf("second sign-out: %d", w.Code)
	}
}

func TestDen_TelemetryAcceptedAndDiscarded(t *testing.T) {
	f := newDen(t, true)
	if w := f.do(t, "POST", "/api/den/v1/telemetry/ingest", map[string]any{"events": []any{1}}, bearerHdr(token(t, f))); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do(t, "POST", "/api/den/v1/telemetry/ingest", nil, nil); w.Code != 401 {
		t.Fatalf("needs a token: %d", w.Code)
	}
}

func TestDen_BothPrefixesServeTheAPI(t *testing.T) {
	f := newDen(t, true)
	tok := token(t, f)
	a := f.do(t, "GET", "/api/den/v1/me", nil, bearerHdr(tok))
	b := f.do(t, "GET", "/openwork/api/den/v1/me", nil, bearerHdr(tok))
	if a.Code != 200 || a.Body.String() != b.Body.String() {
		t.Fatalf("%d %s vs %d %s", a.Code, a.Body, b.Code, b.Body)
	}
}

func TestFromEnv_BlockedCommandsList(t *testing.T) {
	t.Setenv("ACH_OPENWORK_ENABLED", "true")
	t.Setenv("ACH_OPENWORK_BLOCKED_COMMANDS", "rm, sudo ,,")
	c := FromEnv()
	if !c.Enabled || len(c.BlockedCommands) != 2 || c.BlockedCommands[1] != "sudo" || c.GrantTTL != 5*time.Minute || c.TokenTTL != 30*24*time.Hour {
		t.Fatalf("%+v", c)
	}
}

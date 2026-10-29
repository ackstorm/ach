// SPDX-License-Identifier: Apache-2.0

package openwork

import (
	"embed"
	"html/template"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/platformapi/auth"
)

// Handoff page + the two published brand marks (alitellm-auth src/api/brand).
//
//go:embed handoff.html brand/openwork-logo.svg brand/openwork-icon.svg
var assets embed.FS

var handoffTmpl = template.Must(template.ParseFS(assets, "handoff.html"))

// brandAssets is an explicit allow-list, not a path join: this can never
// read outside the brand directory.
var brandAssets = map[string]string{"logo.svg": "brand/openwork-logo.svg", "icon.svg": "brand/openwork-icon.svg"}

// handoff mints a one-time grant for the signed-in browser and shows the
// openwork:// deep link. The desktop opens this URL with ?desktopAuth=1
// and never reads the body: it waits for the deep link, or the user
// pastes the grant. Signed out → the console login, back here after.
func (d Deps) handoff(w http.ResponseWriter, r *http.Request) {
	email := ""
	if c, err := r.Cookie(d.CookieName); err == nil {
		if e, ok, err := d.Session(r.Context(), c.Value); err == nil && ok {
			email = e
		}
	}
	if email == "" {
		next := "/openwork"
		if r.URL.RawQuery != "" {
			next += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, "/platform/console/session/login?next="+url.QueryEscape(next), http.StatusFound)
		return
	}
	grant, err := auth.NewSessionID()
	if err != nil {
		denError(w, 500, "server_error", "")
		return
	}
	if err := d.Store.Put(r.Context(), grantKind, grant, denSession{Email: email, Name: email}, d.GrantTTL); err != nil {
		denError(w, 500, "server_error", "store unavailable")
		return
	}
	deep := "openwork://den-auth?" + url.Values{"grant": {grant}, "denBaseUrl": {d.denAPIBase()}}.Encode()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = handoffTmpl.Execute(w, map[string]any{
		"Brand": d.BrandAppName, "LogoURL": d.BrandLogoURL, "Email": email,
		// template.URL: html/template would otherwise neuter the custom
		// openwork: scheme in href to #ZgotmplZ.
		"DeepLink":   template.URL(deep), //nolint:gosec // our own value, not user input
		"TTLMinutes": int(d.GrantTTL.Minutes()),
	})
}

// brand serves the two published marks. Public: they are not secrets.
func (d Deps) brand(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	path, ok := brandAssets[name]
	if !ok {
		denError(w, 404, "not_found", "No brand asset "+name)
		return
	}
	b, _ := assets.ReadFile(path)
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write(b)
}

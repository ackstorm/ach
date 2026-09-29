// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// WellKnownHandler serves OpenCode's well-known manifest: `opencode auth
// login https://<ach>` (engines v1 and v2) installs the SSO plugin from spec
// with this deployment's options — api for OAuth discovery, platform (the
// origin serving /clients/*) for its config, provider for the provider id it
// signs in. Anonymous, public data only. OpenCode requires an auth block;
// the plugin never uses the credential its command yields, so any portable
// command does.
func WellKnownHandler(baseURL, provider, spec string) http.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	u, err := url.Parse(base)
	if err != nil {
		panic(err) // ACH_BASE_URL is validated at startup
	}
	doc, _ := json.Marshal(map[string]any{
		"auth": map[string]any{"command": []string{"opencode", "--version"}, "env": ""},
		"config": map[string]any{"plugin": []any{[]any{spec, map[string]string{
			"api": base + "/v1", "platform": u.Scheme + "://" + u.Host, "provider": provider,
		}}}},
	})
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(doc)
	}
}

// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// WellKnownHandler serves OpenCode's well-known manifest: `opencode auth
// login https://<ach>` (engines v1 and v2) installs the SSO plugin from spec
// with this deployment's options — api for OAuth discovery, platform (the
// origin serving /clients/*) for its config, provider for the provider id it
// signs in. enabled_providers keeps only that provider, plus extra (ids a user
// may add through their own plugins), in the model picker: v1 reads it as is
// and the user's opencode.json overrides it; v2 turns it into provider.use
// policies the user's config cannot override (anomalyco/opencode#59267).
// Anonymous, public data only.
// OpenCode requires an auth block;
// the plugin never uses the credential its command yields. OpenCode spawns it
// with no shell (cross-spawn), so the command must resolve everywhere: echo is
// /bin/echo on Linux/macOS, and on Windows cross-spawn runs any non-.exe name
// through cmd.exe /c, where echo is a builtin. opencode itself is often not on
// PATH (invoked by absolute path).
func WellKnownHandler(baseURL, provider, spec string, extra []string) http.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	u, err := url.Parse(base)
	if err != nil {
		panic(err) // ACH_BASE_URL is validated at startup
	}
	enabled := []string{provider}
	for _, p := range extra {
		if !slices.Contains(enabled, p) {
			enabled = append(enabled, p)
		}
	}
	doc, _ := json.Marshal(map[string]any{
		"auth": map[string]any{"command": []string{"echo", "ok"}, "env": ""},
		"config": map[string]any{
			"plugin": []any{[]any{spec, map[string]string{
				"api": base + "/v1", "platform": u.Scheme + "://" + u.Host, "provider": provider,
			}}},
			"enabled_providers": enabled,
		},
	})
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(doc)
	}
}

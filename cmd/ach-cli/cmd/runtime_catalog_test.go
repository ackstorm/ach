// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeCatalogIntersectsCallerGrants(t *testing.T) {
	for name, bearer := range map[string]string{"personal": "h.p.s", "environment": testEK} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-ach-key") != bearer {
					t.Error("missing active credential")
				}
				switch r.URL.Path {
				case "/platform/environments":
					if bearer == testEK || r.URL.Query().Get("all") != "" {
						t.Error("requested management inventory outside personal scope")
					}
					if r.URL.Query().Get("cursor") == "" {
						fmt.Fprint(w, `{"items":[{"runtime":{"mcpServers":["unavailable"]}}],"next_cursor":"p2"}`)
					} else {
						fmt.Fprint(w, `{"items":[{"runtime":{"mcpServers":["contacts"]}}]}`)
					}
				case "/platform/hydrate/version":
					if bearer != testEK || r.Method != http.MethodPost {
						t.Error("unexpected bound Environment request")
					}
					fmt.Fprint(w, `{"items":[{"kind":"mcp","name":"contacts"},{"kind":"a2a","name":"public"}]}`)
				case "/v1/mcp/server":
					fmt.Fprint(w, `{"servers":[{"server_name":"public"},
						{"server_name":"contacts","alias":"display","description":"Address book"}]}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			got, err := runtimeCatalog(t.Context(), cred{BaseURL: srv.URL, Bearer: bearer}, false, "mcp")
			if err != nil || len(got) != 1 || got[0].Name != "contacts" || got[0].Description != "Address book" {
				t.Fatalf("catalog: %v, err %v", got, err)
			}
		})
	}
}

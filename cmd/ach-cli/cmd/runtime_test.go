// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeKindList_RendersTable(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		item        map[string]string
		args        []string
		wantContain []string
	}{
		{
			name:        "models",
			path:        "/platform/admin/runtime/models",
			item:        map[string]string{"name": "gpt-4o", "kind": "model", "status": "active"},
			args:        []string{"models", "list"},
			wantContain: []string{"gpt-4o", "active"},
		},
		{
			name:        "teams",
			path:        "/platform/admin/runtime/teams",
			item:        map[string]string{"name": "default", "kind": "team", "status": "active"},
			args:        []string{"teams", "list"},
			wantContain: []string{"default", "team"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adminTestEnv(t)
			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"connector":   map[string]any{"name": "default", "type": "litellm", "status": "active"},
					"items":       []map[string]string{tc.item},
					"next_cursor": nil,
				})
			})
			srv := httptest.NewTLSServer(mux)
			defer srv.Close()
			seedAdminConfig(t, srv.URL)
			swapAdminHTTPClientForTest(t, srv.Client())

			root := newRuntimeCmd()
			stdout, _, code, err := executeCommand(t, root, tc.args...)
			if err != nil || code != 0 {
				t.Fatalf("runtime %s list: code=%d err=%v", tc.name, code, err)
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(stdout, want) {
					t.Fatalf("table missing %q:\n%s", want, stdout)
				}
			}
		})
	}
}

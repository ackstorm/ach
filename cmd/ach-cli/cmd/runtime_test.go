// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeModelsList_RendersTable(t *testing.T) {
	adminTestEnv(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/platform/admin/runtime/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"connector":   map[string]any{"name": "default", "type": "litellm", "status": "active"},
			"items":       []map[string]string{{"name": "gpt-4o", "kind": "model", "status": "active"}},
			"next_cursor": nil,
		})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	seedAdminConfig(t, srv.URL)
	swapAdminHTTPClientForTest(t, srv.Client())

	root := newRuntimeCmd()
	stdout, _, code, err := executeCommand(t, root, "models", "list")
	if err != nil || code != 0 {
		t.Fatalf("runtime models list: code=%d err=%v", code, err)
	}
	if !strings.Contains(stdout, "gpt-4o") || !strings.Contains(stdout, "active") {
		t.Fatalf("table missing model/status:\n%s", stdout)
	}
}

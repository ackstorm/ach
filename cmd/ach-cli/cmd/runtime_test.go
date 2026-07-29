// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
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

func TestWriteRuntimeTable(t *testing.T) {
	t.Run("no attributes renders 3-column header", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeRuntimeTable(&buf, []runtimeItem{
			{Kind: "model", Name: "gpt-4o", Status: "active"},
		}); err != nil {
			t.Fatalf("writeRuntimeTable: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "KIND") || strings.Contains(out, "MODE") {
			t.Fatalf("expected 3-column header, got:\n%s", out)
		}
	})

	t.Run("guardrail row renders mode and default-on", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeRuntimeTable(&buf, []runtimeItem{
			{Kind: "guardrail", Name: "pii-filter", Status: "active",
				Attributes: json.RawMessage(`{"mode":["pre_call"],"defaultOn":true}`)},
		}); err != nil {
			t.Fatalf("writeRuntimeTable: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "pre_call") || !strings.Contains(out, "yes") {
			t.Fatalf("expected mode/default-on rendered, got:\n%s", out)
		}
	})

	t.Run("malformed attributes degrade to dashes", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeRuntimeTable(&buf, []runtimeItem{
			{Kind: "guardrail", Name: "broken", Status: "active",
				Attributes: json.RawMessage(`not json`)},
		}); err != nil {
			t.Fatalf("writeRuntimeTable: %v", err)
		}
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		fields := strings.Fields(lines[len(lines)-1])
		if len(fields) != 5 || fields[3] != "-" || fields[4] != "-" {
			t.Fatalf("expected MODE/DEFAULT-ON columns = '-', got fields %v from:\n%s", fields, buf.String())
		}
	})
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// newInventoryTestServer registers a JSON handler per path in bodies and wires
// the package HTTP-client seam so `ach admin list` reaches the TLS cert.
func newInventoryTestServer(t *testing.T, bodies map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range bodies {
		body := body
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(body)
		})
	}
	srv := httptest.NewTLSServer(mux)
	swapAdminHTTPClientForTest(t, srv.Client())
	t.Cleanup(srv.Close)
	return srv
}

// envelope builds the standard {items, next_cursor:null} page.
func envelope(items ...map[string]any) map[string]any {
	return map[string]any{"items": items, "next_cursor": nil}
}

// TestAdminList_InvalidKind: a bogus kind is rejected client-side before any
// HTTP/credential resolution → exit General.
func TestAdminList_InvalidKind(t *testing.T) {
	adminTestEnv(t)
	_, _, code, err := executeAdmin(t, "", "list", "bogus")
	if code != exit.General {
		t.Fatalf("exit code = %d; want %d", code, exit.General)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Errorf("error missing 'invalid kind': %v", err)
	}
}

// TestAdminList_InvalidOutput: an unsupported -o value is rejected client-side.
func TestAdminList_InvalidOutput(t *testing.T) {
	adminTestEnv(t)
	_, _, code, err := executeAdmin(t, "", "list", "plugins", "-o", "xml")
	if code != exit.General {
		t.Fatalf("exit code = %d; want %d", code, exit.General)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Errorf("error missing 'invalid --output': %v", err)
	}
}

// TestAdminList_SingleKind_Table: a single-kind list renders the grouped table.
func TestAdminList_SingleKind_Table(t *testing.T) {
	adminTestEnv(t)
	srv := newInventoryTestServer(t, map[string]any{
		"/platform/admin/plugins": envelope(map[string]any{
			"kind": "plugin", "name": "caveman", "namespace": "ach",
			"version": "12", "sync": "fresh",
		}),
	})
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "", "list", "plugins")
	if err != nil || code != exit.OK {
		t.Fatalf("err=%v code=%d", err, code)
	}
	for _, want := range []string{"PLUGINS (1)", "caveman", "fresh"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// TestAdminList_PromptFalseGreenFootnote: a fresh* prompt surfaces the footnote.
func TestAdminList_PromptFalseGreenFootnote(t *testing.T) {
	adminTestEnv(t)
	srv := newInventoryTestServer(t, map[string]any{
		"/platform/admin/prompts": envelope(map[string]any{
			"kind": "prompt", "name": "greeting", "namespace": "ach",
			"version": "3", "sync": "fresh*",
		}),
	})
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "", "list", "prompts")
	if err != nil || code != exit.OK {
		t.Fatalf("err=%v code=%d", err, code)
	}
	if !strings.Contains(stdout, "content presence is not gated") {
		t.Errorf("missing false-green footnote:\n%s", stdout)
	}
}

// TestAdminList_All_JSON: `list all -o json` fans out to every kind and emits a
// kind-keyed JSON object. environments maps from EnvironmentView.
func TestAdminList_All_JSON(t *testing.T) {
	adminTestEnv(t)
	bodies := map[string]any{
		"/platform/environments": envelope(map[string]any{
			"namespace": "ach", "name": "prod", "status": "Available", "resourceVersion": "5",
		}),
		"/platform/admin/plugins": envelope(map[string]any{
			"kind": "plugin", "name": "caveman", "namespace": "ach", "version": "12", "sync": "fresh",
		}),
	}
	// Empty envelopes for the remaining admin kinds so every fan-out GET resolves.
	for _, k := range []string{
		"prompts", "artifacts", "skills", "marketplaces",
		"bips", "litellm-connections", "external-refs",
	} {
		bodies["/platform/admin/"+k] = envelope()
	}
	srv := newInventoryTestServer(t, bodies)
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "", "list", "all", "-o", "json")
	if err != nil || code != exit.OK {
		t.Fatalf("err=%v code=%d", err, code)
	}
	var got map[string][]map[string]any
	if e := json.Unmarshal([]byte(stdout), &got); e != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", e, stdout)
	}
	if len(got) != len(adminListKinds) {
		t.Errorf("got %d kinds, want %d", len(got), len(adminListKinds))
	}
	if len(got["plugins"]) != 1 || got["plugins"][0]["name"] != "caveman" {
		t.Errorf("plugins group wrong: %+v", got["plugins"])
	}
	if len(got["environments"]) != 1 || got["environments"][0]["sync"] != "Available" {
		t.Errorf("environments group wrong: %+v", got["environments"])
	}
}

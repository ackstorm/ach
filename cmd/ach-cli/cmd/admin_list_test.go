// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// newInventoryTestServer registers a JSON handler per path in bodies and wires
// the package HTTP-client seam so `ach-cli admin list` reaches the TLS cert.
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
	swapHTTPClientForTest(t, &adminHTTPClient, srv.Client())
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
	if err == nil || !strings.Contains(err.Error(), "-o must be one of") {
		t.Errorf("error missing '-o must be one of': %v", err)
	}
}

// TestAdminKeysList_InvalidStatusFlagErrors: invalid --status on `admin keys list`
// is rejected client-side before any network call.
func TestAdminKeysList_InvalidStatusFlagErrors(t *testing.T) {
	adminTestEnv(t)
	_, _, code, err := executeAdmin(t, "", "keys", "list", "--status", "bogus")
	if code != exit.General {
		t.Fatalf("exit code = %d; want %d (General)", code, exit.General)
	}
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention invalid value 'bogus': %v", err)
	}
}

// TestAdminKeysList_InvalidTypeFlagErrors: invalid --type on `admin keys list`
// is rejected client-side before any network call, same as --status.
func TestAdminKeysList_InvalidTypeFlagErrors(t *testing.T) {
	adminTestEnv(t)
	_, _, code, err := executeAdmin(t, "", "keys", "list", "--type", "bogus")
	if code != exit.General {
		t.Fatalf("exit code = %d; want %d (General)", code, exit.General)
	}
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention invalid value 'bogus': %v", err)
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
		"prompts", "artifacts", "skills", "marketplaces", "skill-marketplaces",
		"bips", "litellm-connections", "external-refs",
	} {
		bodies["/platform/admin/"+k] = envelope()
	}
	for _, route := range adminRuntimeRoutes {
		bodies["/platform/admin/runtime/"+route] = envelope()
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
	if len(got) != len(adminListKinds)+len(adminRuntimeRoutes) {
		t.Errorf("got %d kinds, want %d", len(got), len(adminListKinds)+len(adminRuntimeRoutes))
	}
	if len(got["plugins"]) != 1 || got["plugins"][0]["name"] != "caveman" {
		t.Errorf("plugins group wrong: %+v", got["plugins"])
	}
	if len(got["environments"]) != 1 || got["environments"][0]["sync"] != "Available" {
		t.Errorf("environments group wrong: %+v", got["environments"])
	}
}

// TestAdminList_RuntimeKinds: each runtime kind reads its
// /platform/admin/runtime/* route and renders the KIND NAME STATUS table.
func TestAdminList_RuntimeKinds(t *testing.T) {
	for kind, route := range adminRuntimeRoutes {
		t.Run(kind, func(t *testing.T) {
			adminTestEnv(t)
			srv := newInventoryTestServer(t, map[string]any{
				"/platform/admin/runtime/" + route: envelope(map[string]any{
					"name": "item-" + kind, "kind": kind, "status": "active",
				}),
			})
			seedAdminConfig(t, srv.URL)
			stdout, _, code, err := executeAdmin(t, "", "list", kind)
			if err != nil || code != exit.OK {
				t.Fatalf("err=%v code=%d", err, code)
			}
			for _, want := range []string{"KIND", "NAME", "STATUS", "item-" + kind} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout)
				}
			}
		})
	}
}

// TestAdminList_RuntimeKind_YAML: -o yaml works for a runtime kind.
func TestAdminList_RuntimeKind_YAML(t *testing.T) {
	adminTestEnv(t)
	srv := newInventoryTestServer(t, map[string]any{
		"/platform/admin/runtime/models": envelope(map[string]any{
			"name": "gpt-4o", "kind": "model", "status": "active",
		}),
	})
	seedAdminConfig(t, srv.URL)
	stdout, _, code, err := executeAdmin(t, "", "list", "models", "-o", "yaml")
	if err != nil || code != exit.OK {
		t.Fatalf("err=%v code=%d", err, code)
	}
	if !strings.Contains(stdout, "models:") || !strings.Contains(stdout, "name: gpt-4o") {
		t.Errorf("yaml output:\n%s", stdout)
	}
}

// TestAdminList_All_IncludesRuntime: `list all` also fans out over the
// runtime kinds.
func TestAdminList_All_IncludesRuntime(t *testing.T) {
	adminTestEnv(t)
	bodies := map[string]any{"/platform/environments": envelope()}
	for _, k := range adminListKinds[1:] {
		bodies["/platform/admin/"+k] = envelope()
	}
	for kind, route := range adminRuntimeRoutes {
		bodies["/platform/admin/runtime/"+route] = envelope(map[string]any{
			"name": "item-" + kind, "kind": kind, "status": "active",
		})
	}
	srv := newInventoryTestServer(t, bodies)
	seedAdminConfig(t, srv.URL)
	stdout, _, code, err := executeAdmin(t, "", "list", "all")
	if err != nil || code != exit.OK {
		t.Fatalf("err=%v code=%d", err, code)
	}
	for kind := range adminRuntimeRoutes {
		if !strings.Contains(stdout, "item-"+kind) {
			t.Errorf("stdout missing runtime %s:\n%s", kind, stdout)
		}
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

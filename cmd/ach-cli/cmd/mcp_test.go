// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ackstorm/ach/internal/cli/exit"
)

func TestMCPFromEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	credTestEnv(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var queries, calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/hydrate/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"environment":"sales","items":[{"kind":"mcp","name":"zoho"},{"kind":"mcp","name":"down"}]}`))
	})
	mux.HandleFunc("/v1/mcp/server", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"server_name":"zoho","description":"Sales CRM"},
 {"server_name":"down"},{"server_name":"hidden"}]`))
	})
	m := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	schema := map[string]any{
		"type": "object", "required": []string{"email"},
		"properties": map[string]any{"email": map[string]string{"type": "string"}},
	}
	tool := &mcp.Tool{Name: "crm.list_contacts", Description: "Find a person by email", InputSchema: schema}
	m.AddTool(tool, func(_ context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		var args map[string]string
		if err := json.Unmarshal(r.Params.Arguments, &args); err != nil {
			t.Error(err)
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "reply"}},
			StructuredContent: map[string]any{"email": args["email"], "id": int64(9007199254740993)},
			IsError:           args["email"] == "error",
		}, nil
	})
	options := &mcp.StreamableHTTPOptions{Stateless: true}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m }, options)
	mux.HandleFunc("/mcp/zoho", func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		h.ServeHTTP(w, r)
	})
	mux.HandleFunc("/mcp/down", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) })
	mux.HandleFunc("/mcp/hidden", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "forbidden", 403) })
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-ach-key") != testEK {
			t.Error("request missing active credential")
			http.Error(w, "denied", 403)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer ts.Close()
	t.Setenv("ACH_URL", ts.URL)
	t.Setenv("ACH_KEY", testEK)
	run := func(args ...string) (string, exit.Code, error) {
		t.Helper()
		parent := newMCPCmd()
		parent.SilenceUsage = true
		parent.SilenceErrors = true
		out, _, code, err := executeCommand(t, parent, args...)
		return out, code, err
	}
	out, code, err := run("search", "ZOHO", "-o", "json")
	if code != exit.OK || !strings.Contains(out, "zoho") || queries.Load() != 0 {
		t.Fatalf("server search: %v %s", err, out)
	}
	out, code, err = run("search", "ZoHo", "contacts", "-o", "json")
	if code == exit.OK || !json.Valid([]byte(out)) ||
		!strings.Contains(out, "crm.list_contacts") || !strings.Contains(out, "down") || strings.Contains(out, "hidden") {
		t.Fatalf("partial tool search: %v %s", err, out)
	}
	out, code, err = run("describe", "zoho", "crm.list_contacts", "-o", "json")
	if code != exit.OK || !strings.Contains(out, "required") || !strings.Contains(out, "email") {
		t.Fatalf("tool description: %v %s", err, out)
	}
	for _, email := range []string{"person@example.com", "error"} {
		out, code, err = run("call", "zoho", "list_contacts", `{"email":"`+email+`"}`, "-o", "json")
		if (code == exit.OK) != (email != "error") || !strings.Contains(out, email) ||
			!strings.Contains(out, "9007199254740993") || !json.Valid([]byte(out)) {
			t.Fatalf("tool result: %v %s", err, out)
		}
	}
	for _, args := range [][]string{
		{"call", "hidden", "secret"},
		{"call", "zoho", "crm.list_contacts", "null"},
		{"call", "zoho", "crm.list_contacts", "[]"},
		{"call", "zoho", "crm.list_contacts", "{} {}"},
	} {
		if _, code, _ = run(args...); code == exit.OK {
			t.Errorf("unexpected success for %q", args)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 || calls.Load() != 2 {
		t.Fatalf("workspace entries %v, calls %d, error %v", entries, calls.Load(), err)
	}
}

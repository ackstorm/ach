// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ackstorm/ach/internal/cli/exit"
)

func TestMCPInventoryCache(t *testing.T) {
	credTestEnv(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var sessions, grants atomic.Int32
	var revoked atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/hydrate/version", func(w http.ResponseWriter, _ *http.Request) {
		grants.Add(1)
		rows := `[{"kind":"mcp","name":"crm"}]`
		if revoked.Load() {
			rows = "[]"
		}
		_, _ = w.Write([]byte(`{"environment":"sales","items":` + rows + `}`))
	})
	mux.HandleFunc("/v1/mcp/server", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"server_name":"crm"}]`))
	})
	server := mcp.NewServer(&mcp.Implementation{Name: "crm", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{
		Name: "records", Description: "Search records.\nIncludes suppliers.",
		InputSchema: map[string]any{"type": "object", "description": "private schema"},
	}, nil)
	tools := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux.HandleFunc("/mcp/crm", func(w http.ResponseWriter, r *http.Request) {
		sessions.Add(1)
		tools.ServeHTTP(w, r)
	})
	gateway := httptest.NewServer(mux)
	t.Cleanup(gateway.Close)
	t.Setenv("ACH_URL", gateway.URL)
	t.Setenv("ACH_KEY", testEK)
	check := func(wantTools, wantNetwork bool, args ...string) {
		t.Helper()
		before, catalogs := sessions.Load(), grants.Load()
		out, _, code, err := executeCommand(t, newMCPCmd(), append(args, "-o", "json")...)
		if err != nil || code != exit.OK || strings.Contains(out, "records") != wantTools ||
			(sessions.Load() > before) != wantNetwork || grants.Load() != catalogs+1 {
			t.Fatalf("%v: tools=%v network=%v grants=%d output=%s err=%v",
				args, wantTools, sessions.Load() > before, grants.Load()-catalogs, out, err)
		}
	}
	check(true, true, "list", "--tools")
	check(true, false, "search", "suppliers")
	check(true, true, "search", "suppliers", "--refresh")
	check(true, true, "list", "--tools", "--refresh")
	t.Setenv("ACH_KEY", testEKEnv)
	check(true, true, "list", "--tools")
	revoked.Store(true)
	check(false, false, "list", "--tools")
}

func TestMCPInventoryCacheOutcomes(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var lists atomic.Int32
	var status atomic.Value
	srv := httptest.NewServer(legacyToolGateway(t, &lists, &status))
	t.Cleanup(srv.Close)
	c := cred{BaseURL: srv.URL, Bearer: legacyGatewayKey}

	read := func(refresh bool, count int, errorText string, signIn bool, pages int32) {
		t.Helper()
		n := lists.Load()
		tools, err := cachedMCPTools(t.Context(), c, false, "crm", refresh)
		if len(tools) != count || (errorText == "") != (err == nil) ||
			(err != nil && !strings.Contains(err.Error(), errorText)) ||
			errors.Is(err, errMCPAuthRequired) != signIn || lists.Load()-n != pages {
			t.Fatalf("tools=%d queries=%d err=%v", len(tools), lists.Load()-n, err)
		}
	}
	read(false, 2, "", false, 2)
	file := mcpCachePath(c, "crm")
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache permissions: %v %v", info, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || strings.Contains(string(data), legacyGatewayKey) || strings.Contains(string(data), "inputSchema") {
		t.Fatal("cache must contain only discovery metadata")
	}
	var stale mcpCacheEntry
	if err := json.Unmarshal(data, &stale); err != nil {
		t.Fatal(err)
	}
	stale.Expires = time.Now().Add(-time.Minute)
	data, err = json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	status.Store("auth_required")
	read(false, 1, "ach-cli mcp login crm", true, 2)
	read(false, 1, "ach-cli mcp login crm", true, 0)
	status.Store("transport")
	read(true, 1, "Service Unavailable", false, 2)
	read(false, 1, "Service Unavailable", false, 2)
}

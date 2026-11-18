// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ackstorm/ach/internal/cli/oauthlogin"
)

func TestMCPLoginUsesOriginalCredential(t *testing.T) {
	for _, tc := range []struct {
		name, server        string
		grantAfterLogin     bool
		wantBrowser, wantOK bool
	}{
		{"granted", "zoho", true, true, true},
		{"wrong browser account", "zoho", false, true, false},
		{"outside grant", "unknown", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loginTestEnv(t)
			issuer := newLoginTestServer(t, "unused.browser.token")
			mux := http.NewServeMux()
			mux.Handle("/", issuer.Config.Handler)
			mux.HandleFunc("/platform/environments", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"items":[{"name":"one","runtime":{"mcpServers":["zoho"]}}]}`)
			})
			mux.HandleFunc("/v1/mcp/server", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `[{"server_name":"zoho","description":"Contacts"}]`)
			})
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "zoho", Version: "test"}, nil)
			mcpServer.AddTool(&mcp.Tool{Name: "contacts", InputSchema: map[string]any{"type": "object"}}, nil)
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
			var checked atomic.Bool
			mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
				checked.Store(true)
				if r.Header.Get("x-ach-key") != "h.p.s" || !tc.grantAfterLogin {
					http.Error(w, "provider consent required", http.StatusForbidden)
					return
				}
				handler.ServeHTTP(w, r)
			})
			gateway := httptest.NewServer(mux)
			t.Cleanup(gateway.Close)
			seedProfile(t, "p", oauthProfile(gateway.URL))
			follow := oauthlogin.Opener
			opened := false
			oauthlogin.Opener = func(raw string) error {
				opened = true
				u, err := url.Parse(raw)
				if err != nil {
					return err
				}
				if u.Query().Get("resource") != gateway.URL+"/mcp/zoho" || u.Query().Get("client_id") != "oc_1" {
					t.Error("browser request omitted the selected resource or profile client ID")
				}
				return follow(raw)
			}
			_, _, _, err := executeCommand(t, newMCPLoginCmd(), tc.server)
			if (err == nil) != tc.wantOK || opened != tc.wantBrowser || checked.Load() != tc.wantBrowser {
				t.Fatalf("login err=%v, browser=%v, checked=%v", err, opened, checked.Load())
			}
			if loadConfig(t).Profiles["p"].OAuth.AccessToken != "h.p.s" {
				t.Fatal("provider login replaced the active credential")
			}
		})
	}
}

func TestMCPAuthRemotePrompt(t *testing.T) {
	const authURL = "https://hub.example/authorize"
	const callback = "http://127.0.0.1:32123/callback?state=pending&code=private-code"
	original := oauthlogin.Opener
	t.Cleanup(func() { oauthlogin.Opener = original })
	opened := false
	oauthlogin.Opener = func(string) error {
		opened = true
		return errors.New("no browser")
	}
	for _, noBrowser := range []bool{true, false} {
		var out bytes.Buffer
		prompt := mcpLoginPrompt(noBrowser, strings.NewReader(callback+"\n"), &out)
		received, err := prompt(t.Context(), authURL)
		if err != nil || received != callback || opened == noBrowser {
			t.Fatalf("remote prompt: noBrowser=%v, opened=%v, err=%v", noBrowser, opened, err)
		}
		if !strings.Contains(out.String(), authURL) || strings.Contains(out.String(), "private-code") {
			t.Fatal("expected authorization URL only in terminal output")
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := readMCPCallback(cancelled, strings.NewReader(callback), &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled callback, got %v", err)
	}
	cmd, _, err := newMCPCmd().Find([]string{"auth"})
	if err != nil || cmd.Name() != "login" || cmd.Flags().Lookup("no-browser") == nil {
		t.Fatal("mcp auth must expose login's --no-browser mode")
	}
}

func TestMCPLoginEnvironmentKeyNeedsOwner(t *testing.T) {
	credTestEnv(t)
	seedProfile(t, "p", oauthProfile("https://hub.invalid"))
	_, _, _, err := executeCommand(t, newMCPLoginCmd(), "zoho", "--key", testEK)
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("expected an owner consent hint for the active key, got %v", err)
	}
}

func TestMCPCallbackInputPipeDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	_, err = readMCPCallback(ctx, r, &bytes.Buffer{})
	if err == nil ||
		(!errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "interactive terminal")) {
		t.Fatalf("expected bounded wait for an empty pipe, got %v", err)
	}
}

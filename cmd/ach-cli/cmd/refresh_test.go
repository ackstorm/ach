// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

func refreshServer(t *testing.T, status int, saw *string) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/platform/refresh" {
			http.NotFound(w, r)
			return
		}
		*saw = r.Header.Get("x-ach-key")
		if status != http.StatusNoContent {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"ach_login_required","message":"run ach-cli login"}}`))
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	swapHTTPClientForTest(t, &refreshHTTPClient, ts.Client())
	return ts
}

func TestRefresh_PostsWithTheSession(t *testing.T) {
	dir := credTestEnv(t)
	var saw string
	ts := refreshServer(t, http.StatusNoContent, &saw)
	seedConfig(t, dir, "default", oauthProfile(ts.URL))

	out, _, code, err := executeCommand(t, newRefreshCmd())
	if err != nil || code != exit.OK {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if saw != "h.p.s" || !strings.Contains(out, "Access refreshed") {
		t.Fatalf("saw=%q out=%q", saw, out)
	}
}

func TestRefresh_401_Exit3(t *testing.T) {
	dir := credTestEnv(t)
	var saw string
	ts := refreshServer(t, http.StatusUnauthorized, &saw)
	seedConfig(t, dir, "default", oauthProfile(ts.URL))

	if _, _, code, _ := executeCommand(t, newRefreshCmd()); code != exit.AuthN {
		t.Fatalf("code=%d; want %d", code, exit.AuthN)
	}
}

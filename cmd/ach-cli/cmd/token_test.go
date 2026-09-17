// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
)

// oauthRefreshCalls counts refresh grants the fake AS served; the refresh
// handler sleeps so two concurrent helpers overlap inside the window.
var oauthRefreshCalls atomic.Int32

// oauthASForTest is a minimal fake AS: metadata + DCR + authorize (bounces
// straight back with a code) + token (code → a.b.c/r1, refresh → d.e.f/r2).
func oauthASForTest(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint": srv.URL + "/token", "registration_endpoint": srv.URL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "oc_test"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=thecode&state="+q.Get("state"), 302)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") == "refresh_token" {
			oauthRefreshCalls.Add(1)
			time.Sleep(150 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "d.e.f", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "a.b.c", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r1",
		})
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	prevOpener, prevClient := oauthlogin.Opener, oauthlogin.HTTPClient
	oauthlogin.HTTPClient = srv.Client()
	oauthlogin.Opener = func(u string) error {
		go func() {
			resp, err := srv.Client().Get(u) // trusts the test cert; follows the loopback redirect
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { oauthlogin.Opener, oauthlogin.HTTPClient = prevOpener, prevClient })
	return srv
}

func TestLogin_OAuthDefault_WritesOAuthBlockAndNoPK(t *testing.T) {
	dir := loginTestEnv(t)
	as := oauthASForTest(t)
	_, stderr, code, err := executeLogin(t, "--profile", "prod", "--base-url", as.URL)
	if err != nil {
		t.Fatalf("code=%v err=%v stderr=%s", code, err, stderr)
	}
	file, err := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p := file.Profiles["prod"]
	if p == nil || p.PK != "" || p.OAuth == nil || p.OAuth.AccessToken != "a.b.c" ||
		p.OAuth.ClientID != "oc_test" || p.OAuth.RefreshToken != "r1" {
		t.Fatalf("profile: %+v oauth=%+v", p, p.OAuth)
	}
}

func TestToken_PrintsExactlyTheAccessToken(t *testing.T) {
	dir := loginTestEnv(t)
	as := oauthASForTest(t)
	path := filepath.Join(dir, "ach", "config.yaml")
	save := func(exp time.Time) {
		t.Helper()
		if err := config.Save(path, &config.File{Default: "p", Profiles: map[string]*config.Profile{"p": {
			URL:   as.URL,
			OAuth: &config.OAuthCreds{ClientID: "oc_test", AccessToken: "a.b.c", RefreshToken: "r1", ExpiresAt: exp},
		}}}); err != nil {
			t.Fatal(err)
		}
	}

	save(time.Now().Add(time.Hour))
	stdout, stderr, code, err := executeCommand(t, newTokenCmd())
	if err != nil || stdout != "a.b.c\n" {
		t.Fatalf("fresh: stdout=%q stderr=%q code=%v err=%v", stdout, stderr, code, err)
	}

	// near expiry → refreshed, persisted, and still exactly one line
	save(time.Now().Add(30 * time.Second))
	stdout, _, _, err = executeCommand(t, newTokenCmd())
	if err != nil || stdout != "d.e.f\n" {
		t.Fatalf("refresh: stdout=%q err=%v", stdout, err)
	}
	file, _ := config.Load(path)
	if got := file.Profiles["p"].OAuth; got.RefreshToken != "r2" || got.AccessToken != "d.e.f" {
		t.Fatalf("not persisted: %+v", got)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("credential helpers need exactly one line: %q", stdout)
	}
}

func TestToken_ConcurrentHelpersRefreshOnce(t *testing.T) {
	dir := loginTestEnv(t)
	as := oauthASForTest(t)
	path := filepath.Join(dir, "ach", "config.yaml")
	if err := config.Save(path, &config.File{Default: "p", Profiles: map[string]*config.Profile{"p": {
		URL:   as.URL,
		OAuth: &config.OAuthCreds{ClientID: "oc_test", AccessToken: "a.b.c", RefreshToken: "r1", ExpiresAt: time.Now()},
	}}}); err != nil {
		t.Fatal(err)
	}
	oauthRefreshCalls.Store(0)
	var wg sync.WaitGroup
	outs := make([]string, 2)
	errs := make([]error, 2)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i], _, _, errs[i] = executeCommand(t, newTokenCmd())
		}(i)
	}
	wg.Wait()
	for i := range outs {
		if errs[i] != nil || outs[i] != "d.e.f\n" {
			t.Fatalf("helper %d: stdout=%q err=%v", i, outs[i], errs[i])
		}
	}
	if n := oauthRefreshCalls.Load(); n != 1 {
		t.Fatalf("refresh grants = %d, want 1 (a second refresh spends the rotated token)", n)
	}
}

func TestToken_PKProfilePrintsPK(t *testing.T) {
	dir := loginTestEnv(t)
	path := filepath.Join(dir, "ach", "config.yaml")
	if err := config.Save(path, &config.File{Default: "p", Profiles: map[string]*config.Profile{"p": {
		URL: "https://ach.example.com", PK: "pk_abc",
	}}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, _, err := executeCommand(t, newTokenCmd())
	if err != nil || stdout != "pk_abc\n" {
		t.Fatalf("stdout=%q err=%v", stdout, err)
	}
	if err := config.Save(path, &config.File{Default: "p", Profiles: map[string]*config.Profile{"p": {
		URL: "https://ach.example.com", EK: map[string]string{"prod": "ek_x"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := executeCommand(t, newTokenCmd()); err == nil {
		t.Fatal("ek_-only profile must not print a credential")
	}
}

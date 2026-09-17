// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
)

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
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "d.e.f", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r2"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a.b.c", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r1"})
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
	if p == nil || p.PK != "" || p.OAuth == nil || p.OAuth.AccessToken != "a.b.c" || p.OAuth.ClientID != "oc_test" || p.OAuth.RefreshToken != "r1" {
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
			URL: as.URL, OAuth: &config.OAuthCreds{ClientID: "oc_test", AccessToken: "a.b.c", RefreshToken: "r1", ExpiresAt: exp},
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

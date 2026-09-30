// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
)

// loginTestServer is a minimal AS: metadata, DCR, the device grant
// (approved on the first poll) and the authorization-code grant (the
// "browser" follows /authorize straight back to the loopback redirect).
type loginTestServer struct {
	*httptest.Server
	registrations int32
	accessToken   string
}

func newLoginTestServer(t *testing.T, accessToken string) *loginTestServer {
	t.Helper()
	ts := &loginTestServer{accessToken: accessToken}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": ts.URL, "authorization_endpoint": ts.URL + "/authorize", "token_endpoint": ts.URL + "/token",
			"registration_endpoint": ts.URL + "/register", "device_authorization_endpoint": ts.URL + "/device_authorization",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&ts.registrations, 1)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "oc_test"})
	})
	mux.HandleFunc("/device_authorization", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dev1", "user_code": "BCDF-GHJK", "verification_uri": ts.URL + "/platform/oauth/device",
			"expires_in": 600, "interval": 1,
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=thecode&state="+q.Get("state"), 302)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": ts.accessToken, "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r1",
		})
	})
	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// loginTestEnv sets up XDG_CONFIG_HOME → t.TempDir(), clears synthetic-mode
// env vars, allows the plain-http test server and makes the "browser"
// follow the authorize URL (so option 1 completes without a real browser).
func loginTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ACH_URL", "")
	t.Setenv("ACH_KEY", "")
	t.Setenv("ACH_PROFILE", "")
	t.Setenv("ACH_INSECURE", "1")
	originalOpener := oauthlogin.Opener
	oauthlogin.Opener = func(u string) error {
		go func() {
			resp, err := http.Get(u) //nolint:gosec // test-only
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { oauthlogin.Opener = originalOpener })
	return dir
}

// executeLogin runs a fresh login command with the given args and stdin,
// returning stdout, stderr, the exit code and the raw error.
func executeLogin(t *testing.T, stdin string, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	cmd := newLoginCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return outBuf.String(), errBuf.String(), exit.OK, nil
	}
	var cErr *exit.CodedError
	if errors.As(err, &cErr) {
		return outBuf.String(), errBuf.String(), cErr.Code, err
	}
	return outBuf.String(), errBuf.String(), exit.General, err
}

func loadConfig(t *testing.T) *config.File {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	f, err := config.Load(path)
	if err != nil || f == nil {
		t.Fatalf("config.Load: f=%v err=%v", f, err)
	}
	return f
}

// TestLogin_DeviceGrant_WritesOAuthProfile: --no-browser shows the code
// and the URL, polls the device grant, stores the token pair — never a key.
func TestLogin_DeviceGrant_WritesOAuthProfile(t *testing.T) {
	loginTestEnv(t)
	ts := newLoginTestServer(t, "a.b.c")

	stdout, _, code, err := executeLogin(t, "", "--profile", "prod", ts.URL, "--no-browser")
	if err != nil || code != exit.OK {
		t.Fatalf("login: err=%v code=%d", err, code)
	}
	if !strings.Contains(stdout, "BCDF-GHJK") || !strings.Contains(stdout, ts.URL+"/platform/oauth/device") {
		t.Fatalf("code + verification URL must be shown; stdout=%s", stdout)
	}
	if strings.Contains(stdout, "a.b.c") || strings.Contains(stdout, "pk") {
		t.Fatalf("no credential on stdout; got %s", stdout)
	}
	f := loadConfig(t)
	dep := f.Profiles["prod"]
	if dep == nil || dep.URL != ts.URL || dep.OAuth == nil || dep.OAuth.AccessToken != "a.b.c" ||
		dep.OAuth.ClientID != "oc_test" || dep.Key != "" {
		t.Fatalf("profile: %+v", dep)
	}
	if f.Default != "prod" {
		t.Fatalf("default = %q; want prod (auto-set on first login)", f.Default)
	}
	path, _ := config.Path()
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %#o; want 0600", st.Mode().Perm())
	}
}

// TestLogin_SecondLogin_ReusesClientAndKeepsKeys: the cached DCR client id is
// reused on the same Hub and the profile's saved keys survive a re-login.
func TestLogin_SecondLogin_ReusesClientAndKeepsKeys(t *testing.T) {
	loginTestEnv(t)
	ts := newLoginTestServer(t, "one")
	if _, _, _, err := executeLogin(t, "", "--profile", "prod", ts.URL, "--no-browser"); err != nil {
		t.Fatal(err)
	}
	path, _ := config.Path()
	f := loadConfig(t)
	f.Profiles["prod"].Keys = map[string]config.SavedKey{"demo": {ID: "ekid_keep", Key: testEK}}
	if err := config.Save(path, f); err != nil {
		t.Fatal(err)
	}
	ts.accessToken = "two"
	if _, _, _, err := executeLogin(t, "", "--profile", "prod", ts.URL, "--no-browser"); err != nil {
		t.Fatal(err)
	}
	dep := loadConfig(t).Profiles["prod"]
	if dep.OAuth.AccessToken != "two" || dep.Keys["demo"].ID != "ekid_keep" {
		t.Fatalf("profile after re-login: %+v", dep)
	}
	if atomic.LoadInt32(&ts.registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", ts.registrations)
	}
}

func TestLogin_RejectInvalidScheme(t *testing.T) {
	loginTestEnv(t)
	_, _, code, err := executeLogin(t, "", "--profile", "prod", "ftp://insecure", "--no-browser")
	if err == nil || code != exit.General || !strings.Contains(err.Error(), "http:// or https://") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestLogin_RefusesHTTP_ByDefault(t *testing.T) {
	loginTestEnv(t)
	t.Setenv("ACH_INSECURE", "")
	_, _, code, err := executeLogin(t, "", "--profile", "dev", "http://localhost:8080", "--no-browser")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "ACH_INSECURE") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	// --insecure passes the gate (the URL is then simply unreachable).
	_, _, _, err = executeLogin(t, "", "--profile", "dev", "http://127.0.0.1:1",
		"--no-browser", "--insecure")
	if err != nil && strings.Contains(err.Error(), "ACH_INSECURE") {
		t.Fatalf("--insecure should pass the URL gate, got %v", err)
	}
}

func TestLogin_SyntheticModeRejected(t *testing.T) {
	loginTestEnv(t)
	t.Setenv("ACH_URL", "https://synth.example")
	t.Setenv("ACH_KEY", testEK)
	_, _, code, err := executeLogin(t, "", "--profile", "prod", "https://hub.test", "--no-browser")
	if err == nil || code != exit.General || !strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestLogin_AutoSetsDefault_KeepsOtherProfiles(t *testing.T) {
	dir := loginTestEnv(t)
	path := filepath.Join(dir, "ach", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	seed := &config.File{Profiles: map[string]*config.Profile{"other": {URL: "https://other.example"}}}
	if err := config.Save(path, seed); err != nil {
		t.Fatal(err)
	}
	ts := newLoginTestServer(t, "x")
	if _, _, _, err := executeLogin(t, "", "--profile", "prod", ts.URL, "--no-browser"); err != nil {
		t.Fatal(err)
	}
	f := loadConfig(t)
	if f.Default != "prod" || f.Profiles["other"] == nil {
		t.Fatalf("default=%q profiles=%+v", f.Default, f.Profiles)
	}
}

// failReader fails the test if resolveBaseURL reads stdin — used to
// assert the positional / ACH_URL paths skip the interactive prompt.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Error("resolveBaseURL read stdin but the positional URL / ACH_URL should have satisfied it")
	return 0, errors.New("stdin must not be read")
}

// TestResolveBaseURL_Precedence: positional URL → ACH_URL → prompt.
func TestResolveBaseURL_Precedence(t *testing.T) {
	t.Run("positional wins over env", func(t *testing.T) {
		t.Setenv("ACH_URL", "https://env.example")
		got, err := resolveBaseURL("https://arg.example", failReader{t}, &bytes.Buffer{})
		if err != nil || got != "https://arg.example" {
			t.Fatalf("url=%q err=%v", got, err)
		}
	})
	t.Run("ACH_URL pre-fills, no prompt read", func(t *testing.T) {
		t.Setenv("ACH_URL", "https://env.example")
		var out bytes.Buffer
		got, err := resolveBaseURL("", failReader{t}, &out)
		if err != nil || got != "https://env.example" || !strings.Contains(out.String(), "read from env:ACH_URL") {
			t.Fatalf("url=%q err=%v out=%q", got, err, out.String())
		}
	})
	t.Run("prompt when both empty", func(t *testing.T) {
		t.Setenv("ACH_URL", "")
		got, err := resolveBaseURL("", strings.NewReader("https://prompt.example\n"), &bytes.Buffer{})
		if err != nil || got != "https://prompt.example" {
			t.Fatalf("url=%q err=%v", got, err)
		}
	})
	t.Run("non-http scheme rejected", func(t *testing.T) {
		t.Setenv("ACH_URL", "ftp://env.example")
		_, err := resolveBaseURL("", failReader{t}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "http:// or https://") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestLogin_URLPositionalAndRemovedFlags: `login <url>` works and the old
// --base-url flag is gone.
func TestLogin_URLPositionalAndRemovedFlags(t *testing.T) {
	loginTestEnv(t)
	ts := newLoginTestServer(t, "x")
	t.Setenv("ACH_URL", "https://ignored.example")
	if _, _, _, err := executeLogin(t, "", ts.URL, "--profile", "prod", "--no-browser"); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t).Profiles["prod"].URL; got != ts.URL {
		t.Fatalf("url = %q; want the positional URL", got)
	}
	_, _, _, err := executeLogin(t, "", "--base-url", ts.URL)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("--base-url: err = %v; want unknown flag", err)
	}
}

// TestLogin_InteractivePrompt covers the profile-name prompt: a first login
// suggests "default" (Enter accepts it); with a "default" on disk there is
// no suggestion and an empty name is rejected; a typed name wins. The URL
// argument skips the URL prompt and --no-browser skips the menu, so stdin feeds the
// profile prompt alone.
func TestLogin_InteractivePrompt(t *testing.T) {
	t.Run("empty accepts the suggested default", func(t *testing.T) {
		loginTestEnv(t)
		ts := newLoginTestServer(t, "x")
		stdout, _, code, err := executeLogin(t, "\n", ts.URL, "--no-browser")
		if err != nil || code != exit.OK || !strings.Contains(stdout, "Profile name [default]: ") {
			t.Fatalf("err=%v code=%d stdout=%q", err, code, stdout)
		}
		if f := loadConfig(t); f.Profiles["default"] == nil || f.Default != "default" {
			t.Fatalf("config: %+v", f)
		}
	})
	t.Run("no suggestion when default exists", func(t *testing.T) {
		dir := loginTestEnv(t)
		path := filepath.Join(dir, "ach", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := config.Save(path, &config.File{Default: "default",
			Profiles: map[string]*config.Profile{"default": {URL: "https://existing.example", Key: testEK}}}); err != nil {
			t.Fatal(err)
		}
		ts := newLoginTestServer(t, "x")
		stdout, _, code, err := executeLogin(t, "\n", ts.URL, "--no-browser")
		if err == nil || code != exit.General || strings.Contains(stdout, "[default]") ||
			!strings.Contains(stdout, "Profile name: ") {
			t.Fatalf("err=%v code=%d stdout=%q", err, code, stdout)
		}
		if loadConfig(t).Profiles["default"].Key != testEK {
			t.Fatal("existing default profile was clobbered")
		}
	})
	t.Run("typed name overrides the suggestion", func(t *testing.T) {
		loginTestEnv(t)
		ts := newLoginTestServer(t, "x")
		if _, _, code, err := executeLogin(t, "prod\n", ts.URL, "--no-browser"); err != nil || code != exit.OK {
			t.Fatalf("err=%v code=%d", err, code)
		}
		if f := loadConfig(t); f.Profiles["prod"] == nil || f.Profiles["default"] != nil || f.Default != "prod" {
			t.Fatalf("config: %+v", f)
		}
	})
}

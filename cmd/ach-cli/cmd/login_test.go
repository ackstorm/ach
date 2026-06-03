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
	"github.com/ackstorm/ach/internal/cli/devicecode"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// loginTestServer spins up an httptest server implementing the
// /platform/auth/cli/{init,token} contract with a configurable
// pending-poll count + pk_ payload. Init always 200s; /token returns
// 202 for the first `pending` calls, then 200 with `payload`.
type loginTestServer struct {
	*httptest.Server
	pending      int32
	calls        int32
	pkPlaintext  string
	ownerEmail   string
	pollInterval int
	expiresIn    int
	keyID        string
}

func newLoginTestServer(t *testing.T, pending int, pkPlaintext, ownerEmail string) *loginTestServer {
	t.Helper()
	ts := &loginTestServer{
		pending:      int32(pending),
		pkPlaintext:  pkPlaintext,
		ownerEmail:   ownerEmail,
		pollInterval: 0, // Honor immediate polling for fast tests via 0; client treats as default.
		expiresIn:    300,
		keyID:        "pkid_abc",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/auth/cli/init", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id":       "sess-test",
			"verification_url": "https://hub.test/platform/auth/login?session_id=sess-test",
			"poll_interval":    ts.pollInterval,
			"expires_in":       ts.expiresIn,
		})
	})
	mux.HandleFunc("/platform/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&ts.calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n <= atomic.LoadInt32(&ts.pending) {
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"key_id":      ts.keyID,
			"plaintext":   ts.pkPlaintext,
			"owner_email": ts.ownerEmail,
		})
	})
	ts.Server = httptest.NewTLSServer(mux)
	// Wire the test server's TLS-trusting Client into the devicecode
	// package seam so the login flow can reach the ephemeral cert.
	previous := devicecode.HTTPClient
	devicecode.HTTPClient = ts.Client()
	t.Cleanup(func() { devicecode.HTTPClient = previous })
	return ts
}

// loginTestEnv sets up XDG_CONFIG_HOME → t.TempDir() and clears
// synthetic-mode env vars so tests run hermetically.
func loginTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ACH_BASE_URL", "")
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_PROFILE", "")
	// Defensive: silence the browser opener for the whole test.
	t.Setenv("ACH_TEST_NO_BROWSER", "1")
	originalOpener := devicecode.Opener
	devicecode.Opener = func(string) error { return nil }
	t.Cleanup(func() { devicecode.Opener = originalOpener })
	return dir
}

// executeLogin executes a fresh login command with the given args and
// returns stdout, stderr, exit code (or 0 on success / 1 on error),
// and the raw error.
func executeLogin(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	cmd := newLoginCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
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

// TestLogin_HappyPath_WritesConfig is Test 1: --profile + --base-url
// + --no-browser against a healthy server writes the pk into config.yaml.
func TestLogin_HappyPath_WritesConfig(t *testing.T) {
	loginTestEnv(t)
	// pending=0: login should complete on the first poll. The
	// devicecode package's own test exercises the multi-pending
	// cadence; here we only assert the integration writes config.
	ts := newLoginTestServer(t, 0, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWXYZ", "u@example")
	defer ts.Close()

	stdout, _, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", ts.URL,
		"--no-browser",
	)
	if err != nil {
		t.Fatalf("login err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}

	// Assert config.yaml exists with the expected profile.
	path, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path: %v", err)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if f == nil {
		t.Fatal("config file not written")
	}
	dep, ok := f.Profiles["prod"]
	if !ok {
		t.Fatalf("profiles.prod missing; got %+v", f.Profiles)
	}
	if dep.URL != ts.URL {
		t.Errorf("profiles.prod.url = %q; want %q", dep.URL, ts.URL)
	}
	if dep.PK != "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWXYZ" {
		t.Errorf("profiles.prod.pk = %q; want pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWXYZ", dep.PK)
	}
	// First login → default: should auto-set.
	if f.Default != "prod" {
		t.Errorf("default = %q; want prod", f.Default)
	}
	// File mode 0600.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %#o; want 0600", st.Mode().Perm())
	}
	// stdout contains owner email + masked pk tail (last-4 WXYZ).
	if !strings.Contains(stdout, "u@example") {
		t.Errorf("stdout missing owner email; got: %s", stdout)
	}
	if !strings.Contains(stdout, "pk-****WXYZ") {
		t.Errorf("stdout missing masked pk tail pk-****WXYZ; got: %s", stdout)
	}
	// CLI-04: full pk plaintext (anything longer than the masked form)
	// MUST NOT appear in stdout. Check the long body explicitly.
	if strings.Contains(stdout, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWXYZ") {
		t.Errorf("CLI-04 leak: full pk_ plaintext present in stdout: %s", stdout)
	}
}

// TestLogin_RejectInvalidScheme refuses a URL that is neither http:// nor
// https:// (here ftp://) with exit 1. http:// is now accepted (with a
// plaintext-transport warning) — see resolveBaseURL + runLogin.
func TestLogin_RejectInvalidScheme(t *testing.T) {
	loginTestEnv(t)

	_, _, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", "ftp://insecure",
		"--no-browser",
	)
	if err == nil {
		t.Fatal("login should have errored on ftp:// URL")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "http:// or https://") {
		t.Errorf("err message missing scheme hint; got %q", err.Error())
	}
}

// TestLogin_AutoSetsDefault is Test 3: ach login on a config with NO
// default: sets default to the new profile name.
func TestLogin_AutoSetsDefault(t *testing.T) {
	dir := loginTestEnv(t)
	// Seed an existing config with no default and an unrelated
	// profile, to assert login adds + sets default (NOT touching
	// the existing one).
	path := filepath.Join(dir, "ach", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := config.Save(path, &config.File{
		Profiles: map[string]*config.Profile{
			"other": {URL: "https://other.example"},
		},
	}); err != nil {
		t.Fatalf("seed config.Save: %v", err)
	}

	ts := newLoginTestServer(t, 0, "pk_aaaaaaaaaaaaaaaaaaaaaaaa1234", "u@x")
	defer ts.Close()

	_, _, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", ts.URL,
		"--no-browser",
	)
	if err != nil || code != exit.OK {
		t.Fatalf("login err = %v, code = %d", err, code)
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Default != "prod" {
		t.Errorf("default = %q; want prod (auto-set when previously absent)", f.Default)
	}
	if _, ok := f.Profiles["other"]; !ok {
		t.Errorf("seed profile 'other' was clobbered: %+v", f.Profiles)
	}
}

// TestLogin_OverwritesPriorPK is Test 4: a second login on the same
// profile overwrites the prior pk.
func TestLogin_OverwritesPriorPK(t *testing.T) {
	loginTestEnv(t)
	ts1 := newLoginTestServer(t, 0, "pk_111111111111111111111111oldP", "u@x")
	defer ts1.Close()

	_, _, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", ts1.URL,
		"--no-browser",
	)
	if err != nil || code != exit.OK {
		t.Fatalf("first login: err=%v code=%d", err, code)
	}

	// Second login → different pk.
	ts2 := newLoginTestServer(t, 0, "pk_222222222222222222222222newP", "u@x")
	defer ts2.Close()
	_, _, code, err = executeLogin(t,
		"--profile", "prod",
		"--base-url", ts2.URL,
		"--no-browser",
	)
	if err != nil || code != exit.OK {
		t.Fatalf("second login: err=%v code=%d", err, code)
	}

	path, _ := config.Path()
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dep := f.Profiles["prod"]
	if dep.PK != "pk_222222222222222222222222newP" {
		t.Errorf("pk = %q; want pk_222222222222222222222222newP (overwrite)", dep.PK)
	}
	if dep.URL != ts2.URL {
		t.Errorf("url = %q; want %q (latest login wins)", dep.URL, ts2.URL)
	}
}

// TestLogin_SyntheticModeRejected is Test 5: ACH_BASE_URL +
// ACH_API_KEY both set → synthetic mode active → exit 1.
func TestLogin_SyntheticModeRejected(t *testing.T) {
	loginTestEnv(t)
	t.Setenv("ACH_BASE_URL", "https://synth.example")
	t.Setenv("ACH_API_KEY", "pk_synthetic_test_key_aaaaaaaaaa")

	_, _, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", "https://hub.test",
		"--no-browser",
	)
	if err == nil {
		t.Fatal("synthetic mode should reject ach login")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "synthetic") {
		t.Errorf("err missing 'synthetic' hint: %q", err.Error())
	}
}

// TestLogin_NoBrowserPrintsURL is Test 6: --no-browser prints the
// verification_url for the user to copy/paste.
func TestLogin_NoBrowserPrintsURL(t *testing.T) {
	loginTestEnv(t)
	ts := newLoginTestServer(t, 0, "pk_aaaaaaaaaaaaaaaaaaaaaaaa9999", "u@x")
	defer ts.Close()

	stdout, stderr, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", ts.URL,
		"--no-browser",
	)
	if err != nil || code != exit.OK {
		t.Fatalf("login: err=%v code=%d", err, code)
	}
	combined := stdout + stderr
	// verification_url is `<ts.URL>/platform/auth/login?session_id=sess-test`
	// per the InitResponse stub. (Stub returns it verbatim — the URL
	// can be a localhost httptest URL.)
	wantSubstr := "?session_id=sess-test"
	if !strings.Contains(combined, wantSubstr) {
		t.Errorf("missing verification_url substr %q; combined: %s", wantSubstr, combined)
	}
}

// TestLogin_PrintsOwnerEmailAndMaskedTail is Test 7: stdout contains
// owner email + masked pk_ tail exactly once. Already partially
// covered by TestLogin_HappyPath_WritesConfig but kept as a focused
// assertion on the masked-tail format.
func TestLogin_PrintsOwnerEmailAndMaskedTail(t *testing.T) {
	loginTestEnv(t)
	ts := newLoginTestServer(t, 0, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWXYZ", "user@org.com")
	defer ts.Close()

	stdout, _, code, err := executeLogin(t,
		"--profile", "prod",
		"--base-url", ts.URL,
		"--no-browser",
	)
	if err != nil || code != exit.OK {
		t.Fatalf("login: err=%v code=%d", err, code)
	}

	if !strings.Contains(stdout, "user@org.com") {
		t.Errorf("stdout missing owner email; got: %s", stdout)
	}
	masked := "pk-****WXYZ"
	if c := strings.Count(stdout, masked); c != 1 {
		t.Errorf("masked tail %q count = %d; want exactly 1; stdout: %s", masked, c, stdout)
	}
}

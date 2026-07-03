// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// adminTestEnv stages an isolated XDG_CONFIG_HOME and clears every
// synthetic-mode env-var so each test runs hermetically.
func adminTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ACH_BASE_URL", "")
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_PROFILE", "")
	return dir
}

// seedAdminConfig writes a minimal config.yaml inside XDG_CONFIG_HOME
// with one active profile named "prod" carrying a pk_ that simulates
// an allowlisted admin pk_.
func seedAdminConfig(t *testing.T, baseURL string) string {
	t.Helper()
	cfgPath, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path: %v", err)
	}
	f := &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {
				URL: baseURL,
				PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAnxyz",
			},
		},
	}
	if err := config.Save(cfgPath, f); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	return cfgPath
}

// adminTestServer wires httptest.NewTLSServer + the package-level HTTP
// client seam so `ach admin *` can reach the ephemeral TLS cert.
// Routes:
//
//	POST /platform/admin/keys/revoke
//	POST /platform/admin/users/{email}/revoke-keys
//	POST /platform/admin/refresh
//	GET  /platform/admin/keys
type adminTestServer struct {
	*httptest.Server
	revokeKeyStatus   int
	revokeKeyBody     map[string]any
	revokeUserStatus  int
	revokeUserBody    map[string]any
	refreshStatus     int
	refreshBody       map[string]any
	keysListStatus    int
	keysListBody      map[string]any
	revokeKeyCalls    int32
	revokeUserCalls   int32
	refreshCalls      int32
	keysListCalls     int32
	lastRevokeKeyBody []byte
	lastRefreshBody   []byte
	lastUserEmailPath string
	lastAuthHeader    string
	lastKeysQuery     string
}

func newAdminTestServer(t *testing.T) *adminTestServer {
	t.Helper()
	srv := &adminTestServer{
		revokeKeyStatus:  200,
		revokeUserStatus: 200,
		refreshStatus:    202,
		keysListStatus:   200,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/admin/keys/revoke", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&srv.revokeKeyCalls, 1)
		srv.lastAuthHeader = r.Header.Get("x-ach-key")
		srv.lastRevokeKeyBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(srv.revokeKeyStatus)
		_ = json.NewEncoder(w).Encode(srv.revokeKeyBody)
	})
	mux.HandleFunc("/platform/admin/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		atomic.AddInt32(&srv.keysListCalls, 1)
		srv.lastKeysQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(srv.keysListStatus)
		body := srv.keysListBody
		if body == nil {
			body = map[string]any{"items": []any{}, "next_cursor": ""}
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/platform/admin/users/", func(w http.ResponseWriter, r *http.Request) {
		// Expected path: /platform/admin/users/{email}/revoke-keys
		if !strings.HasSuffix(r.URL.Path, "/revoke-keys") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		atomic.AddInt32(&srv.revokeUserCalls, 1)
		srv.lastUserEmailPath = strings.TrimPrefix(r.URL.Path, "/platform/admin/users/")
		srv.lastUserEmailPath = strings.TrimSuffix(srv.lastUserEmailPath, "/revoke-keys")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(srv.revokeUserStatus)
		_ = json.NewEncoder(w).Encode(srv.revokeUserBody)
	})
	mux.HandleFunc("/platform/admin/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&srv.refreshCalls, 1)
		srv.lastRefreshBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(srv.refreshStatus)
		if srv.refreshBody != nil {
			_ = json.NewEncoder(w).Encode(srv.refreshBody)
		}
	})
	srv.Server = httptest.NewTLSServer(mux)
	swapAdminHTTPClientForTest(t, srv.Client())
	return srv
}

// executeAdmin runs a fresh admin cobra subtree with args + stdin.
// Wraps the shared helpers_test.go::executeCommand driver (which
// handles both *httpclient.ServerError → MapServerError → exit code
// AND *exit.CodedError → cErr.Code dispatch) while still letting
// admin tests pre-populate stdin via a strings.Reader.
func executeAdmin(t *testing.T, stdin string, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	root := newAdminCmd()
	root.SetIn(strings.NewReader(stdin))
	return executeCommand(t, root, args...)
}

// ---------------------------------------------------------------------
// keys revoke tests
// ---------------------------------------------------------------------

// Test 1: admin keys revoke with pkid_ + --yes + 200 → exit 0.
func TestAdminKeysRevoke_Pkid_200(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyBody = map[string]any{
		"key_id": "pkid_abc",
		"status": "revoked",
	}
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "",
		"keys", "revoke", "pkid_abc", "--yes",
	)
	if err != nil {
		t.Fatalf("admin keys revoke pkid_ err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if atomic.LoadInt32(&srv.revokeKeyCalls) != 1 {
		t.Errorf("revokeKeyCalls = %d; want 1", srv.revokeKeyCalls)
	}
	if !strings.Contains(string(srv.lastRevokeKeyBody), "pkid_abc") {
		t.Errorf("expected request body to contain pkid_abc; got: %s", srv.lastRevokeKeyBody)
	}
	if !strings.Contains(stdout, "pkid_abc") || !strings.Contains(stdout, "revoked") {
		t.Errorf("expected stdout to mention pkid_abc and revoked; got:\n%s", stdout)
	}
}

// Test 2: admin keys revoke with ekid_ + --yes + 200 → exit 0 (CLI-13).
func TestAdminKeysRevoke_Ekid_200(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyBody = map[string]any{
		"key_id": "ekid_xyz",
		"status": "revoked",
	}
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "",
		"keys", "revoke", "ekid_xyz", "--yes",
	)
	if err != nil {
		t.Fatalf("admin keys revoke ekid_ err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if atomic.LoadInt32(&srv.revokeKeyCalls) != 1 {
		t.Errorf("revokeKeyCalls = %d; want 1", srv.revokeKeyCalls)
	}
	if !strings.Contains(string(srv.lastRevokeKeyBody), "ekid_xyz") {
		t.Errorf("expected request body to contain ekid_xyz; got: %s", srv.lastRevokeKeyBody)
	}
	if !strings.Contains(stdout, "ekid_xyz") {
		t.Errorf("expected stdout to mention ekid_xyz; got:\n%s", stdout)
	}
}

// Test 3: admin keys revoke with raw pk_ plaintext → exit 1 BEFORE HTTP.
func TestAdminKeysRevoke_RejectsRawPk(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	seedAdminConfig(t, srv.URL)

	_, stderr, code, err := executeAdmin(t, "",
		"keys", "revoke", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz", "--yes",
	)
	if err == nil {
		t.Fatal("expected error for raw pk_ plaintext")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want %d (General)", code, exit.General)
	}
	if atomic.LoadInt32(&srv.revokeKeyCalls) != 0 {
		t.Errorf("revokeKeyCalls = %d; want 0 (rejected before HTTP)", srv.revokeKeyCalls)
	}
	msg := err.Error() + stderr
	if !strings.Contains(msg, "plaintext") && !strings.Contains(msg, "key id") {
		t.Errorf("expected message about plaintext / key id; got: %q", msg)
	}
}

// Test 4: admin keys revoke with raw ek_ plaintext → exit 1 BEFORE HTTP.
func TestAdminKeysRevoke_RejectsRawEk(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	seedAdminConfig(t, srv.URL)

	_, stderr, code, err := executeAdmin(t, "",
		"keys", "revoke", "ek_aaaaaaaaaaaaaaaaaaaaaawxyz", "--yes",
	)
	if err == nil {
		t.Fatal("expected error for raw ek_ plaintext")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want %d (General)", code, exit.General)
	}
	if atomic.LoadInt32(&srv.revokeKeyCalls) != 0 {
		t.Errorf("revokeKeyCalls = %d; want 0 (rejected before HTTP)", srv.revokeKeyCalls)
	}
	msg := err.Error() + stderr
	if !strings.Contains(msg, "plaintext") && !strings.Contains(msg, "key id") {
		t.Errorf("expected message about plaintext / key id; got: %q", msg)
	}
}

// Test 5: admin keys revoke 403 not_admin → exit 3 (CLI-10).
func TestAdminKeysRevoke_403NotAdmin_Exit3(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyStatus = 403
	srv.revokeKeyBody = map[string]any{
		"error": map[string]string{
			"code":    "not_admin",
			"message": "caller not in admin allowlist",
		},
		"request_id": "req_test",
	}
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "",
		"keys", "revoke", "pkid_abc", "--yes",
	)
	if err == nil {
		t.Fatal("expected error on 403 not_admin")
	}
	if code != exit.AuthN {
		t.Errorf("exit code = %d; want %d (AuthN)", code, exit.AuthN)
	}
}

// Test 6: admin keys revoke 401 invalid_key → exit 3.
func TestAdminKeysRevoke_401_Exit3(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyStatus = 401
	srv.revokeKeyBody = map[string]any{
		"error": map[string]string{
			"code":    "invalid_key",
			"message": "key not found",
		},
		"request_id": "req_test",
	}
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "",
		"keys", "revoke", "pkid_abc", "--yes",
	)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if code != exit.AuthN {
		t.Errorf("exit code = %d; want %d (AuthN)", code, exit.AuthN)
	}
}

// Test 7: admin keys revoke 503 → exit 6 (Network).
func TestAdminKeysRevoke_503_Exit6(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyStatus = 503
	srv.revokeKeyBody = map[string]any{
		"error": map[string]string{
			"code":    "litellm_unreachable",
			"message": "litellm down",
		},
		"request_id": "req_test",
	}
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "",
		"keys", "revoke", "pkid_abc", "--yes",
	)
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if code != exit.Network {
		t.Errorf("exit code = %d; want %d (Network)", code, exit.Network)
	}
}

// Test 8a: admin keys revoke without --yes + stdin "n" → exit 1 cancelled, no HTTP.
func TestAdminKeysRevoke_Interactive_Cancelled(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "n\n",
		"keys", "revoke", "pkid_abc",
	)
	if err == nil {
		t.Fatal("expected error when user cancels confirmation")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want %d", code, exit.General)
	}
	if atomic.LoadInt32(&srv.revokeKeyCalls) != 0 {
		t.Errorf("revokeKeyCalls = %d; want 0 on cancel", srv.revokeKeyCalls)
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected 'cancelled' in error; got %q", err.Error())
	}
}

// Test 8b: admin keys revoke without --yes + stdin "y" → exit 0.
func TestAdminKeysRevoke_Interactive_Confirmed(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyBody = map[string]any{
		"key_id": "pkid_abc",
		"status": "revoked",
	}
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "y\n",
		"keys", "revoke", "pkid_abc",
	)
	if err != nil {
		t.Fatalf("interactive confirm err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if atomic.LoadInt32(&srv.revokeKeyCalls) != 1 {
		t.Errorf("revokeKeyCalls = %d; want 1", srv.revokeKeyCalls)
	}
}

// ---------------------------------------------------------------------
// users revoke-keys tests
// ---------------------------------------------------------------------

// Test 9: admin users revoke-keys 200 — URL-escaped email + rendered count.
func TestAdminUsersRevokeKeys_200(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeUserBody = map[string]any{
		"revoked_count": 3,
		"errors":        []string{},
	}
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "",
		"users", "revoke-keys", "test@example.com", "--yes",
	)
	if err != nil {
		t.Fatalf("revoke-keys err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if atomic.LoadInt32(&srv.revokeUserCalls) != 1 {
		t.Errorf("revokeUserCalls = %d; want 1", srv.revokeUserCalls)
	}
	// URL-escape: "test@example.com" → "test%40example.com"
	wantEscaped := url.PathEscape("test@example.com")
	if srv.lastUserEmailPath != wantEscaped {
		t.Errorf("URL-escaped email path = %q; want %q", srv.lastUserEmailPath, wantEscaped)
	}
	if !strings.Contains(stdout, "3") {
		t.Errorf("expected stdout to mention count 3; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "test@example.com") {
		t.Errorf("expected stdout to mention email; got:\n%s", stdout)
	}
}

// Test 10: admin users revoke-keys 200 with errors list → exit 0, errors rendered.
func TestAdminUsersRevokeKeys_PartialErrors(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeUserBody = map[string]any{
		"revoked_count": 2,
		"errors":        []string{"litellm: timeout"},
	}
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "",
		"users", "revoke-keys", "alice@example.com", "--yes",
	)
	if err != nil {
		t.Fatalf("revoke-keys err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "2") {
		t.Errorf("expected count 2 in stdout; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "litellm: timeout") {
		t.Errorf("expected error line rendered; got:\n%s", stdout)
	}
}

// Test 11: admin users revoke-keys 403 → exit 3 (CLI-10).
func TestAdminUsersRevokeKeys_403_Exit3(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeUserStatus = 403
	srv.revokeUserBody = map[string]any{
		"error": map[string]string{
			"code":    "not_admin",
			"message": "caller not in admin allowlist",
		},
		"request_id": "req_test",
	}
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "",
		"users", "revoke-keys", "test@example.com", "--yes",
	)
	if err == nil {
		t.Fatal("expected error on 403 not_admin")
	}
	if code != exit.AuthN {
		t.Errorf("exit code = %d; want %d (AuthN)", code, exit.AuthN)
	}
}

// ---------------------------------------------------------------------
// refresh tests
// ---------------------------------------------------------------------

// Test 12: admin refresh plugin foo 202 → exit 0.
func TestAdminRefresh_PluginFoo_Accepted(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.refreshBody = map[string]any{
		"status": "accepted",
	}
	seedAdminConfig(t, srv.URL)

	stdout, _, code, err := executeAdmin(t, "",
		"refresh", "plugin", "foo",
	)
	if err != nil {
		t.Fatalf("refresh err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if atomic.LoadInt32(&srv.refreshCalls) != 1 {
		t.Errorf("refreshCalls = %d; want 1", srv.refreshCalls)
	}
	if !strings.Contains(string(srv.lastRefreshBody), `"kind":"plugin"`) ||
		!strings.Contains(string(srv.lastRefreshBody), `"name":"foo"`) {
		t.Errorf("expected request body to contain kind/name; got: %s", srv.lastRefreshBody)
	}
	if !strings.Contains(stdout, "plugin") || !strings.Contains(stdout, "foo") {
		t.Errorf("expected stdout to mention plugin/foo; got:\n%s", stdout)
	}
}

// Test 12b: admin refresh accepts skill + the two marketplace aliases and
// sends the canonical SERVER kind on the wire (G8). marketplace →
// pluginmarketplace, skill-marketplace → skillmarketplace, skill passes through.
func TestAdminRefresh_SkillAndMarketplaceKinds_Accepted(t *testing.T) {
	cases := []struct {
		userKind   string
		serverKind string
	}{
		{"skill", "skill"},
		{"marketplace", "pluginmarketplace"},
		{"skill-marketplace", "skillmarketplace"},
	}
	for _, tc := range cases {
		t.Run(tc.userKind, func(t *testing.T) {
			adminTestEnv(t)
			srv := newAdminTestServer(t)
			defer srv.Close()
			srv.refreshBody = map[string]any{"status": "accepted"}
			seedAdminConfig(t, srv.URL)

			_, _, code, err := executeAdmin(t, "", "refresh", tc.userKind, "foo")
			if err != nil {
				t.Fatalf("kind=%s: refresh err = %v", tc.userKind, err)
			}
			if code != exit.OK {
				t.Fatalf("kind=%s: exit code = %d; want 0", tc.userKind, code)
			}
			if atomic.LoadInt32(&srv.refreshCalls) != 1 {
				t.Errorf("kind=%s: refreshCalls = %d; want 1", tc.userKind, srv.refreshCalls)
			}
			wantKind := `"kind":"` + tc.serverKind + `"`
			if !strings.Contains(string(srv.lastRefreshBody), wantKind) {
				t.Errorf("kind=%s: expected request body to carry %s; got: %s",
					tc.userKind, wantKind, srv.lastRefreshBody)
			}
		})
	}
}

// Test 13: admin refresh with invalid kind → exit 1 BEFORE HTTP.
func TestAdminRefresh_InvalidKind_Rejected(t *testing.T) {
	cases := []string{"team", "environment", "backendidentitypolicy", "garbage"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			adminTestEnv(t)
			srv := newAdminTestServer(t)
			defer srv.Close()
			seedAdminConfig(t, srv.URL)

			_, stderr, code, err := executeAdmin(t, "",
				"refresh", kind, "foo",
			)
			if err == nil {
				t.Fatalf("kind=%s: expected error", kind)
			}
			if code != exit.General {
				t.Errorf("kind=%s: code = %d; want %d", kind, code, exit.General)
			}
			if atomic.LoadInt32(&srv.refreshCalls) != 0 {
				t.Errorf("kind=%s: refreshCalls = %d; want 0 (rejected before HTTP)", kind, srv.refreshCalls)
			}
			msg := err.Error() + stderr
			// Closed-set error should name the valid kinds.
			for _, valid := range []string{"plugin", "prompt", "artifact", "marketplace"} {
				if !strings.Contains(msg, valid) {
					t.Errorf("kind=%s: error msg missing %q; got: %q", kind, valid, msg)
				}
			}
		})
	}
}

// Test 14: admin refresh 403 → exit 3.
func TestAdminRefresh_403_Exit3(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.refreshStatus = 403
	srv.refreshBody = map[string]any{
		"error": map[string]string{
			"code":    "not_admin",
			"message": "caller not in admin allowlist",
		},
		"request_id": "req_test",
	}
	seedAdminConfig(t, srv.URL)

	_, _, code, err := executeAdmin(t, "",
		"refresh", "plugin", "foo",
	)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if code != exit.AuthN {
		t.Errorf("exit code = %d; want %d (AuthN)", code, exit.AuthN)
	}
}

// Test 15: admin parent without subcommand prints help and exits 0.
func TestAdmin_NoSubcommand_PrintsHelp(t *testing.T) {
	adminTestEnv(t)
	// No need for a server — help path makes no HTTP calls.
	stdout, _, code, err := executeAdmin(t, "")
	if err != nil {
		t.Fatalf("help err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "admin") {
		t.Errorf("expected help text to mention 'admin'; got:\n%s", stdout)
	}
}

// Test 16: --verbose redacts x-ach-key in stderr header dump.
func TestAdminKeysRevoke_Verbose_RedactsAchKey(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.revokeKeyBody = map[string]any{
		"key_id": "pkid_abc",
		"status": "revoked",
	}
	seedAdminConfig(t, srv.URL)

	_, stderr, code, err := executeAdmin(t, "",
		"keys", "revoke", "pkid_abc", "--yes", "--verbose",
	)
	if err != nil {
		t.Fatalf("verbose err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	// Verbose should dump x-ach-key with a redacted pk-*** value.
	if !strings.Contains(stderr, "pk-***") {
		t.Errorf("expected stderr to contain 'pk-***' (redacted header); got:\n%s", stderr)
	}
	if strings.Contains(stderr, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAnxyz") {
		t.Errorf("CLI-04/T-06-08-02 leak: unredacted pk- in stderr; got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------
// keys list tests
// ---------------------------------------------------------------------

// TestAdminKeys_List_SendsFiltersAndRenders verifies that
// `ach admin keys list --owner-email a@x --type pk` sends the correct
// query filters and renders the table output.
func TestAdminKeys_List_SendsFiltersAndRenders(t *testing.T) {
	adminTestEnv(t)
	srv := newAdminTestServer(t)
	defer srv.Close()
	srv.keysListBody = map[string]any{
		"items": []map[string]any{
			{"key_id": "pkid_a", "type": "pk", "owner_email": "a@x", "status": "active", "created_at": "2026-06-01T00:00:00Z"},
		},
		"next_cursor": "",
	}
	seedAdminConfig(t, srv.URL)

	out, _, _, err := executeAdmin(t, "", "keys", "list", "--owner-email", "a@x", "--type", "pk")
	if err != nil {
		t.Fatalf("admin keys list err=%v", err)
	}
	encodedEmail := strings.Contains(srv.lastKeysQuery, "owner_email=a%40x")
	rawEmail := strings.Contains(srv.lastKeysQuery, "owner_email=a@x")
	if !encodedEmail && !rawEmail {
		t.Errorf("owner_email not sent; query=%q", srv.lastKeysQuery)
	}
	if !strings.Contains(srv.lastKeysQuery, "type=pk") || !strings.Contains(srv.lastKeysQuery, "status=active") {
		t.Errorf("filters not sent; query=%q", srv.lastKeysQuery)
	}
	for _, want := range []string{"KEY-ID", "TYPE", "pkid_a", "pk"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestAdminListKinds_ExcludesOperatorInternalKinds(t *testing.T) {
	for _, banned := range []string{"litellm-connections", "external-refs"} {
		if slices.Contains(adminListKinds, banned) {
			t.Errorf("adminListKinds must not contain operator-internal kind %q", banned)
		}
	}
	want := []string{
		"environments", "plugins", "prompts", "artifacts", "skills",
		"marketplaces", "skill-marketplaces", "bips",
	}
	if len(adminListKinds) != len(want) {
		t.Fatalf("adminListKinds = %v, want %v", adminListKinds, want)
	}
	for i, k := range want {
		if adminListKinds[i] != k {
			t.Errorf("adminListKinds[%d] = %q, want %q", i, adminListKinds[i], k)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// validEkBearer is a well-formed ek- key (ek- + 64 base64url chars).
const validEkBearer = "ek-" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// contentTestServer captures the last request for assertions.
type contentTestServer struct {
	*httptest.Server
	lastPath string
	lastKey  string
	lastEnv  string
	status   int
	body     []byte
	errBody  string
}

func newContentTestServer(t *testing.T) *contentTestServer {
	t.Helper()
	cs := &contentTestServer{status: 200, body: []byte("RAW-PROMPT-BYTES")}
	mux := http.NewServeMux()
	mux.HandleFunc("/content/", func(w http.ResponseWriter, r *http.Request) {
		cs.lastPath = r.URL.Path
		cs.lastKey = r.Header.Get("x-ach-key")
		cs.lastEnv = r.Header.Get("x-ach-environment")
		if cs.status != 200 {
			w.WriteHeader(cs.status)
			_, _ = w.Write([]byte(cs.errBody))
			return
		}
		_, _ = w.Write(cs.body)
	})
	cs.Server = httptest.NewServer(mux)
	t.Cleanup(cs.Close)
	return cs
}

// synthEnv points the CLI at the stub server in synthetic mode with the given
// key (no disk config consulted).
func synthEnv(t *testing.T, url, key string) {
	t.Helper()
	credTestEnv(t)
	t.Setenv("ACH_URL", url)
	t.Setenv("ACH_KEY", key)
}

// sessionEnv points the CLI at the stub server through an OAuth profile
// (the session token is a personal credential, like a pk-).
func sessionEnv(t *testing.T, url string) {
	t.Helper()
	credTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	seedProfile(t, "p", oauthProfile(url))
}

// TestEnvFetch_SessionWritesBytesAndHeaders — a session streams the raw body
// to stdout and sends x-ach-key + x-ach-environment.
func TestEnvFetch_SessionWritesBytesAndHeaders(t *testing.T) {
	srv := newContentTestServer(t)
	sessionEnv(t, srv.URL)

	stdout, _, code, err := executeCommand(t, newEnvCmd(), "fetch", "prod", "prompt", "foo")
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if stdout != "RAW-PROMPT-BYTES" {
		t.Errorf("stdout = %q; want raw body", stdout)
	}
	if srv.lastPath != "/content/prompt/foo" {
		t.Errorf("path = %q; want /content/prompt/foo", srv.lastPath)
	}
	if srv.lastKey != "h.p.s" {
		t.Errorf("x-ach-key = %q; want the bearer", srv.lastKey)
	}
	if srv.lastEnv != "prod" {
		t.Errorf("x-ach-environment = %q; want prod", srv.lastEnv)
	}
}

// TestEnvFetch_FileFlag_EkSendsEnvironment — `env fetch demo plugin foo --file x`
// writes the body to x and sends x-ach-environment: demo even for an ek- key.
func TestEnvFetch_FileFlag_EkSendsEnvironment(t *testing.T) {
	srv := newContentTestServer(t)
	synthEnv(t, srv.URL, validEkBearer)
	out := filepath.Join(t.TempDir(), "x")

	stdout, _, code, err := executeCommand(t, newEnvCmd(), "fetch", "demo", "plugin", "foo", "--file", out)
	if err != nil || code != exit.OK {
		t.Fatalf("fetch: code=%d err=%v", code, err)
	}
	if srv.lastPath != "/content/plugin/foo" {
		t.Errorf("path = %q; want /content/plugin/foo", srv.lastPath)
	}
	if srv.lastEnv != "demo" {
		t.Errorf("x-ach-environment = %q; want demo", srv.lastEnv)
	}
	if stdout != "" {
		t.Errorf("stdout = %q; want empty with --file", stdout)
	}
	if b, _ := os.ReadFile(out); string(b) != "RAW-PROMPT-BYTES" {
		t.Errorf("--file content = %q; want raw body", b)
	}
}

// TestEnvFetch_RequiresEnvironment — the Environment is positional and
// required: two args are a usage error before any HTTP call.
func TestEnvFetch_RequiresEnvironment(t *testing.T) {
	srv := newContentTestServer(t)
	sessionEnv(t, srv.URL)

	_, _, code, err := executeCommand(t, newEnvCmd(), "fetch", "prompt", "foo")
	if err == nil {
		t.Fatal("expected error without <env>")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want %d", code, exit.General)
	}
	if srv.lastPath != "" {
		t.Errorf("server was called (%q); want rejection before HTTP", srv.lastPath)
	}
}

// TestEnvFetch_NotFound — a 404 surfaces a non-zero exit and does not
// write the artifact body to stdout.
func TestEnvFetch_NotFound(t *testing.T) {
	srv := newContentTestServer(t)
	srv.status = http.StatusNotFound
	srv.errBody = `{"error":{"code":"content_not_found","message":"no such artifact"},"request_id":"req_x"}`
	synthEnv(t, srv.URL, validEkBearer)

	stdout, _, code, err := executeCommand(t, newEnvCmd(), "fetch", "demo", "prompt", "missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if code == exit.OK {
		t.Errorf("exit code = %d; want non-zero", code)
	}
	if strings.Contains(stdout, "no such artifact") {
		t.Errorf("error body leaked to stdout: %q", stdout)
	}
}

// TestEnvFetch_InvalidKind — an unsupported kind is rejected before HTTP.
func TestEnvFetch_InvalidKind(t *testing.T) {
	srv := newContentTestServer(t)
	synthEnv(t, srv.URL, validEkBearer)

	_, _, code, err := executeCommand(t, newEnvCmd(), "fetch", "demo", "team", "foo")
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want %d", code, exit.General)
	}
	if srv.lastPath != "" {
		t.Errorf("server was called (%q); want rejection before HTTP", srv.lastPath)
	}
}

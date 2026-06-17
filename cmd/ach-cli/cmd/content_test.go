// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// validPkBearer is a syntactically-correct pk- bearer (pk- + 64 base64url
// chars) so keys.ClassifyBearer returns PrefixPk and the x-ach-environment
// header path is exercised.
const validPkBearer = "pk-" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
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
// bearer (no disk config consulted).
func synthEnv(t *testing.T, url, bearer string) {
	t.Helper()
	t.Setenv("ACH_BASE_URL", url)
	t.Setenv("ACH_API_KEY", bearer)
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_PROFILE", "")
}

// TestContentFetch_PkWritesBytesAndHeaders — a pk- bearer with --environment
// streams the raw body to stdout and sends x-ach-key + x-ach-environment.
func TestContentFetch_PkWritesBytesAndHeaders(t *testing.T) {
	srv := newContentTestServer(t)
	synthEnv(t, srv.URL, validPkBearer)

	stdout, _, code, err := executeCommand(t, newContentCmd(),
		"fetch", "prompt", "foo", "--environment", "prod")
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
	if srv.lastKey != validPkBearer {
		t.Errorf("x-ach-key = %q; want the bearer", srv.lastKey)
	}
	if srv.lastEnv != "prod" {
		t.Errorf("x-ach-environment = %q; want prod", srv.lastEnv)
	}
}

// TestContentFetch_PkRequiresEnvironment — a pk- bearer without --environment
// is rejected before any HTTP call.
func TestContentFetch_PkRequiresEnvironment(t *testing.T) {
	srv := newContentTestServer(t)
	synthEnv(t, srv.URL, validPkBearer)

	_, _, code, err := executeCommand(t, newContentCmd(), "fetch", "prompt", "foo")
	if err == nil {
		t.Fatal("expected error for pk- without --environment")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want %d", code, exit.General)
	}
	if srv.lastPath != "" {
		t.Errorf("server was called (%q); want rejection before HTTP", srv.lastPath)
	}
}

// TestContentFetch_EkOmitsEnvironmentHeader — an ek- bearer needs no
// --environment and sends no x-ach-environment header.
func TestContentFetch_EkOmitsEnvironmentHeader(t *testing.T) {
	srv := newContentTestServer(t)
	synthEnv(t, srv.URL, validEkBearer)

	_, _, code, err := executeCommand(t, newContentCmd(), "fetch", "skill", "bar")
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if code != exit.OK {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if srv.lastEnv != "" {
		t.Errorf("x-ach-environment = %q; want empty for ek-", srv.lastEnv)
	}
	if srv.lastPath != "/content/skill/bar" {
		t.Errorf("path = %q; want /content/skill/bar", srv.lastPath)
	}
}

// TestContentFetch_NotFound — a 404 surfaces a non-zero exit and does not
// write the artifact body to stdout.
func TestContentFetch_NotFound(t *testing.T) {
	srv := newContentTestServer(t)
	srv.status = http.StatusNotFound
	srv.errBody = `{"error":{"code":"content_not_found","message":"no such artifact"},"request_id":"req_x"}`
	synthEnv(t, srv.URL, validEkBearer)

	stdout, _, code, err := executeCommand(t, newContentCmd(), "fetch", "prompt", "missing")
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

// TestContentFetch_InvalidKind — an unsupported kind is rejected before HTTP.
func TestContentFetch_InvalidKind(t *testing.T) {
	srv := newContentTestServer(t)
	synthEnv(t, srv.URL, validEkBearer)

	_, _, code, err := executeCommand(t, newContentCmd(), "fetch", "team", "foo")
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

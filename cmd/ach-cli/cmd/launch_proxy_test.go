// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
)

// proxyThrough starts the launch proxy for the given credential flags and
// sends one request with hdr through it.
func proxyThrough(t *testing.T, f credFlags, upstream string, hdr http.Header) *http.Response {
	t.Helper()
	addr, stop, err := startLaunchProxy(&launchCred{f: f}, upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	req, _ := http.NewRequest(http.MethodPost, addr+"/v1/messages?beta=true", strings.NewReader(`{}`))
	req.Header = hdr
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// The tool's own login and headers reach ACH untouched; the ACH credential
// is the profile's, and any other ACH key the tool carries is dropped.
func TestLaunchProxy_InjectsCredentialKeepsToolLogin(t *testing.T) {
	var got *http.Request
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r }))
	defer up.Close()
	credTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	seedProfile(t, "prod", oauthProfile(up.URL))

	proxyThrough(t, credFlags{}, up.URL, http.Header{
		"Authorization":     {"Bearer sk-ant-oat01-sub"},
		"Anthropic-Beta":    {"oauth-2025-04-20"},
		"X-Litellm-Api-Key": {"Bearer ek-static"},
		"X-Ach-Key":         {"pk-old"},
		"X-Route":           {"keep"},
	})
	if got == nil {
		t.Fatal("upstream not reached")
	}
	for h, want := range map[string]string{
		"Authorization": "Bearer sk-ant-oat01-sub", "Anthropic-Beta": "oauth-2025-04-20",
		"X-Ach-Key": "h.p.s", "X-Litellm-Api-Key": "", "X-Route": "keep",
	} {
		if v := got.Header.Get(h); v != want {
			t.Errorf("%s = %q, want %q", h, v, want)
		}
	}
	if got.URL.RequestURI() != "/v1/messages?beta=true" {
		t.Errorf("path = %q", got.URL.RequestURI())
	}
}

// --profile picks both the credential and, through the caller, the upstream.
func TestLaunchProxy_Profile(t *testing.T) {
	var key string
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("x-ach-key")
	}))
	defer up.Close()
	credTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	path, _ := config.Path()
	abuela := oauthProfile(up.URL)
	abuela.OAuth.AccessToken = "a.b.c"
	if err := config.Save(path, &config.File{Default: "prod", Profiles: map[string]*config.Profile{
		"prod": oauthProfile("https://other.example"), "my-abuela": abuela,
	}}); err != nil {
		t.Fatal(err)
	}

	proxyThrough(t, credFlags{Profile: "my-abuela"}, up.URL, http.Header{})
	if key != "a.b.c" {
		t.Fatalf("x-ach-key = %q, want my-abuela's token", key)
	}
}

// Without a usable sign-in the tool gets an Anthropic-shaped 401 it can show.
func TestLaunchProxy_NoLoginIsAuthError(t *testing.T) {
	credTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	resp := proxyThrough(t, credFlags{}, "http://127.0.0.1:1", http.Header{})
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(string(body), `"type":"authentication_error"`) ||
		!strings.Contains(string(body), "ach-cli login") {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
}

// Only the tool reaches ACH through the proxy, and only /v1: a request
// without the base URL's secret prefix, for another path, or under another
// Host (DNS rebinding) is 404 and never carries the credential upstream.
func TestLaunchProxy_RefusesOtherCallers(t *testing.T) {
	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer up.Close()
	credTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	seedProfile(t, "prod", oauthProfile(up.URL))
	addr, stop, err := startLaunchProxy(&launchCred{}, up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	u, _ := url.Parse(addr)
	for name, req := range map[string]*http.Request{
		"no prefix":    httptest.NewRequest(http.MethodGet, "http://"+u.Host+"/v1/messages", nil),
		"not /v1":      httptest.NewRequest(http.MethodPost, addr+"/platform/keys", nil),
		"foreign host": httptest.NewRequest(http.MethodPost, addr+"/v1/messages", nil),
	} {
		req.RequestURI = ""
		req.URL, _ = url.Parse(req.URL.String())
		if name == "foreign host" {
			req.Host = "evil.example:" + u.Port()
		} else {
			req.Host = u.Host
		}
		req.URL.Host = u.Host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, resp.StatusCode)
		}
	}
	if hits != 0 {
		t.Fatalf("upstream hit %d times", hits)
	}
}

// A streamed response reaches the tool chunk by chunk, not at the end.
func TestLaunchProxy_Streams(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "event: one\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer up.Close()
	defer close(release)
	credTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	seedProfile(t, "prod", oauthProfile(up.URL))

	resp := proxyThrough(t, credFlags{}, up.URL, http.Header{})
	buf := make([]byte, 12)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, buf); done <- err }()
	select {
	case err := <-done:
		if err != nil || string(buf) != "event: one\n\n" {
			t.Fatalf("read %q, %v", buf, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first chunk did not stream")
	}
}

// Once pinned, a profile that now points elsewhere is refused, never sent there.
func TestLaunchCred_PinsProfileURL(t *testing.T) {
	credTestEnv(t)
	seedProfile(t, "prod", oauthProfile("https://one.example"))
	l := &launchCred{}
	if _, err := l.get(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedProfile(t, "prod", oauthProfile("https://two.example"))
	l.forget()
	if _, err := l.get(context.Background()); err == nil || !strings.Contains(err.Error(), "restart launch") {
		t.Fatalf("err = %v, want the pinned-URL refusal", err)
	}
}

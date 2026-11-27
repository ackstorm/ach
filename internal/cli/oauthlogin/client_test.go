// SPDX-License-Identifier: Apache-2.0

package oauthlogin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
)

func TestResourceLoginAcceptsPastedCallback(t *testing.T) {
	as, _ := fakeAS(t, "/mcp/zoho")
	resource := as.URL + "/mcp/zoho"
	prompt := func(_ context.Context, raw string) (string, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		q := u.Query()
		if q.Get("resource") != resource || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			t.Fatal("resource or PKCE challenge missing from authorization URL")
		}
		return q.Get("redirect_uri") + "?code=thecode&state=" + q.Get("state"), nil
	}
	client := Client{BaseURL: as.URL}
	if _, err := client.LoginForResource(t.Context(), "oc_test", resource, prompt); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackMustMatchLoopbackAndState(t *testing.T) {
	const redirect = "http://127.0.0.1:43210/callback"
	const query = "?state=current&code=private-code"
	for _, raw := range []string{
		"https://127.0.0.1:43210/callback" + query,
		"http://localhost:43210/callback" + query,
		"http://127.0.0.1:43211/callback" + query,
		"http://127.0.0.1:43210/other" + query,
		"http://user@127.0.0.1:43210/callback" + query,
		redirect + query + "#fragment",
		redirect + query + "&state=current",
		redirect + query + "&code=other",
		redirect + "?state=wrong&code=private-code",
		redirect + "?error=private-code&state=current",
		redirect + "?state=current",
	} {
		if _, err := callbackCode(raw, redirect, "current"); err == nil || strings.Contains(err.Error(), "private-code") {
			t.Error("invalid callback accepted or authorization value included in error")
		}
	}
}

func TestPastedCallbackExpires(t *testing.T) {
	as, _ := fakeAS(t)
	c := Client{BaseURL: as.URL, LoginTimeout: time.Millisecond}
	prompt := func(ctx context.Context, raw string) (string, error) {
		<-ctx.Done()
		return "expired callback", nil
	}
	_, err := c.LoginForResource(t.Context(), "oc_test", as.URL+"/mcp/zoho", prompt)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected expired authorization, got %v", err)
	}
}

// fakeAS: metadata, DCR, authorize (redirects straight back with a code),
// token (code → pair; refresh → new pair).
func fakeAS(t *testing.T, resourcePath ...string) (*httptest.Server, *int32) {
	t.Helper()
	var registrations int32
	liveRefresh := "r1"
	mux := http.NewServeMux()
	var srv *httptest.Server
	var devicePolls int32
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token",
			"registration_endpoint": srv.URL + "/register", "code_challenge_methods_supported": []string{"S256"},
			"device_authorization_endpoint": srv.URL + "/device_authorization",
		})
	})
	mux.HandleFunc("/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if len(resourcePath) > 0 && r.PostForm.Get("resource") != srv.URL+resourcePath[0] {
			http.Error(w, `{"error":"invalid_target"}`, http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("client_id") != "oc_test" {
			http.Error(w, `{"error":"invalid_client"}`, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dev1", "user_code": "BCDF-GHJK", "verification_uri": srv.URL + "/device",
			"verification_uri_complete": srv.URL + "/device?user_code=BCDF-GHJK",
			"expires_in":                600, "interval": 1,
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&registrations, 1)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "oc_test"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != "oc_test" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "bad authorize", 400)
			return
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=thecode&state="+q.Get("state"), 302)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if len(resourcePath) > 0 && r.PostForm.Get("resource") != srv.URL+resourcePath[0] {
				http.Error(w, `{"error":"invalid_target"}`, http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("code") != "thecode" || r.PostForm.Get("code_verifier") == "" {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a.b.c", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r1"})
		case "urn:ietf:params:oauth:grant-type:device_code":
			if len(resourcePath) > 0 && r.PostForm.Get("resource") != srv.URL+resourcePath[0] {
				http.Error(w, `{"error":"invalid_target"}`, http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("device_code") != "dev1" || r.PostForm.Get("client_id") != "oc_test" {
				http.Error(w, `{"error":"expired_token"}`, 400)
				return
			}
			if atomic.AddInt32(&devicePolls, 1) < 2 { // first poll: user still at the page
				http.Error(w, `{"error":"authorization_pending"}`, 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "g.h.i", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r3"})
		case "refresh_token":
			if r.PostForm.Get("refresh_token") != liveRefresh {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			liveRefresh = "r2" // rotation: r1 is dead now
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "d.e.f", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "r2"})
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &registrations
}

// browserFollow "opens" the authorize URL: fetching it follows the redirect
// to the client's loopback listener.
func browserFollow(u string) error {
	go func() {
		resp, err := http.Get(u) //nolint:gosec // test-only, fake AS
		if err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

func TestLogin_RegistersOnceThenExchangesWithPKCE(t *testing.T) {
	as, regs := fakeAS(t)
	Opener = browserFollow
	c := &Client{BaseURL: as.URL}
	creds, err := c.Login(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if creds.ClientID != "oc_test" || creds.AccessToken != "a.b.c" || creds.RefreshToken != "r1" || time.Until(creds.ExpiresAt) < 55*time.Minute {
		t.Fatalf("%+v", creds)
	}
	if _, err := c.Login(context.Background(), creds.ClientID); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(regs) != 1 {
		t.Fatalf("registrations = %d, want 1", *regs)
	}
}

func TestRefreshAndCurrentAccessToken(t *testing.T) {
	as, _ := fakeAS(t)
	c := &Client{BaseURL: as.URL}
	creds, err := c.Refresh(context.Background(), "oc_test", "r1")
	if err != nil || creds.AccessToken != "d.e.f" || creds.RefreshToken != "r2" {
		t.Fatalf("%+v err=%v", creds, err)
	}
	if _, err := c.Refresh(context.Background(), "oc_test", "r1"); err == nil {
		t.Fatal("dead refresh must error")
	}

	as, _ = fakeAS(t)
	c = &Client{BaseURL: as.URL}
	near := &config.OAuthCreds{ClientID: "oc_test", AccessToken: "a.b.c", RefreshToken: "r1", ExpiresAt: time.Now().Add(30 * time.Second)}
	tok, updated, err := c.CurrentAccessToken(context.Background(), near)
	if err != nil || tok != "d.e.f" || updated == nil || updated.RefreshToken != "r2" {
		t.Fatalf("tok=%q updated=%+v err=%v", tok, updated, err)
	}
	fresh := &config.OAuthCreds{ClientID: "oc_test", AccessToken: "a.b.c", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}
	tok, updated, err = c.CurrentAccessToken(context.Background(), fresh)
	if err != nil || tok != "a.b.c" || updated != nil {
		t.Fatalf("fresh token must be returned as-is: tok=%q updated=%v err=%v", tok, updated, err)
	}
}

func TestLogin_StateMismatchIsRejected(t *testing.T) {
	as, _ := fakeAS(t)
	Opener = func(u string) error {
		go func() {
			p, _ := url.Parse(u)
			resp, err := http.Get(p.Query().Get("redirect_uri") + "?code=thecode&state=WRONG") //nolint:gosec // test-only
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	c := &Client{BaseURL: as.URL, LoginTimeout: 2 * time.Second}
	if _, err := c.Login(context.Background(), "oc_test"); err == nil {
		t.Fatal("expected state mismatch")
	}
}

func TestDeviceLogin_ShowsCodeThenPollsUntilApproved(t *testing.T) {
	as, regs := fakeAS(t, "/mcp/zoho")
	c := &Client{BaseURL: as.URL}
	shown := ""
	creds, err := c.DeviceLogin(context.Background(), "", as.URL+"/mcp/zoho", func(code, uri string, _ bool) { shown = code + " @ " + uri })
	if err != nil {
		t.Fatal(err)
	}
	if shown != "BCDF-GHJK @ "+as.URL+"/device?user_code=BCDF-GHJK" || creds.ClientID != "oc_test" || creds.AccessToken != "g.h.i" || creds.RefreshToken != "r3" {
		t.Fatalf("shown=%q creds=%+v", shown, creds)
	}
	if atomic.LoadInt32(regs) != 1 {
		t.Fatalf("registrations = %d, want 1 (DCR happens once, with a loopback placeholder)", *regs)
	}
}

func TestDeviceLogin_TerminalErrorStopsPolling(t *testing.T) {
	as, _ := fakeAS(t)
	c := &Client{BaseURL: as.URL}
	// A cached client id the AS does not know → device_authorization 400.
	if _, err := c.DeviceLogin(context.Background(), "oc_unknown", "", func(string, string, bool) {}); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("err=%v", err)
	}
}

// TestLoginHintAndRefusalReason: the hint rides /authorize and
// /device_authorization, and ACH's error_description reaches the caller.
func TestLoginHintAndRefusalReason(t *testing.T) {
	const reason = "You are signed in as a@x.com, but this sign-in was started for b@x.com."
	if _, err := callbackCode("http://127.0.0.1:1/callback?state=s&error=access_denied&error_description="+url.QueryEscape(reason),
		"http://127.0.0.1:1/callback", "s"); err == nil || err.Error() != reason {
		t.Fatalf("callback: %v", err)
	}

	as, _ := fakeAS(t)
	client := Client{BaseURL: as.URL, LoginHint: "b@x.com"}
	prompt := func(_ context.Context, raw string) (string, error) {
		u, _ := url.Parse(raw)
		if got := u.Query().Get("login_hint"); got != "b@x.com" {
			t.Fatalf("authorize login_hint = %q", got)
		}
		return "", errors.New("stop")
	}
	if _, err := client.LoginForResource(t.Context(), "oc_test", "", prompt); err == nil || err.Error() != "stop" {
		t.Fatalf("loopback: %v", err)
	}

	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": "http://" + r.Host + "/a", "token_endpoint": "http://" + r.Host + "/token",
				"device_authorization_endpoint": "http://" + r.Host + "/device_authorization"})
		case "/device_authorization":
			if r.PostForm.Get("login_hint") != "b@x.com" {
				http.Error(w, `{"error":"no_hint"}`, 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "d", "user_code": "U", "verification_uri": "v", "expires_in": 60, "interval": 1})
		case "/token":
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "access_denied", "error_description": reason})
		}
	}))
	defer dev.Close()
	client.BaseURL = dev.URL
	if _, err := client.DeviceLogin(t.Context(), "oc_test", "", func(string, string, bool) {}); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("device: %v", err)
	}
}

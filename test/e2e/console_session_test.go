//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// consoleLogin walks /platform/console/session/login → Dex mock →
// as-callback on the single e2e origin (rewriting every redirect hop to
// base like a browser would) and returns the console cookie. The e2e
// base is plain http, so the cookie carries the insecure name.
func consoleLogin(t *testing.T, base string) *http.Cookie {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	baseURL, _ := url.Parse(base)
	cookies := map[string]string{}
	next := base + "/platform/console/session/login?next=/keys"
	for hop := 0; hop < 12; hop++ {
		req, _ := http.NewRequest(http.MethodGet, next, nil)
		if len(cookies) > 0 {
			req.Header.Set("Cookie", encodeCookieMap(cookies))
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("hop %d GET %s: %v", hop, next, err)
		}
		var console *http.Cookie
		for _, c := range resp.Cookies() {
			cookies[c.Name] = c.Value
			if c.Name == "ach_console" && c.Value != "" {
				console = c
			}
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if console != nil {
			if loc != "/keys" {
				t.Fatalf("final redirect %q, want /keys", loc)
			}
			if !console.HttpOnly || console.SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie attrs: %+v", console)
			}
			return console
		}
		if resp.StatusCode/100 != 3 || loc == "" {
			t.Fatalf("hop %d %s: %d (no redirect)\n%s", hop, next, resp.StatusCode, truncate(body, 600))
		}
		cur, _ := url.Parse(next)
		ref, err := url.Parse(loc)
		if err != nil {
			t.Fatal(err)
		}
		abs := cur.ResolveReference(ref)
		abs.Scheme, abs.Host = baseURL.Scheme, baseURL.Host
		next = abs.String()
	}
	t.Fatal("console login never set the cookie")
	return nil
}

// TestConsoleSession — AC-02, AC-03, AC-24 through the public origin:
// the console cookie reaches the same domain handlers a bearer does,
// mutations need fetch metadata, both credentials at once are refused,
// logout ends the web session but not the CLI's refresh chain, and no
// LiteLLM path outside the four families is reachable.
func TestConsoleSession(t *testing.T) {
	base := "http://" + phase4GatewayAuthority(t)
	c := consoleLogin(t, base)
	do := func(method, path string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(`{"environment":"demo","name":"console-e2e"}`))
		req.AddCookie(c)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	// Cookie reaches the same domain handlers as a bearer (AC-03).
	if r := do("GET", "/platform/keys", nil); r.StatusCode != 200 {
		t.Fatalf("GET /platform/keys with cookie: %d", r.StatusCode)
	}
	if r := do("GET", "/platform/console/bootstrap", nil); r.StatusCode != 200 {
		t.Fatalf("bootstrap: %d", r.StatusCode)
	}
	if r := do("GET", "/platform/console/capabilities?scope=environment&name=demo", nil); r.StatusCode != 200 {
		t.Fatalf("capabilities environment: %d", r.StatusCode)
	}
	if r := do("GET", "/platform/console/capabilities?scope=personal", nil); r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("capabilities personal: %d %s", r.StatusCode, b)
	}
	// Mutations need fetch metadata (D-28).
	if r := do("POST", "/platform/keys", nil); r.StatusCode != 403 {
		t.Fatalf("POST without Sec-Fetch-Site: %d, want 403", r.StatusCode)
	}
	if r := do("POST", "/platform/keys", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.StatusCode != 403 {
		t.Fatalf("cross-site POST: %d, want 403", r.StatusCode)
	}
	if r := do("POST", "/platform/keys", map[string]string{"Sec-Fetch-Site": "same-origin"}); r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("same-origin POST: %d %s", r.StatusCode, b)
	}
	// Both credentials at once: rejected (§5.3).
	if r := do("GET", "/platform/keys", map[string]string{"x-ach-key": mustAcquirePk(t)}); r.StatusCode != 400 {
		t.Fatalf("cookie + x-ach-key: %d, want 400", r.StatusCode)
	}
	// Logout kills the web session but not the CLI's refresh chain (AC-02).
	cli := oauthLogin(t, base, "")
	if r := do("POST", "/platform/console/session/logout", map[string]string{"Sec-Fetch-Site": "same-origin"}); r.StatusCode != 204 {
		t.Fatalf("logout: %d", r.StatusCode)
	}
	if r := do("GET", "/platform/keys", nil); r.StatusCode != 401 {
		t.Fatalf("after logout: %d, want 401", r.StatusCode)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cli.Refresh}, "client_id": {cli.ClientID}}
	resp, err := http.Post(base+"/platform/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("CLI refresh after console logout: %d", resp.StatusCode)
	}
	// D-18 / AC-24 through the public origin: nothing of LiteLLM's own
	// surface, and "/" is the console (or its not-built notice), never LiteLLM.
	for _, p := range []string{"/ui", "/ui/", "/key/list", "/health", "/model/info", "/v2/anything"} {
		resp, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("%s reachable through ACH: %d %s", p, resp.StatusCode, truncate(b, 200))
		}
	}
	resp, err = http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(strings.ToLower(string(b)), "litellm") {
		t.Fatalf("/ is LiteLLM: %s", truncate(b, 200))
	}
}

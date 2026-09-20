//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// deviceAuthz is the RFC 8628 §3.2 response.
type deviceAuthz struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Complete        string `json:"verification_uri_complete"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// approveDeviceCode plays the user on "another device": opens the
// verification page, confirms the code, and lets the Dex mock sign in —
// every hop rewritten to base like followToLoopback does.
func approveDeviceCode(t *testing.T, base, userCode string) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page, err := client.Get(base + "/platform/oauth/device?user_code=" + url.QueryEscape(userCode))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(string(body), `value="`+userCode+`"`) {
		t.Fatalf("verification page: %d %s", page.StatusCode, truncate(body, 400))
	}
	resp, err := client.Post(base+"/platform/oauth/device", "application/x-www-form-urlencoded",
		strings.NewReader(url.Values{"user_code": {userCode}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("confirm: %d", resp.StatusCode)
	}
	cookies := map[string]string{}
	for _, c := range resp.Cookies() {
		cookies[c.Name] = c.Value
	}
	baseURL, _ := url.Parse(base)
	next := resp.Header.Get("Location")
	for hop := 0; hop < 12; hop++ {
		u, _ := url.Parse(next)
		u.Scheme, u.Host = baseURL.Scheme, baseURL.Host
		req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		req.Header.Set("Cookie", encodeCookieMap(cookies))
		r, err := client.Do(req)
		if err != nil {
			t.Fatalf("hop %d %s: %v", hop, u, err)
		}
		for _, c := range r.Cookies() {
			cookies[c.Name] = c.Value
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.StatusCode/100 == 3 {
			next = r.Header.Get("Location")
			continue
		}
		if r.StatusCode != 200 || !strings.Contains(string(b), "Return to your terminal") {
			t.Fatalf("hop %d %s: %d %s", hop, u, r.StatusCode, truncate(b, 400))
		}
		return
	}
	t.Fatal("device approval never reached the as-callback page")
}

// deviceGrant drives the RFC 8628 grant against one release's AS without
// the CLI: device_authorization → (poll: authorization_pending) → approve
// in the "browser" → poll: token. Returns the access token.
func deviceGrant(t *testing.T, base string) string {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	regBody := `{"client_name":"e2e-device","redirect_uris":["http://127.0.0.1/callback"]}`
	regResp, err := client.Post(base+"/platform/oauth/register", "application/json", strings.NewReader(regBody))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(regResp.Body).Decode(&reg)
	_ = regResp.Body.Close()

	daResp, err := client.Post(base+"/platform/oauth/device_authorization", "application/x-www-form-urlencoded",
		strings.NewReader(url.Values{"client_id": {reg.ClientID}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var da deviceAuthz
	_ = json.NewDecoder(daResp.Body).Decode(&da)
	_ = daResp.Body.Close()
	if daResp.StatusCode != 200 || da.DeviceCode == "" || da.VerificationURI != base+"/platform/oauth/device" ||
		da.Complete != da.VerificationURI+"?user_code="+da.UserCode || da.Interval < 1 {
		t.Fatalf("device_authorization: %d %+v", daResp.StatusCode, da)
	}

	tokenForm := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {da.DeviceCode}, "client_id": {reg.ClientID}}
	poll := func() (int, map[string]any) {
		r, err := client.Post(base+"/platform/oauth/token", "application/x-www-form-urlencoded",
			strings.NewReader(tokenForm.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		return r.StatusCode, body
	}
	if code, body := poll(); code != 400 || body["error"] != "authorization_pending" {
		t.Fatalf("poll before approval: %d %v", code, body)
	}
	approveDeviceCode(t, base, da.UserCode)
	code, body := poll()
	access, _ := body["access_token"].(string)
	if code != 200 || access == "" || body["refresh_token"] == "" {
		t.Fatalf("poll after approval: %d %v", code, body)
	}
	if code, body := poll(); code != 400 || body["error"] != "expired_token" {
		t.Fatalf("device_code must be single-redemption: %d %v", code, body)
	}
	return access
}

// TestDeviceGrant: the headless login on the full release, then the token
// used like any other on the authenticated surface; a wrong code is
// refused by the page.
func TestDeviceGrant(t *testing.T) {
	phase6SuiteGuard(t)
	base := phase6PlatformAPIURL(t)

	access := deviceGrant(t, base)
	req, _ := http.NewRequest(http.MethodGet, base+"/platform/whoami", nil)
	req.Header.Set("x-ach-key", access)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("kilgore@kilgore.trout")) {
		t.Fatalf("whoami with the device-grant token: %d %s", resp.StatusCode, truncate(body, 300))
	}

	t.Run("wrong_code", func(t *testing.T) {
		resp, err := http.Post(base+"/platform/oauth/device", "application/x-www-form-urlencoded",
			strings.NewReader("user_code=ZZZZ-ZZZZ"))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), "not found") {
			t.Fatalf("wrong code: %d %s", resp.StatusCode, truncate(b, 300))
		}
	})

	// The real binary, headless: `login --no-browser` prints the code, we
	// approve it, the CLI stores the pair and `whoami --verify` works.
	t.Run("ach-cli_login_no_browser", func(t *testing.T) {
		xdg := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, phase6BinaryPath, "login", "--profile", "demo", "--base-url", base, "--no-browser")
		cmd.Env = append(cleanEnv(os.Environ()), "XDG_CONFIG_HOME="+xdg, "ACH_INSECURE=1")
		// stdout goes to a file: the process writes while the test reads.
		outPath := filepath.Join(t.TempDir(), "stdout")
		outFile, err := os.Create(outPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = outFile.Close() }()
		var stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = outFile, &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		codeRe := regexp.MustCompile(`\b[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}\b`)
		var userCode string
		for i := 0; i < 100 && userCode == ""; i++ {
			out, _ := os.ReadFile(outPath)
			userCode = codeRe.FindString(string(out))
			time.Sleep(100 * time.Millisecond)
		}
		if userCode == "" {
			_ = cmd.Process.Kill()
			out, _ := os.ReadFile(outPath)
			t.Fatalf("no user code printed within 10s\nstdout=%s\nstderr=%s", out, stderr.String())
		}
		approveDeviceCode(t, base, userCode)
		waitErr := cmd.Wait()
		out, _ := os.ReadFile(outPath)
		if waitErr != nil || !strings.Contains(string(out), `Logged in (profile "demo")`) {
			t.Fatalf("login exit: %v\nstdout=%s\nstderr=%s", waitErr, out, stderr.String())
		}
		out, errb, err := phase6RunAch(t, xdg, "whoami", "--verify")
		if err != nil || !bytes.Contains(out, []byte("Key: OAuth session")) {
			t.Fatalf("whoami after device login: err=%v\nstdout=%s\nstderr=%s", err, out, errb)
		}
	})
}

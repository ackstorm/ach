// SPDX-License-Identifier: Apache-2.0

// Package oauthlogin is ach-cli's OAuth 2.1 public client: RFC 8414
// discovery, one-time DCR (the client id is cached on the profile),
// authorization-code + PKCE S256 with a loopback redirect on a random port
// (RFC 8252 §7.3 — the AS registered one port and matches any), the RFC
// 8628 device grant for a host with no browser, and refresh. Stdlib only.
// The browser opener is a seam.
package oauthlogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
)

// Opener launches the system browser. Tests replace it.
var Opener = openInBrowser

// openInBrowser dispatches to the platform's URL-open command and does not
// wait for it.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("no browser opener for GOOS %q", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// HTTPClient is the seam for tests; nil → a 30s stdlib client.
var HTTPClient *http.Client

// Client is one ACH base URL (= the issuer).
type Client struct {
	BaseURL      string
	LoginTimeout time.Duration // wait for the browser; zero → 5 min
}

type metadata struct {
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RegistrationEndpoint        string `json:"registration_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func (c *Client) http() *http.Client {
	if HTTPClient != nil {
		return HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) discover(ctx context.Context) (*metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery: %s returned %d", req.URL, resp.StatusCode)
	}
	var m metadata
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil || m.TokenEndpoint == "" || m.AuthorizationEndpoint == "" {
		return nil, errors.New("discovery: metadata has no endpoints")
	}
	return &m, nil
}

func (c *Client) register(ctx context.Context, m *metadata, redirectURI string) (string, error) {
	if m.RegistrationEndpoint == "" {
		return "", errors.New("the authorization server does not offer dynamic client registration")
	}
	body, _ := json.Marshal(map[string]any{"client_name": "ach-cli", "redirect_uris": []string{redirectURI}, "token_endpoint_auth_method": "none"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ClientID == "" {
		return "", fmt.Errorf("registration: status %d, no client_id", resp.StatusCode)
	}
	return out.ClientID, nil
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Login runs the full ceremony. clientID may be empty (first login on this
// profile) — the returned creds carry the id to cache.
func (c *Client) Login(ctx context.Context, clientID string) (*config.OAuthCreds, error) {
	return c.LoginForResource(ctx, clientID, "", nil)
}

// LoginForResource runs loopback login with the RFC 8707 resource that selects
// a backend's provider consent policy on ACH. prompt opens the authorization
// URL or returns a callback URL pasted by the user.
func (c *Client) LoginForResource(ctx context.Context, clientID, resource string,
	prompt func(context.Context, string) (string, error),
) (*config.OAuthCreds, error) {
	m, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = ln.Close() }()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	if clientID == "" {
		if clientID, err = c.register(ctx, m, redirectURI); err != nil {
			return nil, err
		}
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	state, err := randomURLSafe(16)
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"scope": {"offline_access"},
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	authorizeURL := m.AuthorizationEndpoint + "?" + q.Encode()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		code, err := callbackCode("http://"+r.Host+r.URL.RequestURI(), redirectURI, state)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, callbackPage, "err", "m9.5 9.5 5 5m0-5-5 5", "Sign-in failed", "Nothing was granted. Return to your terminal for details.")
		} else {
			_, _ = fmt.Fprintf(w, callbackPage, "", "m9 12 2 2 4-4", "Signed in to ACH", "Return to your terminal — you can close this tab.")
		}
		select {
		case done <- result{code: code, err: err}:
		default: // a second hit after the first result: ignore
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	timeout := c.LoginTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var pasted, code string
	if prompt != nil {
		pasted, err = prompt(wait, authorizeURL)
	} else if err = Opener(authorizeURL); err != nil {
		err = fmt.Errorf("open browser: %w", err)
	}
	if err != nil {
		return nil, err
	}
	if wait.Err() != nil {
		return nil, fmt.Errorf("waiting for the browser callback: %w", wait.Err())
	}
	if pasted != "" {
		code, err = callbackCode(pasted, redirectURI, state)
		if err != nil {
			return nil, err
		}
	} else {
		select {
		case r := <-done:
			if r.err != nil {
				return nil, r.err
			}
			code = r.code
		case <-wait.Done():
			return nil, fmt.Errorf("waiting for the browser callback: %w", wait.Err())
		}
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirectURI}, "code_verifier": {verifier},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	creds, err := c.exchange(ctx, m.TokenEndpoint, form)
	if err != nil {
		return nil, err
	}
	creds.ClientID = clientID
	return creds, nil
}

func callbackCode(raw, redirectURI, state string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Scheme+"://"+u.Host+u.EscapedPath() != redirectURI {
		return "", errors.New("callback URL does not match this sign-in's loopback address")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["state"]) != 1 || q.Get("state") != state {
		return "", errors.New("state mismatch on the loopback redirect")
	}
	if q.Has("error") {
		return "", errors.New("authorization was declined or failed")
	}
	if len(q["code"]) != 1 || q.Get("code") == "" {
		return "", errors.New("callback is missing a single authorization code")
	}
	return q.Get("code"), nil
}

// deviceGrantType is the RFC 8628 grant_type on /token.
// callbackPage is the loopback redirect's browser page; its colours are the
// device page's (internal/platformapi/auth/page.html). Args: main class
// ("err" or ""), shield mark path, title, subtitle.
const callbackPage = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ACH — %[3]s</title>
<style>
:root { color-scheme: dark; --bg: oklch(0.14 0.005 260); --card: oklch(0.18 0.008 260); --border: oklch(0.28 0.01 260); --text: oklch(0.93 0.005 260); --muted: oklch(0.65 0.01 260); --accent: oklch(0.72 0.19 150); }
@media (prefers-color-scheme: light) { :root { color-scheme: light; --bg: oklch(0.985 0.004 260); --card: oklch(1 0 0); --border: oklch(0.9 0.008 260); --text: oklch(0.22 0.015 260); --muted: oklch(0.44 0.012 260); --accent: oklch(0.68 0.19 150); } }
.err { --accent: oklch(0.6 0.2 25); }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--text); font: 14px/1.6 system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif; }
main { width: min(420px, calc(100%% - 48px)); padding: 32px; background: var(--card); border: 1px solid var(--border); border-radius: 12px; }
.brand { display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 18px; }
svg { width: 22px; height: 22px; fill: none; stroke: var(--accent); stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
h1 { margin: 20px 0 4px; font-size: 22px; font-weight: 600; }
p { margin: 0; color: var(--muted); }
</style></head>
<body><main class="%[1]s"><div class="brand"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2 4 6v6c0 4.5 3.4 7.3 8 10 4.6-2.7 8-5.5 8-10V6z"/><path d="%[2]s"/></svg>ACH</div><h1>%[3]s</h1><p>%[4]s</p></main></body></html>
`

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// DeviceLogin runs the RFC 8628 device grant for a host with no browser:
// device_authorization → show(code, uri) → poll /token every interval
// until a token, a terminal error, or expires_in. The registered redirect
// is a loopback placeholder — the device grant never uses it, but DCR
// needs one and the same client id then serves both flows.
func (c *Client) DeviceLogin(ctx context.Context, clientID, resource string, show func(userCode, verificationURI string, prefilled bool)) (*config.OAuthCreds, error) {
	m, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	if m.DeviceAuthorizationEndpoint == "" {
		return nil, errors.New("the authorization server does not offer the device grant")
	}
	if clientID == "" {
		if clientID, err = c.register(ctx, m, "http://127.0.0.1/callback"); err != nil {
			return nil, err
		}
	}
	form := url.Values{"client_id": {clientID}}
	if resource != "" {
		form.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.DeviceAuthorizationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	var da struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Complete        string `json:"verification_uri_complete"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&da)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || da.DeviceCode == "" {
		return nil, fmt.Errorf("device authorization: %s (status %d)", da.Error, resp.StatusCode)
	}
	// RFC 8628 §3.3.1: the complete URI pre-fills the code; the code is still
	// shown so the user can confirm the page matches this terminal.
	uri := da.VerificationURI
	if da.Complete != "" {
		uri = da.Complete
	}
	show(da.UserCode, uri, da.Complete != "")

	if da.Interval <= 0 {
		da.Interval = 5 // RFC 8628 §3.2: absent → 5 seconds
	}
	interval := time.Duration(da.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(da.ExpiresIn) * time.Second)
	form.Set("grant_type", deviceGrantType)
	form.Set("device_code", da.DeviceCode)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		creds, err := c.exchange(ctx, m.TokenEndpoint, form)
		if err == nil {
			creds.ClientID = clientID
			return creds, nil
		}
		switch {
		case strings.Contains(err.Error(), "authorization_pending"):
		case strings.Contains(err.Error(), "slow_down"):
			interval += 5 * time.Second // RFC 8628 §3.5
		default:
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the code expired before you signed in")
		}
	}
}

// Refresh rotates the pair. An invalid_grant means the refresh token is dead:
// the caller falls back to Login.
func (c *Client) Refresh(ctx context.Context, clientID, refreshToken string) (*config.OAuthCreds, error) {
	m, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := c.exchange(ctx, m.TokenEndpoint, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {clientID},
	})
	if err != nil {
		return nil, err
	}
	creds.ClientID = clientID
	return creds, nil
}

func (c *Client) exchange(ctx context.Context, tokenEndpoint string, form url.Values) (*config.OAuthCreds, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var tr tokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.Error == "" {
			tr.Error = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("token endpoint: %s", tr.Error)
	}
	return &config.OAuthCreds{
		AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// RefreshSkew: refresh when this close to expiry, so a token handed to a
// tool (apiKeyHelper caches 5 min) is not already dead when it is used.
const RefreshSkew = 6 * time.Minute

// CurrentAccessToken returns a usable access token, refreshing first when
// the stored one is within RefreshSkew of expiry. updated is non-nil only
// when a refresh happened — the caller persists it.
func (c *Client) CurrentAccessToken(ctx context.Context, creds *config.OAuthCreds) (string, *config.OAuthCreds, error) {
	if time.Until(creds.ExpiresAt) > RefreshSkew {
		return creds.AccessToken, nil, nil
	}
	fresh, err := c.Refresh(ctx, creds.ClientID, creds.RefreshToken)
	if err != nil {
		return "", nil, err
	}
	return fresh.AccessToken, fresh, nil
}

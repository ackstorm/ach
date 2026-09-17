// SPDX-License-Identifier: Apache-2.0

// Package oauthlogin is ach-cli's OAuth 2.1 public client: RFC 8414
// discovery, one-time DCR (the client id is cached on the profile),
// authorization-code + PKCE S256 with a loopback redirect on a random port
// (RFC 8252 §7.3 — the AS registered one port and matches any), and refresh.
// Stdlib only, like devicecode. The browser opener is a seam.
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
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/devicecode"
)

// Opener launches the system browser. Tests replace it.
var Opener = devicecode.Opener

// HTTPClient is the seam for tests; nil → a 30s stdlib client.
var HTTPClient *http.Client

// Client is one ACH base URL (= the issuer).
type Client struct {
	BaseURL      string
	LoginTimeout time.Duration // wait for the browser; zero → 5 min
}

type metadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	m, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
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
		rq := r.URL.Query()
		var res result
		switch {
		case rq.Get("error") != "":
			_, _ = fmt.Fprintf(w, "<h1>Login failed</h1><p>%s</p>", rq.Get("error"))
			res.err = fmt.Errorf("authorization server returned %s", rq.Get("error"))
		case rq.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			res.err = errors.New("state mismatch on the loopback redirect")
		default:
			_, _ = fmt.Fprint(w, "<h1>Signed in to ACH</h1><p>You can close this tab.</p>")
			res.code = rq.Get("code")
		}
		select {
		case done <- res:
		default: // a second hit after the first result: ignore
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if err := Opener(authorizeURL); err != nil {
		return nil, fmt.Errorf("open browser: %w (open this URL yourself: %s)", err, authorizeURL)
	}
	timeout := c.LoginTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	var code string
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		code = r.code
	case <-time.After(timeout):
		return nil, errors.New("timed out waiting for the browser")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	creds, err := c.exchange(ctx, m.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirectURI}, "code_verifier": {verifier},
	})
	if err != nil {
		return nil, err
	}
	creds.ClientID = clientID
	return creds, nil
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
	defer resp.Body.Close()
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

// refreshSkew: refresh when this close to expiry, so a token handed to a
// tool (apiKeyHelper caches 5 min) is not already dead when it is used.
const refreshSkew = 6 * time.Minute

// CurrentAccessToken returns a usable access token, refreshing first when
// the stored one is within refreshSkew of expiry. updated is non-nil only
// when a refresh happened — the caller persists it.
func (c *Client) CurrentAccessToken(ctx context.Context, creds *config.OAuthCreds) (string, *config.OAuthCreds, error) {
	if time.Until(creds.ExpiresAt) > refreshSkew {
		return creds.AccessToken, nil, nil
	}
	fresh, err := c.Refresh(ctx, creds.ClientID, creds.RefreshToken)
	if err != nil {
		return "", nil, err
	}
	return fresh.AccessToken, fresh, nil
}

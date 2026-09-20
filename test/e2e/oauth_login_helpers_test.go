//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// userCreds is what a login leaves on an ach-cli profile: the OAuth token
// pair for one Hub. Access resolves to the user's purpose='oauth' pk_ row
// wherever a pk_ used to be presented (x-ach-key, Authorization: Bearer).
type userCreds struct {
	ClientID, Access, Refresh string
	ExpiresAt                 time.Time
}

// oauthLogin runs the authorization-code ceremony against a release's AS
// (DCR → /authorize → Dex mock → as-callback → loopback code → /token),
// rewriting every redirect hop to base like a browser on the single e2e
// origin would. resource, when set, is sent on /authorize (RFC 8707).
func oauthLogin(t *testing.T, base, resource string) userCreds {
	t.Helper()
	noRedirect := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	regBody, _ := json.Marshal(map[string]any{"client_name": "e2e", "redirect_uris": []string{"http://127.0.0.1:1/cb"}})
	resp, err := noRedirect.Post(base+"/platform/oauth/register", "application/json", strings.NewReader(string(regBody)))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || reg.ClientID == "" {
		t.Fatalf("oauthLogin register on %s: %d %+v", base, resp.StatusCode, reg)
	}
	verifier := "e2e-verifier-" + strings.Repeat("y", 40)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {"http://127.0.0.1:1/cb"},
		"state": {"s1"}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	authCode := followToLoopback(t, noRedirect, base, base+"/platform/oauth/authorize?"+q.Encode())
	if authCode == "" {
		t.Fatalf("oauthLogin on %s: no authorization code", base)
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {authCode}, "client_id": {reg.ClientID},
		"redirect_uri": {"http://127.0.0.1:1/cb"}, "code_verifier": {verifier},
	}
	resp, err = noRedirect.Post(base+"/platform/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	if resp.StatusCode != 200 || tok.Access == "" {
		t.Fatalf("oauthLogin token on %s: %d %+v", base, resp.StatusCode, tok)
	}
	return userCreds{ClientID: reg.ClientID, Access: tok.Access, Refresh: tok.Refresh,
		ExpiresAt: time.Now().Add(time.Duration(tok.Expires) * time.Second)}
}

// writeCLIConfig stages an ach-cli config.yaml under a temp XDG_CONFIG_HOME
// with `default: demo` and the profile's OAuth block, exactly as `ach-cli
// login` would leave it. Returns the XDG_CONFIG_HOME path.
func writeCLIConfig(t *testing.T, baseURL string, creds userCreds) string {
	t.Helper()
	if baseURL == "" || creds.Access == "" {
		t.Fatalf("writeCLIConfig: baseURL and credentials must be non-empty")
	}
	tmp := t.TempDir()
	achDir := filepath.Join(tmp, "ach")
	if err := os.MkdirAll(achDir, 0o700); err != nil {
		t.Fatalf("writeCLIConfig: mkdir %s: %v", achDir, err)
	}
	contents := fmt.Sprintf(""+
		"default: demo\n"+
		"profiles:\n"+
		"    demo:\n"+
		"        url: %s\n"+
		"        oauth:\n"+
		"            client_id: %s\n"+
		"            access_token: %s\n"+
		"            refresh_token: %s\n"+
		"            expires_at: %s\n",
		baseURL, creds.ClientID, creds.Access, creds.Refresh, creds.ExpiresAt.UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(achDir, "config.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("writeCLIConfig: %v", err)
	}
	return tmp
}

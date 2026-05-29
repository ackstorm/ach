// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"crypto"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// minimalDeps builds a Deps struct sufficient for LoginHandler tests:
// only OAuth2Cfg is consulted by the login redirect path. The remaining
// fields are populated with zero/nil so the struct compiles; CallbackHandler
// tests later in this file build their own enriched Deps.
func minimalLoginDeps() Deps {
	return Deps{
		OAuth2Cfg: &oauth2.Config{
			ClientID:     "ach-platform-api",
			ClientSecret: "test-secret",
			RedirectURL:  "https://ach.example.com/platform/auth/sso/callback",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://fake-dex.example.com/dex/auth",
				TokenURL: "https://fake-dex.example.com/dex/token",
			},
		},
		Logger: slog.New(slog.NewTextHandler(io_Discard{}, nil)),
		Audit:  slog.New(slog.NewTextHandler(io_Discard{}, nil)),
	}
}

// io_Discard is an io.Writer that discards everything written to it.
// Avoids depending on io.Discard's deprecation cycle across Go versions.
type io_Discard struct{}

func (io_Discard) Write(p []byte) (int, error) { return len(p), nil }

// --- Task 1: cookies.go behavior tests ---

// TestSSOCookieSetShape verifies setSSOCookie writes a Set-Cookie header with
// the __Host- prefixed name, Path=/, HttpOnly, Secure, SameSite=Strict, and a
// 10-minute Max-Age — the invariant the __Host- prefix requires.
func TestSSOCookieSetShape(t *testing.T) {
	w := httptest.NewRecorder()
	setSSOCookie(w, "s1", "v1", false)

	headers := w.Result().Header.Values("Set-Cookie")
	if len(headers) != 1 {
		t.Fatalf("expected exactly 1 Set-Cookie header, got %d: %v", len(headers), headers)
	}
	got := headers[0]

	// __Host- prefix invariants
	if !strings.HasPrefix(got, "__Host-ach_sso=") {
		t.Errorf("expected cookie name __Host-ach_sso, got header: %q", got)
	}
	if !strings.Contains(got, "Path=/") {
		t.Errorf("expected Path=/, got header: %q", got)
	}
	if !strings.Contains(got, "HttpOnly") {
		t.Errorf("expected HttpOnly, got header: %q", got)
	}
	if !strings.Contains(got, "Secure") {
		t.Errorf("expected Secure, got header: %q", got)
	}
	if !strings.Contains(got, "SameSite=Strict") {
		t.Errorf("expected SameSite=Strict, got header: %q", got)
	}
	if !strings.Contains(got, "Max-Age=600") {
		t.Errorf("expected Max-Age=600 (10min TTL), got header: %q", got)
	}
	// __Host- prefix prohibits Domain= attribute.
	if strings.Contains(got, "Domain=") {
		t.Errorf("__Host- prefix prohibits Domain attribute, got header: %q", got)
	}
}

// TestSSOCookieInsecureShape verifies the dev-mode (insecure=true) cookie
// drops the __Host- prefix and the Secure flag — the two attributes that
// make local http://localhost SSO unrecoverable under curl. Path, HttpOnly,
// SameSite, and Max-Age stay identical.
func TestSSOCookieInsecureShape(t *testing.T) {
	w := httptest.NewRecorder()
	setSSOCookie(w, "s1", "v1", true)

	headers := w.Result().Header.Values("Set-Cookie")
	if len(headers) != 1 {
		t.Fatalf("expected exactly 1 Set-Cookie header, got %d: %v", len(headers), headers)
	}
	got := headers[0]
	if !strings.HasPrefix(got, "ach_sso=") {
		t.Errorf("expected cookie name ach_sso, got header: %q", got)
	}
	if strings.HasPrefix(got, "__Host-") {
		t.Errorf("__Host- prefix MUST be absent in insecure mode, got header: %q", got)
	}
	// Secure is a bare attribute (no =value) — match on a word boundary to
	// avoid spurious matches in the value.
	if strings.Contains(got, "; Secure") || strings.HasSuffix(got, "Secure") {
		t.Errorf("Secure MUST be absent in insecure mode, got header: %q", got)
	}
	if !strings.Contains(got, "Path=/") {
		t.Errorf("expected Path=/, got header: %q", got)
	}
	if !strings.Contains(got, "HttpOnly") {
		t.Errorf("expected HttpOnly, got header: %q", got)
	}
	if !strings.Contains(got, "SameSite=Strict") {
		t.Errorf("expected SameSite=Strict, got header: %q", got)
	}
	if !strings.Contains(got, "Max-Age=600") {
		t.Errorf("expected Max-Age=600 (10min TTL), got header: %q", got)
	}
}

// TestSSOCookieInsecureRoundTrip verifies that the insecure-mode cookie
// also round-trips state+verifier correctly under setSSOCookie(..., true)
// + readSSOCookie(req, true).
func TestSSOCookieInsecureRoundTrip(t *testing.T) {
	w := httptest.NewRecorder()
	setSSOCookie(w, "state-insec", "verifier-insec", true)

	cookie := w.Result().Cookies()[0]
	if cookie.Name != cookieNameInsecure {
		t.Fatalf("insecure cookie name: got %q, want %q", cookie.Name, cookieNameInsecure)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	state, verifier, err := readSSOCookie(req, true)
	if err != nil {
		t.Fatalf("readSSOCookie(req, true): unexpected error: %v", err)
	}
	if state != "state-insec" || verifier != "verifier-insec" {
		t.Errorf("round-trip: got (%q, %q), want (state-insec, verifier-insec)", state, verifier)
	}

	// readSSOCookie(req, false) MUST NOT find the insecure-named cookie —
	// the name selector is what decides which cookie applies in each mode.
	if _, _, err := readSSOCookie(req, false); !errors.Is(err, ErrCookieMissing) {
		t.Errorf("readSSOCookie(req, false) on insecure cookie: got %v, want ErrCookieMissing", err)
	}
}

// TestSSOCookieRoundTrip verifies that setSSOCookie + readSSOCookie round-trip
// the (state, verifier) pair faithfully.
func TestSSOCookieRoundTrip(t *testing.T) {
	w := httptest.NewRecorder()
	setSSOCookie(w, "state-value-123", "verifier-value-XYZ_with-special.chars", false)

	// Construct a request carrying the cookie that was just set.
	cookie := w.Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	state, verifier, err := readSSOCookie(req, false)
	if err != nil {
		t.Fatalf("readSSOCookie: unexpected error: %v", err)
	}
	if state != "state-value-123" {
		t.Errorf("state: got %q, want %q", state, "state-value-123")
	}
	if verifier != "verifier-value-XYZ_with-special.chars" {
		t.Errorf("verifier: got %q, want %q", verifier, "verifier-value-XYZ_with-special.chars")
	}
}

// TestSSOCookieMissing verifies that readSSOCookie returns ErrCookieMissing
// when no Cookie header is present on the request.
func TestSSOCookieMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	state, verifier, err := readSSOCookie(req, false)
	if !errors.Is(err, ErrCookieMissing) {
		t.Errorf("err: got %v, want ErrCookieMissing", err)
	}
	if state != "" || verifier != "" {
		t.Errorf("expected empty state+verifier on missing cookie, got %q / %q", state, verifier)
	}
}

// TestSSOCookieMalformed verifies that readSSOCookie returns ErrCookieMalformed
// on a payload that fails base64 decode OR fails the "|"-split count check.
func TestSSOCookieMalformed(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "not-base64", value: "not-base64-!@#$%"},
		{name: "no-separator", value: "YWJjZGVm"},            // base64("abcdef") — no "|" separator
		{name: "too-many-separators", value: "c3wxfHZ8eHl6"}, // base64("s1|v|xyz") — splits to 3 parts
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.AddCookie(&http.Cookie{Name: cookieNameSecure, Value: tc.value})
			state, verifier, err := readSSOCookie(req, false)
			if !errors.Is(err, ErrCookieMalformed) {
				t.Errorf("err: got %v, want ErrCookieMalformed", err)
			}
			if state != "" || verifier != "" {
				t.Errorf("expected empty state+verifier on malformed cookie, got %q / %q", state, verifier)
			}
		})
	}
}

// TestSSOCookieClear verifies that clearSSOCookie writes a Set-Cookie header
// with Max-Age=0 (deletion semantics) preserving the security attributes.
func TestSSOCookieClear(t *testing.T) {
	w := httptest.NewRecorder()
	clearSSOCookie(w, false)

	headers := w.Result().Header.Values("Set-Cookie")
	if len(headers) != 1 {
		t.Fatalf("expected exactly 1 Set-Cookie header, got %d: %v", len(headers), headers)
	}
	got := headers[0]
	if !strings.HasPrefix(got, "__Host-ach_sso=") {
		t.Errorf("expected cookie name __Host-ach_sso, got header: %q", got)
	}
	if !strings.Contains(got, "Path=/") {
		t.Errorf("expected Path=/, got header: %q", got)
	}
	if !strings.Contains(got, "HttpOnly") {
		t.Errorf("expected HttpOnly on clear, got header: %q", got)
	}
	if !strings.Contains(got, "Secure") {
		t.Errorf("expected Secure on clear, got header: %q", got)
	}
	if !strings.Contains(got, "SameSite=Strict") {
		t.Errorf("expected SameSite=Strict on clear, got header: %q", got)
	}
	// Max-Age=0 OR Max-Age=-1 both work as delete signals; Go's http.SetCookie
	// emits Max-Age=0 for MaxAge<0, so we accept either literal form.
	if !strings.Contains(got, "Max-Age=0") && !strings.Contains(got, "Max-Age=-1") {
		t.Errorf("expected Max-Age=0 or -1 on clear, got header: %q", got)
	}
}

// --- Task 2: LoginHandler behavior tests ---

// extractLocationParam pulls a query-string parameter from the redirect URL
// emitted by LoginHandler. Helper used by Tests 1, 3, 4.
func extractLocationParam(t *testing.T, location, key string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location %q: %v", location, err)
	}
	return u.Query().Get(key)
}

// TestLoginHandlerHappyPath verifies the redirect URL contains the PKCE
// challenge_method=S256 and a non-empty code_challenge parameter, and that
// the response is a 302 Found.
func TestLoginHandlerHappyPath(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/platform/auth/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status: got %d, want %d (302)", resp.StatusCode, http.StatusFound)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatalf("Location header missing")
	}
	if !strings.HasPrefix(loc, deps.OAuth2Cfg.Endpoint.AuthURL) {
		t.Errorf("Location prefix: got %q, want prefix %q", loc, deps.OAuth2Cfg.Endpoint.AuthURL)
	}
	method := extractLocationParam(t, loc, "code_challenge_method")
	if method != "S256" {
		t.Errorf("code_challenge_method: got %q, want S256", method)
	}
	challenge := extractLocationParam(t, loc, "code_challenge")
	if challenge == "" {
		t.Errorf("code_challenge: empty")
	}
}

// TestLoginHandlerCookieSet verifies LoginHandler writes the __Host-ach_sso
// cookie with the canonical security attributes (Path=/, HttpOnly, Secure,
// SameSite=Strict, Max-Age=600).
func TestLoginHandlerCookieSet(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/platform/auth/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected at least one Set-Cookie, got none")
	}
	var ssoCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == cookieNameSecure {
			ssoCookie = c
		}
	}
	if ssoCookie == nil {
		t.Fatalf("expected %s cookie, got: %v", cookieNameSecure, cookies)
	}
	if ssoCookie.Path != "/" {
		t.Errorf("Path: got %q, want /", ssoCookie.Path)
	}
	if !ssoCookie.HttpOnly {
		t.Errorf("HttpOnly: false, want true")
	}
	if !ssoCookie.Secure {
		t.Errorf("Secure: false, want true")
	}
	if ssoCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite: got %v, want Strict", ssoCookie.SameSite)
	}
	if ssoCookie.MaxAge != int(cookieTTL.Seconds()) {
		t.Errorf("MaxAge: got %d, want %d", ssoCookie.MaxAge, int(cookieTTL.Seconds()))
	}
}

// TestLoginHandlerStateRandomness verifies that two consecutive calls produce
// different state values in the redirect URL — i.e., state derivation is
// cryptographically random, not deterministic.
func TestLoginHandlerStateRandomness(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	loc1 := mustInvoke(t, h)
	loc2 := mustInvoke(t, h)

	s1 := extractLocationParam(t, loc1, "state")
	s2 := extractLocationParam(t, loc2, "state")
	if s1 == "" || s2 == "" {
		t.Fatalf("state empty: s1=%q s2=%q", s1, s2)
	}
	if s1 == s2 {
		t.Errorf("state not random: both calls returned %q", s1)
	}
}

// TestLoginHandlerVerifierRandomness verifies code_challenge varies across
// calls (challenge is SHA-256 of a random verifier, so non-random verifier
// would produce identical challenge).
func TestLoginHandlerVerifierRandomness(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	loc1 := mustInvoke(t, h)
	loc2 := mustInvoke(t, h)

	c1 := extractLocationParam(t, loc1, "code_challenge")
	c2 := extractLocationParam(t, loc2, "code_challenge")
	if c1 == "" || c2 == "" {
		t.Fatalf("code_challenge empty: c1=%q c2=%q", c1, c2)
	}
	if c1 == c2 {
		t.Errorf("code_challenge not random: both calls returned %q", c1)
	}
}

// TestLoginHandlerCookiePayloadFormat verifies the cookie payload decodes
// to state + "|" + verifier (the format readSSOCookie consumes in Task 3).
func TestLoginHandlerCookiePayloadFormat(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/platform/auth/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var ssoCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameSecure {
			ssoCookie = c
		}
	}
	if ssoCookie == nil {
		t.Fatalf("%s cookie missing", cookieNameSecure)
	}
	raw, err := base64.URLEncoding.DecodeString(ssoCookie.Value)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	parts := strings.Split(string(raw), cookieSeparator)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts separated by %q, got %d: %v", cookieSeparator, len(parts), parts)
	}
	urlState := extractLocationParam(t, w.Result().Header.Get("Location"), "state")
	if parts[0] != urlState {
		t.Errorf("cookie state %q != URL state %q", parts[0], urlState)
	}
	if parts[1] == "" {
		t.Errorf("verifier is empty in cookie payload")
	}
}

// mustInvoke executes the handler with a fresh request and returns the
// Location response header value.
func mustInvoke(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/platform/auth/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	loc := w.Result().Header.Get("Location")
	if loc == "" {
		t.Fatalf("Location header missing on invocation")
	}
	return loc
}

// --- Task 3: CallbackHandler behavior tests ---
//
// Strategy: instead of mocking the IDTokenVerifier interface (real
// *oidc.IDToken cannot be constructed externally — its fields are
// unexported), we stand up a self-contained in-memory OIDC issuer per
// test via realOIDC. The issuer generates an RSA key, exposes the
// /.well-known/openid-configuration + /keys + /token endpoints, and
// signs a real RS256 ID token CallbackHandler can verify end-to-end.
// For the negative case (signature/verify failure) we substitute an
// errOnlyVerifier directly on the Deps struct.

// realOIDC stands up a self-contained OIDC issuer for unit tests:
// generates an RSA key pair, exposes /.well-known/openid-configuration
// and /keys, signs ID tokens with the email claim, and provides an
// oidc.Provider against this issuer that CallbackHandler can verify
// against.
//
// Test pattern:
//
//	fix := newRealOIDC(t, "alice@example.com")
//	defer fix.Close()
//	deps := callbackDeps(t, fix, fakeLiteLLM, …)
//	// ... drive CallbackHandler via httptest.NewRecorder ...
type realOIDC struct {
	server   *httptest.Server
	priv     *rsa.PrivateKey
	jwks     []byte
	issuer   string
	clientID string
	provider *oidc.Provider
	cfg      *oauth2.Config

	// Per-call switches set before invoking the SUT.
	tokenStatus int    // 200 by default; set to 500 to trigger Exchange error
	idEmail     string // sub of issued id token
	noIDToken   bool   // drop id_token from /token response
	noEmail     bool   // sign id_token without email claim
}

// newRealOIDC builds the OIDC fixture and returns a ready-to-use struct
// with an oauth2.Config wired to its /token endpoint.
func newRealOIDC(t *testing.T, clientID string) *realOIDC {
	t.Helper()
	priv, err := rsa.GenerateKey(crand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	mux := http.NewServeMux()
	r := &realOIDC{priv: priv, clientID: clientID, tokenStatus: http.StatusOK, idEmail: "user@example.com"}
	server := httptest.NewServer(mux)
	r.server = server
	r.issuer = server.URL

	// Build JWKS — a single RSA public key with kid="test-key" + RS256.
	jwksJSON := buildJWKS(t, priv)
	r.jwks = jwksJSON

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"issuer": "%s",
			"authorization_endpoint": "%s/auth",
			"token_endpoint": "%s/token",
			"jwks_uri": "%s/keys",
			"id_token_signing_alg_values_supported": ["RS256"]
		}`, r.issuer, r.issuer, r.issuer, r.issuer)))
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, req *http.Request) {
		if r.tokenStatus != http.StatusOK {
			http.Error(w, "token error", r.tokenStatus)
			return
		}
		// Sign id_token with claims based on switches.
		idToken := ""
		if !r.noIDToken {
			emailToSign := r.idEmail
			if r.noEmail {
				emailToSign = ""
			}
			idToken = signIDToken(t, priv, r.issuer, r.clientID, "user-sub-123", emailToSign)
		}
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"access_token": "fake-access",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if idToken != "" {
			body["id_token"] = idToken
		}
		_ = json.NewEncoder(w).Encode(body)
	})

	provider, err := oidc.NewProvider(context.Background(), r.issuer)
	if err != nil {
		server.Close()
		t.Fatalf("oidc.NewProvider: %v", err)
	}
	r.provider = provider
	r.cfg = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "https://ach.example.com/platform/auth/sso/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     provider.Endpoint(),
	}
	return r
}

func (r *realOIDC) Close() {
	if r.server != nil {
		r.server.Close()
	}
}

// buildJWKS produces the JSON representation of a single-key JWKS that
// matches the configured RSA private key. kid="test-key", use="sig",
// alg="RS256".
func buildJWKS(t *testing.T, priv *rsa.PrivateKey) []byte {
	t.Helper()
	pub := &priv.PublicKey
	// JWKS shape: {"keys":[{"kty":"RSA","kid":"test-key","use":"sig","alg":"RS256","n":...,"e":...}]}
	nB := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	eB := base64.RawURLEncoding.EncodeToString(eBytes)
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": "test-key",
				"use": "sig",
				"alg": "RS256",
				"n":   nB,
				"e":   eB,
			},
		},
	}
	b, err := json.Marshal(jwks)
	if err != nil {
		t.Fatalf("buildJWKS marshal: %v", err)
	}
	return b
}

// signIDToken produces a real RS256-signed ID token containing the
// minimum claims go-oidc.Verifier validates: iss, aud, exp, iat, sub,
// plus the email claim CallbackHandler extracts.
func signIDToken(t *testing.T, priv *rsa.PrivateKey, issuer, audience, sub, email string) string {
	t.Helper()
	now := time.Now()
	payload := map[string]any{
		"iss": issuer,
		"aud": audience,
		"sub": sub,
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	}
	if email != "" {
		payload["email"] = email
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(payload)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerB64 + "." + payloadB64

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(crand.Reader, priv, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15: %v", err)
	}
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sigB64
}

// callRecord captures the LiteLLM operations invoked during a single
// CallbackHandler test invocation. The fake LiteLLM client mutates this
// struct.
type callRecord struct {
	userInfoByEmailCalls  int
	userNewCalls          int
	teamMemberAddCalls    int
	keyGenerateCalls      int
	revokeKeyCalls        int
	revokeKeyTokensSeen   []string
	lastUserNewEmail      string
	lastTeamMemberAddTeam string
	lastTeamMemberAddUser string
	lastTeamMemberAddRole string
	lastKeyGenerateKey    string
	lastKeyGenerateUser   string
	lastKeyGenerateBudget *float64
	listTeamsCalls        int
	lastListTeamsAlias    string
}

// fakeLiteLLM is the test client implementing litellm.Client. Per-method
// behaviour is configured by setter funcs the tests assign before
// invoking CallbackHandler. Default behaviour is "first-time user happy
// path": UserInfoByEmail returns ErrNotFound, UserNew succeeds, etc.
type fakeLiteLLM struct {
	rec *callRecord

	// Behaviour switches (defaults match Test 1: first-time SSO happy).
	userInfoBehaviour    func(email string) (*litellm.UserInfo, error)
	userNewBehaviour     func(req *litellm.UserNewRequest) (*litellm.UserInfo, error)
	teamMemberAddError   func(teamID, userID, role string) error
	keyGenerateBehaviour func(req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error)
	revokeKeyError       func(keyID string) error
	listTeamsBehaviour   func(alias string) ([]litellm.TeamListEntry, error)
}

func newFakeLiteLLM() *fakeLiteLLM {
	return &fakeLiteLLM{
		rec: &callRecord{},
		userInfoBehaviour: func(string) (*litellm.UserInfo, error) {
			return nil, litellm.ErrNotFound
		},
		userNewBehaviour: func(req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "litellm-user-" + req.UserEmail, UserEmail: req.UserEmail}, nil
		},
		teamMemberAddError: func(string, string, string) error { return nil },
		keyGenerateBehaviour: func(req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
			return &litellm.KeyGenerateResponse{
				Key:    req.Key,
				Token:  "litellm-token-" + req.UserID,
				UserID: req.UserID,
			}, nil
		},
		revokeKeyError: func(string) error { return nil },
		listTeamsBehaviour: func(string) ([]litellm.TeamListEntry, error) {
			// Match production LiteLLM: alias "default" resolves to a UUID
			// team_id. Tests asserting on team_id should override this.
			return []litellm.TeamListEntry{
				{TeamID: "team-uuid-default", TeamAlias: "default"},
			}, nil
		},
	}
}

func (f *fakeLiteLLM) ListTeamsByAlias(_ context.Context, alias string) ([]litellm.TeamListEntry, error) {
	f.rec.listTeamsCalls++
	f.rec.lastListTeamsAlias = alias
	return f.listTeamsBehaviour(alias)
}

func (f *fakeLiteLLM) EnsureDefaultTeam(_ context.Context) error { return nil }

// Unused-method shims to satisfy the wider litellm.Client interface.
func (f *fakeLiteLLM) DeleteAccessGroup(context.Context, string) error { return nil }
func (f *fakeLiteLLM) DeleteTag(context.Context, string) error         { return nil }
func (f *fakeLiteLLM) ListModels(context.Context) ([]litellm.ModelInfoResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListMCPServers(context.Context) ([]litellm.MCPServerEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListA2AAgents(context.Context) ([]litellm.AgentEntry, error) { return nil, nil }
func (f *fakeLiteLLM) ListUserKeys(context.Context, string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}

func (f *fakeLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	f.rec.userInfoByEmailCalls++
	return f.userInfoBehaviour(email)
}
func (f *fakeLiteLLM) UserNew(_ context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	f.rec.userNewCalls++
	f.rec.lastUserNewEmail = req.UserEmail
	return f.userNewBehaviour(req)
}
func (f *fakeLiteLLM) TeamMemberAdd(_ context.Context, teamID, userID, role string) error {
	f.rec.teamMemberAddCalls++
	f.rec.lastTeamMemberAddTeam = teamID
	f.rec.lastTeamMemberAddUser = userID
	f.rec.lastTeamMemberAddRole = role
	return f.teamMemberAddError(teamID, userID, role)
}
func (f *fakeLiteLLM) KeyGenerate(_ context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	f.rec.keyGenerateCalls++
	f.rec.lastKeyGenerateKey = req.Key
	f.rec.lastKeyGenerateUser = req.UserID
	f.rec.lastKeyGenerateBudget = req.MaxBudget
	return f.keyGenerateBehaviour(req)
}
func (f *fakeLiteLLM) RevokeKey(_ context.Context, keyID string) error {
	f.rec.revokeKeyCalls++
	f.rec.revokeKeyTokensSeen = append(f.rec.revokeKeyTokensSeen, keyID)
	return f.revokeKeyError(keyID)
}

// dbInsertRecord captures the (last) PkInsertRow seen by the seam-injected
// InsertPersonalKey, plus the count + an optional error.
type dbInsertRecord struct {
	calls       int
	lastRow     db.PkInsertRow
	failWith    error
	rowsByKeyID map[string]db.PkInsertRow
}

func newDBInsertRecord() *dbInsertRecord {
	return &dbInsertRecord{rowsByKeyID: map[string]db.PkInsertRow{}}
}

func (d *dbInsertRecord) insertFn(_ context.Context, row db.PkInsertRow) error {
	d.calls++
	d.lastRow = row
	d.rowsByKeyID[row.KeyID] = row
	return d.failWith
}

// callbackTestCase scaffolds a single CallbackHandler test execution.
type callbackTestCase struct {
	name        string
	stateCookie string // "" = no cookie set
	urlState    string // "" = no state= query param
	urlCode     string // "" = no code= query param
	skipCookie  bool   // if true, do not set ANY cookie
	// fixtures
	oidcFix   *realOIDC
	verifyErr error
	litellm   *fakeLiteLLM
	dbInsert  *dbInsertRecord
	pepper    []byte
	// expected
	wantStatus int
	wantCode   string // body.error.code OR substring of body
}

// runCallback drives a fully wired CallbackHandler and returns the
// recorder for assertions.
func runCallback(t *testing.T, tc *callbackTestCase) *httptest.ResponseRecorder {
	t.Helper()

	// Drive LoginHandler first so we get a real cookie with valid
	// (state, verifier) — unless the test wants to drive its own.
	var cookieHeader string
	if !tc.skipCookie && tc.stateCookie != "" {
		// Construct an explicit cookie via setSSOCookie.
		w := httptest.NewRecorder()
		setSSOCookie(w, tc.stateCookie, "verifier-"+tc.stateCookie, false)
		cookieHeader = w.Result().Header.Get("Set-Cookie")
	}

	// Build the Deps.
	auditBuf := &bytes.Buffer{}
	deps := Deps{
		OIDCProvider:    tc.oidcFix.provider,
		IDTokenVerifier: tc.oidcFix.provider.Verifier(&oidc.Config{ClientID: tc.oidcFix.clientID}),
		OAuth2Cfg:       tc.oidcFix.cfg,
		LiteLLM:         tc.litellm,
		Pepper:          tc.pepper,
		Audit:           slog.New(slog.NewJSONHandler(auditBuf, nil)),
		Logger:          slog.New(slog.NewTextHandler(io_Discard{}, nil)),
		Namespace:       "ach-system",
		InsertPKFn:      tc.dbInsert.insertFn,
		NowFn:           func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
	if tc.verifyErr != nil {
		deps.IDTokenVerifier = &errOnlyVerifier{err: tc.verifyErr}
	}

	// Build the callback URL.
	target := "/platform/auth/sso/callback"
	q := url.Values{}
	if tc.urlState != "" {
		q.Set("state", tc.urlState)
	}
	if tc.urlCode != "" {
		q.Set("code", tc.urlCode)
	}
	if len(q) > 0 {
		target = target + "?" + q.Encode()
	}

	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookieHeader != "" {
		req.Header.Add("Cookie", cookieHeader)
	}
	w := httptest.NewRecorder()
	CallbackHandler(deps).ServeHTTP(w, req)

	// Stash audit log in the recorder via header for inspection in tests.
	w.Header().Set("X-Test-Audit", auditBuf.String())
	return w
}

// errOnlyVerifier is the no-op IDTokenVerifier returning a fixed error.
// Used by Test 4 to simulate ID-token verification failure without
// reaching into the realOIDC issuer's signing path.
type errOnlyVerifier struct{ err error }

func (e *errOnlyVerifier) Verify(context.Context, string) (*oidc.IDToken, error) {
	return nil, e.err
}

// Tests below ----------------------------------------------------------

// TestCallbackHandler_FirstTimeSSOHappyPath (behavior 1):
// UserInfoByEmail → 404 → UserNew + TeamMemberAdd succeed → KeyGenerate
// → DB INSERT happy → 200 + JSON body with plaintext/key_id/owner_email.
func TestCallbackHandler_FirstTimeSSOHappyPath(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "alice@example.com"

	flm := newFakeLiteLLM() // defaults to first-time SSO
	dbRec := newDBInsertRecord()
	pepper := []byte("test-pepper-32-bytes-long-aa")

	tc := &callbackTestCase{
		stateCookie: "state-abc",
		urlState:    "state-abc",
		urlCode:     "code-xyz",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      pepper,
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	var got callbackResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body decode: %v; body=%s", err, string(body))
	}
	if !strings.HasPrefix(got.KeyID, "pkid_") {
		t.Errorf("key_id: got %q, want pkid_ prefix", got.KeyID)
	}
	if !strings.HasPrefix(got.Plaintext, "pk_") {
		t.Errorf("plaintext: got %q, want pk_ prefix", got.Plaintext)
	}
	if got.OwnerEmail != "alice@example.com" {
		t.Errorf("owner_email: got %q, want alice@example.com", got.OwnerEmail)
	}

	// LiteLLM call counts.
	if flm.rec.userInfoByEmailCalls != 1 {
		t.Errorf("UserInfoByEmail calls: got %d, want 1", flm.rec.userInfoByEmailCalls)
	}
	if flm.rec.userNewCalls != 1 {
		t.Errorf("UserNew calls: got %d, want 1", flm.rec.userNewCalls)
	}
	if flm.rec.teamMemberAddCalls != 1 {
		t.Errorf("TeamMemberAdd calls: got %d, want 1", flm.rec.teamMemberAddCalls)
	}
	if flm.rec.keyGenerateCalls != 1 {
		t.Errorf("KeyGenerate calls: got %d, want 1", flm.rec.keyGenerateCalls)
	}
	if flm.rec.lastKeyGenerateBudget != nil {
		t.Errorf("KeyGenerate.MaxBudget: got non-nil %v, want nil (KEY-10)", *flm.rec.lastKeyGenerateBudget)
	}
	// FIX01 §A.6: ACH does NOT supply req.Key (LiteLLM mints its own
	// sk-… plaintext). The legacy D-13 echo assertion is obsolete.
	if flm.rec.lastKeyGenerateKey != "" {
		t.Errorf("KeyGenerate.Key: got %q, want empty (FIX01 §A.6: ACH never supplies LiteLLM virtual-key plaintext)",
			flm.rec.lastKeyGenerateKey)
	}

	// DB INSERT row.
	if dbRec.calls != 1 {
		t.Errorf("DB insert calls: got %d, want 1", dbRec.calls)
	}
	// NOTE: PkInsertRow has no Status field — InsertPersonalKey hardcodes
	// status='active' in the SQL (see internal/db/personal_keys.go). This
	// invariant is verified by the integration tests of Plan 03-03; the
	// handler test only confirms the row reaches the insert path.
	if dbRec.lastRow.OwnerEmail != "alice@example.com" {
		t.Errorf("DB OwnerEmail: got %q, want alice@example.com", dbRec.lastRow.OwnerEmail)
	}
	// credential_hash must match HMAC(pepper, plaintext).
	wantHash, _ := credhash.Hash(pepper, []byte(got.Plaintext))
	if dbRec.lastRow.CredentialHash != wantHash {
		t.Errorf("DB CredentialHash mismatch: got %q, want %q", dbRec.lastRow.CredentialHash, wantHash)
	}
	wantExpires := time.Unix(1700000000, 0).UTC().Add(pkExpiryWindow)
	if !dbRec.lastRow.ExpiresAt.Equal(wantExpires) {
		t.Errorf("DB ExpiresAt: got %v, want %v", dbRec.lastRow.ExpiresAt, wantExpires)
	}

	// Cookie cleared on success.
	setCookieHeaders := resp.Header.Values("Set-Cookie")
	foundClear := false
	for _, h := range setCookieHeaders {
		if strings.Contains(h, cookieNameSecure+"=") && (strings.Contains(h, "Max-Age=0") || strings.Contains(h, "Max-Age=-1")) {
			foundClear = true
		}
	}
	if !foundClear {
		t.Errorf("expected cleared %s cookie in response, got: %v", cookieNameSecure, setCookieHeaders)
	}

	// Audit event.
	auditLog := w.Header().Get("X-Test-Audit")
	if !strings.Contains(auditLog, audit.ActionSSOLogin) {
		t.Errorf("audit log missing ActionSSOLogin; got: %s", auditLog)
	}
	if !strings.Contains(auditLog, audit.OutcomeCreated) {
		t.Errorf("audit log missing OutcomeCreated; got: %s", auditLog)
	}
	if !strings.Contains(auditLog, got.KeyID) {
		t.Errorf("audit log missing key.id %q; got: %s", got.KeyID, auditLog)
	}
	// Plaintext MUST NOT appear in audit log.
	if strings.Contains(auditLog, got.Plaintext) {
		t.Errorf("FATAL: plaintext leaked into audit log: %s", auditLog)
	}

	// Plaintext appears EXACTLY ONCE in the full response (headers + body).
	fullResp := string(body) + "\n" + strings.Join(append([]string{}, headersToLines(resp.Header)...), "\n")
	count := strings.Count(fullResp, got.Plaintext)
	if count != 1 {
		t.Errorf("plaintext occurrence in response (headers+body): got %d, want 1; fullResp=%s", count, fullResp)
	}
}

// TestCallbackHandler_ExistingUserSSO (behavior 2): UserInfoByEmail → 200
// → UserNew is NOT called → TeamMemberAdd IS called (idempotent per BLK-05
// sub-point 3 + D-25) → KeyGenerate succeeds → DB INSERT succeeds.
func TestCallbackHandler_ExistingUserSSO(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "bob@example.com"

	flm := newFakeLiteLLM()
	flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
		return &litellm.UserInfo{UserID: "litellm-existing-bob", UserEmail: email}, nil
	}
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-existing",
		urlState:    "state-existing",
		urlCode:     "code-existing",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-existing-32-bytes-aaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	if flm.rec.userNewCalls != 0 {
		t.Errorf("UserNew calls: got %d, want 0 (existing-user path)", flm.rec.userNewCalls)
	}
	if flm.rec.teamMemberAddCalls < 1 {
		t.Errorf("TeamMemberAdd calls: got %d, want >=1 (BLK-05 idempotency)", flm.rec.teamMemberAddCalls)
	}
	if flm.rec.keyGenerateCalls != 1 {
		t.Errorf("KeyGenerate calls: got %d, want 1", flm.rec.keyGenerateCalls)
	}
	if dbRec.calls != 1 {
		t.Errorf("DB insert calls: got %d, want 1", dbRec.calls)
	}
}

// TestCallbackHandler_ExistingUserTeamMemberAddIdempotent (behavior 2b):
// existing user + TeamMemberAdd returns 200 → handler proceeds to
// KeyGenerate; TeamMemberAdd called exactly once with team_id="default",
// role="user".
func TestCallbackHandler_ExistingUserTeamMemberAddIdempotent(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "carol@example.com"

	flm := newFakeLiteLLM()
	flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
		return &litellm.UserInfo{UserID: "litellm-carol", UserEmail: email}, nil
	}
	// TeamMemberAdd already returns nil by default — explicit for clarity.
	flm.teamMemberAddError = func(string, string, string) error { return nil }
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-carol",
		urlState:    "state-carol",
		urlCode:     "code-carol",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-carol-32-bytes-aaaaaaaa"),
	}
	w := runCallback(t, tc)
	if w.Result().StatusCode != http.StatusOK {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("status: got %d, want 200; body=%s", w.Result().StatusCode, string(body))
	}
	if flm.rec.teamMemberAddCalls != 1 {
		t.Errorf("TeamMemberAdd calls: got %d, want 1", flm.rec.teamMemberAddCalls)
	}
	// Post FIX01 §A.4: provisionUser resolves the team_id by alias via
	// ListTeamsByAlias before calling TeamMemberAdd. The default fake
	// returns {TeamID:"team-uuid-default", TeamAlias:"default"}.
	if flm.rec.lastTeamMemberAddTeam != "team-uuid-default" {
		t.Errorf("TeamMemberAdd team_id: got %q, want team-uuid-default (resolved by alias)",
			flm.rec.lastTeamMemberAddTeam)
	}
	if flm.rec.lastTeamMemberAddUser != "litellm-carol" {
		t.Errorf("TeamMemberAdd user_id: got %q, want litellm-carol", flm.rec.lastTeamMemberAddUser)
	}
	if flm.rec.lastTeamMemberAddRole != "user" {
		t.Errorf("TeamMemberAdd role: got %q, want user", flm.rec.lastTeamMemberAddRole)
	}
}

// TestCallbackHandler_DuplicateTeamMemberAddSwallowed — FIX01 §A.3.
// LiteLLM v1.83 UserNew(teams:[…]) auto-enrolls the user, then the
// explicit TeamMemberAdd in provisionUser hits a 400 "already added".
// provisionUser MUST swallow that case and proceed to KeyGenerate.
// Same swallow applies to the existing-user branch on subsequent
// SSO attempts (idempotency under out-of-band team-membership state).
func TestCallbackHandler_DuplicateTeamMemberAddSwallowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		userBeh func(email string) (*litellm.UserInfo, error)
	}{
		{
			name: "first-time-branch",
			userBeh: func(string) (*litellm.UserInfo, error) {
				return nil, litellm.ErrNotFound
			},
		},
		{
			name: "existing-user-branch",
			userBeh: func(email string) (*litellm.UserInfo, error) {
				return &litellm.UserInfo{UserID: "litellm-existing", UserEmail: email}, nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fix := newRealOIDC(t, "ach")
			defer fix.Close()
			fix.idEmail = "dup@example.com"

			flm := newFakeLiteLLM()
			flm.userInfoBehaviour = tc.userBeh
			// LiteLLM 1.83 returns 400 on duplicate-add and the
			// restclient.go 4xx wrapper drops the response body, so
			// the error string only carries path + status (no
			// "already" substring). isDuplicateAddErr matches on
			// (path + 400) instead. Format mirrors restclient.go:152.
			flm.teamMemberAddError = func(string, string, string) error {
				return errors.New("litellm: 400 on POST /team/member_add (code=400)")
			}
			dbRec := newDBInsertRecord()

			ctc := &callbackTestCase{
				stateCookie: "state-dup",
				urlState:    "state-dup",
				urlCode:     "code-dup",
				oidcFix:     fix,
				litellm:     flm,
				dbInsert:    dbRec,
				pepper:      []byte("pepper-dup-32-bytes-aaaaaaaaaa"),
			}
			w := runCallback(t, ctc)
			resp := w.Result()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200 (duplicate-add must be swallowed); body=%s",
					resp.StatusCode, string(body))
			}
			if flm.rec.keyGenerateCalls != 1 {
				t.Errorf("KeyGenerate calls: got %d, want 1 (handler must proceed past duplicate-add)",
					flm.rec.keyGenerateCalls)
			}
			if dbRec.calls != 1 {
				t.Errorf("DB insert calls: got %d, want 1", dbRec.calls)
			}
		})
	}
}

// TestCallbackHandler_TeamIDResolvedByAlias — FIX01 §A.4.
// provisionUser MUST call ListTeamsByAlias("default") and use the
// resolved team_id UUID for the subsequent TeamMemberAdd. The literal
// "default" string is NOT a valid team_id under LiteLLM's UUID-based
// team identity scheme.
func TestCallbackHandler_TeamIDResolvedByAlias(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "alias@example.com"

	flm := newFakeLiteLLM()
	// Override the default fake to return a specific UUID for "default".
	flm.listTeamsBehaviour = func(alias string) ([]litellm.TeamListEntry, error) {
		if alias != "default" {
			t.Errorf("ListTeamsByAlias: got alias %q, want default", alias)
		}
		return []litellm.TeamListEntry{
			{TeamID: "9f4a2c01-aaaa-bbbb-cccc-deadbeef1234", TeamAlias: "default"},
		}, nil
	}
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-alias",
		urlState:    "state-alias",
		urlCode:     "code-alias",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-alias-32-bytes-aaaaaaaa"),
	}
	w := runCallback(t, tc)
	if w.Result().StatusCode != http.StatusOK {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("status: got %d, want 200; body=%s", w.Result().StatusCode, string(body))
	}
	if flm.rec.listTeamsCalls != 1 {
		t.Errorf("ListTeamsByAlias calls: got %d, want 1", flm.rec.listTeamsCalls)
	}
	if flm.rec.lastListTeamsAlias != "default" {
		t.Errorf("ListTeamsByAlias alias: got %q, want default", flm.rec.lastListTeamsAlias)
	}
	if flm.rec.lastTeamMemberAddTeam != "9f4a2c01-aaaa-bbbb-cccc-deadbeef1234" {
		t.Errorf("TeamMemberAdd team_id: got %q, want resolved UUID",
			flm.rec.lastTeamMemberAddTeam)
	}
}

// TestCallbackHandler_DefaultTeamAliasNotInLiteLLM — FIX01 §A.4 hard-fail
// path. When ListTeamsByAlias returns zero entries, provisionUser MUST
// surface default_team_missing (deployer misconfig per Hub §17 / API-02).
func TestCallbackHandler_DefaultTeamAliasNotInLiteLLM(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "noteam@example.com"

	flm := newFakeLiteLLM()
	flm.listTeamsBehaviour = func(string) ([]litellm.TeamListEntry, error) {
		return []litellm.TeamListEntry{}, nil
	}
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-noteam",
		urlState:    "state-noteam",
		urlCode:     "code-noteam",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-noteam-32-bytes-aaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500 (no default team); body=%s",
			resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeDefaultTeamMissing) {
		t.Errorf("body missing %s code; body=%s", audit.OutcomeDefaultTeamMissing, string(body))
	}
	if dbRec.calls != 0 {
		t.Errorf("DB insert MUST NOT run on default_team_missing; got %d calls", dbRec.calls)
	}
}

// TestCallbackHandler_ExistingUserTeamMemberAddDefaultTeamMissing (behavior 2c):
// existing user + TeamMemberAdd returns ErrNotFound → 500 default_team_missing
// + audit OutcomeDefaultTeamMissing + DB INSERT NOT called.
func TestCallbackHandler_ExistingUserTeamMemberAddDefaultTeamMissing(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "dave@example.com"

	flm := newFakeLiteLLM()
	flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
		return &litellm.UserInfo{UserID: "litellm-dave", UserEmail: email}, nil
	}
	flm.teamMemberAddError = func(string, string, string) error {
		return litellm.ErrNotFound
	}
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-dave",
		urlState:    "state-dave",
		urlCode:     "code-dave",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-dave-32-bytes-aaaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeDefaultTeamMissing) {
		t.Errorf("body missing %s code; body=%s", audit.OutcomeDefaultTeamMissing, string(body))
	}
	if dbRec.calls != 0 {
		t.Errorf("DB insert MUST NOT run on default_team_missing; got %d calls", dbRec.calls)
	}
	auditLog := w.Header().Get("X-Test-Audit")
	if !strings.Contains(auditLog, audit.OutcomeDefaultTeamMissing) {
		t.Errorf("audit missing %s; got: %s", audit.OutcomeDefaultTeamMissing, auditLog)
	}
}

// TestCallbackHandler_StateMismatch (behavior 3): cookie state != URL state
// → 400 invalid_argument + OutcomeStateInvalid audit + cookie cleared.
func TestCallbackHandler_StateMismatch(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "s1",
		urlState:    "s2", // mismatch
		urlCode:     "code-mm",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-mm-32-bytes-aaaaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeStateInvalid) {
		t.Errorf("body missing %s; body=%s", audit.OutcomeStateInvalid, string(body))
	}
	if flm.rec.keyGenerateCalls != 0 {
		t.Errorf("KeyGenerate MUST NOT run on state-mismatch; got %d", flm.rec.keyGenerateCalls)
	}
}

// TestCallbackHandler_URLStateAbsent (behavior 3b): cookie set, URL has
// NO state= param → 400 invalid_argument + OutcomeStateInvalid.
func TestCallbackHandler_URLStateAbsent(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "s1",
		urlState:    "", // absent
		urlCode:     "code-abs",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-abs-32-bytes-aaaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeStateInvalid) {
		t.Errorf("body missing %s; body=%s", audit.OutcomeStateInvalid, string(body))
	}
}

// TestCallbackHandler_URLStateEmpty (behavior 3c): URL has ?state= (empty
// value) — handled identically to absent.
func TestCallbackHandler_URLStateEmpty(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()

	// Build a URL with literal ?state= (empty value, NOT absent).
	target := "/platform/auth/sso/callback?state=&code=cc"
	cookieW := httptest.NewRecorder()
	setSSOCookie(cookieW, "s1", "verifier-s1", false)
	cookieHeader := cookieW.Result().Header.Get("Set-Cookie")

	auditBuf := &bytes.Buffer{}
	deps := Deps{
		OIDCProvider:    fix.provider,
		IDTokenVerifier: fix.provider.Verifier(&oidc.Config{ClientID: fix.clientID}),
		OAuth2Cfg:       fix.cfg,
		LiteLLM:         flm,
		Pepper:          []byte("pepper-empty-32-bytes-aaaaaaaa"),
		Audit:           slog.New(slog.NewJSONHandler(auditBuf, nil)),
		Logger:          slog.New(slog.NewTextHandler(io_Discard{}, nil)),
		Namespace:       "ach-system",
		InsertPKFn:      dbRec.insertFn,
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Add("Cookie", cookieHeader)
	w := httptest.NewRecorder()
	CallbackHandler(deps).ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeStateInvalid) {
		t.Errorf("body missing %s; body=%s", audit.OutcomeStateInvalid, string(body))
	}
	if !strings.Contains(auditBuf.String(), audit.OutcomeStateInvalid) {
		t.Errorf("audit missing %s; got: %s", audit.OutcomeStateInvalid, auditBuf.String())
	}
}

// TestCallbackHandler_DefaultTeamMissing (behavior 4): first-time SSO,
// UserNew succeeds but TeamMemberAdd returns 4xx team_not_found → 500
// default_team_missing + audit + DB INSERT skipped.
func TestCallbackHandler_DefaultTeamMissing(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "eve@example.com"
	flm := newFakeLiteLLM()
	// UserInfoByEmail returns ErrNotFound (default) -> UserNew is called.
	// UserNew returns ok (default). TeamMemberAdd returns a fake "team not found" error.
	flm.teamMemberAddError = func(string, string, string) error {
		// Simulate the 4xx team_not_found body from LiteLLM. The fmt string
		// includes "404" so isLiteLLMNotFound would match, but the planner
		// classifies any TeamMemberAdd error after UserNew as default_team_missing
		// regardless of the wire shape.
		return errors.New("litellm: POST /team/member_add status: 404 team_not_found team_id: default")
	}
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-eve",
		urlState:    "state-eve",
		urlCode:     "code-eve",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-eve-32-bytes-aaaaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeDefaultTeamMissing) {
		t.Errorf("body missing %s; body=%s", audit.OutcomeDefaultTeamMissing, string(body))
	}
	if dbRec.calls != 0 {
		t.Errorf("DB insert MUST NOT run on default_team_missing; got %d", dbRec.calls)
	}
	auditLog := w.Header().Get("X-Test-Audit")
	if !strings.Contains(auditLog, audit.OutcomeDefaultTeamMissing) {
		t.Errorf("audit missing %s; got: %s", audit.OutcomeDefaultTeamMissing, auditLog)
	}
}

// TestCallbackHandler_KeyGenerateUnreachable (behavior 5): everything works
// up to KeyGenerate which returns a transport error → 503 litellm_unreachable
// + audit + DB INSERT skipped.
func TestCallbackHandler_KeyGenerateUnreachable(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "frank@example.com"

	flm := newFakeLiteLLM()
	flm.keyGenerateBehaviour = func(*litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
		return nil, errors.New("network: connection refused")
	}
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-frank",
		urlState:    "state-frank",
		urlCode:     "code-frank",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-frank-32-bytes-aaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeLitellmUnreachable) {
		t.Errorf("body missing %s; body=%s", audit.OutcomeLitellmUnreachable, string(body))
	}
	if dbRec.calls != 0 {
		t.Errorf("DB insert MUST NOT run on KeyGenerate failure; got %d", dbRec.calls)
	}
}

// TestCallbackHandler_DBInsertFailureWithCompensation (behavior 6):
// KeyGenerate succeeds but DB INSERT fails → handler calls RevokeKey
// (compensation, exactly once) → returns 500 db_insert_failed.
func TestCallbackHandler_DBInsertFailureWithCompensation(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "grace@example.com"

	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()
	dbRec.failWith = errors.New("db: simulated transient")

	tc := &callbackTestCase{
		stateCookie: "state-grace",
		urlState:    "state-grace",
		urlCode:     "code-grace",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-grace-32-bytes-aaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), audit.OutcomeDbInsertFailed) {
		t.Errorf("body missing %s; body=%s", audit.OutcomeDbInsertFailed, string(body))
	}
	// LiteLLM compensation: RevokeKey called exactly once with the token
	// echoed back by KeyGenerate.
	if flm.rec.revokeKeyCalls != 1 {
		t.Errorf("RevokeKey calls: got %d, want 1 (compensation)", flm.rec.revokeKeyCalls)
	}
	if len(flm.rec.revokeKeyTokensSeen) != 1 || !strings.HasPrefix(flm.rec.revokeKeyTokensSeen[0], "litellm-token-") {
		t.Errorf("RevokeKey token: got %v, want one starting with litellm-token-", flm.rec.revokeKeyTokensSeen)
	}
}

// TestCallbackHandler_MissingCookie (behavior 7): callback request with no
// __Host-ach_sso cookie → 400 + OutcomeStateInvalid audit + LiteLLM
// untouched.
func TestCallbackHandler_MissingCookie(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		skipCookie: true,
		urlState:   "s1",
		urlCode:    "c1",
		oidcFix:    fix,
		litellm:    flm,
		dbInsert:   dbRec,
		pepper:     []byte("pepper-noco-32-bytes-aaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", resp.StatusCode, string(body))
	}
	if flm.rec.keyGenerateCalls != 0 || flm.rec.userInfoByEmailCalls != 0 {
		t.Errorf("LiteLLM MUST NOT be invoked on missing cookie")
	}
}

// TestCallbackHandler_OneTimePlaintextInvariant (behavior 8): plaintext
// appears EXACTLY ONCE in the full HTTP response (headers + body).
// Already inlined in TestCallbackHandler_FirstTimeSSOHappyPath above; this
// test repeats the invariant against a different email to lock the
// guarantee under varying inputs.
func TestCallbackHandler_OneTimePlaintextInvariant(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "harriet+plus@example.com"
	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-h",
		urlState:    "state-h",
		urlCode:     "code-h",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-h-32-bytes-aaaaaaaaaaaa"),
	}
	w := runCallback(t, tc)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	var got callbackResponse
	_ = json.Unmarshal(body, &got)
	full := string(body) + "\n" + strings.Join(append([]string{}, headersToLines(resp.Header)...), "\n")
	count := strings.Count(full, got.Plaintext)
	if count != 1 {
		t.Errorf("plaintext occurrence in full response: got %d, want exactly 1", count)
	}
	// Plaintext must NOT appear in audit log.
	if strings.Contains(w.Header().Get("X-Test-Audit"), got.Plaintext) {
		t.Errorf("plaintext leaked into audit log")
	}
}

// TestCallbackHandler_HMACPepperUsedForCredentialHash (behavior 9): the
// credential_hash persisted in personal_keys is HMAC(pepper, plaintext) —
// constructed with the deps.Pepper byte slice.
func TestCallbackHandler_HMACPepperUsedForCredentialHash(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "ivan@example.com"
	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()
	pepper := []byte("known-pepper-value-32-bytes-aaaa")

	tc := &callbackTestCase{
		stateCookie: "state-i",
		urlState:    "state-i",
		urlCode:     "code-i",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      pepper,
	}
	w := runCallback(t, tc)
	body, _ := io.ReadAll(w.Result().Body)
	var got callbackResponse
	_ = json.Unmarshal(body, &got)
	want, err := credhash.Hash(pepper, []byte(got.Plaintext))
	if err != nil {
		t.Fatalf("credhash.Hash: %v", err)
	}
	if dbRec.lastRow.CredentialHash != want {
		t.Errorf("CredentialHash mismatch: got %q want %q", dbRec.lastRow.CredentialHash, want)
	}
	// The plaintext must NOT appear in any DB-row field.
	if strings.Contains(dbRec.lastRow.KeyID, got.Plaintext) ||
		strings.Contains(dbRec.lastRow.OwnerEmail, got.Plaintext) {
		t.Errorf("plaintext leaked into DB row fields")
	}
}

// headersToLines flattens an http.Header into "k: v" line strings — used
// by the one-time-plaintext invariant check.
func headersToLines(h http.Header) []string {
	out := make([]string, 0, len(h))
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, k+": "+v)
		}
	}
	return out
}

// §7 stubs — interface satisfaction only (issue #17: /v1 surface).
func (f *fakeLiteLLM) CreateAccessGroup(_ context.Context, _ litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) GetAccessGroupByName(_ context.Context, _ string) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) UpdateAccessGroup(_ context.Context, _ string, _ litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteAccessGroupByID(_ context.Context, _ string) error { return nil }

// --- Phase 6 D-20 — LoginHandler + CallbackHandler session_id threading ---

// TestLoginHandlerPacksSessionIDIntoState — when ?session_id is set on
// the login URL, LoginHandler must pack it into the OAuth2 state as
// "<random_state>|<session_id>" so Dex echoes it back unchanged on
// the callback. The cookie still stores ONLY the random state so the
// CSRF check in CallbackHandler remains intact (URL state's prefix is
// compared against the cookie).
func TestLoginHandlerPacksSessionIDIntoState(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/platform/auth/login?session_id=devcode-xyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatalf("Location header empty")
	}
	parsedLoc, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v (%s)", err, loc)
	}
	stateParam := parsedLoc.Query().Get("state")
	if stateParam == "" {
		t.Fatalf("Location missing state= param: %s", loc)
	}
	if !strings.HasSuffix(stateParam, "|devcode-xyz") {
		t.Errorf("state param: %q does not end with '|devcode-xyz'", stateParam)
	}
	if strings.Count(stateParam, "|") != 1 {
		t.Errorf("state param: %q must contain exactly one '|' separator", stateParam)
	}

	// Cookie payload MUST contain only the random_state (NOT the
	// session_id suffix). Decode and check.
	cookies := rec.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == cookieNameSecure {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatalf("missing %s cookie", cookieNameSecure)
	}
	raw, decErr := base64.URLEncoding.DecodeString(found.Value)
	if decErr != nil {
		t.Fatalf("cookie b64 decode: %v", decErr)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		t.Fatalf("cookie payload does not split into (state, verifier): %q", string(raw))
	}
	cookieState := parts[0]
	if strings.Contains(cookieState, "devcode-xyz") {
		t.Errorf("cookie state must NOT carry session_id: %q", cookieState)
	}
	// The random_state prefix of the URL state MUST equal the cookie state.
	urlStatePrefix := strings.SplitN(stateParam, "|", 2)[0]
	if urlStatePrefix != cookieState {
		t.Errorf("URL state prefix %q != cookie state %q", urlStatePrefix, cookieState)
	}
}

// TestLoginHandlerNoSessionIDLeavesStateUnpacked — backward compat:
// when ?session_id is absent the URL state is the raw random_state
// (no separator), preserving pre-Phase-6 e2e behavior.
func TestLoginHandlerNoSessionIDLeavesStateUnpacked(t *testing.T) {
	deps := minimalLoginDeps()
	h := LoginHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/platform/auth/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	if strings.Contains(state, "|") {
		t.Errorf("state must not contain '|' when session_id is absent; got %q", state)
	}
}

// TestCallbackHandler_NoSessionIDPreservesJSONBranch — the D-20
// backward-compat invariant: when the OAuth2 state lacks the suffix,
// CallbackHandler renders the pre-Phase-6 JSON response and Redis is
// NEVER touched. test/e2e/phase3_invariants assertions depend on this.
func TestCallbackHandler_NoSessionIDPreservesJSONBranch(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "noaml@example.com"

	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()

	tc := &callbackTestCase{
		stateCookie: "state-no-session",
		urlState:    "state-no-session", // no |suffix
		urlCode:     "code-no-session",
		oidcFix:     fix,
		litellm:     flm,
		dbInsert:    dbRec,
		pepper:      []byte("pepper-no-sess-32-bytes-aaaaaa"),
	}
	// runCallback does NOT inject Redis — the existing helper leaves
	// deps.Redis nil, exercising the absence-of-Redis branch.
	w := runCallback(t, tc)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, string(body))
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json prefix (D-20 absence-of-session_id)", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"plaintext":"pk_`) {
		t.Errorf("expected JSON body with plaintext; got %s", string(body))
	}
}

// TestCallbackHandler_WithSessionIDWritesRedisAndRendersHTML — D-20
// active branch: URL state carries "<random_state>|<session_id>",
// deps.Redis is wired; on success the pk_ payload is written under
// "ach:cli-session:<id>" and the response is the HTML close-window
// page (NOT JSON, NOT carrying the pk_ in the browser body).
func TestCallbackHandler_WithSessionIDWritesRedisAndRendersHTML(t *testing.T) {
	fix := newRealOIDC(t, "ach")
	defer fix.Close()
	fix.idEmail = "browser@example.com"

	flm := newFakeLiteLLM()
	dbRec := newDBInsertRecord()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis Run: %v", err)
	}
	defer mr.Close()
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rc.Close()

	// Build a callback driver mirroring runCallback but injecting Redis.
	auditBuf := &bytes.Buffer{}
	deps := Deps{
		OIDCProvider:    fix.provider,
		IDTokenVerifier: fix.provider.Verifier(&oidc.Config{ClientID: fix.clientID}),
		OAuth2Cfg:       fix.cfg,
		LiteLLM:         flm,
		Pepper:          []byte("pepper-d20-32-bytes-aaaaaa"),
		Audit:           slog.New(slog.NewJSONHandler(auditBuf, nil)),
		Logger:          slog.New(slog.NewTextHandler(io_Discard{}, nil)),
		Namespace:       "ach-system",
		InsertPKFn:      dbRec.insertFn,
		NowFn:           func() time.Time { return time.Unix(1700000000, 0).UTC() },
		Redis:           rc,
	}

	// Drive the cookie + URL state with the packed session_id suffix.
	cookieRecorder := httptest.NewRecorder()
	setSSOCookie(cookieRecorder, "state-d20", "verifier-state-d20", false)
	cookieHeader := cookieRecorder.Result().Header.Get("Set-Cookie")

	target := "/platform/auth/sso/callback?state=" + url.QueryEscape("state-d20|sess-d20-id") + "&code=code-d20"
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Add("Cookie", cookieHeader)
	w := httptest.NewRecorder()
	CallbackHandler(deps).ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, string(body))
	}

	// Content-Type MUST be text/html.
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html prefix", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "You may close this window") {
		t.Errorf("html body missing close-window text; got: %s", string(body))
	}
	// The pk_ plaintext MUST NOT be in the browser body — only Redis
	// gets it (consumed via /platform/auth/cli/token).
	if strings.Contains(string(body), "pk_") {
		t.Errorf("FATAL: pk_ plaintext leaked into browser HTML body: %s", string(body))
	}

	// Redis MUST carry the session payload at "ach:cli-session:sess-d20-id".
	redisKey := "ach:cli-session:sess-d20-id"
	if !mr.Exists(redisKey) {
		t.Fatalf("expected Redis key %q to exist after D-20 callback", redisKey)
	}
	raw, gerr := mr.Get(redisKey)
	if gerr != nil {
		t.Fatalf("miniredis Get %q: %v", redisKey, gerr)
	}
	var stored map[string]string
	if uerr := json.Unmarshal([]byte(raw), &stored); uerr != nil {
		t.Fatalf("unmarshal stored session: %v raw=%s", uerr, raw)
	}
	if !strings.HasPrefix(stored["key_id"], "pkid_") {
		t.Errorf("stored key_id: %q does not start with pkid_", stored["key_id"])
	}
	if !strings.HasPrefix(stored["plaintext"], "pk_") {
		t.Errorf("stored plaintext: %q does not start with pk_", stored["plaintext"])
	}
	if stored["owner_email"] != "browser@example.com" {
		t.Errorf("stored owner_email: %q want browser@example.com", stored["owner_email"])
	}

	// Audit emission MUST still be the SSO action (NOT the cli action —
	// that one fires at /token consumption, Task 2 territory).
	auditStr := auditBuf.String()
	if !strings.Contains(auditStr, audit.ActionSSOLogin) {
		t.Errorf("audit log missing platform.sso.login: %s", auditStr)
	}
	if strings.Contains(auditStr, audit.ActionCliLogin) {
		t.Errorf("audit log MUST NOT carry platform.cli.login at callback time: %s", auditStr)
	}
}

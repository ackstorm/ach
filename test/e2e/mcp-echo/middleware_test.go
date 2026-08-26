// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	echojwt "github.com/ackstorm/ach/test/e2e/mcp-echo/jwt"
)

func newSignedTokenFor(t *testing.T, iss, aud string) (jwksURL string, token string, cleanup func()) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const kid = "test-kid"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA",
				"kid": kid, "x": base64.RawURLEncoding.EncodeToString(pub),
			}},
		})
	}))

	now := time.Now().Unix()
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, jwtv5.MapClaims{
		"iss": iss, "aud": aud, "sub": "ns/alice@example.com",
		"groups": []string{"team-a", "team-b"},
		"iat":    now, "exp": now + 60,
	})
	tok.Header["kid"] = kid
	tok.Header["typ"] = "JWT"
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return srv.URL, signed, srv.Close
}

func TestRequireJWT_RejectsMissingHeader(t *testing.T) {
	sink := newCapture()
	mw := jwtMiddleware(echojwt.NewVerifier(echojwt.NewKeyCache("http://unused"), echojwt.Expectations{
		Issuer: "i", Audience: []string{"a"},
	}), sink, true)

	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("inner handler should not be invoked")
	})

	r := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	mw(inner).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("WWW-Authenticate: got %q", got)
	}
}

func TestRequireJWT_AcceptsValidToken(t *testing.T) {
	jwksURL, signed, cleanup := newSignedTokenFor(t, "https://hub.example", "mcp:demo-mcp-echo")
	defer cleanup()

	sink := newCapture()
	mw := jwtMiddleware(echojwt.NewVerifier(echojwt.NewKeyCache(jwksURL), echojwt.Expectations{
		Issuer:   "https://hub.example",
		Audience: []string{"mcp:demo-mcp-echo"},
	}), sink, true)

	var sawClaims echojwt.Verified
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := claimsFromContext(r.Context()); ok {
			sawClaims = v
		}
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"hello":"world"}`))
	r.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	mw(inner).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("status: got %d want 200 (body=%s)", w.Code, body)
	}
	if sawClaims.Sub != "ns/alice@example.com" {
		t.Fatalf("claims not propagated: %+v", sawClaims)
	}
	snap := sink.snapshot()
	if snap.JWTClaims.Aud != "mcp:demo-mcp-echo" {
		t.Fatalf("capture missing claims: %+v", snap)
	}
	if snap.BodyRaw != `{"hello":"world"}` {
		t.Fatalf("body not captured: %q", snap.BodyRaw)
	}
}

func TestRequireJWT_RejectsMalformedBearer(t *testing.T) {
	sink := newCapture()
	mw := jwtMiddleware(echojwt.NewVerifier(echojwt.NewKeyCache("http://unused"), echojwt.Expectations{
		Issuer: "i", Audience: []string{"a"},
	}), sink, true)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")))
	w := httptest.NewRecorder()
	mw(inner).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", w.Code)
	}
}

func TestClaimsFromContext_AbsentReturnsFalse(t *testing.T) {
	if _, ok := claimsFromContext(context.Background()); ok {
		t.Fatalf("expected absent claims to return false")
	}
}

// TestJWTMiddleware_OptionalAcceptsMissingHeader covers the BIP
// forwardIdentityJWT=false (nojwt) path: with require=false a tokenless
// request is accepted, reaches the inner handler, and is recorded with
// jwt_present=false so the closed-loop e2e can assert the absence.
func TestJWTMiddleware_OptionalAcceptsMissingHeader(t *testing.T) {
	sink := newCapture()
	mw := jwtMiddleware(echojwt.NewVerifier(echojwt.NewKeyCache("http://unused"), echojwt.Expectations{
		Issuer: "i", Audience: []string{"a"},
	}), sink, false)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"hello":"nojwt"}`))
	w := httptest.NewRecorder()
	mw(inner).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (optional mode must accept no-JWT)", w.Code)
	}
	if !called {
		t.Fatalf("inner handler not invoked on optional no-JWT path")
	}
	snap := sink.snapshot()
	if snap.JWTPresent {
		t.Fatalf("jwt_present must be false on no-JWT path: %+v", snap)
	}
	if snap.AuthorizationSeen != "" {
		t.Fatalf("authorization_seen must be empty on no-JWT path: %q", snap.AuthorizationSeen)
	}
	if snap.BodyRaw != `{"hello":"nojwt"}` {
		t.Fatalf("body not captured on no-JWT path: %q", snap.BodyRaw)
	}
}

// TestJWTMiddleware_OptionalStillRejectsBadToken proves optional mode only
// tolerates the ABSENCE of a token — a present-but-invalid token is still a
// hard 401 (wrong issuer here), never silently accepted.
func TestJWTMiddleware_OptionalStillRejectsBadToken(t *testing.T) {
	jwksURL, signed, cleanup := newSignedTokenFor(t, "https://wrong-issuer", "mcp:demo-mcp-nojwt")
	defer cleanup()

	sink := newCapture()
	mw := jwtMiddleware(echojwt.NewVerifier(echojwt.NewKeyCache(jwksURL), echojwt.Expectations{
		Issuer:   "https://hub.example",
		Audience: []string{"mcp:demo-mcp-nojwt"},
	}), sink, false)

	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("inner handler must not run for an invalid token even in optional mode")
	})

	r := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	mw(inner).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401 (bad token must 401 even in optional mode)", w.Code)
	}
}

// The "groups" claim is captured so the e2e suite can assert it survived
// the forwarder mint and LiteLLM's MCP gateway.
func TestVerify_CapturesGroupsClaim(t *testing.T) {
	jwksURL, token, cleanup := newSignedTokenFor(t, "https://ach.example.com", "mcp:x")
	defer cleanup()

	v := echojwt.NewVerifier(
		echojwt.NewKeyCache(jwksURL),
		echojwt.Expectations{Issuer: "https://ach.example.com", Audience: []string{"mcp:x"}},
	)
	got, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(got.Groups) != 2 || got.Groups[0] != "team-a" || got.Groups[1] != "team-b" {
		t.Fatalf("Groups = %v; want [team-a team-b]", got.Groups)
	}
}

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
		"iat": now, "exp": now + 60,
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
	mw := requireJWT(echojwt.NewVerifier(echojwt.NewKeyCache("http://unused"), echojwt.Expectations{
		Issuer: "i", Audience: []string{"a"},
	}), sink)

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
	mw := requireJWT(echojwt.NewVerifier(echojwt.NewKeyCache(jwksURL), echojwt.Expectations{
		Issuer:   "https://hub.example",
		Audience: []string{"mcp:demo-mcp-echo"},
	}), sink)

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
	mw := requireJWT(echojwt.NewVerifier(echojwt.NewKeyCache("http://unused"), echojwt.Expectations{
		Issuer: "i", Audience: []string{"a"},
	}), sink)
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

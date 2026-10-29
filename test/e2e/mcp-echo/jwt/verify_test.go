// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// signTestJWT mints a compact JWS exactly the way the Forwarder does:
// header {alg=EdDSA, typ=JWT, kid}, payload as given.
func signTestJWT(t *testing.T, kid string, priv ed25519.PrivateKey, claims jwtv5.MapClaims) string {
	t.Helper()
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	tok.Header["kid"] = kid
	tok.Header["typ"] = "JWT"
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func newJWKSServer(t *testing.T, kid string, pub ed25519.PublicKey) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA",
				"kid": kid,
				"x":   base64.RawURLEncoding.EncodeToString(pub),
			}},
		})
	}))
}

func TestVerifier_HappyPath(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := newJWKSServer(t, "k1", pub)
	defer srv.Close()

	v := NewVerifier(NewKeyCache(srv.URL), Expectations{
		Issuer:   "https://hub.example",
		Audience: []string{"mcp:demo-mcp-echo"},
	})

	now := time.Now().Unix()
	tok := signTestJWT(t, "k1", priv, jwtv5.MapClaims{
		"iss": "https://hub.example",
		"sub": "ach-system/alice@example.com",
		"aud": "mcp:demo-mcp-echo",
		"iat": now, "exp": now + 60,
	})

	got, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Iss != "https://hub.example" || got.Aud != "mcp:demo-mcp-echo" || got.Kid != "k1" {
		t.Fatalf("unexpected claims: %+v", got)
	}
}

func TestVerifier_RejectsWrongIssuer(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := newJWKSServer(t, "k1", pub)
	defer srv.Close()
	v := NewVerifier(NewKeyCache(srv.URL), Expectations{
		Issuer: "https://hub.example", Audience: []string{"mcp:x"},
	})
	now := time.Now().Unix()
	tok := signTestJWT(t, "k1", priv, jwtv5.MapClaims{
		"iss": "https://other.example", "aud": "mcp:x",
		"iat": now, "exp": now + 60,
	})
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatalf("expected wrong-iss to fail")
	}
}

func TestVerifier_RejectsWrongAudience(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := newJWKSServer(t, "k1", pub)
	defer srv.Close()
	v := NewVerifier(NewKeyCache(srv.URL), Expectations{
		Issuer: "i", Audience: []string{"mcp:x"},
	})
	now := time.Now().Unix()
	tok := signTestJWT(t, "k1", priv, jwtv5.MapClaims{
		"iss": "i", "aud": "mcp:y",
		"iat": now, "exp": now + 60,
	})
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatalf("expected wrong-aud to fail")
	}
}

func TestVerifier_RejectsExpired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := newJWKSServer(t, "k1", pub)
	defer srv.Close()
	v := NewVerifier(NewKeyCache(srv.URL), Expectations{
		Issuer: "i", Audience: []string{"a"},
	})
	now := time.Now().Unix()
	tok := signTestJWT(t, "k1", priv, jwtv5.MapClaims{
		"iss": "i", "aud": "a",
		"iat": now - 200, "exp": now - 100,
	})
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatalf("expected expired token to fail")
	}
}

func TestVerifier_RejectsTamperedSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := newJWKSServer(t, "k1", pub)
	defer srv.Close()
	v := NewVerifier(NewKeyCache(srv.URL), Expectations{
		Issuer: "i", Audience: []string{"a"},
	})
	now := time.Now().Unix()
	tok := signTestJWT(t, "k1", priv, jwtv5.MapClaims{
		"iss": "i", "aud": "a",
		"iat": now, "exp": now + 60,
	})
	tampered := tok + "x"
	if _, err := v.Verify(context.Background(), tampered); err == nil {
		t.Fatalf("expected tampered signature to fail")
	}
}

func TestVerifier_RejectsRS256Confusion(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	srv := newJWKSServer(t, "k1", pub)
	defer srv.Close()
	v := NewVerifier(NewKeyCache(srv.URL), Expectations{
		Issuer: "i", Audience: []string{"a"},
	})
	// header advertises alg=none with kid=k1 — must be rejected
	const noneTok = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIiwia2lkIjoiazEifQ." +
		"eyJpc3MiOiJpIiwiYXVkIjoiYSJ9."
	if _, err := v.Verify(context.Background(), noneTok); err == nil {
		t.Fatalf("expected alg=none to be rejected")
	}
}

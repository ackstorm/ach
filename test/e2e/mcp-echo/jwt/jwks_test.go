// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// b64url encodes without padding per RFC 7515 §3.
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func TestKeyCache_LoadsEd25519OKP(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "OKP",
				"crv": "Ed25519",
				"use": "sig",
				"alg": "EdDSA",
				"kid": "kid-abc",
				"x":   b64url(pub),
			}},
		})
	}))
	defer srv.Close()

	c := NewKeyCache(srv.URL)
	got, err := c.Lookup(t.Context(), "kid-abc")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if string(got) != string(pub) {
		t.Fatalf("public key mismatch")
	}
}

func TestKeyCache_UnknownKidErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()

	c := NewKeyCache(srv.URL)
	if _, err := c.Lookup(t.Context(), "no-such-kid"); err == nil {
		t.Fatalf("expected error for unknown kid")
	}
}

func TestKeyCache_RejectsNonOKP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"x"}]}`))
	}))
	defer srv.Close()

	c := NewKeyCache(srv.URL)
	if _, err := c.Lookup(t.Context(), "x"); err == nil {
		t.Fatalf("expected RSA key to be rejected (Ed25519 OKP only)")
	}
}

func TestKeyCache_RejectsShortPublicKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"x","x":"AAAA"}]}`))
	}))
	defer srv.Close()

	c := NewKeyCache(srv.URL)
	if _, err := c.Lookup(t.Context(), "x"); err == nil {
		t.Fatalf("expected non-32-byte x to be rejected")
	}
}

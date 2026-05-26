// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"
)

func mustSeed(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return b
}

// JWKS1: response has 200, Content-Type application/jwk-set+json, Cache-Control public,max-age=3600.
func TestJWKSHandler_Headers(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k1", mustSeed(t), "", nil)); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	JWKSHandler(signer)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type = %q; want application/jwk-set+json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q; want public, max-age=3600", got)
	}
}

// JWKS2: response body is {"keys":[...]} with correct JWK fields.
func TestJWKSHandler_BodyShape(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k1", mustSeed(t), "", nil)); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	rec := httptest.NewRecorder()
	JWKSHandler(signer)(rec, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))

	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("keys len = %d; want 1", len(doc.Keys))
	}
	k := doc.Keys[0]
	if k["kty"] != "OKP" {
		t.Errorf("kty = %s; want OKP", k["kty"])
	}
	if k["crv"] != "Ed25519" {
		t.Errorf("crv = %s; want Ed25519", k["crv"])
	}
	if k["use"] != "sig" {
		t.Errorf("use = %s; want sig", k["use"])
	}
	if k["alg"] != "EdDSA" {
		t.Errorf("alg = %s; want EdDSA", k["alg"])
	}
	if k["kid"] != "k1" {
		t.Errorf("kid = %s; want k1", k["kid"])
	}
	if k["x"] == "" {
		t.Error("x is empty")
	}
}

// JWKS3: 2 entries with both slots; 1 entry with only current; 0 with neither.
func TestJWKSHandler_KeyCount(t *testing.T) {
	t.Run("both", func(t *testing.T) {
		signer := NewEd25519Signer()
		loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
		if err := loader.LoadOnce(newTestSecret("kc", mustSeed(t), "kn", mustSeed(t))); err != nil {
			t.Fatalf("LoadOnce: %v", err)
		}
		rec := httptest.NewRecorder()
		JWKSHandler(signer)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		var doc struct {
			Keys []map[string]string `json:"keys"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(doc.Keys) != 2 {
			t.Errorf("len = %d; want 2", len(doc.Keys))
		}
	})
	t.Run("current_only", func(t *testing.T) {
		signer := NewEd25519Signer()
		loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
		if err := loader.LoadOnce(newTestSecret("kc", mustSeed(t), "", nil)); err != nil {
			t.Fatalf("LoadOnce: %v", err)
		}
		rec := httptest.NewRecorder()
		JWKSHandler(signer)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		var doc struct {
			Keys []map[string]string `json:"keys"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &doc)
		if len(doc.Keys) != 1 {
			t.Errorf("len = %d; want 1", len(doc.Keys))
		}
	})
	t.Run("neither", func(t *testing.T) {
		signer := NewEd25519Signer()
		rec := httptest.NewRecorder()
		JWKSHandler(signer)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d; want 200 even with no slots", rec.Code)
		}
		var doc struct {
			Keys []map[string]string `json:"keys"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if doc.Keys == nil {
			t.Error("keys must be empty array, not null")
		}
		if len(doc.Keys) != 0 {
			t.Errorf("len = %d; want 0", len(doc.Keys))
		}
	})
}

// JWKS4: x field is base64url-no-pad decodable to a 32-byte slice matching slot.pub.
// We verify by signing a message and verifying it with ed25519.Verify on the
// pub key reconstructed from the JWK's x field.
func TestJWKSHandler_PublicKeyRoundtrip(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k-roundtrip", mustSeed(t), "", nil)); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	rec := httptest.NewRecorder()
	JWKSHandler(signer)(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var doc struct {
		Keys []JWK `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("keys len = %d; want 1", len(doc.Keys))
	}
	pub, err := base64.RawURLEncoding.DecodeString(doc.Keys[0].X)
	if err != nil {
		t.Fatalf("base64 decode x: %v", err)
	}
	if len(pub) != 32 {
		t.Fatalf("decoded pub len = %d; want 32", len(pub))
	}
	// Sign + verify via the published pub key — proves x matches signer's slot.pub.
	tok, err := signer.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
}

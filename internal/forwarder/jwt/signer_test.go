// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// freshSeed returns a 32-byte random seed for tests. Failing crypto/rand
// is a host-level catastrophe — there is no in-test recovery path.
func freshSeed(t *testing.T) []byte {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return seed
}

// Test 1 — Loaded()/Sign() pre-loadCurrent surface area.
func TestEd25519Signer_NoCurrentSlot(t *testing.T) {
	s := NewEd25519Signer()
	if s.Loaded() {
		t.Fatal("Loaded() = true on fresh signer; want false")
	}
	if _, err := s.Sign(context.Background(), Claims{Iss: "x", Sub: "y", Aud: "z"}); !errors.Is(err, ErrNoCurrentSlot) {
		t.Fatalf("Sign() error = %v; want ErrNoCurrentSlot", err)
	}
	if got := s.JWKS(); got != nil {
		t.Fatalf("JWKS() on fresh signer = %v; want nil", got)
	}
}

// Test 2 — newSignerSlot refuse-to-construct discipline.
func TestNewSignerSlot_RefuseToConstruct(t *testing.T) {
	validSeed := freshSeed(t)

	cases := []struct {
		name    string
		kid     string
		seed    []byte
		wantErr error
	}{
		{"empty kid + valid seed", "", validSeed, ErrEmptyKid},
		{"non-empty kid + nil seed", "k", nil, ErrEmptySeed},
		{"non-empty kid + 31-byte seed", "k", make([]byte, 31), ErrEmptySeed},
		{"non-empty kid + 33-byte seed", "k", make([]byte, 33), ErrEmptySeed},
		{"non-empty kid + empty seed", "k", []byte{}, ErrEmptySeed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slot, err := newSignerSlot(tc.kid, tc.seed)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v; want %v", err, tc.wantErr)
			}
			if slot != nil {
				t.Fatalf("slot = %+v; want nil on error", slot)
			}
		})
	}
}

// Test 3 — newSignerSlot success produces 64-byte priv + 32-byte pub.
func TestNewSignerSlot_KeyLengths(t *testing.T) {
	seed := freshSeed(t)
	slot, err := newSignerSlot("ach-jwt-k1", seed)
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	if len(slot.priv) != ed25519.PrivateKeySize {
		t.Fatalf("priv len = %d; want %d", len(slot.priv), ed25519.PrivateKeySize)
	}
	if len(slot.pub) != ed25519.PublicKeySize {
		t.Fatalf("pub len = %d; want %d", len(slot.pub), ed25519.PublicKeySize)
	}
	// pub == priv[32:] (ed25519.NewKeyFromSeed sets the last 32 bytes
	// of the 64-byte private key to the public key).
	for i := range slot.pub {
		if slot.pub[i] != slot.priv[ed25519.SeedSize+i] {
			t.Fatalf("pub mismatch at byte %d: priv-suffix=%x pub=%x", i, slot.priv[32:], slot.pub)
		}
	}
}

// Test 4 — loadCurrent flips Loaded; loadNext does NOT; loadNext(nil) clears.
func TestEd25519Signer_LoadFlags(t *testing.T) {
	s := NewEd25519Signer()
	slotA, err := newSignerSlot("k1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot k1: %v", err)
	}
	slotB, err := newSignerSlot("k2", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot k2: %v", err)
	}

	// loadNext alone does not flip Loaded — Loaded gates on current.
	s.loadNext(slotB)
	if s.Loaded() {
		t.Fatal("Loaded() = true after only loadNext; want false")
	}

	s.loadCurrent(slotA)
	if !s.Loaded() {
		t.Fatal("Loaded() = false after loadCurrent; want true")
	}

	// loadNext(nil) clears next; JWKS drops to 1 entry.
	s.loadNext(nil)
	if got := s.JWKS(); len(got) != 1 {
		t.Fatalf("JWKS() len after loadNext(nil) = %d; want 1", len(got))
	}
}

// Test 5 — Sign emits a compact JWS with the expected header shape.
func TestEd25519Signer_Sign_Header(t *testing.T) {
	s := NewEd25519Signer()
	slot, err := newSignerSlot("kid-h1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	s.loadCurrent(slot)
	tok, err := s.Sign(context.Background(), Claims{Iss: "https://h.example", Sub: "ns/u@e", Aud: "mcp:x"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("Sign() parts = %d; want 3 (header.payload.signature)", len(parts))
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if got := hdr["alg"]; got != "EdDSA" {
		t.Fatalf("alg = %v; want EdDSA", got)
	}
	if got := hdr["typ"]; got != "JWT" {
		t.Fatalf("typ = %v; want JWT", got)
	}
	if got := hdr["kid"]; got != "kid-h1" {
		t.Fatalf("kid = %v; want kid-h1", got)
	}
}

// Test 6 — Sign emits {iss, sub, aud, iat, exp=iat+120} and NO jti.
func TestEd25519Signer_Sign_ClaimsShape(t *testing.T) {
	s := NewEd25519Signer()
	slot, err := newSignerSlot("kid-c1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	s.loadCurrent(slot)
	tok, err := s.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	for _, k := range []string{"iss", "sub", "aud", "iat", "exp"} {
		if _, ok := claims[k]; !ok {
			t.Fatalf("claims missing required key %q (got %v)", k, claims)
		}
	}
	if _, ok := claims["jti"]; ok {
		t.Fatalf("claims contains jti (FWD-07 / Hub §9.1 forbid): %v", claims)
	}
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	if int64(exp)-int64(iat) != 120 {
		t.Fatalf("exp-iat = %d; want 120", int64(exp)-int64(iat))
	}
}

// Email claim — emitted (bare email, no namespace prefix) only when non-empty.
// sub stays namespace-qualified; email is the additive claim for consumers that
// key by bare email.
func TestEd25519Signer_Sign_EmailClaim(t *testing.T) {
	s := NewEd25519Signer()
	slot, err := newSignerSlot("kid-c1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	s.loadCurrent(slot)

	decode := func(t *testing.T, c Claims) map[string]any {
		t.Helper()
		tok, err := s.Sign(context.Background(), c)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		payload, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
		if err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		var claims map[string]any
		if err := json.Unmarshal(payload, &claims); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		return claims
	}

	// Non-empty Email → "email" claim present and equal to the bare email.
	withEmail := decode(t, Claims{Iss: "i", Sub: "u@example.com", Aud: "mcp:x", Email: "u@example.com"})
	if got, ok := withEmail["email"]; !ok || got != "u@example.com" {
		t.Fatalf("email claim = %v (present=%v); want %q", got, ok, "u@example.com")
	}
	// sub is the bare owner-email; email mirrors it (additive, not a replacement).
	if withEmail["sub"] != "u@example.com" {
		t.Fatalf("sub mutated = %v; want bare owner-email unchanged", withEmail["sub"])
	}

	// Empty Email → "email" claim omitted entirely (no empty string emitted).
	noEmail := decode(t, Claims{Iss: "i", Sub: "u@example.com", Aud: "mcp:x"})
	if _, ok := noEmail["email"]; ok {
		t.Fatalf("email claim must be omitted when empty, got %v", noEmail["email"])
	}
}

// Test 7 — Sign always uses current; next is published in JWKS only.
func TestEd25519Signer_Sign_AlwaysCurrent(t *testing.T) {
	s := NewEd25519Signer()
	slotCur, err := newSignerSlot("K1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot K1: %v", err)
	}
	slotNxt, err := newSignerSlot("K2", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot K2: %v", err)
	}
	s.loadCurrent(slotCur)
	s.loadNext(slotNxt)

	tok, err := s.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	hdrBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var hdr map[string]any
	_ = json.Unmarshal(hdrBytes, &hdr)
	if hdr["kid"] != "K1" {
		t.Fatalf("kid with both slots = %v; want K1 (current)", hdr["kid"])
	}

	// Clear next; current still wins.
	s.loadNext(nil)
	tok, err = s.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
	if err != nil {
		t.Fatalf("Sign post-clear: %v", err)
	}
	parts = strings.Split(tok, ".")
	hdrBytes, _ = base64.RawURLEncoding.DecodeString(parts[0])
	hdr = map[string]any{}
	_ = json.Unmarshal(hdrBytes, &hdr)
	if hdr["kid"] != "K1" {
		t.Fatalf("kid post-clear = %v; want K1", hdr["kid"])
	}
}

// Test 8 — concurrent Sign + loadCurrent: no panic, no torn slot. Run
// under -race for the publication-safety assertion. We assert ONLY that
// the Sign result is a parseable JWS whose kid is one of the published
// kids — never an empty or torn value.
func TestEd25519Signer_AtomicSwap(t *testing.T) {
	s := NewEd25519Signer()

	// Pre-load with K1 so Sign never observes a nil slot.
	slot1, err := newSignerSlot("K-atomic-1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot K1: %v", err)
	}
	s.loadCurrent(slot1)

	slot2, err := newSignerSlot("K-atomic-2", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot K2: %v", err)
	}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			tok, err := s.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
			if err != nil {
				t.Errorf("Sign #%d: %v", i, err)
				return
			}
			parts := strings.Split(tok, ".")
			if len(parts) != 3 {
				t.Errorf("Sign #%d parts = %d", i, len(parts))
				return
			}
			hdrBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
			var hdr map[string]any
			if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
				t.Errorf("unmarshal #%d: %v", i, err)
				return
			}
			kid, _ := hdr["kid"].(string)
			if kid != "K-atomic-1" && kid != "K-atomic-2" {
				t.Errorf("Sign #%d kid = %q; want one of {K-atomic-1, K-atomic-2}", i, kid)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				s.loadCurrent(slot1)
			} else {
				s.loadCurrent(slot2)
			}
		}
	}()
	wg.Wait()
}

// Test 9 — JWKS slot-count semantics: 0/1/2 published slots.
func TestEd25519Signer_JWKS_SlotCount(t *testing.T) {
	s := NewEd25519Signer()

	if got := s.JWKS(); got != nil {
		t.Fatalf("JWKS() pre-load = %v; want nil", got)
	}

	slotA, _ := newSignerSlot("k-only-cur", freshSeed(t))
	s.loadCurrent(slotA)
	if got := s.JWKS(); len(got) != 1 {
		t.Fatalf("JWKS() current-only len = %d; want 1", len(got))
	}

	slotB, _ := newSignerSlot("k-also-nxt", freshSeed(t))
	s.loadNext(slotB)
	got := s.JWKS()
	if len(got) != 2 {
		t.Fatalf("JWKS() current+next len = %d; want 2", len(got))
	}
	if got[0].Kid != "k-only-cur" || got[1].Kid != "k-also-nxt" {
		t.Fatalf("JWKS order kids = [%q, %q]; want [current, next]", got[0].Kid, got[1].Kid)
	}
}

// Test 10 — JWKS entry shape: kty/crv/use/alg fixed; x is base64url-no-pad of 32-byte pub.
func TestEd25519Signer_JWKS_EntryShape(t *testing.T) {
	s := NewEd25519Signer()
	seed := freshSeed(t)
	slot, err := newSignerSlot("shape-kid", seed)
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	s.loadCurrent(slot)

	jwks := s.JWKS()
	if len(jwks) != 1 {
		t.Fatalf("JWKS len = %d; want 1", len(jwks))
	}
	jwk := jwks[0]
	if jwk.Kty != "OKP" {
		t.Fatalf("kty = %q; want OKP", jwk.Kty)
	}
	if jwk.Crv != "Ed25519" {
		t.Fatalf("crv = %q; want Ed25519", jwk.Crv)
	}
	if jwk.Use != "sig" {
		t.Fatalf("use = %q; want sig", jwk.Use)
	}
	if jwk.Alg != "EdDSA" {
		t.Fatalf("alg = %q; want EdDSA", jwk.Alg)
	}
	if jwk.Kid != "shape-kid" {
		t.Fatalf("kid = %q; want shape-kid", jwk.Kid)
	}
	// x must decode (unpadded base64url) to the 32-byte public key.
	pub, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		t.Fatalf("decode x (unpadded base64url): %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("decoded x len = %d; want %d", len(pub), ed25519.PublicKeySize)
	}
	// And the decoded bytes must equal the slot's pub.
	expected := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	for i := range pub {
		if pub[i] != expected[i] {
			t.Fatalf("pub byte %d: got %x want %x", i, pub[i], expected[i])
		}
	}
	// Reject any padding equals in x (RawURLEncoding emits none; this
	// guards against accidental use of base64.URLEncoding).
	if strings.Contains(jwk.X, "=") {
		t.Fatalf("x contains padding: %q", jwk.X)
	}
}

// Test 11 — Round-trip: Sign produces a JWS that ed25519.Verify accepts
// against the JWKS-published pub key. Proves the signing key matches
// the publicly-published verification key.
func TestEd25519Signer_RoundTripVerify(t *testing.T) {
	s := NewEd25519Signer()
	seed := freshSeed(t)
	slot, err := newSignerSlot("rt-kid", seed)
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	s.loadCurrent(slot)

	tok, err := s.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Reconstruct the signing input bytes (header.payload) and the
	// signature bytes; verify directly with ed25519.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("parts = %d; want 3", len(parts))
	}
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	// Pull the published pub key from JWKS (not from slot directly) so we
	// prove the round-trip uses the public surface, not internal state.
	jwks := s.JWKS()
	if len(jwks) != 1 {
		t.Fatalf("JWKS len = %d; want 1", len(jwks))
	}
	pubBytes, err := base64.RawURLEncoding.DecodeString(jwks[0].X)
	if err != nil {
		t.Fatalf("decode jwks x: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte(signingInput), sig) {
		t.Fatal("ed25519.Verify(JWKS-published-pub, signing-input, sig) = false; want true")
	}

	// Belt-and-braces: jwt.Parse against the same pub MUST also accept it.
	parsed, err := jwtv5.Parse(tok, func(token *jwtv5.Token) (any, error) {
		if _, ok := token.Method.(*jwtv5.SigningMethodEd25519); !ok {
			return nil, errors.New("unexpected method")
		}
		return ed25519.PublicKey(pubBytes), nil
	})
	if err != nil {
		t.Fatalf("jwt.Parse: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("jwt.Parse marked token invalid; want valid")
	}
}

// Test — Sign emits "groups" when non-empty and omits it entirely when empty.
func TestEd25519Signer_Sign_GroupsClaim(t *testing.T) {
	s := NewEd25519Signer()
	slot, err := newSignerSlot("kid-g1", freshSeed(t))
	if err != nil {
		t.Fatalf("newSignerSlot: %v", err)
	}
	s.loadCurrent(slot)

	decode := func(t *testing.T, c Claims) map[string]any {
		t.Helper()
		tok, err := s.Sign(context.Background(), c)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		payload, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
		if err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		var claims map[string]any
		if err := json.Unmarshal(payload, &claims); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		return claims
	}

	// Non-empty Groups → "groups" present, order preserved as supplied.
	withGroups := decode(t, Claims{
		Iss:    "i",
		Sub:    "u@example.com",
		Aud:    "mcp:ach-memory",
		Groups: []string{"team-a", "team-b"},
	})
	raw, ok := withGroups["groups"].([]any)
	if !ok {
		t.Fatalf("groups claim = %v; want a JSON array", withGroups["groups"])
	}
	if len(raw) != 2 || raw[0] != "team-a" || raw[1] != "team-b" {
		t.Fatalf("groups = %v; want [team-a team-b]", raw)
	}

	// Nil Groups → key omitted entirely (ach-memory reads absent as "no groups").
	none := decode(t, Claims{Iss: "i", Sub: "u@example.com", Aud: "mcp:x"})
	if _, ok := none["groups"]; ok {
		t.Fatalf("groups must be omitted when nil, got %v", none["groups"])
	}

	// Empty (non-nil) Groups → also omitted; never an empty array on the wire.
	empty := decode(t, Claims{Iss: "i", Sub: "u@example.com", Aud: "mcp:x", Groups: []string{}})
	if _, ok := empty["groups"]; ok {
		t.Fatalf("groups must be omitted when empty, got %v", empty["groups"])
	}
}

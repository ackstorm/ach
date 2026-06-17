// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"sync/atomic"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// ErrEmptyKid is returned by newSignerSlot when the kid argument is the
// empty string. A JWT with kid="" would fail backend JWKS lookups
// silently (no matching key), so the loader refuses to construct a slot
// without a non-empty identifier.
var ErrEmptyKid = errors.New("jwt: kid must be non-empty")

// ErrEmptySeed is returned by newSignerSlot when the seed argument is
// not exactly ed25519.SeedSize (32) bytes. Hub §9.1 + RFC 8032 §5.1.5
// fix the Ed25519 seed length at 32 bytes; any other length is a
// malformed Secret and is refused outright per FWD-09 refuse-to-start
// (LoadOnce) / refuse-to-update (Reload) discipline.
var ErrEmptySeed = errors.New("jwt: ed25519 seed must be exactly 32 bytes")

// ErrNoCurrentSlot is returned by Ed25519Signer.Sign when no current
// slot has been loaded yet. The cobra RunE for the forwarder MUST gate
// process readiness on Loaded() so the live /readyz probe never returns
// 200 before LoadOnce succeeds — but Sign still defends in depth so a
// caller racing the loader receives a typed error rather than a panic.
var ErrNoCurrentSlot = errors.New("jwt: no current slot loaded")

// Claims is the caller-supplied subset of the JWT payload. iat and exp
// are synthesized inside Sign (iat = time.Now().Unix(), exp = iat+120)
// per FWD-07 / Hub §9.1; the caller MUST NOT pre-compute them.
// NO jti claim is emitted (Hub §9.1 + §20, accepted v1alpha1 threat
// model — replay-window restatement is v1beta1 backlog).
type Claims struct {
	// Iss is the JWT "iss" (issuer) claim. Production forwarder code
	// sets this to ACH_BASE_URL verbatim (already validated as http(s)://
	// at process start).
	Iss string
	// Sub is the JWT "sub" (subject) claim — the bare owner email
	// (no namespace prefix; G18 / commit 4c646d4). Hub §9.1.
	Sub string
	// Aud is the JWT "aud" (audience) claim. Production forwarder code
	// sets "mcp:<name>" on /mcp/<name> and "a2a:<name>" on /a2a/<name>.
	Aud string
	// Email is the JWT "email" claim: the bare owner email (no namespace
	// prefix), for consumers that key by email. Optional; omitted when empty.
	Email string
}

// JWK is the RFC 7517 JSON Web Key wire shape for a single Ed25519
// signing key. Hub §9.2 fixes Kty="OKP", Crv="Ed25519", Use="sig",
// Alg="EdDSA"; the only per-slot fields are Kid and X. X is the
// base64url-no-pad encoding of the 32-byte public key (RFC 8037 §2).
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

// Signer is the contract the proxy per-route handler (Plan 04-07) and
// the JWKSHandler (jwks.go) consume. Production wires *Ed25519Signer.
type Signer interface {
	// Sign returns a compact JWS (header.payload.signature) over the
	// supplied claims plus iat/exp synthesized internally per FWD-07.
	// Returns ErrNoCurrentSlot before LoadOnce succeeds. ctx is reserved
	// for future signing-side timeouts; the current implementation does
	// not observe it.
	Sign(ctx context.Context, c Claims) (string, error)
	// JWKS returns the current + optional next slot as a JWK slice
	// suitable for serialization as the "keys" member of the JWK Set
	// at /.well-known/jwks.json. Returns nil (which the handler renders
	// as "keys":[]) before LoadOnce succeeds.
	JWKS() []JWK
	// Loaded returns true once the current slot has been populated. The
	// forwarder /readyz probe gates on this so containers never serve
	// traffic with a nil signing slot.
	Loaded() bool
}

// signerSlot is the unexported triple {kid, priv, pub} that backs each
// of the current + next slots inside Ed25519Signer. Constructed only by
// newSignerSlot — which validates kid and seed up front — so any
// non-nil *signerSlot is guaranteed to have a 32-byte pub and a 64-byte
// priv (seed||pub per ed25519.NewKeyFromSeed).
type signerSlot struct {
	kid  string
	priv ed25519.PrivateKey // 64 bytes = seed||pub
	pub  ed25519.PublicKey  // 32 bytes
}

// newSignerSlot validates kid + seed and constructs a signerSlot. Refuses
// to construct on empty kid (ErrEmptyKid) or non-32-byte seed
// (ErrEmptySeed) per FWD-09 refuse-to-load discipline. The caller (the
// SecretLoader) is responsible for surfacing the error to the cobra
// RunE on the startup path, and for logging + keep-prior-slot on the
// informer-event path.
func newSignerSlot(kid string, seed []byte) (*signerSlot, error) {
	if kid == "" {
		return nil, ErrEmptyKid
	}
	if len(seed) != ed25519.SeedSize {
		return nil, ErrEmptySeed
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub, _ := priv.Public().(ed25519.PublicKey)
	return &signerSlot{kid: kid, priv: priv, pub: pub}, nil
}

// Ed25519Signer is the production Signer implementation. Both current
// and next slots are atomic.Pointer[signerSlot] so the Sign hot path
// observes a single lock-free Load — no RWMutex, no double-check, no
// torn read possible (atomic.Pointer publication is sequenced via the
// Go memory model). next is read only by JWKS(); Sign always uses
// current.
type Ed25519Signer struct {
	current atomic.Pointer[signerSlot]
	next    atomic.Pointer[signerSlot]
}

// NewEd25519Signer returns a zero-slots signer. The caller (the cobra
// RunE) MUST invoke SecretLoader.LoadOnce before treating Loaded() as
// true; until then, Sign returns ErrNoCurrentSlot and JWKS returns nil.
func NewEd25519Signer() *Ed25519Signer {
	return &Ed25519Signer{}
}

// loadCurrent publishes a new current slot via atomic.Pointer.Store.
// Pass a slot from newSignerSlot so the kid/seed invariants are
// pre-validated. The atomic publication makes the new key visible to
// in-flight Sign() calls without a lock.
func (s *Ed25519Signer) loadCurrent(slot *signerSlot) {
	s.current.Store(slot)
}

// loadNext publishes a new next slot (nil clears it after a completed
// rotation — JWKS drops back to one published key).
func (s *Ed25519Signer) loadNext(slot *signerSlot) {
	s.next.Store(slot)
}

// Loaded reports whether a current slot has been published. The
// forwarder /readyz probe MUST gate on this so backends never see a
// JWKS endpoint that races a Sign call to an empty signer.
func (s *Ed25519Signer) Loaded() bool {
	return s.current.Load() != nil
}

// Sign returns a compact JWS over the supplied claims plus iat/exp
// synthesized per FWD-07 / Hub §9.1. The header is
// {"alg":"EdDSA","typ":"JWT","kid":"<current.kid>"}; the payload is
// {"iss","sub","aud","iat","exp"} — explicitly NO jti per Hub §9.1 +
// §20 (accepted v1alpha1 threat model).
//
// The signing slot is the result of s.current.Load() at Sign-entry; a
// concurrent loadCurrent racing the in-flight Sign uses whichever slot
// won the atomic publication race — never torn, never nil after the
// first successful loadCurrent.
func (s *Ed25519Signer) Sign(_ context.Context, c Claims) (string, error) {
	slot := s.current.Load()
	if slot == nil {
		return "", ErrNoCurrentSlot
	}
	now := time.Now().Unix()
	claims := jwtv5.MapClaims{
		"iss": c.Iss,
		"sub": c.Sub,
		"aud": c.Aud,
		"iat": now,
		"exp": now + 120, // FWD-07 / Hub §9.1: 120-second skew window. NO jti.
	}
	// "email" mirrors "sub" (both the bare owner email) for consumers that key
	// by email. Additive; omitted when empty. (G18: sub is NOT namespace-qualified.)
	if c.Email != "" {
		claims["email"] = c.Email
	}
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	token.Header["kid"] = slot.kid
	return token.SignedString(slot.priv)
}

// JWKS returns current + optional next as a JWK slice in publication
// order (current first). Returns a nil slice when no slot is loaded;
// the JWKSHandler wraps a nil result as "keys":[] in the wire JSON.
func (s *Ed25519Signer) JWKS() []JWK {
	var out []JWK
	if cur := s.current.Load(); cur != nil {
		out = append(out, slotToJWK(cur))
	}
	if nxt := s.next.Load(); nxt != nil {
		out = append(out, slotToJWK(nxt))
	}
	return out
}

// slotToJWK projects a signerSlot into its RFC 7517 / RFC 8037 wire
// shape: kty/crv/use/alg fixed, kid passed through verbatim, x is the
// base64url-no-pad encoding of the 32-byte public key.
func slotToJWK(s *signerSlot) JWK {
	return JWK{
		Kty: "OKP",
		Crv: "Ed25519",
		Use: "sig",
		Alg: "EdDSA",
		Kid: s.kid,
		X:   base64.RawURLEncoding.EncodeToString(s.pub),
	}
}

// Compile-time canary: *Ed25519Signer MUST satisfy Signer. A drift in
// the interface signature surfaces at build time, not at runtime.
var _ Signer = (*Ed25519Signer)(nil)

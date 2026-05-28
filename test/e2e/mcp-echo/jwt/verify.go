// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"errors"
	"fmt"
	"slices"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// Expectations is the claim contract this verifier enforces. Issuer +
// Audience are required; both must match exactly (Audience is satisfied
// when the token aud appears in the list).
type Expectations struct {
	Issuer   string
	Audience []string
}

// Verified is the surface returned to callers (middleware / tool) on
// a successful Verify.
type Verified struct {
	Iss string
	Sub string
	Aud string
	Kid string
	Iat int64
	Exp int64
}

// Verifier resolves Ed25519 public keys via a KeyCache and validates
// the standard claim set + EdDSA signature against Expectations.
type Verifier struct {
	keys   *KeyCache
	expect Expectations
}

// NewVerifier returns a Verifier. Callers must populate expect.Issuer
// and at least one expect.Audience entry — a Verify call with empty
// expectations would silently accept any token, so the responsibility
// to populate is upfront at the call site.
func NewVerifier(keys *KeyCache, expect Expectations) *Verifier {
	return &Verifier{keys: keys, expect: expect}
}

// ErrInvalidToken wraps every verification failure so callers can
// branch on a single sentinel without leaking the underlying detail
// to the HTTP response.
var ErrInvalidToken = errors.New("invalid token")

// Verify parses a compact JWS, resolves its kid against the JWKS,
// verifies the EdDSA signature, and asserts iss/aud/exp. The returned
// Verified struct carries the claims used downstream; failures wrap
// ErrInvalidToken (so middleware can map any failure to 401 without
// leaking internals).
func (v *Verifier) Verify(ctx context.Context, raw string) (Verified, error) {
	parser := jwtv5.NewParser(
		jwtv5.WithValidMethods([]string{jwtv5.SigningMethodEdDSA.Alg()}),
		jwtv5.WithIssuer(v.expect.Issuer),
		jwtv5.WithExpirationRequired(),
	)

	var claims jwtv5.MapClaims
	tok, err := parser.ParseWithClaims(raw, &claims, func(t *jwtv5.Token) (any, error) {
		kidAny, ok := t.Header["kid"]
		if !ok {
			return nil, fmt.Errorf("%w: missing kid", ErrInvalidToken)
		}
		kid, ok := kidAny.(string)
		if !ok || kid == "" {
			return nil, fmt.Errorf("%w: malformed kid", ErrInvalidToken)
		}
		pub, kerr := v.keys.Lookup(ctx, kid)
		if kerr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidToken, kerr)
		}
		return pub, nil
	})
	if err != nil {
		return Verified{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !tok.Valid {
		return Verified{}, ErrInvalidToken
	}

	aud, _ := claims["aud"].(string)
	if !slices.Contains(v.expect.Audience, aud) {
		return Verified{}, fmt.Errorf("%w: aud=%q not in allowlist", ErrInvalidToken, aud)
	}

	kid, _ := tok.Header["kid"].(string)
	sub, _ := claims["sub"].(string)
	iss, _ := claims["iss"].(string)
	iat := claimInt(claims, "iat")
	exp := claimInt(claims, "exp")

	// jwtv5 already validated exp; defense-in-depth catches the
	// (unlikely) case where exp parsed but the constraint was lifted.
	if exp == 0 {
		return Verified{}, fmt.Errorf("%w: missing exp", ErrInvalidToken)
	}

	return Verified{Iss: iss, Sub: sub, Aud: aud, Kid: kid, Iat: iat, Exp: exp}, nil
}

func claimInt(m jwtv5.MapClaims, k string) int64 {
	if v, ok := m[k].(float64); ok {
		return int64(v)
	}
	if v, ok := m[k].(int64); ok {
		return v
	}
	return 0
}

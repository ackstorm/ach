// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"errors"
	"fmt"
	"strings"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// ErrNotAJWT is returned for input that is not three dot-separated
// segments. Callers use it to fall through to pk_/ek_ handling.
var ErrNotAJWT = errors.New("jwt: not a compact JWS")

// Verified is what Verify hands back: the subject and the scope claim
// ("" when absent). Callers that only need identity read Sub.
type Verified struct {
	Sub   string
	Scope string
}

// Verify checks a token this signer (or its next slot) produced and returns
// its subject and scope. iss and aud are exact matches. Keys are the
// in-memory slots — no JWKS fetch, both services hold the same Secret.
func (s *Ed25519Signer) Verify(raw, iss, aud string) (*Verified, error) {
	if strings.Count(raw, ".") != 2 {
		return nil, ErrNotAJWT
	}
	keyfunc := func(t *jwtv5.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		for _, slot := range []*signerSlot{s.current.Load(), s.next.Load()} {
			if slot != nil && slot.kid == kid {
				return slot.pub, nil
			}
		}
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	tok, err := jwtv5.Parse(raw, keyfunc,
		jwtv5.WithValidMethods([]string{jwtv5.SigningMethodEdDSA.Alg()}),
		jwtv5.WithExpirationRequired(),
		jwtv5.WithIssuer(iss),
		jwtv5.WithAudience(aud),
	)
	if err != nil {
		return nil, err
	}
	sub, err := tok.Claims.GetSubject()
	if err != nil || sub == "" {
		return nil, errors.New("jwt: no subject")
	}
	scope, _ := tok.Claims.(jwtv5.MapClaims)["scope"].(string)
	return &Verified{Sub: sub, Scope: scope}, nil
}

// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/keys"
)

// JWTVerifier is what *jwt.Ed25519Signer provides: both services hold the
// signing Secret, so verification never fetches a JWKS.
type JWTVerifier interface {
	Verify(raw, iss, aud string) (string, error)
}

// OAuthPKLookup returns the user's single active purpose='oauth' row.
type OAuthPKLookup func(ctx context.Context, email string) (*db.PkKeyInfo, error)

type oauthResolver struct {
	inner    Resolver
	verifier JWTVerifier
	iss, aud string
	lookup   OAuthPKLookup
}

// NewOAuthResolver wraps the pk_/ek_ resolver: a bearer that is a compact
// JWS is verified and mapped to its subject's OAuth pk_ row; anything else
// falls through to inner. Sits INSIDE the Redis cache so the 60-second
// revocation window and singleflight apply to JWTs too. A JWS that fails
// verification, or a subject with no live row, reads as (nil, nil) —
// expired_or_revoked, indistinguishable by design (KEY-04 / KEY-06).
func NewOAuthResolver(inner Resolver, v JWTVerifier, iss, aud string, lookup OAuthPKLookup) Resolver {
	return &oauthResolver{inner: inner, verifier: v, iss: strings.TrimRight(iss, "/"), aud: aud, lookup: lookup}
}

// NewOAuthResolverDB is the production constructor (lookup = db.ActiveOAuthPK).
func NewOAuthResolverDB(inner Resolver, v JWTVerifier, iss, aud string, pool *pgxpool.Pool) Resolver {
	return NewOAuthResolver(inner, v, iss, aud, func(ctx context.Context, email string) (*db.PkKeyInfo, error) {
		return db.ActiveOAuthPK(ctx, pool, email)
	})
}

func (r *oauthResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
	if !keys.LooksLikeJWS(plaintext) {
		return r.inner.Resolve(ctx, plaintext)
	}
	v, err := r.verifier.Verify(plaintext, r.iss, r.aud)
	if err != nil {
		if errors.Is(err, jwt.ErrNotAJWT) {
			return r.inner.Resolve(ctx, plaintext)
		}
		return nil, nil // bad signature / expired / wrong aud: 401, not 500
	}
	row, err := r.lookup(ctx, v)
	if err != nil || row == nil {
		return nil, err
	}
	return KeyInfoFromPK(row), nil
}

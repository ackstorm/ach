// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
)

// dbLookupFn is the per-prefix DB-lookup callable shape. The two
// production callables are wrappers around db.PkCheckAndExtend and
// db.EkResolve; unit tests inject in-memory stubs (see keystore_test.go).
//
// Returning (*KeyInfo, error) here rather than (*db.PkKeyInfo, ...) /
// (*db.EkKeyInfo, ...) means the wrappers do the type conversion and
// dbResolver stays branch-free per prefix.
type dbLookupFn func(ctx context.Context, credentialHashHex string) (*KeyInfo, error)

// dbResolver is the Postgres-backed Resolver. It hashes the plaintext
// with the configured pepper, classifies the bearer prefix via
// keys.ClassifyBearer, and dispatches to the per-prefix lookup callable.
//
// Per D-08 this is the inner Resolver wrapped by NewCachedResolver in
// production wiring. The Authn middleware (Plan 03-06+) ALWAYS calls
// the cached wrapper, never the dbResolver directly.
type dbResolver struct {
	pepper []byte
	pkFn   dbLookupFn
	ekFn   dbLookupFn
}

// PkExtendHook is called whenever a pk_ resolve actually slid the sliding
// window forward (db.PkKeyInfo.Extended), so the backing LiteLLM key's expiry
// can be re-based to match. litellmToken is the opaque LiteLLM token ACH
// persists; it is never the plaintext bearer.
//
// The hook is invoked with a request-detached context and MUST return
// promptly — it sits in the auth path of every service. The production
// implementation (NewLiteLLMPkExtendHook) does the LiteLLM round-trip in the
// background for exactly that reason. It has no error return: ACH's own row is
// the authority for the auth decision, and a missed LiteLLM update only means
// the LiteLLM key keeps its previous expiry until the next attempt, which the
// 5-minute debounce naturally retries.
type PkExtendHook func(ctx context.Context, litellmToken string)

// NewDBResolver constructs the production dbResolver wired to the
// pgxpool helpers from Plan 03-03 (db.PkCheckAndExtend, db.EkResolve).
// Returns ErrEmptyPepper on a nil/zero-length pepper for the same
// reasoning as NewCachedResolver.
//
// extendHook may be nil — content-service passes nil because it holds no
// LiteLLM client; platform-api and the forwarder wire the LiteLLM
// POST /key/update mirror.
func NewDBResolver(pool *pgxpool.Pool, pepper []byte, extendHook PkExtendHook) (Resolver, error) {
	if len(pepper) == 0 {
		return nil, ErrEmptyPepper
	}
	if pool == nil {
		return nil, errors.New("keystore: nil pgx pool")
	}
	return &dbResolver{
		pepper: append([]byte(nil), pepper...),
		pkFn:   pkLookupFor(pool, extendHook),
		ekFn:   ekLookupFor(pool),
	}, nil
}

// newDBResolverWith is the test-only constructor that injects the
// per-prefix lookup callables directly. Production code MUST use
// NewDBResolver.
func newDBResolverWith(pepper []byte, pkFn, ekFn dbLookupFn) *dbResolver {
	return &dbResolver{
		pepper: append([]byte(nil), pepper...),
		pkFn:   pkFn,
		ekFn:   ekFn,
	}
}

// Resolve implements the prefix-dispatched DB lookup path per D-08.
//
//  1. ClassifyBearer rejects malformed input. ErrInvalidBearer maps to
//     (nil, nil) — treated as unknown, indistinguishable from
//     revoked/expired per KEY-04.
//  2. Hash the plaintext with the configured pepper.
//  3. Dispatch by prefix:
//     - PrefixPk → db.PkCheckAndExtend (Hub §7.1 atomic sliding-window)
//     - PrefixEk → db.EkResolve (Hub §8.1 debounced last_used_at)
//  4. Wrap any returned error with the package prefix so downstream
//     log/audit code can attribute the failure.
func (r *dbResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
	prefix, err := keys.ClassifyBearer(plaintext)
	if err != nil {
		// Invalid plaintext is treated as unknown so the auth-layer
		// renders 401 expired_or_revoked (revoked/expired/unknown
		// indistinguishable per KEY-04 / KEY-06).
		return nil, nil
	}
	hash, err := credhash.Hash(r.pepper, []byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("keystore: dbResolver: hash: %w", err)
	}
	switch prefix {
	case keys.PrefixPk:
		info, err := r.pkFn(ctx, hash)
		if err != nil {
			return nil, fmt.Errorf("keystore: dbResolver: pk: %w", err)
		}
		return info, nil
	case keys.PrefixEk:
		info, err := r.ekFn(ctx, hash)
		if err != nil {
			return nil, fmt.Errorf("keystore: dbResolver: ek: %w", err)
		}
		return info, nil
	default:
		// Unreachable — ClassifyBearer above returns only PrefixPk or
		// PrefixEk on success, but defense-in-depth.
		return nil, nil
	}
}

// pkLookupFor returns a dbLookupFn closure bound to the given pgx pool
// that calls db.PkCheckAndExtend and maps the resulting *db.PkKeyInfo
// to *KeyInfo (or returns (nil, nil) on the revoked/expired/unknown
// path).
//
// When the call actually slid the sliding window forward and an extendHook is
// wired, the hook mirrors the new expiry onto the backing LiteLLM key. The
// context is detached from the request so a client that hangs up cannot abort
// the mirror mid-flight; the 5-minute debounce inside PkCheckAndExtend caps
// this to at most one LiteLLM round-trip per key per 5 minutes.
func pkLookupFor(pool *pgxpool.Pool, extendHook PkExtendHook) dbLookupFn {
	return func(ctx context.Context, credentialHashHex string) (*KeyInfo, error) {
		row, err := db.PkCheckAndExtend(ctx, pool, credentialHashHex)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, nil
		}
		if row.Extended && extendHook != nil && row.LiteLLMToken != nil {
			extendHook(context.WithoutCancel(ctx), *row.LiteLLMToken)
		}
		expires := row.ExpiresAt
		return &KeyInfo{
			KeyID:         row.KeyID,
			KeyType:       keys.PrefixPk,
			OwnerEmail:    row.OwnerEmail,
			ExpiresAt:     &expires,
			LiteLLMUserID: row.LiteLLMUserID,
			LiteLLMToken:  row.LiteLLMToken,
			// TESTING-PHASE (reverts FIX01 §A.6)
			LiteLLMKeyMaterial: row.LiteLLMKeyMaterial,
		}, nil
	}
}

// ekLookupFor returns a dbLookupFn closure bound to the given pgx pool
// that calls db.EkResolve and maps *db.EkKeyInfo to *KeyInfo.
func ekLookupFor(pool *pgxpool.Pool) dbLookupFn {
	return func(ctx context.Context, credentialHashHex string) (*KeyInfo, error) {
		row, err := db.EkResolve(ctx, pool, credentialHashHex)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, nil
		}
		return &KeyInfo{
			KeyID:         row.KeyID,
			KeyType:       keys.PrefixEk,
			OwnerEmail:    row.OwnerEmail,
			Environment:   row.Environment,
			LiteLLMUserID: row.LiteLLMUserID,
			LiteLLMToken:  row.LiteLLMToken,
			// TESTING-PHASE (reverts FIX01 §A.6)
			LiteLLMKeyMaterial: row.LiteLLMKeyMaterial,
		}, nil
	}
}

// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/oklog/ulid/v2"
)

// Exported namespace prefix constants. Downstream handlers (Phase 3
// plans 03-07, 03-08, 03-10) MUST refer to these constants rather than
// string literals so any future namespace shift surfaces as a compile
// break, not silent drift.
//
// pk- / ek-  : plaintext bearer prefixes (returned exactly once on
//
//	POST /platform/auth/sso/callback and POST /platform/env-keys).
//
// pkid_ / ekid_ : opaque key_id prefixes stored in the
//
//	personal_keys.key_id and environment_keys.key_id columns,
//	with load-bearing CHECK constraints in
//	db/migrations/000001_init.up.sql.
const (
	PkBearerPrefix  = "pk-"
	EkBearerPrefix  = "ek-"
	PkidKeyIDPrefix = "pkid_"
	EkidKeyIDPrefix = "ekid_"
)

// bearerSuffixLen is the length of the base64url-no-pad suffix following
// the pk-/ek- prefix. 48 random bytes encode to 64 base64url chars
// (ceil(48/3*4)=64), padding disabled.
const bearerSuffixLen = 64

// BearerPrefix is a newtype around the string prefix used to discriminate
// the two bearer kinds at the type level. The exported PrefixPk / PrefixEk
// values are the only well-formed inputs to NewBearer; any other value
// returns ErrInvalidPrefix.
type BearerPrefix string

// PrefixPk / PrefixEk are the only two well-formed BearerPrefix values.
const (
	PrefixPk BearerPrefix = PkBearerPrefix
	PrefixEk BearerPrefix = EkBearerPrefix
)

// ErrInvalidPrefix is returned by NewBearer when the caller supplies a
// prefix that is not PrefixPk or PrefixEk.
var ErrInvalidPrefix = errors.New("keys: invalid bearer prefix; expected pk- or ek-")

// bearerEncoding is RFC 4648 §5 base64url with padding disabled.
// base64url is case-significant — callers must not lowercase the output.
var bearerEncoding = base64.RawURLEncoding

// NewBearer returns a plaintext bearer credential of the form
// "<prefix><64-char base64url-no-pad suffix>" — for example
// "pk-Kx9TmQ2bv_Hn4P..." or "ek-def456...". The suffix is derived from
// 48 cryptographically random bytes drawn via crypto/rand (384 bits of
// entropy, far above the birthday bound for any realistic key space).
//
// Caller responsibility (Hub §16.1, package doc.go):
//
//   - The returned string MUST be emitted exactly once — to the response
//     body of the originating endpoint — and then discarded.
//   - The caller MUST hash the returned plaintext with
//     internal/credhash.Hash(pepper, plaintext) and persist ONLY the hex
//     digest; the plaintext MUST NOT be logged, cached, or stored in any
//     other form anywhere.
//
// Returns ErrInvalidPrefix when prefix is not PrefixPk or PrefixEk.
// Returns the wrapped crypto/rand error if entropy collection fails;
// rand.Read on Unix delegates to getrandom(2) and any failure is
// effectively process-fatal, so this branch exists for defense-in-depth
// rather than expected operation.
func NewBearer(prefix BearerPrefix) (string, error) {
	if prefix != PrefixPk && prefix != PrefixEk {
		return "", ErrInvalidPrefix
	}
	var b [48]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// base64url is case-significant — emit verbatim, no ToLower.
	return string(prefix) + bearerEncoding.EncodeToString(b[:]), nil
}

// KeyIDPrefix is a newtype around the string prefix for opaque key_id
// values. The exported PrefixPkid / PrefixEkid values are the only
// well-formed inputs to NewKeyID; any other value returns
// ErrInvalidPrefix.
type KeyIDPrefix string

// PrefixPkid / PrefixEkid are the only two well-formed KeyIDPrefix values.
// They correspond to the personal_keys.key_id and environment_keys.key_id
// column CHECK constraints in db/migrations/000001_init.up.sql.
const (
	PrefixPkid KeyIDPrefix = PkidKeyIDPrefix
	PrefixEkid KeyIDPrefix = EkidKeyIDPrefix
)

// ErrInvalidBearer is returned by ClassifyBearer when the input does not
// match the strict pk-<64-base64url> / ek-<64-base64url> grammar.
var ErrInvalidBearer = errors.New("keys: invalid bearer plaintext; expected pk-<64-base64url> or ek-<64-base64url>")

// NewKeyID returns an opaque key_id of the form
// "<prefix><26-char ULID base32-lowercase>" — for example
// "pkid_01h7azv3jryr2gh3kdpr3y0rkw" or
// "ekid_01h7azv3jryr2gh3kdpr3y0rkw".
//
// The ULID generator (github.com/oklog/ulid/v2) embeds a 48-bit
// millisecond timestamp followed by 80 bits of randomness; within a
// single millisecond ulid.Make() uses monotonic entropy, so consecutive
// calls are strictly increasing. This makes the resulting key_id values
// time-ordered, which (a) gives Postgres index locality on the
// personal_keys.key_id / environment_keys.key_id primary keys, and
// (b) makes audit logs naturally sortable.
//
// The 48-bit timestamp leaks creation time but NOT identity; per Hub §16
// "opaque" means consumers don't parse the structure, not that the
// structure is unpredictable.
//
// Returns ErrInvalidPrefix when prefix is not PrefixPkid or PrefixEkid.
func NewKeyID(prefix KeyIDPrefix) (string, error) {
	if prefix != PrefixPkid && prefix != PrefixEkid {
		return "", ErrInvalidPrefix
	}
	id := ulid.Make().String() // 26 uppercase Crockford base32 chars
	return string(prefix) + strings.ToLower(id), nil
}

// ClassifyBearer parses a bearer plaintext and returns its kind. The
// keystore Resolver (Phase 3 plan 03-05) calls ClassifyBearer to dispatch
// between PkCheckAndExtend and EkResolve before performing the DB lookup.
//
// Accepted forms (strict):
//
//   - "pk-" followed by exactly 64 chars from the RFC 4648 base64url
//     alphabet [A-Za-z0-9_-] → returns (PrefixPk, nil).
//   - "ek-" followed by the same suffix grammar → returns (PrefixEk, nil).
//
// Rejected (returns ("", ErrInvalidBearer)):
//
//   - Empty string, wrong length, chars outside [A-Za-z0-9_-], and any
//     pkid_*/ekid_* keyID values (those are opaque identifiers, not bearer
//     plaintexts).
//
// The parser is deliberately manual (not regexp) to avoid the allocation
// overhead of regexp.MatchString on the resolver hot path.
func ClassifyBearer(s string) (BearerPrefix, error) {
	const fullLen = 3 + bearerSuffixLen // "pk-" or "ek-" + 64 chars = 67
	if len(s) != fullLen {
		return "", ErrInvalidBearer
	}
	var prefix BearerPrefix
	switch {
	case strings.HasPrefix(s, PkBearerPrefix):
		prefix = PrefixPk
	case strings.HasPrefix(s, EkBearerPrefix):
		prefix = PrefixEk
	default:
		return "", ErrInvalidBearer
	}
	// Suffix must be exactly 64 chars from the base64url alphabet [A-Za-z0-9_-].
	for i := 3; i < fullLen; i++ {
		c := s[i]
		if !isBase64URL(c) {
			return "", ErrInvalidBearer
		}
	}
	return prefix, nil
}

// isBase64URL reports whether c is in the RFC 4648 base64url alphabet:
// [A-Za-z0-9_-].
func isBase64URL(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z',
		c >= 'a' && c <= 'z',
		c >= '0' && c <= '9',
		c == '-', c == '_':
		return true
	}
	return false
}

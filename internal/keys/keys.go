// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"strings"

	"github.com/oklog/ulid/v2"
)

// Exported namespace prefix constants. Downstream handlers (Phase 3
// plans 03-07, 03-08, 03-10) MUST refer to these constants rather than
// string literals so any future namespace shift surfaces as a compile
// break, not silent drift.
//
// pk_ / ek_  : plaintext bearer prefixes (returned exactly once on
//
//	POST /platform/auth/sso/callback and POST /platform/env-keys).
//
// pkid_ / ekid_ : opaque key_id prefixes stored in the
//
//	personal_keys.key_id and environment_keys.key_id columns,
//	with load-bearing CHECK constraints in
//	db/migrations/000001_init.up.sql.
const (
	PkBearerPrefix  = "pk_"
	EkBearerPrefix  = "ek_"
	PkidKeyIDPrefix = "pkid_"
	EkidKeyIDPrefix = "ekid_"
)

// bearerSuffixLen is the length of the base32-no-pad suffix following
// the pk_/ek_ prefix. 16 random bytes encode to 26 base32 chars
// (ceil(16 * 8 / 5) = 26) when padding is disabled.
const bearerSuffixLen = 26

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
var ErrInvalidPrefix = errors.New("keys: invalid bearer prefix; expected pk_ or ek_")

// bearerEncoding is the RFC 4648 base32 alphabet with padding disabled.
// 16 input bytes encode to 26 output chars; the result is then lowercased
// for grep-ergonomics with the rest of the codebase (matches
// strings.ToLower(ulid.Make().String()) idiom used by NewKeyID below).
var bearerEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewBearer returns a plaintext bearer credential of the form
// "<prefix><26-char base32-lowercase no-pad suffix>" — for example
// "pk_abc123..." or "ek_def456...". The suffix is derived from 16
// cryptographically random bytes drawn via crypto/rand (~130 bits of
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
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// EncodeToString yields 26 uppercase base32 chars. Lowercase for grep
	// ergonomics — same convention as NewKeyID's strings.ToLower wrap on
	// the ULID suffix.
	suffix := strings.ToLower(bearerEncoding.EncodeToString(b[:]))
	return string(prefix) + suffix, nil
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
// match the strict pk_<26-base32-lower> / ek_<26-base32-lower> grammar.
var ErrInvalidBearer = errors.New("keys: invalid bearer plaintext; expected pk_<26-base32-lower> or ek_<26-base32-lower>")

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
//   - "pk_" followed by exactly 26 chars from the lowercase RFC 4648
//     base32-no-pad alphabet [a-z2-7] → returns (PrefixPk, nil).
//   - "ek_" followed by the same suffix grammar → returns (PrefixEk, nil).
//
// Rejected (returns ("", ErrInvalidBearer)):
//
//   - Empty string, wrong length, uppercase prefix or suffix, chars
//     outside [a-z2-7], and any pkid_*/ekid_* keyID values (those are
//     opaque identifiers, not bearer plaintexts).
//
// The parser is deliberately manual (not regexp) to avoid the allocation
// overhead of regexp.MatchString on the resolver hot path.
func ClassifyBearer(s string) (BearerPrefix, error) {
	const fullLen = 3 + bearerSuffixLen // "pk_" or "ek_" + 26 chars = 29
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
	// Suffix must be exactly 26 chars from [a-z2-7].
	for i := 3; i < fullLen; i++ {
		c := s[i]
		if !isBase32Lower(c) {
			return "", ErrInvalidBearer
		}
	}
	return prefix, nil
}

// isBase32Lower reports whether c is in the RFC 4648 base32-no-pad
// alphabet, lowercased: [a-z2-7].
func isBase32Lower(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= '2' && c <= '7':
		return true
	}
	return false
}

// ConstantTimeEqual returns true iff a and b are byte-for-byte equal,
// performing the comparison in time that depends only on the input
// lengths (not on the content). This is the canonical defense against
// timing-oracle attacks on bearer-credential equality checks.
//
// Per crypto/subtle.ConstantTimeCompare semantics, a length mismatch
// returns 0 immediately — this early-return is NOT a timing leak because
// string length is not secret (the bearer grammar fixes it at 29 chars).
//
// The handler path calls ClassifyBearer first to enforce the 29-char
// length; ConstantTimeEqual is the secondary compare used wherever a
// credential equality check is needed outside the keystore resolver
// (e.g. webhook-signature parity in future plans).
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

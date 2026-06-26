// SPDX-License-Identifier: Apache-2.0

// Package keys is the single owner of the ACH bearer/key-id namespace.
//
// The pk-, ek-, pkid_, ekid_ prefixes are load-bearing invariants:
//
//   - pk-<64-char base64url-no-pad>: Personal Key plaintext bearer
//     (Hub §3, §7). 48 random bytes from crypto/rand → 64 base64url
//     chars yields 384 bits of entropy. CASE-SIGNIFICANT.
//   - ek-<64-char base64url-no-pad>: Environment Key plaintext bearer
//     (Hub §3, §8). Same entropy source as pk-.
//   - pkid_<26-char ULID base32-lowercase>: opaque key_id for the
//     personal_keys.key_id column. The CHECK (key_id LIKE 'pkid_%')
//     constraint in db/migrations/000001_init.up.sql is load-bearing.
//   - ekid_<26-char ULID base32-lowercase>: opaque key_id for the
//     environment_keys.key_id column. The CHECK (key_id LIKE 'ekid_%')
//     constraint in db/migrations/000001_init.up.sql is load-bearing.
//
// Discipline (binding on every importer):
//
//   - Plaintext bearer values (pk-*/ek-*) MUST NOT be logged, persisted, or
//     transmitted anywhere except:
//
//   - the one-time return body of POST /platform/auth/sso/callback
//     (Phase 3 plan 03-07)
//
//   - the one-time return body of POST /platform/keys
//     (Phase 3 plan 03-08)
//     A static-analysis grep gate in CI scans handler files for these
//     patterns outside the two allowed emission sites.
//
//   - This package has NO logger dependency. If a function in this package
//     emits to log, log/slog, or fmt.Print*, that is a bug; the build's
//     go-vet should surface it. See internal/credhash/doc.go for the same
//     discipline carried over from Phase 1.
//
//   - key_id values (pkid_*/ekid_*) are treated as opaque identifiers by
//     downstream consumers — they may appear in DB rows, audit events,
//     metrics, and admin URLs. They MUST NEVER be confused with plaintext
//     bearer values: ClassifyBearer rejects pkid_*/ekid_* inputs.
//
// Underlying randomness: crypto/rand (48 bytes → 384 bits) for bearers.
// Underlying ULID: github.com/oklog/ulid/v2 (time-ordered for DB-index
// locality and audit-log readability).
package keys

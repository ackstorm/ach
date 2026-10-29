// SPDX-License-Identifier: Apache-2.0

// Package credhash implements credential hashing for ACH per Hub spec §16.1
// and PRD decisions D-09 / D-10.
//
// Contract:
//
//   - HMAC-SHA-256(pepper, plaintext) is the ONLY hash function callers use.
//   - The pepper is sourced from the ACH_CREDENTIAL_HASH_PEPPER env var,
//     mounted from the Kubernetes Secret `ach-credential-hash-pepper` per
//     Plan 09. This package does NOT read env vars — callers pass the pepper
//     as a byte slice argument so the package surface stays pure.
//   - Hashes are compared by an indexed Postgres lookup
//     (WHERE credential_hash = $1), not in Go — this package exposes no
//     comparison helper. Equality is exact-match on the hex digest, so no
//     constant-time compare is needed (the digest is not itself a secret).
//
// This package MUST NOT import any logger and MUST NOT call any logging
// function: plaintext bearer keys (pk_…, ek_…) flow through the Hash
// signature, and logging them — even at debug level — would violate DB-04
// (no plaintext key material in logs, DB, Redis, audit, metrics, or traces).
// The deliberate absence of `log`, `log/slog`, `fmt`, and `os` imports is
// part of the threat-model mitigation for T-04-01.
package credhash

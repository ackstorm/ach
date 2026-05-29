// SPDX-License-Identifier: Apache-2.0

// Package hash wraps github.com/zeebo/xxh3 to produce the canonical
// `xxh3:<32-char-lowercase-hex>` string the CLI state ledger and drift
// detector consume per CLI spec §8.2, PRD decision D-10, and STATE-02.
//
// Contract:
//
//   - Hash(io.Reader) (string, error) — streams large extracted files
//     (typical state.FileEntry sources) via xxh3.New() + io.Copy.
//   - HashBytes([]byte) string — in-memory convenience form for adapter
//     output and small known buffers; no error path because there is
//     no I/O.
//   - Both forms produce the same output for the same input bytes:
//     `Hash(bytes.NewReader(b)) == HashBytes(b)`.
//   - The 128-bit xxh3 output is encoded as 32 lowercase hex chars
//     (big-endian Hi||Lo per xxh3.Uint128.Bytes), prefixed with the
//     literal string `xxh3:`. Every downstream consumer matches on
//     this exact shape — the format is a stable contract.
//
// Discipline (mirrors internal/credhash + internal/cli/config):
//
//   - Stdlib + ONE external dep (`github.com/zeebo/xxh3`) per D-10.
//     `cespare/xxhash/v2` already in go.mod is xxh64 — INSUFFICIENT
//     because spec §8.2 mandates the xxh3 family.
//   - No `log`, `log/slog`, `fmt`, or `os` imports — the wrapper hashes
//     opaque byte streams and never owns I/O lifecycle beyond what
//     io.Reader provides.
//   - SPDX header on every source file; the pre-push gate enforces.
//
// Discipline (cryptographic posture):
//
//   - xxh3 is a NON-cryptographic content-addressed integrity primitive.
//     It is NOT collision-resistant under adversarial input. Never use
//     it for credential hashing or signature paths — those belong to
//     `internal/credhash` (HMAC-SHA-256) and `crypto/ed25519` exclusively.
//     The use case here is per D-14: drift detection compares four
//     `xxh3:<hex>` strings (state.hash, state.sourceHash, fresh-on-disk
//     hash, fresh-source hash) where adversarial collision is out of
//     scope — the threat is bit-rot, partial writes, or upstream churn.
//
// Reference:
//
//   - CLI spec §8.2 (state.FileEntry.hash + state.FileEntry.sourceHash)
//   - PRD D-10 (xxh3 via github.com/zeebo/xxh3, pure-Go, no cgo)
//   - PRD D-14 (dual-hash drift discipline)
//   - STATE-02 (state.schemaVersion + hash format)
package hash

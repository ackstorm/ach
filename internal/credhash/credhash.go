// SPDX-License-Identifier: Apache-2.0

package credhash

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrEmptyPepper is returned by Hash when the pepper is nil or zero-length.
// Hub §16.1 mandates a server-side pepper for every credential hash; the
// fallback to an empty pepper would silently weaken the digest to a public
// SHA-256, so this layer refuses outright. Plan 06's operator main also
// aborts process startup when ACH_CREDENTIAL_HASH_PEPPER is unset (D-09) —
// this check is the second line of defense.
var ErrEmptyPepper = errors.New("credhash: pepper is empty")

// Hash computes HMAC-SHA-256(pepper, plaintext) and returns the digest as a
// lowercase hex-encoded string (64 chars, 32 bytes × 2).
//
// Plaintext is the full bearer key (e.g. "pk_abc…" or "ek_def…"); pepper is
// the server-side secret sourced from the ACH_CREDENTIAL_HASH_PEPPER env var
// per D-09 (and never read by this package — callers pass it as an argument).
//
// Returns ErrEmptyPepper and an empty string when pepper is nil or
// zero-length. NEVER logs, never persists, never panics on adversarial
// input.
func Hash(pepper, plaintext []byte) (string, error) {
	if len(pepper) == 0 {
		return "", ErrEmptyPepper
	}
	h := hmac.New(sha256.New, pepper)
	h.Write(plaintext)
	return hex.EncodeToString(h.Sum(nil)), nil
}

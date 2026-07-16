// SPDX-License-Identifier: Apache-2.0

// Package pepperenv loads and validates the ACH_CREDENTIAL_HASH_PEPPER
// env var per D-09 / Hub §16.1. It lives in a sibling subpackage so the
// parent credhash package can keep its no-os-imports / no-logging
// discipline (T-04-01 mitigation). The pepper byte slice flows by value
// from the operator main to whichever Phase 3 hashing wrapper consumes
// it — the env var is read at this single site, validated, then handed
// off.
//
// Consumers (wired): the pk_ mint path (internal/platformapi/auth/sso.go)
// and the ek_ create path (internal/platformapi/envkeys/handler.go) hash
// the bearer plaintext with credhash.Hash(pepper, plaintext) and persist
// only the digest. The operator main also calls Load() at startup so the
// "still carries placeholder" guard runs every reboot — operators can
// trust the next deployment cycle catches a misconfigured Secret.
//
// This package MUST NOT log the pepper value or write it to any
// observable surface beyond the return byte slice. The error message
// surface mentions only the env var NAME and the placeholder prefix.
package pepperenv

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvVarName is the canonical env var holding the pepper (D-09).
const EnvVarName = "ACH_CREDENTIAL_HASH_PEPPER"

// PlaceholderPrefix is the literal sentinel that ships in the Helm-
// rendered ach-credential-hash-pepper Secret (Plan 08). The operator
// refuses to start when the deployer has not replaced the placeholder
// with a real random value — running with the placeholder would make
// every HMAC-SHA-256 credential hash predictable across deployments.
// This is the runtime half of the Plan 11 verifier B2 contract (the
// static half is the Secret manifest carrying the literal placeholder
// text).
const PlaceholderPrefix = "REPLACE-ME-WITH-RANDOM-"

// ErrMissing is returned when ACH_CREDENTIAL_HASH_PEPPER is unset or
// empty.
var ErrMissing = errors.New("pepperenv: ACH_CREDENTIAL_HASH_PEPPER is unset or empty")

// ErrPlaceholder is returned when ACH_CREDENTIAL_HASH_PEPPER still
// carries the Plan 08 literal placeholder value. Operator main is
// expected to convert this to a fatal exit; the package itself does
// not call os.Exit.
var ErrPlaceholder = errors.New("pepperenv: ACH_CREDENTIAL_HASH_PEPPER still carries the placeholder value (Plan 08 Secret was not replaced before deployment)")

// Load reads the pepper from the configured env var, validates it
// against the Plan 08 placeholder, and returns the value as a byte
// slice (the form credhash.Hash expects). Both error returns wrap the
// underlying sentinel for errors.Is dispatch; neither error string
// includes the pepper value.
//
// Returns:
//   - (pepperBytes, nil) on success.
//   - (nil, ErrMissing) when the env var is unset or empty.
//   - (nil, ErrPlaceholder) when the env var still carries the Plan 08
//     literal placeholder.
//
// The caller (cmd/operator/main.go) is responsible for converting
// either error into a fatal exit. The pepper byte slice MUST NOT be
// logged anywhere; only the env var NAME and the placeholder prefix
// are safe to surface.
func Load() ([]byte, error) {
	v := os.Getenv(EnvVarName)
	if v == "" {
		return nil, fmt.Errorf("%s required (D-09 / Hub §16.1): %w", EnvVarName, ErrMissing)
	}
	if strings.HasPrefix(v, PlaceholderPrefix) {
		return nil, fmt.Errorf("placeholderPrefix=%q: %w", PlaceholderPrefix, ErrPlaceholder)
	}
	return []byte(v), nil
}

// SPDX-License-Identifier: Apache-2.0

// Package dekenv loads the data-encryption key (DEK) used by keycrypt from the
// environment. Mirrors internal/credhash/pepperenv: the value is base64std of
// exactly 32 raw bytes (AES-256). Required by platform-api and the forwarder.
//
// Like pepperenv, this package MUST NOT log the DEK value or write it to any
// observable surface beyond the returned byte slice. Error messages mention
// only the env var NAME, the placeholder prefix, and the decoded length.
package dekenv

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ackstorm/ach/internal/keycrypt"
)

// EnvVarName is the environment variable holding the base64 DEK.
const EnvVarName = "ACH_KEY_ENCRYPTION_KEY"

// PlaceholderPrefix marks an un-replaced template value.
const PlaceholderPrefix = "REPLACE-ME-WITH-RANDOM-"

var (
	// ErrMissing means the env var is unset/empty.
	ErrMissing = fmt.Errorf("%s is not set", EnvVarName)
	// ErrPlaceholder means the template default was not replaced.
	ErrPlaceholder = fmt.Errorf("%s still holds the placeholder value", EnvVarName)
)

// Load reads, base64-decodes, and validates the DEK (must be 32 bytes).
func Load() ([]byte, error) {
	v := os.Getenv(EnvVarName)
	if v == "" {
		return nil, ErrMissing
	}
	if strings.HasPrefix(v, PlaceholderPrefix) {
		return nil, ErrPlaceholder
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	if err != nil {
		return nil, fmt.Errorf("%s must be base64: %w", EnvVarName, err)
	}
	if len(raw) != keycrypt.KeySize {
		return nil, fmt.Errorf("%s decoded to %d bytes: %w", EnvVarName, len(raw), errors.New("must be 32 bytes"))
	}
	return raw, nil
}

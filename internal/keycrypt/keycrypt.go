// SPDX-License-Identifier: Apache-2.0

// Package keycrypt envelope-encrypts secret material at rest with
// AES-256-GCM. The stored form is base64std(version || nonce || ciphertext).
// Used by platform-api (seal on mint) and the forwarder (open on use) to keep
// LiteLLM virtual-key material out of cleartext in Postgres (G3).
package keycrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the required data-encryption-key length (AES-256).
const KeySize = 32

// formatV1 tags the on-disk blob so a future rotation/format can be told apart
// without ambiguity. Single-key only today; multi-key rotation is a P1 follow-up.
const formatV1 byte = 1

var (
	// ErrKeySize is returned when the DEK is not exactly KeySize bytes.
	ErrKeySize = fmt.Errorf("keycrypt: key must be %d bytes", KeySize)
	// ErrFormat is returned when a stored blob is malformed or uses an
	// unknown version byte.
	ErrFormat = errors.New("keycrypt: malformed or unknown ciphertext format")
)

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext and returns base64std(version || nonce || ciphertext).
func Seal(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := []byte{formatV1}
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal. It returns ErrFormat on a malformed blob and a GCM
// authentication error on a wrong key or tampered ciphertext.
func Open(key []byte, blob string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, ErrFormat
	}
	ns := gcm.NonceSize()
	if len(raw) < 1+ns || raw[0] != formatV1 {
		return nil, ErrFormat
	}
	nonce := raw[1 : 1+ns]
	ct := raw[1+ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

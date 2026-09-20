// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"encoding/base64"
)

// NewSessionID returns a base64url-encoded 24-byte random identifier (32
// ASCII chars, no padding): 192 bits, the opaque id for every AS artefact
// (client ids, pendings, codes, refresh tokens, device codes).
func NewSessionID() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

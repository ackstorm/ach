// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadFromDir populates the signer from a directory holding the
// ach-jwt-signing-keys Secret as files (a Helm secret volume): current.kid,
// current.seed and optionally next.kid, next.seed — the same keys the
// forwarder's SecretLoader reads from Secret.Data. Startup-only; there is no
// reload. platform-api uses this so it never needs a Kubernetes client.
func LoadFromDir(s *Ed25519Signer, dir string) error {
	kid, seed, err := readSlotFiles(dir, DataKeyCurrentKid, DataKeyCurrentSeed)
	if err != nil {
		return err
	}
	cur, err := newSignerSlot(kid, seed)
	if err != nil {
		return fmt.Errorf("jwt: current slot in %s: %w", dir, err)
	}
	s.loadCurrent(cur)

	nkid, nseed, err := readSlotFiles(dir, DataKeyNextKid, DataKeyNextSeed)
	if err != nil || nkid == "" {
		s.loadNext(nil) // no rotation pending, or next.* absent
		return nil
	}
	if nxt, err := newSignerSlot(nkid, nseed); err == nil {
		s.loadNext(nxt)
	} else {
		s.loadNext(nil)
	}
	return nil
}

// LoadSeed populates the current slot directly. Used by tests in other
// packages and by nothing else; production paths go through LoadFromDir or
// the SecretLoader.
func LoadSeed(s *Ed25519Signer, kid string, seed []byte) error {
	slot, err := newSignerSlot(kid, seed)
	if err != nil {
		return err
	}
	s.loadCurrent(slot)
	return nil
}

func readSlotFiles(dir, kidFile, seedFile string) (kid string, seed []byte, err error) {
	k, err := os.ReadFile(filepath.Join(dir, kidFile))
	if err != nil {
		return "", nil, err
	}
	seed, err = os.ReadFile(filepath.Join(dir, seedFile))
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(string(k)), seed, nil
}

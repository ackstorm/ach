// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadFromDir populates the signer from a directory holding the
// ach-jwt-signing-keys Secret as files (a Helm secret volume): current.kid,
// current.seed and optionally next.kid, next.seed — the same keys the
// forwarder's SecretLoader reads from Secret.Data. WatchDir re-runs it so a
// rotation reaches a running Pod. platform-api uses this so it never needs a
// Kubernetes client.
func LoadFromDir(s *Ed25519Signer, dir string) error {
	// kubelet swaps a Secret volume by repointing ..data at a fresh
	// directory; reading through the resolved target keeps kid and seed
	// from straddling a swap.
	if d, err := filepath.EvalSymlinks(filepath.Join(dir, "..data")); err == nil {
		dir = d
	}
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

// DirReloadInterval is how often WatchDir re-reads the mounted keys; kubelet
// itself only refreshes a Secret volume about once a minute.
const DirReloadInterval = 30 * time.Second

// WatchDir re-reads dir every interval until ctx ends, so a key rotation in
// the mounted Secret reaches the signer without a restart (review #8; the
// forwarder gets the same from its Secret informer). A failed read keeps the
// keys already loaded.
func WatchDir(ctx context.Context, s *Ed25519Signer, dir string, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := LoadFromDir(s, dir); err != nil {
				log.Warn("jwt: reload signing keys failed; keeping the loaded keys", "dir", dir, "error", err)
			}
		}
	}
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

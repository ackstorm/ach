// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/go-logr/logr/testr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestSecret(curKid string, curSeed []byte, nxtKid string, nxtSeed []byte) *corev1.Secret {
	data := map[string][]byte{}
	if curKid != "" || curSeed != nil {
		data[DataKeyCurrentKid] = []byte(curKid)
		data[DataKeyCurrentSeed] = curSeed
	}
	if nxtKid != "" || nxtSeed != nil {
		data[DataKeyNextKid] = []byte(nxtKid)
		data[DataKeyNextSeed] = nxtSeed
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "ach-system",
		},
		Data: data,
	}
}

// SL1: NewSecretLoader returns a non-nil loader.
func TestSecretLoader_New_NonNil(t *testing.T) {
	signer := NewEd25519Signer()
	log := testr.New(t)
	loader := NewSecretLoader(signer, "ach-system", SecretName, log)
	if loader == nil {
		t.Fatal("NewSecretLoader returned nil")
	}
	if loader.signer != signer {
		t.Error("loader.signer != signer")
	}
}

// SL1b: NewSecretLoader panics on nil signer (programmer error).
func TestSecretLoader_New_NilSignerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil signer; got none")
		}
	}()
	_ = NewSecretLoader(nil, "ach-system", SecretName, testr.New(t))
}

// SL2: LoadOnce on Secret with empty current.kid returns error wrapping ErrEmptyKid.
func TestSecretLoader_LoadOnce_EmptyKid(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	sec := newTestSecret("", freshSeed(t), "", nil)
	err := loader.LoadOnce(sec)
	if err == nil {
		t.Fatal("expected error on empty current.kid")
	}
	if !errors.Is(err, ErrEmptyKid) {
		t.Errorf("expected error wrapping ErrEmptyKid; got %v", err)
	}
	if signer.Loaded() {
		t.Error("signer.Loaded() should be false after refused load")
	}
}

// SL3: LoadOnce on Secret with current.seed wrong length returns error wrapping ErrEmptySeed.
func TestSecretLoader_LoadOnce_WrongSeedLength(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	sec := newTestSecret("k1", make([]byte, 31), "", nil)
	err := loader.LoadOnce(sec)
	if err == nil {
		t.Fatal("expected error on 31-byte seed")
	}
	if !errors.Is(err, ErrEmptySeed) {
		t.Errorf("expected error wrapping ErrEmptySeed; got %v", err)
	}
	if signer.Loaded() {
		t.Error("signer.Loaded() should be false after refused load")
	}
}

// SL4: LoadOnce on valid current returns nil, signer loaded, Sign succeeds.
func TestSecretLoader_LoadOnce_CurrentOnly(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	sec := newTestSecret("k-cur", freshSeed(t), "", nil)
	if err := loader.LoadOnce(sec); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	if !signer.Loaded() {
		t.Fatal("signer.Loaded() should be true")
	}
	tok, err := signer.Sign(context.Background(), Claims{Iss: "https://h", Sub: "ns/u", Aud: "mcp:x"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" {
		t.Error("Sign returned empty token")
	}
	if got := len(signer.JWKS()); got != 1 {
		t.Errorf("JWKS len = %d; want 1", got)
	}
}

// SL5: LoadOnce on valid current + next loads both slots.
func TestSecretLoader_LoadOnce_BothSlots(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	sec := newTestSecret("k-cur", freshSeed(t), "k-nxt", freshSeed(t))
	if err := loader.LoadOnce(sec); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	keys := signer.JWKS()
	if len(keys) != 2 {
		t.Fatalf("JWKS len = %d; want 2", len(keys))
	}
	if keys[0].Kid != "k-cur" || keys[1].Kid != "k-nxt" {
		t.Errorf("JWKS kid order = [%s, %s]; want [k-cur, k-nxt]", keys[0].Kid, keys[1].Kid)
	}
}

// SL5b: LoadOnce with malformed next (empty kid+empty seed) only loads current.
func TestSecretLoader_LoadOnce_NextAbsent(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	sec := newTestSecret("k-cur", freshSeed(t), "", nil)
	if err := loader.LoadOnce(sec); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	if got := len(signer.JWKS()); got != 1 {
		t.Errorf("JWKS len = %d; want 1", got)
	}
}

// SL5c: LoadOnce with malformed next slot (good kid, wrong seed) clears next + logs, returns nil.
func TestSecretLoader_LoadOnce_NextMalformed(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	sec := newTestSecret("k-cur", freshSeed(t), "k-nxt", make([]byte, 31))
	if err := loader.LoadOnce(sec); err != nil {
		t.Fatalf("LoadOnce: %v (want nil — next-malformed is non-fatal)", err)
	}
	if got := len(signer.JWKS()); got != 1 {
		t.Errorf("JWKS len = %d; want 1 (next was malformed, must be cleared)", got)
	}
}

// SL6: Reload atomic swap correctness — concurrent Sign + Reload, no panic, no torn slot.
func TestSecretLoader_Reload_ConcurrentSign(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k0", freshSeed(t), "", nil)); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	const iters = 100
	var wg sync.WaitGroup
	wg.Add(2)

	signErrs := make(chan error, iters)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := signer.Sign(context.Background(), Claims{Iss: "h", Sub: "s", Aud: "a"}); err != nil {
				signErrs <- err
				return
			}
		}
	}()

	reloadErrs := make(chan error, iters)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			sec := newTestSecret("k-rotated", freshSeed(t), "", nil)
			if err := loader.Reload(sec); err != nil {
				reloadErrs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(signErrs)
	close(reloadErrs)
	if err, ok := <-signErrs; ok {
		t.Fatalf("Sign error during concurrent reload: %v", err)
	}
	if err, ok := <-reloadErrs; ok {
		t.Fatalf("Reload error: %v", err)
	}
	if !signer.Loaded() {
		t.Error("signer.Loaded() == false after concurrent reload")
	}
}

// SL7: Reload with malformed current returns error, keeps prior valid slot.
func TestSecretLoader_Reload_MalformedKeepsPrior(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k-good", freshSeed(t), "", nil)); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	priorKid := signer.JWKS()[0].Kid

	bad := newTestSecret("", freshSeed(t), "", nil)
	err := loader.Reload(bad)
	if err == nil {
		t.Fatal("expected error on Reload with empty current.kid")
	}
	if !errors.Is(err, ErrEmptyKid) {
		t.Errorf("expected error wrapping ErrEmptyKid; got %v", err)
	}
	if !signer.Loaded() {
		t.Error("signer.Loaded() should still be true after refused reload")
	}
	if got := signer.JWKS()[0].Kid; got != priorKid {
		t.Errorf("after refused reload kid = %s; want prior %s", got, priorKid)
	}
	// Sign with prior slot still works.
	if _, err := signer.Sign(context.Background(), Claims{Iss: "h", Sub: "s", Aud: "a"}); err != nil {
		t.Errorf("Sign with prior slot: %v", err)
	}
}

// SL8: Reload that clears next (empty next.kid in updated Secret) — JWKS drops to 1 entry.
func TestSecretLoader_Reload_ClearsNext(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k-cur", freshSeed(t), "k-nxt", freshSeed(t))); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	if got := len(signer.JWKS()); got != 2 {
		t.Fatalf("after LoadOnce JWKS len = %d; want 2", got)
	}
	// Updated Secret no longer carries next.
	if err := loader.Reload(newTestSecret("k-cur", freshSeed(t), "", nil)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := len(signer.JWKS()); got != 1 {
		t.Errorf("after Reload-without-next JWKS len = %d; want 1", got)
	}
}

// SL-extra: LoadOnce(nil) returns error.
func TestSecretLoader_LoadOnce_NilSecret(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	err := loader.LoadOnce(nil)
	if err == nil {
		t.Fatal("expected error on nil Secret")
	}
	if signer.Loaded() {
		t.Error("signer should not be loaded after nil Secret")
	}
}

// SL-extra: Reload(nil) returns error and keeps prior slot.
func TestSecretLoader_Reload_NilSecret(t *testing.T) {
	signer := NewEd25519Signer()
	loader := NewSecretLoader(signer, "ach-system", SecretName, testr.New(t))
	if err := loader.LoadOnce(newTestSecret("k-good", freshSeed(t), "", nil)); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	if err := loader.Reload(nil); err == nil {
		t.Fatal("expected error on Reload(nil)")
	}
	if !signer.Loaded() {
		t.Error("signer should remain loaded after Reload(nil)")
	}
}

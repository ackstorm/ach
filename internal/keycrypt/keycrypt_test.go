// SPDX-License-Identifier: Apache-2.0

package keycrypt

import (
	"bytes"
	"testing"
)

func testKey() []byte { // 32 bytes
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestSealOpen_Roundtrip(t *testing.T) {
	k := testKey()
	pt := []byte("sk-abc123-secret")
	blob, err := Seal(k, pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains([]byte(blob), pt) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := Open(k, blob)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
}

func TestSeal_NonDeterministic(t *testing.T) {
	k := testKey()
	a, _ := Seal(k, []byte("x"))
	b, _ := Seal(k, []byte("x"))
	if a == b {
		t.Fatal("two seals identical — nonce not random")
	}
}

func TestOpen_TamperFails(t *testing.T) {
	k := testKey()
	blob, _ := Seal(k, []byte("payload"))
	// Flip the final base64 char (it lands in the GCM tag region), choosing a
	// guaranteed-different rune. Tampering the *first* char is a no-op here: the
	// version byte 0x01 base64-encodes to a leading 'A', so "A"+blob[1:] would
	// reproduce the original unchanged.
	last := len(blob) - 1
	repl := byte('A')
	if blob[last] == 'A' {
		repl = 'B'
	}
	bad := blob[:last] + string(repl)
	if _, err := Open(k, bad); err == nil {
		t.Fatal("tampered ciphertext opened without error")
	}
}

func TestOpen_WrongKeyFails(t *testing.T) {
	blob, _ := Seal(testKey(), []byte("payload"))
	other := make([]byte, KeySize) // all zeros
	if _, err := Open(other, blob); err == nil {
		t.Fatal("wrong key opened ciphertext")
	}
}

func TestSeal_BadKeySize(t *testing.T) {
	if _, err := Seal(make([]byte, 16), []byte("x")); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

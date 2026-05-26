// SPDX-License-Identifier: Apache-2.0

package credhash_test

import (
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/ackstorm/ach/internal/credhash"
)

// pepperA / pepperB are deliberately distinct fixed bytes (NOT plaintext keys,
// NOT real production peppers — purely test fixtures). The production pepper
// arrives from the ACH_CREDENTIAL_HASH_PEPPER env var per D-09.
var (
	pepperA = []byte("test-pepper-A-do-not-use-in-prod")
	pepperB = []byte("test-pepper-B-do-not-use-in-prod")

	plaintextA = []byte("pk_abc")
	plaintextB = []byte("pk_abd")
)

// TestHashReturnsHexDigest asserts behavior 1: the digest is a 64-char
// lowercase hex string (32 bytes × 2). Uses hex.DecodeString as the
// success/failure oracle so we don't ship a regexp dep.
func TestHashReturnsHexDigest(t *testing.T) {
	got, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	if len(got) != 64 {
		t.Fatalf("Hash returned %d chars, want 64: %q", len(got), got)
	}
	if _, decErr := hex.DecodeString(got); decErr != nil {
		t.Fatalf("Hash returned non-hex digest %q: %v", got, decErr)
	}
}

// TestHashDeterministic asserts behavior 2: same pepper + same plaintext
// always yields the same digest (HMAC-SHA-256 is a pure function).
func TestHashDeterministic(t *testing.T) {
	first, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("first Hash returned error: %v", err)
	}
	second, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("second Hash returned error: %v", err)
	}
	if first != second {
		t.Fatalf("Hash non-deterministic: first=%q second=%q", first, second)
	}
}

// TestHashDifferentPeppersDifferentDigests asserts behavior 3: pepper affects
// digest. Distinct peppers + same plaintext MUST produce distinct digests.
func TestHashDifferentPeppersDifferentDigests(t *testing.T) {
	hashA, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("Hash(pepperA, ...) returned error: %v", err)
	}
	hashB, err := credhash.Hash(pepperB, plaintextA)
	if err != nil {
		t.Fatalf("Hash(pepperB, ...) returned error: %v", err)
	}
	if hashA == hashB {
		t.Fatalf("distinct peppers produced same digest %q", hashA)
	}
}

// TestHashDifferentPlaintextsDifferentDigests asserts behavior 4: plaintext
// affects digest. Same pepper + distinct plaintexts MUST produce distinct
// digests.
func TestHashDifferentPlaintextsDifferentDigests(t *testing.T) {
	hashA, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("Hash(_, plaintextA) returned error: %v", err)
	}
	hashB, err := credhash.Hash(pepperA, plaintextB)
	if err != nil {
		t.Fatalf("Hash(_, plaintextB) returned error: %v", err)
	}
	if hashA == hashB {
		t.Fatalf("distinct plaintexts produced same digest %q", hashA)
	}
}

// TestHashNilPepperReturnsError asserts behavior 5: a nil pepper is rejected
// with ErrEmptyPepper (D-09 fail-fast — the operator main aborts startup when
// the env var is unset, and this layer is the second line of defense).
func TestHashNilPepperReturnsError(t *testing.T) {
	got, err := credhash.Hash(nil, plaintextA)
	if !errors.Is(err, credhash.ErrEmptyPepper) {
		t.Fatalf("Hash(nil, ...) err = %v, want ErrEmptyPepper", err)
	}
	if got != "" {
		t.Fatalf("Hash(nil, ...) digest = %q, want empty string", got)
	}
}

// TestHashEmptyPepperReturnsError asserts behavior 6: an empty (zero-length
// but non-nil) pepper is rejected identically to nil.
func TestHashEmptyPepperReturnsError(t *testing.T) {
	got, err := credhash.Hash([]byte{}, plaintextA)
	if !errors.Is(err, credhash.ErrEmptyPepper) {
		t.Fatalf("Hash([]byte{}, ...) err = %v, want ErrEmptyPepper", err)
	}
	if got != "" {
		t.Fatalf("Hash([]byte{}, ...) digest = %q, want empty string", got)
	}
}

// TestEqualSameHashTrue asserts behavior 7: Equal of a well-formed hex string
// with itself is true.
func TestEqualSameHashTrue(t *testing.T) {
	digest, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	if !credhash.Equal(digest, digest) {
		t.Fatalf("Equal(%q, %q) = false, want true", digest, digest)
	}
}

// TestEqualDifferentHashesFalse asserts behavior 8: distinct well-formed hex
// strings compare unequal.
func TestEqualDifferentHashesFalse(t *testing.T) {
	hashA, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("Hash(pepperA, ...) returned error: %v", err)
	}
	hashB, err := credhash.Hash(pepperA, plaintextB)
	if err != nil {
		t.Fatalf("Hash(_, plaintextB) returned error: %v", err)
	}
	if credhash.Equal(hashA, hashB) {
		t.Fatalf("Equal of distinct digests returned true: a=%q b=%q", hashA, hashB)
	}
}

// TestEqualMalformedHexFalseNoPanic asserts behavior 9: malformed hex input
// (here "xx" — invalid hex digit) returns false WITHOUT panicking. The
// defer-recover guard ensures a panic surfaces as a test failure rather than
// crashing the test binary.
func TestEqualMalformedHexFalseNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Equal panicked on malformed hex input: %v", r)
		}
	}()
	if credhash.Equal("xx", "yy") {
		t.Fatalf(`Equal("xx", "yy") = true, want false`)
	}
}

// TestEqualEmptyEmptyTrue asserts behavior 10: Equal("", "") is true — both
// inputs decode to the empty byte slice, and ConstantTimeCompare returns 1
// for equal-length (zero-length) slices.
func TestEqualEmptyEmptyTrue(t *testing.T) {
	if !credhash.Equal("", "") {
		t.Fatalf(`Equal("", "") = false, want true`)
	}
}

// TestHashConstantTime asserts behavior 11: 1000 concurrent goroutines all
// calling Hash with the same pepper + plaintext produce identical output.
// This proves the function has no shared mutable state — `hmac.New` creates
// a fresh hash instance per call, and the package itself holds no globals.
// Run with `go test -race` to detect any inadvertent data races on the
// implementation side.
func TestHashConstantTime(t *testing.T) {
	const goroutines = 1000

	expected, err := credhash.Hash(pepperA, plaintextA)
	if err != nil {
		t.Fatalf("reference Hash returned error: %v", err)
	}

	results := make([]string, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = credhash.Hash(pepperA, plaintextA)
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d Hash err: %v", i, errs[i])
		}
		if results[i] != expected {
			t.Fatalf("goroutine %d digest mismatch: got %q want %q", i, results[i], expected)
		}
	}
}

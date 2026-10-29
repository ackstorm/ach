// SPDX-License-Identifier: Apache-2.0

package hash_test

import (
	"bytes"
	"crypto/rand"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ackstorm/ach/internal/cli/hash"
)

// formatRE matches the canonical "xxh3:<32 lowercase hex chars>" output shape
// per CLI spec §8.2 + D-10 + 'Claude's Discretion' default format. The state
// engine (07-W1-02) and extract package (07-W2-*) match on exactly this
// shape — drift detection's four-way string compare assumes every input is
// produced by this package and therefore conforms to this regexp.
var formatRE = regexp.MustCompile(`^xxh3:[0-9a-f]{32}$`)

// TestHashEmptyInputFormatInvariant asserts behavior: streaming an empty
// reader still produces the canonical "xxh3:" prefix + 32 lowercase hex
// chars. Empty input is a real path — extract may end up writing zero-byte
// files when an upstream blob is empty, and state must record a valid
// digest for them.
func TestHashEmptyInputFormatInvariant(t *testing.T) {
	got, err := hash.Hash(bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("Hash(empty) returned error: %v", err)
	}
	if !formatRE.MatchString(got) {
		t.Fatalf("Hash(empty) = %q, want match %q", got, formatRE.String())
	}
}

// TestHashBytesEmptyInputFormatInvariant asserts the same invariant for the
// non-streaming convenience form.
func TestHashBytesEmptyInputFormatInvariant(t *testing.T) {
	got := hash.HashBytes(nil)
	if !formatRE.MatchString(got) {
		t.Fatalf("HashBytes(nil) = %q, want match %q", got, formatRE.String())
	}
}

// TestHashAbcFormatInvariant asserts the invariant on a small known input
// ("abc"). This is intentionally NOT a hard-coded vector test — the planner
// (07-W1-04-PLAN.md <behavior>) explicitly forbids inventing vectors. We
// assert the format invariant and the Hash↔HashBytes consistency on the
// same input; the hash library owns the bit-exact value.
func TestHashAbcFormatInvariant(t *testing.T) {
	got, err := hash.Hash(bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("Hash(\"abc\") returned error: %v", err)
	}
	if !formatRE.MatchString(got) {
		t.Fatalf("Hash(\"abc\") = %q, want match %q", got, formatRE.String())
	}
}

// TestHashBytesAbcConsistency asserts behavior: streaming and non-streaming
// forms produce identical output for the same input ("abc"). This is the
// load-bearing property — the state engine sometimes has an io.Reader (a
// staged file mid-extract) and sometimes has []byte (a synthesized adapter
// output), and the dual-hash drift compare requires both code paths to
// produce strings that compare equal byte-for-byte.
func TestHashBytesAbcConsistency(t *testing.T) {
	streamed, err := hash.Hash(bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("Hash(\"abc\") returned error: %v", err)
	}
	memHash := hash.HashBytes([]byte("abc"))
	if streamed != memHash {
		t.Fatalf("Hash and HashBytes disagree on \"abc\": stream=%q mem=%q",
			streamed, memHash)
	}
}

// TestHashLargeInputAgreesWithHashBytes asserts behavior: on a 1MB random
// buffer, streaming via Hash and one-shot via HashBytes return the same
// digest. This is a regression guard for any future refactor that splits
// the streaming path away from xxh3.Hash128's all-in-one path.
func TestHashLargeInputAgreesWithHashBytes(t *testing.T) {
	buf := make([]byte, 1<<20) // 1 MiB
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("seed buf via crypto/rand: %v", err)
	}
	streamed, err := hash.Hash(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("Hash(1MB) returned error: %v", err)
	}
	memHash := hash.HashBytes(buf)
	if streamed != memHash {
		t.Fatalf("Hash and HashBytes disagree on 1MB random buf: stream=%q mem=%q",
			streamed, memHash)
	}
	if !formatRE.MatchString(streamed) {
		t.Fatalf("Hash(1MB) = %q, want match %q", streamed, formatRE.String())
	}
}

// TestHashDeterministicAcrossRuns asserts behavior: hashing the same input
// twice in the same process yields equal digests. xxh3 is a pure function;
// the wrapper holds no state across calls.
func TestHashDeterministicAcrossRuns(t *testing.T) {
	first, err := hash.Hash(bytes.NewReader([]byte("ach")))
	if err != nil {
		t.Fatalf("first Hash returned error: %v", err)
	}
	second, err := hash.Hash(bytes.NewReader([]byte("ach")))
	if err != nil {
		t.Fatalf("second Hash returned error: %v", err)
	}
	if first != second {
		t.Fatalf("Hash non-deterministic: first=%q second=%q", first, second)
	}
}

// TestHashBytesDeterministicAcrossRuns asserts the same property for the
// in-memory form.
func TestHashBytesDeterministicAcrossRuns(t *testing.T) {
	first := hash.HashBytes([]byte("ach"))
	second := hash.HashBytes([]byte("ach"))
	if first != second {
		t.Fatalf("HashBytes non-deterministic: first=%q second=%q", first, second)
	}
}

// TestHashBytesFormatInvariant asserts the canonical format holds across a
// table of representative inputs. The state engine treats the output as an
// opaque string, but downstream tooling (CLAUDE.md failure-mode log,
// `ach-cli hydrate --verbose`, future debug dumps) reads it visually and
// expects the literal "xxh3:" prefix.
func TestHashBytesFormatInvariant(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("a"),
		[]byte("abc"),
		[]byte("the quick brown fox jumps over the lazy dog"),
		bytes.Repeat([]byte{0xff}, 4096),
		bytes.Repeat([]byte{0x00}, 65536),
	}
	for _, in := range cases {
		got := hash.HashBytes(in)
		if !formatRE.MatchString(got) {
			t.Fatalf("HashBytes(len=%d) = %q, want match %q",
				len(in), got, formatRE.String())
		}
		if !strings.HasPrefix(got, "xxh3:") {
			t.Fatalf("HashBytes(len=%d) = %q, missing 'xxh3:' prefix",
				len(in), got)
		}
	}
}

// TestHashDifferentInputsDifferentDigests asserts behavior: two distinct
// inputs yield two distinct digests. This is the basic content-addressing
// property the state engine relies on for change detection.
func TestHashDifferentInputsDifferentDigests(t *testing.T) {
	a := hash.HashBytes([]byte("plugin-a"))
	b := hash.HashBytes([]byte("plugin-b"))
	if a == b {
		t.Fatalf("distinct inputs produced same digest: a=%q b=%q", a, b)
	}
}

// TestHashConcurrentSafe asserts behavior: 1000 concurrent goroutines all
// hashing the same input produce identical output (no shared mutable state
// in the wrapper; each call constructs its own xxh3.Hasher via xxh3.New).
// Run with `go test -race` to confirm no data races on the implementation
// side — mirrors internal/credhash/credhash_test.go TestHashConstantTime.
func TestHashConcurrentSafe(t *testing.T) {
	const goroutines = 1000
	input := []byte("ach-concurrent-input")

	expected := hash.HashBytes(input)

	results := make([]string, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = hash.Hash(bytes.NewReader(input))
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d Hash err: %v", i, errs[i])
		}
		if results[i] != expected {
			t.Fatalf("goroutine %d digest mismatch: got %q want %q",
				i, results[i], expected)
		}
	}
}

// failingReader returns a fixed error after a partial read; the streaming
// Hash path must surface it as an error return rather than swallowing it.
type failingReader struct{ err error }

func (f *failingReader) Read(_ []byte) (int, error) { return 0, f.err }

// TestHashIOErrorSurfaced asserts behavior: when the underlying io.Reader
// returns an error, Hash propagates it (empty string + non-nil error). The
// state engine treats this as a fatal extract failure — silently swallowing
// the error and returning a digest of partial data would lie about content.
func TestHashIOErrorSurfaced(t *testing.T) {
	sentinel := errReader("simulated I/O failure")
	got, err := hash.Hash(&failingReader{err: sentinel})
	if err == nil {
		t.Fatalf("Hash on failing reader returned nil err, want non-nil")
	}
	if got != "" {
		t.Fatalf("Hash on failing reader returned %q, want empty string", got)
	}
}

// errReader is a tiny error type so we don't import errors just for this.
type errReader string

func (e errReader) Error() string { return string(e) }

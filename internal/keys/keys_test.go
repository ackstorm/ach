// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// NewBearer tests (Task 2)
// ---------------------------------------------------------------------

// bearerPkRe matches pk_<26 base32-lowercase no-pad chars>.
//
// RFC 4648 base32 alphabet uplower-case is [A-Z2-7]; lowercased becomes
// [a-z2-7]. We constrain the regex to that exact set so any drift from
// the encoder choice (e.g. accidental Crockford or hex) trips the test.
var bearerPkRe = regexp.MustCompile(`^pk_[a-z2-7]{26}$`)
var bearerEkRe = regexp.MustCompile(`^ek_[a-z2-7]{26}$`)

func TestNewBearer_PkShape(t *testing.T) {
	got, err := NewBearer(PrefixPk)
	if err != nil {
		t.Fatalf("NewBearer(PrefixPk) returned err: %v", err)
	}
	if !bearerPkRe.MatchString(got) {
		t.Fatalf("NewBearer(PrefixPk) = %q; want match %s", got, bearerPkRe)
	}
	if !strings.HasPrefix(got, "pk_") {
		t.Fatalf("NewBearer(PrefixPk) = %q; want pk_ prefix", got)
	}
	if len(got) != 3+26 {
		t.Fatalf("NewBearer(PrefixPk) len = %d; want %d", len(got), 3+26)
	}
}

func TestNewBearer_EkShape(t *testing.T) {
	got, err := NewBearer(PrefixEk)
	if err != nil {
		t.Fatalf("NewBearer(PrefixEk) returned err: %v", err)
	}
	if !bearerEkRe.MatchString(got) {
		t.Fatalf("NewBearer(PrefixEk) = %q; want match %s", got, bearerEkRe)
	}
	if !strings.HasPrefix(got, "ek_") {
		t.Fatalf("NewBearer(PrefixEk) = %q; want ek_ prefix", got)
	}
	if len(got) != 3+26 {
		t.Fatalf("NewBearer(PrefixEk) len = %d; want %d", len(got), 3+26)
	}
}

func TestNewBearer_Uniqueness(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		v, err := NewBearer(PrefixPk)
		if err != nil {
			t.Fatalf("iter %d: NewBearer err: %v", i, err)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("iter %d: NewBearer returned duplicate %q (entropy bug?)", i, v)
		}
		seen[v] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("got %d unique bearers; want %d", len(seen), n)
	}
}

func TestNewBearer_InvalidPrefix(t *testing.T) {
	// Use a value that is NOT one of the two exported BearerPrefix constants.
	_, err := NewBearer(BearerPrefix("xx_"))
	if err == nil {
		t.Fatalf("NewBearer(\"xx_\") returned nil err; want ErrInvalidPrefix")
	}
	if !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("NewBearer(\"xx_\") err = %v; want ErrInvalidPrefix", err)
	}
	// Also reject the keyID prefixes (pkid_/ekid_) at the bearer entrypoint.
	if _, err := NewBearer(BearerPrefix(PkidKeyIDPrefix)); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("NewBearer(pkid_) err = %v; want ErrInvalidPrefix", err)
	}
	if _, err := NewBearer(BearerPrefix(EkidKeyIDPrefix)); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("NewBearer(ekid_) err = %v; want ErrInvalidPrefix", err)
	}
	if _, err := NewBearer(BearerPrefix("")); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("NewBearer(\"\") err = %v; want ErrInvalidPrefix", err)
	}
}

func TestNewBearer_AlphabetIsBase32Lower(t *testing.T) {
	// Tighter than the per-shape regexes: assert every char of the suffix
	// is in [a-z2-7] (RFC 4648 base32 no-pad alphabet, lowercased).
	allowed := regexp.MustCompile(`^[a-z2-7]+$`)
	for i := 0; i < 100; i++ {
		v, err := NewBearer(PrefixPk)
		if err != nil {
			t.Fatalf("iter %d: NewBearer err: %v", i, err)
		}
		suffix := strings.TrimPrefix(v, "pk_")
		if len(suffix) != 26 {
			t.Fatalf("iter %d: suffix len = %d; want 26", i, len(suffix))
		}
		if !allowed.MatchString(suffix) {
			t.Fatalf("iter %d: suffix %q has chars outside [a-z2-7]", i, suffix)
		}
	}
}

func TestNewBearer_ConstantsHaveExpectedValues(t *testing.T) {
	if PkBearerPrefix != "pk_" {
		t.Errorf("PkBearerPrefix = %q; want %q", PkBearerPrefix, "pk_")
	}
	if EkBearerPrefix != "ek_" {
		t.Errorf("EkBearerPrefix = %q; want %q", EkBearerPrefix, "ek_")
	}
	if PkidKeyIDPrefix != "pkid_" {
		t.Errorf("PkidKeyIDPrefix = %q; want %q", PkidKeyIDPrefix, "pkid_")
	}
	if EkidKeyIDPrefix != "ekid_" {
		t.Errorf("EkidKeyIDPrefix = %q; want %q", EkidKeyIDPrefix, "ekid_")
	}
	if string(PrefixPk) != PkBearerPrefix {
		t.Errorf("PrefixPk = %q; want %q", PrefixPk, PkBearerPrefix)
	}
	if string(PrefixEk) != EkBearerPrefix {
		t.Errorf("PrefixEk = %q; want %q", PrefixEk, EkBearerPrefix)
	}
}

// ---------------------------------------------------------------------
// NewKeyID tests (Task 3)
// ---------------------------------------------------------------------

// keyIDPkidRe / keyIDEkidRe match the lowercased Crockford base32 ULID.
// Crockford base32 alphabet is [0123456789abcdefghjkmnpqrstvwxyz] when
// lowercased (excludes i, l, o, u); we use [0-9a-z] as a permissive
// upper bound — the ulid library guarantees Crockford output.
var keyIDPkidRe = regexp.MustCompile(`^pkid_[0-9a-z]{26}$`)
var keyIDEkidRe = regexp.MustCompile(`^ekid_[0-9a-z]{26}$`)

func TestNewKeyID_PkidShape(t *testing.T) {
	got, err := NewKeyID(PrefixPkid)
	if err != nil {
		t.Fatalf("NewKeyID(PrefixPkid) returned err: %v", err)
	}
	if !keyIDPkidRe.MatchString(got) {
		t.Fatalf("NewKeyID(PrefixPkid) = %q; want match %s", got, keyIDPkidRe)
	}
	if len(got) != 5+26 {
		t.Fatalf("NewKeyID(PrefixPkid) len = %d; want %d", len(got), 5+26)
	}
}

func TestNewKeyID_EkidShape(t *testing.T) {
	got, err := NewKeyID(PrefixEkid)
	if err != nil {
		t.Fatalf("NewKeyID(PrefixEkid) returned err: %v", err)
	}
	if !keyIDEkidRe.MatchString(got) {
		t.Fatalf("NewKeyID(PrefixEkid) = %q; want match %s", got, keyIDEkidRe)
	}
	if len(got) != 5+26 {
		t.Fatalf("NewKeyID(PrefixEkid) len = %d; want %d", len(got), 5+26)
	}
}

func TestNewKeyID_TimeOrdering(t *testing.T) {
	// ULID's leading 48 bits are a wall-clock timestamp; within the same
	// millisecond ULID's monotonic entropy ensures the second draw
	// lexicographically >= the first. We compare suffix-only (strip the
	// pkid_ prefix) so the prefix doesn't dominate the ordering check.
	a, err := NewKeyID(PrefixPkid)
	if err != nil {
		t.Fatalf("first NewKeyID err: %v", err)
	}
	b, err := NewKeyID(PrefixPkid)
	if err != nil {
		t.Fatalf("second NewKeyID err: %v", err)
	}
	aSuffix := strings.TrimPrefix(a, "pkid_")
	bSuffix := strings.TrimPrefix(b, "pkid_")
	if !(bSuffix >= aSuffix) {
		t.Fatalf("ULID monotonicity broken: a=%q b=%q (b should be >= a)", a, b)
	}
}

func TestNewKeyID_Uniqueness(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		v, err := NewKeyID(PrefixEkid)
		if err != nil {
			t.Fatalf("iter %d: NewKeyID err: %v", i, err)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("iter %d: NewKeyID returned duplicate %q", i, v)
		}
		seen[v] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("got %d unique keyIDs; want %d", len(seen), n)
	}
}

func TestNewKeyID_InvalidPrefix(t *testing.T) {
	for _, p := range []KeyIDPrefix{KeyIDPrefix(""), KeyIDPrefix("foo_"), KeyIDPrefix(PkBearerPrefix), KeyIDPrefix(EkBearerPrefix)} {
		_, err := NewKeyID(p)
		if !errors.Is(err, ErrInvalidPrefix) {
			t.Errorf("NewKeyID(%q) err = %v; want ErrInvalidPrefix", p, err)
		}
	}
}

// ---------------------------------------------------------------------
// ClassifyBearer tests (Task 3)
// ---------------------------------------------------------------------

func TestClassifyBearer_PkAccept(t *testing.T) {
	// Generate a real plaintext so the alphabet is guaranteed correct.
	plaintext, err := NewBearer(PrefixPk)
	if err != nil {
		t.Fatalf("NewBearer: %v", err)
	}
	got, err := ClassifyBearer(plaintext)
	if err != nil {
		t.Fatalf("ClassifyBearer(%q) err = %v; want nil", plaintext, err)
	}
	if got != PrefixPk {
		t.Fatalf("ClassifyBearer(%q) = %q; want PrefixPk", plaintext, got)
	}
}

func TestClassifyBearer_EkAccept(t *testing.T) {
	plaintext, err := NewBearer(PrefixEk)
	if err != nil {
		t.Fatalf("NewBearer: %v", err)
	}
	got, err := ClassifyBearer(plaintext)
	if err != nil {
		t.Fatalf("ClassifyBearer(%q) err = %v; want nil", plaintext, err)
	}
	if got != PrefixEk {
		t.Fatalf("ClassifyBearer(%q) = %q; want PrefixEk", plaintext, got)
	}
}

func TestClassifyBearer_KeyIDsRejected(t *testing.T) {
	// keyIDs are NOT bearer plaintexts; ClassifyBearer must reject them
	// even when their lengths happen to be valid base32 candidates.
	for _, in := range []string{
		"pkid_01h7azv3jryr2gh3kdpr3y0rkw",
		"ekid_01h7azv3jryr2gh3kdpr3y0rkw",
	} {
		got, err := ClassifyBearer(in)
		if !errors.Is(err, ErrInvalidBearer) {
			t.Errorf("ClassifyBearer(%q) err = %v; want ErrInvalidBearer", in, err)
		}
		if got != "" {
			t.Errorf("ClassifyBearer(%q) prefix = %q; want zero", in, got)
		}
	}
}

func TestClassifyBearer_EmptyRejected(t *testing.T) {
	got, err := ClassifyBearer("")
	if !errors.Is(err, ErrInvalidBearer) {
		t.Fatalf("ClassifyBearer(\"\") err = %v; want ErrInvalidBearer", err)
	}
	if got != "" {
		t.Fatalf("ClassifyBearer(\"\") prefix = %q; want zero", got)
	}
}

func TestClassifyBearer_WrongLengthRejected(t *testing.T) {
	for _, in := range []string{
		"pk_abc",
		"ek_abc",
		"pk_",
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaaaa", // 27 chars after prefix
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaa",   // 25 chars after prefix
	} {
		got, err := ClassifyBearer(in)
		if !errors.Is(err, ErrInvalidBearer) {
			t.Errorf("ClassifyBearer(%q) err = %v; want ErrInvalidBearer", in, err)
		}
		if got != "" {
			t.Errorf("ClassifyBearer(%q) prefix = %q; want zero", in, got)
		}
	}
}

func TestClassifyBearer_UppercaseRejected(t *testing.T) {
	// Get a valid lowercase suffix, then upper-case it / the prefix.
	plaintext, err := NewBearer(PrefixPk)
	if err != nil {
		t.Fatalf("NewBearer: %v", err)
	}
	for _, in := range []string{
		"PK_" + plaintext[3:],                  // upper prefix
		strings.ToUpper(plaintext),             // all upper
		"pk_" + strings.ToUpper(plaintext[3:]), // upper suffix only
	} {
		got, err := ClassifyBearer(in)
		if !errors.Is(err, ErrInvalidBearer) {
			t.Errorf("ClassifyBearer(%q) err = %v; want ErrInvalidBearer", in, err)
		}
		if got != "" {
			t.Errorf("ClassifyBearer(%q) prefix = %q; want zero", in, got)
		}
	}
}

func TestClassifyBearer_OutOfAlphabetRejected(t *testing.T) {
	// Suffix length 26 but contains chars outside [a-z2-7].
	for _, in := range []string{
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaa1", // contains '1' (not in base32 lowered)
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaa0", // contains '0'
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaa8", // contains '8'
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaa9", // contains '9'
		"pk_aaaaaaaaaaaaaaaaaaaaaaaaa!", // contains '!'
	} {
		got, err := ClassifyBearer(in)
		if !errors.Is(err, ErrInvalidBearer) {
			t.Errorf("ClassifyBearer(%q) err = %v; want ErrInvalidBearer", in, err)
		}
		if got != "" {
			t.Errorf("ClassifyBearer(%q) prefix = %q; want zero", in, got)
		}
	}
}

// ---------------------------------------------------------------------
// ConstantTimeEqual tests (Task 3)
// ---------------------------------------------------------------------

func TestConstantTimeEqual_Match(t *testing.T) {
	a := "pk_abcdefghij1234567890abcde"
	b := "pk_abcdefghij1234567890abcde"
	if !ConstantTimeEqual(a, b) {
		t.Fatalf("ConstantTimeEqual identical inputs returned false")
	}
}

func TestConstantTimeEqual_Mismatch(t *testing.T) {
	a := "pk_abcdefghij1234567890abcde"
	b := "pk_zbcdefghij1234567890abcde" // first suffix char differs
	if ConstantTimeEqual(a, b) {
		t.Fatalf("ConstantTimeEqual differing inputs returned true")
	}
}

func TestConstantTimeEqual_DifferentLengths(t *testing.T) {
	// crypto/subtle.ConstantTimeCompare returns 0 on length mismatch;
	// our wrapper preserves that contract.
	if ConstantTimeEqual("pk_short", "pk_abcdefghij1234567890abcde") {
		t.Fatalf("ConstantTimeEqual short vs long returned true")
	}
	if ConstantTimeEqual("", "pk_abcdefghij1234567890abcde") {
		t.Fatalf("ConstantTimeEqual empty vs non-empty returned true")
	}
	if ConstantTimeEqual("pk_abcdefghij1234567890abcde", "") {
		t.Fatalf("ConstantTimeEqual non-empty vs empty returned true")
	}
}

func TestConstantTimeEqual_BothEmpty(t *testing.T) {
	if !ConstantTimeEqual("", "") {
		t.Fatalf("ConstantTimeEqual empty vs empty returned false")
	}
}

func TestKeyIDPrefixConstantsHaveExpectedValues(t *testing.T) {
	if string(PrefixPkid) != PkidKeyIDPrefix {
		t.Errorf("PrefixPkid = %q; want %q", PrefixPkid, PkidKeyIDPrefix)
	}
	if string(PrefixEkid) != EkidKeyIDPrefix {
		t.Errorf("PrefixEkid = %q; want %q", PrefixEkid, EkidKeyIDPrefix)
	}
}

// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/hash"
)

// TestSpillAndHashXxh3_MatchesHashFile pins the equivalence that makes the
// single-pass spill safe: the digest computed DURING the spill must be
// byte-identical to the one the old two-pass path produced (spill, then
// hash.HashFile re-reading the file). A divergence here would silently
// invalidate every on-disk state.json, since the ledger matches on this string.
func TestSpillAndHashXxh3_MatchesHashFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello world")},
		{"nul bytes", []byte{0x00, 0x01, 0x00, 0xff}},
		{"multi-chunk", bytes.Repeat([]byte("abcdefghij"), 100_000)}, // ~1 MiB, forces >1 Write
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "source.bin")

			got, err := spillAndHashXxh3(bytes.NewReader(tc.body), p)
			if err != nil {
				t.Fatalf("spillAndHashXxh3: %v", err)
			}

			// The two-pass digest the old implementation returned.
			want, err := hash.HashFile(p)
			if err != nil {
				t.Fatalf("hash.HashFile: %v", err)
			}
			if got != want {
				t.Errorf("digest mismatch:\n single-pass = %s\n HashFile    = %s", got, want)
			}
			if !strings.HasPrefix(got, "xxh3:") {
				t.Errorf("digest %q lost the canonical xxh3: prefix", got)
			}
			// The spilled bytes must be the body, verbatim.
			if h := hash.HashBytes(tc.body); h != got {
				t.Errorf("spilled content differs from body: HashBytes=%s spill=%s", h, got)
			}
		})
	}
}

// TestSpillAndHashXxh3_ExistingFile asserts the O_EXCL discipline is kept: a
// staging path that already exists must error, never silently truncate.
func TestSpillAndHashXxh3_ExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "source.bin")
	if _, err := spillAndHashXxh3(bytes.NewReader([]byte("first")), p); err != nil {
		t.Fatalf("first spill: %v", err)
	}
	if _, err := spillAndHashXxh3(bytes.NewReader([]byte("second")), p); err == nil {
		t.Fatal("second spill to an existing path: want error (O_EXCL), got nil")
	}
}

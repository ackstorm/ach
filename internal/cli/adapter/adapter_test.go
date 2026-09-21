// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectFromSignals covers the hit-count → Confidence ladder and that
// Reasons keep signal order (only the hits, in the order given).
func TestDetectFromSignals(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sig := func(names ...string) []Signal {
		out := make([]Signal, 0, len(names))
		for _, n := range names {
			out = append(out, Signal{Path: filepath.Join(root, n), Reason: "found " + n})
		}
		return out
	}

	cases := []struct {
		name    string
		signals []Signal
		want    Confidence
		reasons []string
	}{
		{"no hits", sig("missing", "nope"), 0, nil},
		{"one hit", sig("missing", "a"), ConfidenceLow, []string{"found a"}},
		{"two hits", sig("b", "missing", "a"), ConfidenceMedium, []string{"found b", "found a"}},
		{"three hits", sig("c", "a", "missing", "b"), ConfidenceHigh, []string{"found c", "found a", "found b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectFromSignals("x", tc.signals)
			if tc.want == 0 {
				if got.ID != "" || got.Confidence != 0 || len(got.Reasons) != 0 {
					t.Fatalf("expected empty Match, got %+v", got)
				}
				return
			}
			if got.ID != "x" || got.Confidence != tc.want {
				t.Fatalf("got %+v, want id=x confidence=%d", got, tc.want)
			}
			if len(got.Reasons) != len(tc.reasons) {
				t.Fatalf("reasons = %v, want %v", got.Reasons, tc.reasons)
			}
			for i := range tc.reasons {
				if got.Reasons[i] != tc.reasons[i] {
					t.Fatalf("reasons = %v, want %v", got.Reasons, tc.reasons)
				}
			}
		})
	}
}

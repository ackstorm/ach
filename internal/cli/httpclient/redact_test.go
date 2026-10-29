// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// TestRedact_PrefixForms asserts Test 9: pk-/ek- values reduce to
// "<prefix>-***"; anything else falls through to a literal "redacted"
// marker (no prefix detected).
func TestRedact_PrefixForms(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"pk-abc", "pk-***"},
		{"pk-supersecretlong", "pk-***"},
		{"ek-xyz", "ek-***"},
		{"garbage", "redacted"},
		{"", "redacted"},
		{"Bearer secret", "redacted"},
	}
	for _, tc := range cases {
		if got := httpclient.Redact(tc.in); got != tc.want {
			t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHeaderDump_RedactsAchKey asserts Test 10: HeaderDump runs the
// x-ach-key header (case-insensitive) through Redact and leaves other
// headers verbatim. Output is sorted by canonical header name.
func TestHeaderDump_RedactsAchKey(t *testing.T) {
	h := http.Header{}
	h.Set("X-Ach-Key", "pk-abc")
	h.Set("Authorization", "Bearer y")
	h.Set("Accept-Encoding", "gzip")

	got := httpclient.HeaderDump(h)
	if !strings.Contains(got, "X-Ach-Key: pk-***") {
		t.Errorf("HeaderDump missing redacted x-ach-key:\n%s", got)
	}
	if !strings.Contains(got, "Authorization: Bearer y") {
		t.Errorf("HeaderDump dropped Authorization:\n%s", got)
	}
	if !strings.Contains(got, "Accept-Encoding: gzip") {
		t.Errorf("HeaderDump dropped Accept-Encoding:\n%s", got)
	}
	if strings.Contains(got, "pk-abc") {
		t.Errorf("HeaderDump leaked pk-abc plaintext:\n%s", got)
	}

	// Determinism: sorted by canonical header name → Accept-Encoding
	// comes before Authorization comes before X-Ach-Key.
	idxAE := strings.Index(got, "Accept-Encoding")
	idxAuth := strings.Index(got, "Authorization")
	idxKey := strings.Index(got, "X-Ach-Key")
	if !(idxAE < idxAuth && idxAuth < idxKey) {
		t.Errorf("HeaderDump not sorted by canonical name:\n%s", got)
	}
}

// TestHeaderDump_CaseInsensitive verifies redaction triggers when the
// header is stored under a non-canonical case (defensive guard).
func TestHeaderDump_CaseInsensitive(t *testing.T) {
	h := http.Header{}
	h.Set("x-ach-key", "ek-secret123") // http.Header.Set canonicalizes
	got := httpclient.HeaderDump(h)
	if strings.Contains(got, "ek-secret123") {
		t.Errorf("HeaderDump leaked plaintext under lowercase key:\n%s", got)
	}
	if !strings.Contains(got, "ek-***") {
		t.Errorf("HeaderDump missed lowercase x-ach-key:\n%s", got)
	}
}

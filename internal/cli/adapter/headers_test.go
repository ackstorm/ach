// SPDX-License-Identifier: Apache-2.0

package adapter

import "testing"

// TestHeadersWithCredential_EmitsKeyAndEnvironment asserts the headers
// map carries the bearer under "x-ach-key" and the Environment name
// under "x-ach-environment" when both are non-empty.
func TestHeadersWithCredential_EmitsKeyAndEnvironment(t *testing.T) {
	h := HeadersWithCredential("pk_demo", "demo")
	if got := h["x-ach-key"]; got != "pk_demo" {
		t.Errorf("x-ach-key = %q, want %q", got, "pk_demo")
	}
	if got := h["x-ach-environment"]; got != "demo" {
		t.Errorf("x-ach-environment = %q, want %q", got, "demo")
	}
}

// TestHeadersWithCredential_EmptyCredentialOmitsKey: an OAuth hydrate
// renders NO x-ach-key at all — a present-but-empty header is a credential
// the tool sends instead of authenticating. With nothing to emit the map
// is nil so the entry's `omitempty` drops the key.
func TestHeadersWithCredential_EmptyCredentialOmitsKey(t *testing.T) {
	h := HeadersWithCredential("", "prod")
	if _, ok := h["x-ach-key"]; ok {
		t.Errorf("x-ach-key must be absent for an empty credential; got %v", h)
	}
	if h["x-ach-environment"] != "prod" {
		t.Errorf("x-ach-environment = %q, want prod", h["x-ach-environment"])
	}
	if got := HeadersWithCredential("", ""); got != nil {
		t.Errorf("expected nil map, got %v", got)
	}
}

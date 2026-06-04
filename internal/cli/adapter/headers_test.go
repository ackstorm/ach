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

// TestHeadersWithCredential_EmptyEnvironmentOmitsHeader asserts the
// x-ach-environment header is absent (not an empty value) when the
// Environment is empty, so offline / dry-run renders stay minimal. The
// x-ach-key header is still emitted (empty value) to keep the rendered
// shape stable.
func TestHeadersWithCredential_EmptyEnvironmentOmitsHeader(t *testing.T) {
	h := HeadersWithCredential("", "")
	if _, ok := h["x-ach-key"]; !ok {
		t.Errorf("x-ach-key must always be present (empty value ok); got %v", h)
	}
	if _, ok := h["x-ach-environment"]; ok {
		t.Errorf("x-ach-environment must be absent when env is empty; got %v", h)
	}
}

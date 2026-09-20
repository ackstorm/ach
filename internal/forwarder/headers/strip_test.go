// SPDX-License-Identifier: Apache-2.0

package headers_test

import (
	"net/http"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder/headers"
)

func TestStripAndRewrite(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer upstream-cred")
	h.Set("X-Litellm-Api-Key", "client-supplied")
	h.Set("X-Litellm-Session-Id", "sess-123")
	h.Set("X-Litellm-Tags", "a,b")
	h.Set("X-Ach-Key", "pk_secret")
	h["x-ACH-environment"] = []string{"dev"} // non-canonical key: the prefix match is case-insensitive
	h.Set("X-Goog-Api-Key", "client-goog")
	h.Set("Connection", "keep-alive")
	h.Set("Content-Type", "application/json")

	headers.StripAndRewrite(h, "sk-user-material")

	if got := h.Get("X-Litellm-Api-Key"); got != "sk-user-material" {
		t.Errorf("x-litellm-api-key = %q; want the caller's key", got)
	}
	for _, k := range []string{"X-Ach-Key", "x-ACH-environment"} {
		if _, ok := h[k]; ok {
			t.Errorf("%s must be dropped", k)
		}
	}
	for k, want := range map[string]string{
		"Authorization": "Bearer upstream-cred", "X-Litellm-Session-Id": "sess-123", "X-Litellm-Tags": "a,b",
		"X-Goog-Api-Key": "client-goog", "Connection": "keep-alive", "Content-Type": "application/json",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q; want %q (passes as it came)", k, got, want)
		}
	}
}

func TestStripAndRewrite_EmptyMaterial(t *testing.T) {
	h := http.Header{}
	headers.StripAndRewrite(h, "")
	if got, ok := h["X-Litellm-Api-Key"]; !ok || got[0] != "" {
		t.Errorf("empty material → empty header (no fallback); got %v", got)
	}
}

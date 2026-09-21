// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameSite(t *testing.T) {
	const issuer = "https://ach.test"
	cases := []struct {
		name string
		hdr  map[string]string
		want bool
	}{
		{"fetch metadata same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"fetch metadata cross-site", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": issuer}, false},
		{"fetch metadata same-site (subdomain)", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"no metadata, own origin", map[string]string{"Origin": issuer}, true},
		{"no metadata, foreign origin", map[string]string{"Origin": "https://evil.test"}, false},
		{"no metadata, no origin", nil, false},
		{"origin with path is rejected", map[string]string{"Origin": issuer + "/x"}, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/platform/keys", nil)
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		if got := SameSite(r, issuer); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

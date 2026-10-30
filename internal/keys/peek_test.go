// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"encoding/base64"
	"slices"
	"testing"
)

func TestPeekAudience(t *testing.T) {
	enc := func(payload string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".c2ln"
	}
	cases := map[string]struct {
		raw  string
		want []string
	}{
		"string":     {enc(`{"aud":"https://ach.test"}`), []string{"https://ach.test"}},
		"list":       {enc(`{"aud":["a","https://ach.test"]}`), []string{"a", "https://ach.test"}},
		"absent":     {enc(`{"sub":"x"}`), nil},
		"not base64": {"a.%%%.c", nil},
		"not a JWS":  {"ek-abc", nil},
	}
	for name, tc := range cases {
		if got := PeekAudience(tc.raw); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

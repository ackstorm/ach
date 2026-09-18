// SPDX-License-Identifier: Apache-2.0

package oauthsvc

import "testing"

func TestParse(t *testing.T) {
	m, err := Parse(`{"mcp-zoho-desk-ro":{"store":"zoho-desk-ro","broker":"https://api.example/zoho-desk-ro-callback/"}}`)
	if err != nil || len(m) != 1 || m["mcp-zoho-desk-ro"].Store != "zoho-desk-ro" || m["mcp-zoho-desk-ro"].Broker != "https://api.example/zoho-desk-ro-callback" {
		t.Fatalf("%+v %v", m, err)
	}
	if m, err := Parse(""); err != nil || len(m) != 0 {
		t.Fatalf("empty must be a nil map: %+v %v", m, err)
	}
	for _, bad := range []string{`{"a":{"store":"","broker":"https://b"}}`, `{"a":{"store":"s","broker":"ftp://b"}}`, `{"a b":{"store":"s","broker":"https://b"}}`, `nope`} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if k := Keys(map[string]Service{"b": {}, "a": {}}); len(k) != 2 || k[0] != "a" {
		t.Fatalf("Keys must be sorted: %v", k)
	}
}

// SPDX-License-Identifier: Apache-2.0

package jwt

import "testing"

func TestASMetadata_ScopesSupported(t *testing.T) {
	m := ASMetadata("https://ach.test/", "ach", nil)
	if got := m["scopes_supported"].([]string); len(got) != 2 || got[0] != "offline_access" || got[1] != "ach" {
		t.Fatalf("no services: %v", got)
	}
	m = ASMetadata("https://ach.test", "ach", []string{"mcp-b", "mcp-a"})
	if got := m["scopes_supported"].([]string); len(got) != 4 || got[2] != "mcp-a" || got[3] != "mcp-b" {
		t.Fatalf("services must be sorted after the fixed scopes: %v", got)
	}
}

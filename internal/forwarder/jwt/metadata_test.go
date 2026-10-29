// SPDX-License-Identifier: Apache-2.0

package jwt

import "testing"

func TestASMetadata_ScopesSupported(t *testing.T) {
	m := ASMetadata("https://ach.test/")
	if got := m["scopes_supported"].([]string); len(got) != 1 || got[0] != "offline_access" {
		t.Fatalf("no services: %v", got)
	}
}

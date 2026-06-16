// SPDX-License-Identifier: Apache-2.0

package admin

import "testing"

// TestIsRefreshableKind locks the closed set Platform API may force-refresh.
// skill + skillmarketplace joined plugin/prompt/artifact/pluginmarketplace in
// G8; non-content kinds (team/environment/backendidentitypolicy) stay rejected.
func TestIsRefreshableKind(t *testing.T) {
	accepted := []string{"plugin", "prompt", "artifact", "pluginmarketplace", "skill", "skillmarketplace"}
	for _, k := range accepted {
		if !isRefreshableKind(k) {
			t.Errorf("isRefreshableKind(%q) = false; want true", k)
		}
	}
	rejected := []string{"team", "environment", "backendidentitypolicy", "marketplace", "skill-marketplace", "garbage", ""}
	for _, k := range rejected {
		if isRefreshableKind(k) {
			t.Errorf("isRefreshableKind(%q) = true; want false", k)
		}
	}
}

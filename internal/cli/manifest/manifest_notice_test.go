// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"strings"
	"testing"
)

// TestDecode_NoticeAccepted asserts the strict decoder accepts and binds the
// notice field (regression guard: DisallowUnknownFields would 400 otherwise).
func TestDecode_NoticeAccepted(t *testing.T) {
	body := `{"schemaVersion":"v1alpha1","environment":"demo",` +
		`"runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},` +
		`"context":{"prompts":[],"plugins":[],"artifacts":[],"skills":[]},` +
		`"notice":"re-login after rotation"}`
	m, err := Decode(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m.Notice != "re-login after rotation" {
		t.Errorf("Notice = %q, want decoded", m.Notice)
	}
}

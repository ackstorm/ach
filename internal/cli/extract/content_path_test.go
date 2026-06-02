// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"testing"
)

// TestContentPath_EscapesName asserts that URL metacharacters in the
// resource name are PathEscaped in the generated content service path
// (security finding S2).
func TestContentPath_EscapesName(t *testing.T) {
	got := contentPath(KindPrompt, "a?b#c")
	if got != "/content/prompt/a%3Fb%23c" {
		t.Fatalf("contentPath = %q, want escaped", got)
	}
}

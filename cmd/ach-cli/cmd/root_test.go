// SPDX-License-Identifier: Apache-2.0

package cmd

import "testing"

// TestRoot_NoHandWrittenLong: cobra lists the commands; a hand-written
// command list in the root Long only drifts.
func TestRoot_NoHandWrittenLong(t *testing.T) {
	if rootCmd.Long != "" {
		t.Errorf("root Long should be empty (cobra lists the commands); got:\n%s", rootCmd.Long)
	}
	for _, want := range []string{"login", "logout", "whoami", "token", "profile", "env", "keys", "admin", "local"} {
		if c, _, err := rootCmd.Find([]string{want}); err != nil || c == rootCmd {
			t.Errorf("root is missing command %q", want)
		}
	}
	if c, _, _ := rootCmd.Find([]string{"runtime"}); c != rootCmd {
		t.Errorf("runtime is still registered")
	}
}

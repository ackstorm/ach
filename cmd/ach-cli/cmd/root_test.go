// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"strings"
	"testing"
)

func TestRootHelp_UsesKeysNotEnvKeys(t *testing.T) {
	long := rootCmd.Long
	if !strings.Contains(long, "\n  keys ") {
		t.Errorf("root help blurb should list `keys`; got:\n%s", long)
	}
	if strings.Contains(long, "\n  env-keys ") {
		t.Errorf("root help blurb still lists `env-keys` (alias); should say `keys`")
	}
}

// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestHelp_NoInternalSpecTags(t *testing.T) {
	banned := []string{"CLI spec §", "CLI-10", "CLI-12", "CLI-13", "STATE-0", "STATE-1",
		"Hub §", "§3.3", "§5.3", "§6.", "§7.1", "§9.3", "14-step", "three-tier auto-claim",
		"D-05", "D-11", "D-12", "D-14", "SAFE-0", "Phase 7", "SAFE-01"}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		hay := c.Short + "\n" + c.Long
		c.Flags().VisitAll(func(f *pflag.Flag) { hay += "\n" + f.Usage })
		for _, b := range banned {
			if strings.Contains(hay, b) {
				t.Errorf("%s help contains internal tag %q", c.CommandPath(), b)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

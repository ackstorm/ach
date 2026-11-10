// SPDX-License-Identifier: Apache-2.0

package cmd

import "testing"

// TestRoot_NoHandWrittenLong: cobra lists the commands; a hand-written
// command list in the root Long only drifts.
func TestRoot_NoHandWrittenLong(t *testing.T) {
	if rootCmd.Long != "" {
		t.Errorf("root Long should be empty (cobra lists the commands); got:\n%s", rootCmd.Long)
	}
	for _, want := range []string{
		"login", "logout", "whoami", "token", "profile", "environment", "keys", "admin", "local", "launch",
	} {
		if c, _, err := rootCmd.Find([]string{want}); err != nil || c == rootCmd {
			t.Errorf("root is missing command %q", want)
		}
	}
	if c, _, _ := rootCmd.Find([]string{"runtime"}); c != rootCmd {
		t.Errorf("runtime is still registered")
	}
}

// env is an alias of environment everywhere: the command and every flag.
func TestEnvAliasesEnvironment(t *testing.T) {
	for _, name := range []string{"environment", "env"} {
		c, _, err := rootCmd.Find([]string{name, "hydrate"})
		if err != nil || c.CommandPath() != "ach-cli environment hydrate" {
			t.Errorf("%q hydrate resolved to %q, %v", name, c.CommandPath(), err)
		}
	}
	for _, path := range [][]string{{"launch"}, {"keys", "list"}, {"admin", "keys", "list"}} {
		c, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatalf("%v: %v", path, err)
		}
		f := c.Flags().Lookup("env")
		if f == nil || f.Name != "environment" {
			t.Errorf("%v: --env does not resolve to --environment (%v)", path, f)
		}
	}
}

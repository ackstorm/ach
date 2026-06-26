// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// executeAllRoot builds a minimal root with all top-level parent commands
// registered and executes args through it. Used by TestParents_* to verify
// that nested parents (e.g. "local repo frobnicate") resolve correctly.
func executeAllRoot(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	root := &cobra.Command{
		Use:           "ach-cli",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(newKeysCmd(), newConfigCmd(), newEnvCmd(), newLocalCmd(),
		newAdminCmd(), newContentCmd())
	return executeCommand(t, root, args...)
}

// TestParents_UnknownSubcommand_Exit1 asserts every parent command rejects an
// unknown subcommand with a non-zero exit (was exit 0 + help, B3).
func TestParents_UnknownSubcommand_Exit1(t *testing.T) {
	cases := [][]string{
		{"keys", "frobnicate"},
		{"config", "frobnicate"},
		{"env", "frobnicate"},
		{"local", "frobnicate"},
		{"local", "repo", "frobnicate"},
		{"local", "skill", "frobnicate"},
		{"admin", "frobnicate"},
		{"admin", "keys", "frobnicate"},
		{"admin", "users", "frobnicate"},
		{"content", "frobnicate"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, code, err := executeAllRoot(t, args...)
			if err == nil {
				t.Fatalf("%v: expected error for unknown subcommand", args)
			}
			if code != exit.General {
				t.Errorf("%v: exit code = %d; want %d", args, code, exit.General)
			}
		})
	}
}

// TestRoot_UnknownSubcommand_Exit1 asserts the real rootCmd errors on a typo'd
// top-level command. Unlike the child parents (B3), root needs no RunE guard —
// cobra's legacyArgs already rejects an unknown command at the root level. This
// locks that behavior so `ach-cli <typo>` never silently exits 0.
func TestRoot_UnknownSubcommand_Exit1(t *testing.T) {
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs([]string{"frobnicate"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("ach-cli frobnicate: expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("ach-cli frobnicate: error = %q; want it to mention unknown command", err.Error())
	}
}

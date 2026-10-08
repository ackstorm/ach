// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// TestWithUsageOnArgErrors: a bare cobra arity error gains the usage line; a
// friendly CodedError from a custom validator is left untouched.
func TestWithUsageOnArgErrors(t *testing.T) {
	root := &cobra.Command{Use: "ach-cli"}
	noop := func(*cobra.Command, []string) error { return nil }
	root.AddCommand(&cobra.Command{Use: "revoke <id>", Args: cobra.ExactArgs(1), RunE: noop})
	root.AddCommand(&cobra.Command{Use: "describe <name>", RunE: noop, Args: func(*cobra.Command, []string) error {
		return &exit.CodedError{Code: exit.General, Msg: "missing environment."}
	}})
	withUsageOnArgErrors(root)

	root.SetArgs([]string{"revoke"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "usage: ach-cli revoke <id>") {
		t.Errorf("arity error lacks usage: %v", err)
	}
	root.SetArgs([]string{"describe"})
	if err := root.Execute(); err == nil || err.Error() != "missing environment." {
		t.Errorf("CodedError rewritten: %v", err)
	}
}

func TestEmptyKeysMessage(t *testing.T) {
	for _, tc := range []struct{ status, env, want string }{
		{"active", "", "No active keys (try --status all)\n"},
		{"active", "nope", "No active keys for environment \"nope\" (try --status all)\n"},
		{"all", "nope", "No keys for environment \"nope\" (check the name with `ach-cli env list`)\n"},
		{"all", "", "No keys; `ach-cli keys create <env>` makes one\n"},
	} {
		if got := emptyKeysMessage(tc.status, tc.env); got != tc.want {
			t.Errorf("(%q,%q) = %q, want %q", tc.status, tc.env, got, tc.want)
		}
	}
}

func TestNothingInstalledMessage(t *testing.T) {
	root := t.TempDir()
	if got := nothingInstalledMessage("nope", root); got != `nothing installed for environment "nope" in `+root {
		t.Errorf("empty root: %q", got)
	}
	_ = os.Mkdir(filepath.Join(root, "ackstorm"), 0o755)
	_ = os.Mkdir(filepath.Join(root, "lock-only"), 0o755) // no state: not hydrated
	_ = os.WriteFile(filepath.Join(root, "ackstorm", "state-codex.json"), []byte("{}"), 0o600)
	if got := nothingInstalledMessage("nope", root); !strings.HasSuffix(got, "; hydrated there: ackstorm") {
		t.Errorf("with env: %q", got)
	}
}

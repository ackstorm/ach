// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"errors"
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// Version is overridden via -ldflags at build time (see Makefile build target).
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "ach-cli",
	Short:   "ACH CLI — operator/developer client for the ACH control plane",
	Version: Version,
	// main.go renders errors via exit.DispatchAndRender (the single §9.3
	// renderer). Silence cobra's own error + usage dump so failures
	// surface exactly once, without an unhelpful flags listing on a
	// plain user error (e.g. "login canceled").
	SilenceErrors: true,
	SilenceUsage:  true,
	// An unknown top-level token (typo'd command) already errors via cobra's
	// legacyArgs (a root WITH subcommands + leftover args → "unknown command"),
	// so — unlike the child parents (B3) — root needs no RunE guard. Bare
	// `ach-cli` (no args) reaches this RunE and shows the banner + help.
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRoot(cmd)
	},
}

// runRoot handles bare `ach-cli` (no subcommand). It prints the decorative
// banner — gated on stdout being a TTY so it never lands in a pipe/CI —
// followed by the help text. The banner is shown HERE and nowhere else:
// `--help`, `--version`, and every subcommand short-circuit before this
// RunE, so no other invocation surfaces it.
func runRoot(cmd *cobra.Command) error {
	if isTerminal(cmd.OutOrStdout()) {
		writeBanner(cmd.OutOrStdout())
	}
	return cmd.Help()
}

var wrapArgsOnce sync.Once

func Execute() error {
	wrapArgsOnce.Do(func() { withUsageOnArgErrors(rootCmd) })
	return rootCmd.Execute()
}

// withUsageOnArgErrors wraps every subcommand's positional-args validator so
// a bare cobra arity error ("accepts 1 arg(s), received 0") also prints the
// usage line and where to get help. Validators that already return a
// friendly *exit.CodedError are left as they are.
func withUsageOnArgErrors(root *cobra.Command) {
	for _, c := range root.Commands() {
		withUsageOnArgErrors(c)
		if c.Args == nil {
			continue
		}
		orig := c.Args
		c.Args = func(cmd *cobra.Command, args []string) error {
			err := orig(cmd, args)
			var ce *exit.CodedError
			if err == nil || errors.As(err, &ce) {
				return err
			}
			return fmt.Errorf("%w\nusage: %s\nRun '%s --help' for details", err, cmd.UseLine(), cmd.CommandPath())
		}
	}
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("ach-cli %s\n", Version))
}

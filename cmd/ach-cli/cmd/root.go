// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/featuregate"
)

// localHelpLine is the rootCmd help-text summary for the `local` parent. The
// `plugin` child is gated behind featuregate.PluginsEnabled, so the noun list
// drops "plugin" when the flag is off.
func localHelpLine() string {
	if featuregate.PluginsEnabled {
		return "  local        Local, ungoverned package path (repo/plugin/skill)"
	}
	return "  local        Local, ungoverned package path (repo/skill)"
}

// Version is overridden via -ldflags at build time (see Makefile build target).
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "ach-cli",
	Short: "ACH CLI — operator/developer client for the ACH control plane",
	Long: `ach-cli is the user-facing client for the ACH (Agent Capability Hub)
control plane. Subcommands:

  login        Authenticate against the platform-api
  logout       Revoke local session
  whoami       Show current identity
  config       Inspect / mutate local config
  env          Inspect environments; hydrate/status/uninstall workspace
  keys         Manage your API keys (create/list/revoke/prune)
  admin        Admin subcommands (keys revoke, users revoke-keys, refresh)
` + localHelpLine() + `

For service-mode commands (operator, platform-api, forwarder,
content-service, migrate), use the 'ach' binary instead.`,
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

func Execute() error { return rootCmd.Execute() }

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("ach-cli %s\n", Version))
}

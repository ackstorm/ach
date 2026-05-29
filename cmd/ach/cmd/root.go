// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is overridden via -ldflags at build time (see Makefile build target).
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "ach",
	Short: "ACH — Agent Configuration Hub",
	Long: `ach is the service-mode binary for the ACH control plane.

Run a long-running service:
  ach operator
  ach platform-api
  ach forwarder
  ach content-service

Run a one-shot job:
  ach migrate

For the user-facing CLI (login/whoami/logout/config/env/env-keys/
hydrate/admin), use the sibling 'ach-cli' binary.`,
	Version: Version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("ach %s\n", Version))
}

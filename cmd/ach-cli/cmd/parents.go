// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// helpOrUnknownSubcommand is the RunE for parent commands: it shows help when
// invoked with no args, and errors on an unknown subcommand token (cobra passes
// unmatched positional args here). Prevents the silent exit-0-on-typo (B3).
func helpOrUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("unknown subcommand %q for %q", args[0], cmd.CommandPath()),
		}
	}
	return cmd.Help()
}

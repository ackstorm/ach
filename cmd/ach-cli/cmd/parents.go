// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// helpOrUnknownSubcommand is the RunE for parent commands: it shows help when
// invoked with no args, and errors on an unknown subcommand token (cobra passes
// unmatched positional args here). Prevents the silent exit-0-on-typo (B3).
// The error names the closest subcommands, as cobra does for the root: a
// name within two edits, a prefix, or a command's SuggestFor synonym.
func helpOrUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		msg := fmt.Sprintf("unknown subcommand %q for %q", args[0], cmd.CommandPath())
		if cmd.SuggestionsMinimumDistance <= 0 {
			cmd.SuggestionsMinimumDistance = 2 // cobra sets this default only on the root
		}
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
		}
		return &exit.CodedError{Code: exit.General, Msg: msg}
	}
	return cmd.Help()
}

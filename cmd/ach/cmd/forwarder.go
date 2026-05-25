// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var forwarderCmd = &cobra.Command{
	Use:   "forwarder",
	Short: "Run the ACH MCP/A2A forwarder",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach forwarder: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(forwarderCmd)
}

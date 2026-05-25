// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var platformAPICmd = &cobra.Command{
	Use:   "platform-api",
	Short: "Run the ACH Platform REST API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach platform-api: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(platformAPICmd)
}

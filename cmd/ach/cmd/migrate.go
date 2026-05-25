// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach migrate: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}

// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var contentServiceCmd = &cobra.Command{
	Use:   "content-service",
	Short: "Run the ACH artifact content service",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach content-service: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(contentServiceCmd)
}

// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Run the ACH Kubernetes operator (controller-runtime manager)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach operator: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(operatorCmd)
}

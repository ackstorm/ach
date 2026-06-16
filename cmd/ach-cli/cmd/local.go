// SPDX-License-Identifier: Apache-2.0

// `ach-cli local` is the parent for the serverless, ungoverned developer
// package path: `repo` (register a git/marketplace source), `plugin`, and
// `skill` (install/uninstall/update/list a name@repo into per-tool adapter
// dirs). Re-parented under `local` (G9) to separate it visually + semantically
// from the governed `env` path (platform-api → Dex → hydrate).

package cmd

import "github.com/spf13/cobra"

// newLocalCmd returns the `local` parent with repo/plugin/skill children.
func newLocalCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "local",
		Short: "Local, ungoverned developer package path (repo/plugin/skill)",
		Long: `Local, ungoverned developer path — installs directly from git into your
adapter dirs. NOT governed by the ACH Hub (no Environment, no authorization,
no audit, no central reproducibility). For governed/reproducible distribution
use 'ach-cli env hydrate'.`,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	parent.AddCommand(newRepoCmd(), newPluginCmd(), newSkillCmd())
	return parent
}

func init() {
	rootCmd.AddCommand(newLocalCmd())
}

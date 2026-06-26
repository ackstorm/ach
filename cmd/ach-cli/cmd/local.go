// SPDX-License-Identifier: Apache-2.0

// `ach-cli local` is the parent for the serverless, ungoverned developer
// package path: `repo` (register a git/marketplace source), `plugin`, and
// `skill` (install/uninstall/update/list a name@repo into per-tool adapter
// dirs). Re-parented under `local` (G9) to separate it visually + semantically
// from the governed `env` path (platform-api → Dex → hydrate).

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/featuregate"
)

// newLocalCmd returns the `local` parent with repo/(plugin)/skill children.
// The `plugin` child is gated behind featuregate.PluginsEnabled (the skill
// command shares the plugin code path, so newPluginCmd stays compiled).
func newLocalCmd() *cobra.Command {
	nouns := "(repo/skill)"
	if featuregate.PluginsEnabled {
		nouns = "(repo/plugin/skill)"
	}
	parent := &cobra.Command{
		Use:   "local",
		Short: "Local, ungoverned developer package path " + nouns,
		Long: `Local, ungoverned developer path — installs directly from git into your
adapter dirs. NOT governed by the ACH Hub (no Environment, no authorization,
no audit, no central reproducibility). For governed/reproducible distribution
use 'ach-cli env hydrate'.`,
		RunE: helpOrUnknownSubcommand,
	}
	children := []*cobra.Command{newRepoCmd()}
	if featuregate.PluginsEnabled {
		children = append(children, newPluginCmd())
	}
	children = append(children, newSkillCmd())
	parent.AddCommand(children...)
	return parent
}

func init() {
	rootCmd.AddCommand(newLocalCmd())
}

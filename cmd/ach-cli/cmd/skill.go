// SPDX-License-Identifier: Apache-2.0

// `ach-cli skill` manages locally installed agent skills.
// Four children (all behaviour is shared via pkgcmd.go):
//
//   - install    Fetch and install a skill from a registered repo
//   - uninstall  Remove an installed skill
//   - update     Re-resolve and re-install (or all if no args)
//   - list       Show installed skills (from installed.json)
//
// Repo registration: use `ach-cli repo add` first.
// Lens preference: skill-marketplace > skill.

package cmd

import "github.com/spf13/cobra"

func newSkillCmd() *cobra.Command {
	return newPkgCmd(kindSkill)
}

func init() {
	rootCmd.AddCommand(newSkillCmd())
}

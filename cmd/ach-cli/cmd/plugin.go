// SPDX-License-Identifier: Apache-2.0

// `ach-cli plugin` manages locally installed Claude Code plugins.
// Four children (all behaviour is shared via pkgcmd.go):
//
//   - install    Fetch and install a plugin from a registered repo
//   - uninstall  Remove an installed plugin
//   - update     Re-resolve and re-install (or all if no args)
//   - list       Show installed plugins (from installed.json)
//
// Repo registration: use `ach-cli local repo add` first.
// Lens preference: plugin-marketplace > plugin.
//
// Registered under the `local` parent (G9) — see local.go.

package cmd

import "github.com/spf13/cobra"

func newPluginCmd() *cobra.Command {
	return newPkgCmd(kindPlugin)
}

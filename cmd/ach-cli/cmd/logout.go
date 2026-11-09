// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// newLogoutCmd returns `ach-cli logout`: it clears the profile's own
// credential (OAuth session and Key) and keeps its URL, so a later login
// resumes without re-prompting for the URL.
func newLogoutCmd() *cobra.Command {
	var flagProfile string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Sign out of the active profile",
		Long: `Sign out of the active profile: its session (and machine key, if it has
one) is removed from ~/.config/ach/config.yaml; the URL stays. Keys made
with keys create keep working until you revoke them (ach-cli keys revoke).

With ACH_URL and ACH_KEY both set (synthetic mode) logout exits 1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLogout(cmd, flagProfile)
		},
	}
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Sign out of this profile instead of the active one")
	return cmd
}

func doLogout(cmd *cobra.Command, flagProfile string) error {
	if err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateSession}); err != nil {
		return err
	}
	configPath, err := config.Path()
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.Load(configPath)
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil || len(file.Profiles) == 0 {
		return &exit.CodedError{Code: exit.General, Msg: "no profile configured; nothing to log out of"}
	}
	name, dep, err := config.ResolveActive(file, flagProfile, os.Getenv("ACH_PROFILE"))
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}
	dep.OAuth = nil
	dep.Key = ""
	if err := config.Save(configPath, file); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed out of %q\n", name)
	return nil
}

func init() {
	rootCmd.AddCommand(newLogoutCmd())
}

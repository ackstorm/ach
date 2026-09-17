// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
)

// newTokenCmd prints a valid OAuth access token and NOTHING else — it is
// the credential helper the tools run: Claude Code `apiKeyHelper`, Codex
// `[model_providers.*].auth.command`, opencode `.well-known/opencode`.
// Every diagnostic goes to stderr.
func newTokenCmd() *cobra.Command {
	var flagProfile string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print a valid OAuth access token for the active profile (credential helper)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.Path()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			file, err := config.Load(path)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			if file == nil {
				return &exit.CodedError{Code: exit.General, Msg: "no profile configured; run `ach-cli login`"}
			}
			name, prof, err := config.ResolveActive(file, flagProfile, os.Getenv("ACH_PROFILE"))
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("%v; run `ach-cli login`", err), Wrapped: err}
			}
			if prof.OAuth == nil {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("profile %q has no OAuth login; run `ach-cli login`", name)}
			}
			client := &oauthlogin.Client{BaseURL: prof.URL}
			tok, updated, err := client.CurrentAccessToken(cmd.Context(), prof.OAuth)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("token: %v; run `ach-cli login`", err), Wrapped: err}
			}
			if updated != nil {
				prof.OAuth = updated
				if err := config.Save(path, file); err != nil {
					return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
				}
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), tok)
			return err
		},
	}
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	return cmd
}

func init() { rootCmd.AddCommand(newTokenCmd()) }

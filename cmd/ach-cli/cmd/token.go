// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/lock"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
)

// tokenLockTimeout bounds the wait for a concurrent helper. Claude Code and
// Codex both exec `ach-cli token` on their own timers, so two helpers hitting
// the refresh window together is the normal case, not contention.
const tokenLockTimeout = 30 * time.Second

// newTokenCmd prints a valid credential and NOTHING else — it is the
// credential helper the tools run: Claude Code `apiKeyHelper`, Codex
// `[model_providers.*].auth.command`, opencode `.well-known/opencode`.
// An OAuth profile prints its access token (refreshed under a file lock —
// the AS rotates refresh tokens, so two concurrent refreshes would spend
// one and strand the other); a pk_ profile prints the pk_. Every
// diagnostic goes to stderr.
func newTokenCmd() *cobra.Command {
	var flagProfile string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print a valid credential for the active profile (credential helper)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.Path()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			lease, err := lock.NewLocker(filepath.Join(filepath.Dir(path), "token.lock")).
				Acquire(cmd.Context(), lock.AcquireWithTimeout, tokenLockTimeout)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("token: %v", err), Wrapped: err}
			}
			defer func() { _ = lease.Release() }()

			// Load INSIDE the lock: a helper that waited sees the pair the
			// first one just rotated and returns it without refreshing again.
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
			tok := prof.PK
			if prof.OAuth != nil {
				client := &oauthlogin.Client{BaseURL: prof.URL}
				var updated *config.OAuthCreds
				tok, updated, err = client.CurrentAccessToken(cmd.Context(), prof.OAuth)
				if err != nil {
					return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("token: %v; run `ach-cli login`", err), Wrapped: err}
				}
				if updated != nil {
					prof.OAuth = updated
					if err := config.Save(path, file); err != nil {
						return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
					}
				}
			}
			if tok == "" {
				return &exit.CodedError{
					Code: exit.General, Msg: fmt.Sprintf("profile %q has no login; run `ach-cli login`", name),
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

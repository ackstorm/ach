// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// refreshHTTPClient is the test seam for the refresh HTTP transport.
var refreshHTTPClient *http.Client

func newRefreshCmd() *cobra.Command {
	var f credFlags
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Re-read your identity provider groups and update your access",
		Long: `Ask the Hub to re-read your identity at the identity provider and join the
teams your current SSO groups map to, without a browser. Run it after being
added to a group, instead of logging in again.

Environments follow at their next reconcile (a few minutes). Teams are only
added, never removed. Needs a login session (ach-cli login), not a key.

Exit codes: 0 success, 3 not authorized (log in again), 6 network error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := resolveCred(cmd.Context(), f, synthetic.GateAPI)
			if err != nil {
				return err
			}
			hc := newAPIClient(c.BaseURL, c.Bearer, refreshHTTPClient, f.Verbose, cmd.ErrOrStderr())
			if err := hc.Do(cmd.Context(), http.MethodPost, "/platform/refresh", nil, nil); err != nil {
				return mapVerifyError(err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Access refreshed. Environments update within a few minutes.")
			return nil
		},
	}
	registerCredFlags(cmd, &f)
	return cmd
}

func init() {
	rootCmd.AddCommand(newRefreshCmd())
}

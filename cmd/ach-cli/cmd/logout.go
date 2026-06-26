// SPDX-License-Identifier: Apache-2.0

// `ach logout` wipes the `pk:` field from the active profile per
// CLI spec §5.2 / D-06. URL + EK map are preserved so a subsequent
// `ach login` resumes against the same profile without re-prompting
// for the URL. The profile entry itself stays, and `default:` is
// untouched.
//
// Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) → exit 1 per
// CLI spec §3.3 (D-06).
//
// Note: server-side, the pk- remains valid for its sliding-window TTL
// (Hub §7.1) — by design, so a re-login on the same device resumes
// against an unexpired key. An operator who wants immediate
// revocation runs `ach admin keys revoke pkid_…` (06-08).

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// newLogoutCmd returns a fresh `ach logout` cobra.Command.
func newLogoutCmd() *cobra.Command {
	var flagProfile string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Wipe the active profile's pk- (preserve url:)",
		Long: `Wipe the active profile's pk- from ~/.config/ach/config.yaml.

URL and EK map are preserved; the profile entry stays and
default: is untouched. A subsequent ach login resumes against the
same profile without re-prompting for the URL.

Server-side, the pk- remains valid for its sliding-window TTL.
For immediate revocation, use ach admin keys revoke pkid_….

Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) exits 1.
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLogout(cmd, flagProfile)
		},
	}

	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	return cmd
}

// doLogout is the RunE body.
func doLogout(cmd *cobra.Command, flagProfile string) error {
	stdout := cmd.OutOrStdout()

	// Synthetic-mode gate via the centralized 06-07 helper (D-06).
	// Also rejects half-set before any disk read.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateLogout,
		ProfileFlag: flagProfile,
	}); err != nil {
		return err
	}

	// Load config.
	configPath, err := config.Path()
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.Load(configPath)
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil || len(file.Profiles) == 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "no profile configured; nothing to log out of",
		}
	}

	// Resolve active profile (CLI-08 precedence).
	envProfile := os.Getenv("ACH_PROFILE")
	name, dep, err := config.ResolveActive(file, flagProfile, envProfile)
	if err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     err.Error(),
			Wrapped: err,
		}
	}

	// D-06: wipe pk: only. Preserve URL + EK map.
	dep.PK = ""

	if err := config.Save(configPath, file); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}

	_, _ = fmt.Fprintf(stdout, "Logged out of %s\n", name)
	return nil
}

func init() {
	rootCmd.AddCommand(newLogoutCmd())
}

// SPDX-License-Identifier: Apache-2.0

// `ach logout` wipes the `pk:` field from the active deployment per
// CLI spec §5.2 / D-06. URL + EK map are preserved so a subsequent
// `ach login` resumes against the same deployment without re-prompting
// for the URL. The deployment entry itself stays, and `default:` is
// untouched.
//
// Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) → exit 1 per
// CLI spec §3.3 (D-06).
//
// Note: server-side, the pk_ remains valid for its sliding-window TTL
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
	var flagDeployment string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Wipe the active deployment's pk_ (preserve url:)",
		Long: `Wipe the active deployment's pk_ from ~/.config/ach/config.yaml.

URL and EK map are preserved; the deployment entry stays and
default: is untouched. A subsequent ach login resumes against the
same deployment without re-prompting for the URL.

Server-side, the pk_ remains valid for its sliding-window TTL (Hub
§7.1). For immediate revocation, use ach admin keys revoke pkid_….

Synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) exits 1 per
CLI spec §3.3.
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLogout(cmd, flagDeployment)
		},
	}

	cmd.Flags().StringVar(&flagDeployment, "deployment", "", "Override deployment selection")
	return cmd
}

// doLogout is the RunE body.
func doLogout(cmd *cobra.Command, flagDeployment string) error {
	stdout := cmd.OutOrStdout()

	// Synthetic-mode gate via the centralized 06-07 helper (D-06).
	// Also rejects half-set before any disk read.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:           synthetic.GateLogout,
		DeploymentFlag: flagDeployment,
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
	if file == nil || len(file.Deployments) == 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "no deployment configured; nothing to log out of",
		}
	}

	// Resolve active deployment (CLI-08 precedence).
	envDeployment := os.Getenv("ACH_DEPLOYMENT")
	name, dep, err := config.ResolveActive(file, flagDeployment, envDeployment)
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

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// newEnvCloneCmd builds `ach-cli env clone <env> [dir]`: git-clone semantics
// over the hydrate engine. It creates <dir> (default ./<env>), refuses a
// non-empty destination and hydrates into it. A later bare `ach-cli env hydrate`
// inside the folder updates it (the "pull") from what .ach/ records.
func newEnvCloneCmd() *cobra.Command {
	var (
		flagNoWarnings  bool
		flagInsecure    bool
		flagCred        credFlags
		flagOnlyRuntime bool
		flagTarget      []string
		flagModels      bool
	)
	cmd := &cobra.Command{
		Use:   "clone <env> [dir]",
		Args:  cobra.RangeArgs(1, 2),
		Short: "Create a folder for an Environment and hydrate it (like git clone)",
		Long: `Create <dir> (default: ./<env>) and hydrate the Environment into it. Refuses
a destination that exists and is not empty. On failure the folder is removed
if clone created it.

To update later: cd <dir> && ach-cli env hydrate

An empty folder has nothing to autodetect, so --target (or ACH_TARGET) is
required.

Example:
  ach-cli env clone aws-rw --target claude-code`,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, dir := args[0], args[0]
			if len(args) == 2 {
				dir = args[1]
			}
			if len(flagTarget) == 0 && os.Getenv("ACH_TARGET") == "" {
				return &exit.CodedError{Code: exit.General,
					Msg: "pass --target <id> (or set ACH_TARGET): an empty folder has nothing to autodetect"}
			}
			created, err := prepareCloneDir(dir)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Cloning into '%s'...\n", dir)
			err = runHydrate(cmd, hydrateInputs{
				environment: env,
				noWarnings:  flagNoWarnings,
				verbose:     flagCred.Verbose,
				insecure:    flagInsecure,
				cred:        flagCred,
				onlyRuntime: flagOnlyRuntime,
				output:      dir,
				platform:    flagTarget,
				models:      flagModels,
			})
			if err != nil {
				if created {
					_ = os.RemoveAll(dir)
				}
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Update later with: cd %s && ach-cli env hydrate\n", dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false,
		"Suppress the tip to use an environment key (ek-) instead of your sign-in")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false,
		"Allow a plaintext http:// Hub URL (credentials sent unencrypted; localhost still requires this)")
	registerCredFlags(cmd, &flagCred)
	cmd.Flags().BoolVar(&flagOnlyRuntime, "only-runtime", false,
		"Install ONLY runtime entries (skip context)")
	cmd.Flags().StringSliceVar(&flagTarget, "target", nil,
		"Agent target(s), comma-separated or repeated: claude-code / codex / gemini-cli / opencode / pimono")
	cmd.Flags().BoolVar(&flagModels, "models", false,
		"Route the tool's model traffic through ACH (apiKeyHelper / model provider wiring)")
	return cmd
}

// prepareCloneDir makes dir ready for a clone, mirroring git: a missing dir is
// created (created=true), an empty one is reused, a non-empty one is refused.
func prepareCloneDir(dir string) (created bool, err error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return false, &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
		}
		return true, nil
	case err != nil:
		return false, &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	case len(entries) > 0:
		return false, &exit.CodedError{Code: exit.General,
			Msg: fmt.Sprintf("destination path '%s' already exists and is not an empty directory", dir)}
	}
	return false, nil
}

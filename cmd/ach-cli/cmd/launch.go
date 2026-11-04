// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hydrate"
)

// launchTools maps a hydrate target to the command that starts the tool.
var launchTools = map[string]string{
	"claude-code": "claude",
	"codex":       "codex",
	"gemini-cli":  "gemini",
	"opencode":    "opencode",
	"pi":          "pi",
}

const launchToolList = "claude, codex, gemini, opencode, pi"

// newLaunchCmd builds `ach-cli launch <tool>`: `env hydrate` for one target,
// then the tool started in the hydrated folder with the terminal handed over.
func newLaunchCmd() *cobra.Command {
	var (
		f       hydrateFlags
		flagEnv string
	)
	cmd := &cobra.Command{
		Use:   "launch <tool> [-- tool args...]",
		Args:  cobra.MinimumNArgs(1),
		Short: "Hydrate an Environment, then start the agent tool in it",
		Long: `Hydrate an Environment for one tool (same engine and flags as
` + "`ach-cli env hydrate`" + `), then start the tool in --dir (default: the
current directory). With no --environment and no ACH_ENVIRONMENT, every
Environment already hydrated in that folder (.ach/) is updated for the tool.
If hydrate fails, the tool is not started. ach-cli exits with the tool's exit
code.

Tools: ` + launchToolList + `

Arguments after <tool> are passed to the tool; put the tool's own flags
after --.

Example:
  ach-cli launch claude --environment frontend-dev
  ach-cli launch opencode
  ach-cli launch codex --environment aws-rw --dir ~/work/infra -- --model ackstorm.smart`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := hydrate.ResolvePlatform(args[0])
			bin := launchTools[id]
			if err != nil || bin == "" {
				return &exit.CodedError{Code: exit.General,
					Msg: fmt.Sprintf("unknown tool %q (%s)", args[0], launchToolList)}
			}
			path, err := exec.LookPath(bin)
			if err != nil {
				return &exit.CodedError{Code: exit.General,
					Msg: fmt.Sprintf("%q not found on PATH — install it first, then re-run", bin), Wrapped: err}
			}
			in, err := f.inputs(flagEnv)
			if err != nil {
				return err
			}
			in.platform = []string{id}
			if err := runHydrate(cmd, in); err != nil {
				return err
			}

			line := strings.Join(append([]string{bin}, args[1:]...), " ")
			if in.output != "" {
				line += "   (in " + in.output + ")"
			}
			newUI(cmd.ErrOrStderr(), in.verbose).verb("Launching", "%s", line)
			return runTool(cmd, path, args[1:], in.output)
		},
	}
	cmd.Flags().StringVar(&flagEnv, "environment", "",
		"Environment to hydrate (ACH_ENVIRONMENT is the same; default: those in .ach/)")
	f.register(cmd)
	return cmd
}

// runTool runs the tool on this terminal and returns its exit code as a
// silent CodedError (the tool already reported its own failure).
func runTool(cmd *cobra.Command, path string, args []string, dir string) error {
	c := exec.Command(path, args...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	// Ctrl-C reaches the whole foreground process group: the tool handles
	// it, and ach-cli must outlive it to hand back the tool's exit code.
	// Notify (not Ignore) so the child starts with the default disposition.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() > 0 {
		return &exit.CodedError{Code: exit.Code(ee.ExitCode()), Wrapped: err}
	}
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newLaunchCmd())
}

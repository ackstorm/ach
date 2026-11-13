// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

// claudeCodeID is the target launch fronts with its proxy.
const claudeCodeID = "claude-code"

// newLaunchCmd builds `ach-cli launch <tool>`: `environment hydrate` for one target,
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
` + "`ach-cli environment hydrate`" + `), then start the tool in --dir (default: the
current directory). With no --environment and no ACH_ENVIRONMENT, every
Environment already hydrated in that folder (.ach/) is updated for the tool;
a folder with none starts the tool without hydrating. If hydrate fails, the
tool is not started. ach-cli exits with the tool's exit code.

claude keeps its own Claude login and sends its model traffic through ACH:
launch serves the profile's URL on a local port for as long as claude runs,
adding your ACH sign-in (or --key) to every request. ANTHROPIC_BASE_URL and
ANTHROPIC_CUSTOM_HEADERS in your shell are ignored; a Claude settings file
that sets ANTHROPIC_BASE_URL stops the launch. --models is refused for
claude.

Tools: ` + launchToolList + `

Arguments after <tool> are passed to the tool; put the tool's own flags
after --.

Example:
  ach-cli launch claude --environment frontend-dev
  ach-cli launch claude --profile work -- -c
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
			u := newUI(cmd.ErrOrStderr(), in.verbose)

			var (
				src *launchCred
				c   cred
			)
			if id == claudeCodeID {
				if in.models {
					return &exit.CodedError{Code: exit.General,
						Msg: "--models writes apiKeyHelper, which replaces your Claude login; " +
							"launch claude routes models through ACH without it"}
				}
				cf := in.cred
				cf.Insecure = in.insecure
				src = &launchCred{f: cf}
				if c, err = src.get(cmd.Context()); err != nil {
					return err
				}
				for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS"} {
					if os.Getenv(k) != "" {
						u.verb("Warning", "ignoring %s from your shell; launch connects claude to ACH itself", k)
					}
				}
			}

			if launchNeedsHydrate(in) {
				if err := runHydrate(cmd, in); err != nil {
					return err
				}
			} else {
				u.verb("Note", "no Environment in this folder; starting without hydrating")
			}

			line := strings.Join(append([]string{bin}, args[1:]...), " ")
			if in.output != "" {
				line += "   (in " + in.output + ")"
			}
			var env []string
			if src != nil {
				if file := claudeSettingsBaseURL(in.output); file != "" {
					return &exit.CodedError{Code: exit.General,
						Msg: file + " sets env.ANTHROPIC_BASE_URL, which overrides launch and bypasses ACH\n" +
							`hint: remove "ANTHROPIC_BASE_URL" from the "env" block of ` + file +
							` (or, if ` + "`ach-cli environment hydrate --models`" + ` wrote it, re-run that hydrate without --models)`}
				}
				addr, stop, err := startLaunchProxy(src, c.BaseURL)
				if err != nil {
					return &exit.CodedError{Code: exit.General, Msg: "launch proxy: " + err.Error(), Wrapped: err}
				}
				defer stop()
				env = claudeEnv(os.Environ(), addr)
				via := "models via " + c.BaseURL
				if in.cred.Profile != "" || os.Getenv("ACH_PROFILE") != "" {
					via += ", profile " + c.ProfileName
				}
				line += "   (" + via + ", Claude login kept)"
			}
			u.verb("Launching", "%s", line)
			return runTool(cmd, path, args[1:], in.output, env)
		},
	}
	cmd.Flags().StringVar(&flagEnv, "environment", "",
		"Environment to hydrate (ACH_ENVIRONMENT is the same; default: those in .ach/)")
	f.register(cmd)
	return cmd
}

// runTool runs the tool on this terminal and returns its exit code as a
// silent CodedError (the tool already reported its own failure).
func runTool(cmd *cobra.Command, path string, args []string, dir string, env []string) error {
	c := exec.Command(path, args...)
	c.Dir, c.Env = dir, env
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

// launchNeedsHydrate is false only when nothing names an Environment and
// the folder holds none: the tool then starts as it is.
func launchNeedsHydrate(in hydrateInputs) bool {
	if in.environment != "" || os.Getenv("ACH_ENVIRONMENT") != "" {
		return true
	}
	root := in.output
	if root == "" {
		root = "."
	}
	envs, err := hydratedEnvs(root)
	return err != nil || len(envs) > 0
}

// claudeSettingsBaseURL names the first Claude Code settings file whose env
// block sets ANTHROPIC_BASE_URL: Claude Code applies it over the process
// environment, so it would send the tool past the launch proxy.
func claudeSettingsBaseURL(dir string) string {
	if dir == "" {
		dir = "."
	}
	user := os.Getenv("CLAUDE_CONFIG_DIR")
	if user == "" {
		home, _ := os.UserHomeDir()
		user = filepath.Join(home, ".claude")
	}
	for _, p := range []string{
		filepath.Join(user, "settings.json"),
		filepath.Join(dir, ".claude", "settings.json"),
		filepath.Join(dir, ".claude", "settings.local.json"),
	} {
		b, err := os.ReadFile(p) // #nosec G304 — Claude Code's settings files, user config dir or launch dir
		if err != nil {
			continue
		}
		var s struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(b, &s) == nil && s.Env["ANTHROPIC_BASE_URL"] != nil && s.Env["ANTHROPIC_BASE_URL"] != "" {
			return p
		}
	}
	return ""
}

// claudeEnv is the launch environment for claude: the shell's, minus the
// gateway variables launch replaces, pointed at the proxy.
func claudeEnv(environ []string, addr string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") && !strings.HasPrefix(kv, "ANTHROPIC_CUSTOM_HEADERS=") {
			out = append(out, kv)
		}
	}
	return append(out, "ANTHROPIC_BASE_URL="+addr)
}

func init() {
	rootCmd.AddCommand(newLaunchCmd())
}

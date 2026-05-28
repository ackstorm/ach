// SPDX-License-Identifier: Apache-2.0

// `ach config` is the local-mutate registry surface (D-05 / CLI spec
// §5.4) — five children that read and write ~/.config/ach/config.yaml
// without ever contacting the server:
//
//   - list    Print the deployments table (NAME, URL, PK presence, EK count).
//   - show    Print one deployment's URL + pk + ek map. --reveal opts
//             into the full plaintext unmask for the named deployment
//             only (D-05 — masked-by-default is the contract).
//   - use     Set `default:` to <name>. Refuses unknown names.
//   - remove  Delete a deployment. Active default removal needs --force
//             AND clears `default:` so subsequent commands don't silently
//             route to an unintended deployment.
//   - rename  Rename a map key, preserving PK + EK map. Refuses rename
//             onto an existing target. Updates `default:` when it was
//             pointing at <old>.
//
// Synthetic-mode short-circuit: when ACH_BASE_URL + ACH_API_KEY are
// both set, every child exits 1 (CLI spec §3.3) — config-mutating
// commands have no meaning when the credential lives in env. The
// short-circuit mirrors the inline check in login/logout.go;
// centralization lands in W3-P1 via internal/cli/synthetic.

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/render"
)

// newConfigCmd returns a fresh `ach config` parent cobra.Command with
// all 5 children registered. Factory shape (per 06-03 Pattern P2)
// keeps tests hermetic — each subtest constructs an isolated tree.
func newConfigCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "config",
		Short: "Manage ~/.config/ach/config.yaml (deployments registry)",
		Long: `Manage the local CLI configuration at ~/.config/ach/config.yaml.

Children:
  list      Print the deployments table
  show      Print one deployment (--reveal unmasks pk_/ek_)
  use       Set default: to <name>
  remove    Delete a deployment (--force required for active default)
  rename    Rename a map key (preserves PK + EK map)

All children exit 1 in synthetic mode (ACH_BASE_URL + ACH_API_KEY
both set) per CLI spec §3.3 — synthetic-mode credentials live in
env, so the on-disk registry has no role.
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	parent.AddCommand(
		newConfigListCmd(),
		newConfigShowCmd(),
		newConfigUseCmd(),
		newConfigRemoveCmd(),
		newConfigRenameCmd(),
	)
	return parent
}

// newConfigListCmd returns the `ach config list` leaf.
func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the deployments table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := configSyntheticGuard("list"); err != nil {
				return err
			}
			f, err := loadConfigForCmd()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatConfigList(f))
			return nil
		},
	}
}

// newConfigShowCmd returns the `ach config show [deployment]` leaf.
func newConfigShowCmd() *cobra.Command {
	var reveal bool
	c := &cobra.Command{
		Use:   "show [deployment]",
		Short: "Print one deployment (pk_/ek_ masked unless --reveal)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := configSyntheticGuard("show"); err != nil {
				return err
			}
			f, err := loadConfigForCmd()
			if err != nil {
				return err
			}
			if f == nil || len(f.Deployments) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no deployments configured; run `ach login`",
				}
			}
			var name string
			if len(args) == 1 {
				name = args[0]
			}
			resolved, dep, resErr := config.ResolveActive(f, name, "")
			if resErr != nil {
				return &exit.CodedError{
					Code:    exit.General,
					Msg:     resErr.Error(),
					Wrapped: resErr,
				}
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatConfigShow(resolved, dep, reveal))
			return nil
		},
	}
	c.Flags().BoolVar(&reveal, "reveal", false, "Unmask pk_/ek_ for the named deployment only (D-05)")
	return c
}

// newConfigUseCmd returns the `ach config use <name>` leaf.
func newConfigUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set default: to <name>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := configSyntheticGuard("use"); err != nil {
				return err
			}
			path, err := config.Path()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			f, err := config.Load(path)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			if f == nil || len(f.Deployments) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no deployments configured; run `ach login`",
				}
			}
			name := args[0]
			if _, ok := f.Deployments[name]; !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("deployment %q not found", name),
				}
			}
			f.Default = name
			if err := config.Save(path, f); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "default set to %s\n", name)
			return nil
		},
	}
}

// newConfigRemoveCmd returns the `ach config remove <name>` leaf.
func newConfigRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a deployment (--force required for active default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := configSyntheticGuard("remove"); err != nil {
				return err
			}
			path, err := config.Path()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			f, err := config.Load(path)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			if f == nil || len(f.Deployments) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no deployments configured; nothing to remove",
				}
			}
			name := args[0]
			if _, ok := f.Deployments[name]; !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("deployment %q not found", name),
				}
			}
			if f.Default == name && !force {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("cannot remove active default %q; use --force", name),
				}
			}
			delete(f.Deployments, name)
			if f.Default == name {
				// T-06-04-02 mitigation: clear default so subsequent
				// commands don't silently route to a vanished deployment.
				f.Default = ""
			}
			if err := config.Save(path, f); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Required to remove the active default deployment")
	return c
}

// newConfigRenameCmd returns the `ach config rename <old> <new>` leaf.
func newConfigRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a deployment map key (preserves PK + EK map)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := configSyntheticGuard("rename"); err != nil {
				return err
			}
			path, err := config.Path()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			f, err := config.Load(path)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			if f == nil || len(f.Deployments) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no deployments configured; nothing to rename",
				}
			}
			oldName, newName := args[0], args[1]
			dep, ok := f.Deployments[oldName]
			if !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("deployment %q not found", oldName),
				}
			}
			if _, exists := f.Deployments[newName]; exists {
				// T-06-04-03 mitigation: target exists → exit 1; never
				// silently merge.
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("rename target %q already exists; remove it first", newName),
				}
			}
			delete(f.Deployments, oldName)
			f.Deployments[newName] = dep
			if f.Default == oldName {
				f.Default = newName
			}
			if err := config.Save(path, f); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "renamed %s -> %s\n", oldName, newName)
			return nil
		},
	}
}

// loadConfigForCmd loads the config file via the canonical Path +
// Load helpers, wrapping config errors with exit.ConfigFile (code 8)
// so the cobra dispatcher in main.go maps to the right exit code.
func loadConfigForCmd() (*config.File, error) {
	path, err := config.Path()
	if err != nil {
		return nil, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	f, err := config.Load(path)
	if err != nil {
		return nil, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	return f, nil
}

// configSyntheticGuard returns a CodedError when synthetic-mode env
// vars are set (ACH_BASE_URL + ACH_API_KEY). Mirrors the inline check
// used by login.go + logout.go; centralization lands in W3-P1
// (06-07) via internal/cli/synthetic.GuardCommand.
func configSyntheticGuard(sub string) error {
	if syntheticActive() {
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"ach config %s is not available in synthetic mode "+
					"(ACH_BASE_URL + ACH_API_KEY set; see CLI spec §3.3)",
				sub,
			),
		}
	}
	return nil
}

// syntheticActive reports whether both ACH_BASE_URL and ACH_API_KEY
// are non-empty (synthetic-mode trigger per CLI spec §3.3). Extracted
// as a package-level helper so config.go's 5 children share one
// definition with potential future callers.
func syntheticActive() bool {
	return os.Getenv("ACH_BASE_URL") != "" && os.Getenv("ACH_API_KEY") != ""
}

func init() {
	rootCmd.AddCommand(newConfigCmd())
}

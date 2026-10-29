// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// newProfileCmd returns `ach-cli profile`: the local profiles registry in
// ~/.config/ach/config.yaml. No verb contacts the Hub.
func newProfileCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "profile",
		Short: "Manage your profiles (~/.config/ach/config.yaml)",
		Long: `Manage the profiles in ~/.config/ach/config.yaml. A profile is a Hub URL
plus a credential: an OAuth session from ach-cli login, or a machine key
(ek-…) from ach-cli profile add. Keys made with keys create are saved in it
by name.

With ACH_URL and ACH_KEY both set (synthetic mode) every verb exits 1.`,
		RunE: helpOrUnknownSubcommand,
	}
	parent.AddCommand(newProfileListCmd(), newProfileShowCmd(), newProfileUseCmd(),
		newProfileAddCmd(), newProfileRenameCmd(), newProfileRemoveCmd())
	return parent
}

// loadProfiles is the shared preamble of every profile verb: the synthetic
// gate, then the config file (nil when absent).
func loadProfiles() (string, *config.File, error) {
	if err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateSession}); err != nil {
		return "", nil, err
	}
	path, err := config.Path()
	if err != nil {
		return "", nil, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	f, err := config.Load(path)
	if err != nil {
		return "", nil, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	return path, f, nil
}

// requireProfiles loads the file and fails when it has no profile.
func requireProfiles() (string, *config.File, error) {
	path, f, err := loadProfiles()
	if err == nil && (f == nil || len(f.Profiles) == 0) {
		err = &exit.CodedError{Code: exit.General,
			Msg: "no profiles configured; run `ach-cli login` or `ach-cli profile add`"}
	}
	return path, f, err
}

func profileNotFound(f *config.File, name string) error {
	return &exit.CodedError{
		Code: exit.General,
		Msg:  fmt.Sprintf("profile %q not found; available: %s", name, strings.Join(f.ProfileNames(), ", ")),
	}
}

func saveProfiles(path string, f *config.File) error {
	if err := config.Save(path, f); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	return nil
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles (* marks the default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, f, err := loadProfiles()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatProfileList(f))
			return nil
		},
	}
}

func newProfileShowCmd() *cobra.Command {
	var reveal bool
	c := &cobra.Command{
		Use:   "show [name]",
		Short: "Show one profile and its saved keys (masked unless --reveal)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, f, err := requireProfiles()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			resolved, dep, err := config.ResolveActive(f, name, "")
			if err != nil {
				if name != "" {
					return profileNotFound(f, name)
				}
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatProfileShow(resolved, dep, reveal))
			return nil
		},
	}
	c.Flags().BoolVar(&reveal, "reveal", false, "Print the keys in full")
	return c
}

func newProfileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Make <name> the default profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, f, err := requireProfiles()
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := f.Profiles[name]; !ok {
				return profileNotFound(f, name)
			}
			f.Default = name
			if err := saveProfiles(path, f); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "default set to %s\n", name)
			return nil
		},
	}
}

func newProfileRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a profile (--force for the default one)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, f, err := requireProfiles()
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := f.Profiles[name]; !ok {
				return profileNotFound(f, name)
			}
			if f.Default == name && !force {
				return &exit.CodedError{Code: exit.General,
					Msg: fmt.Sprintf("cannot remove the default profile %q; use --force", name)}
			}
			delete(f.Profiles, name)
			reassigned := ""
			if f.Default == name {
				// Never leave default dangling: pick the sorted-first remaining
				// profile and say so.
				f.Default = ""
				if names := f.ProfileNames(); len(names) > 0 {
					f.Default, reassigned = names[0], names[0]
				}
			}
			if err := saveProfiles(path, f); err != nil {
				return err
			}
			if reassigned != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s; default reassigned to %s\n", name, reassigned)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Allow removing the default profile")
	return c
}

func newProfileRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a profile (keeps its credential and saved keys)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, f, err := requireProfiles()
			if err != nil {
				return err
			}
			oldName, newName := args[0], args[1]
			dep, ok := f.Profiles[oldName]
			if !ok {
				return profileNotFound(f, oldName)
			}
			if _, exists := f.Profiles[newName]; exists {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("profile %q already exists; remove it first", newName)}
			}
			delete(f.Profiles, oldName)
			f.Profiles[newName] = dep
			if f.Default == oldName {
				f.Default = newName
			}
			if err := saveProfiles(path, f); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "renamed %s -> %s\n", oldName, newName)
			return nil
		},
	}
}

// newProfileAddCmd is the headless counterpart to login: a machine profile
// (CI, an agent box) holding one ek-… key instead of an OAuth session.
func newProfileAddCmd() *cobra.Command {
	var (
		flagURL      string
		flagKey      string
		flagDefault  bool
		flagForce    bool
		flagInsecure bool
	)
	c := &cobra.Command{
		Use:   "add <name> --url <url> --key <ek-…>",
		Short: "Add a machine profile that signs in with a key (no browser)",
		Long: `Add a profile that authenticates with an environment key (ek-…) instead
of an OAuth session — for CI and agent boxes where a browser login is not
possible. Make the key with ach-cli keys create --no-save on a signed-in
machine.

The first profile added becomes the default; --default makes this one the
default anyway. --force overwrites a profile of the same name (its saved
keys are kept).`,
		Example: `  ach-cli profile add ci --url https://hub.example.com --key ek-…`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfileAdd(cmd, args[0], flagURL, flagKey, flagDefault, flagForce, flagInsecure)
		},
	}
	c.Flags().StringVar(&flagURL, "url", "", "Hub URL (https://; http:// needs --insecure)")
	c.Flags().StringVar(&flagKey, "key", "", "The environment key (ek-…) this profile signs in with")
	c.Flags().BoolVar(&flagDefault, "default", false, "Make this the default profile")
	c.Flags().BoolVar(&flagForce, "force", false, "Overwrite an existing profile of the same name")
	c.Flags().BoolVar(&flagInsecure, "insecure", false, "Allow a plaintext http:// Hub URL (the key travels unencrypted)")
	_ = c.MarkFlagRequired("url")
	_ = c.MarkFlagRequired("key")
	return c
}

func runProfileAdd(cmd *cobra.Command, name, url, key string, setDefault, force, insecure bool) error {
	path, file, err := loadProfiles()
	if err != nil {
		return err
	}
	if !profileNamePattern.MatchString(name) {
		return &exit.CodedError{Code: exit.General,
			Msg: fmt.Sprintf("profile name %q is invalid; expected DNS-1123 label (lower-case [a-z0-9-])", name)}
	}
	if !synthetic.IsRawKey(key) {
		return &exit.CodedError{Code: exit.General, Msg: "--key must be an environment key (ek-…)"}
	}
	allowInsecure := insecure || config.InsecureFromEnv()
	if err := config.ValidateSecureURL(url, allowInsecure); err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		file = &config.File{}
	}
	if file.Profiles == nil {
		file.Profiles = map[string]*config.Profile{}
	}
	existing := file.Profiles[name]
	if existing != nil && !force {
		return &exit.CodedError{Code: exit.General,
			Msg: fmt.Sprintf("profile %q already exists; pass --force to overwrite", name)}
	}
	dep := &config.Profile{URL: url, Key: key}
	if existing != nil {
		dep.Keys = existing.Keys
	}
	file.Profiles[name] = dep
	if setDefault || file.Default == "" {
		file.Default = name
	}
	if err := config.SaveInsecure(path, file, allowInsecure); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "added profile %s (%s)\n", name, config.Mask(key))
	return nil
}

func init() {
	rootCmd.AddCommand(newProfileCmd())
}

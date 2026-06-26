// SPDX-License-Identifier: Apache-2.0

// `ach config` is the local-mutate registry surface (D-05 / CLI spec
// §5.4) — six children that read and write ~/.config/ach/config.yaml
// without ever contacting the server:
//
//   - add     Register a profile from an existing pk-/ek- (the headless
//             counterpart to `ach login` — no browser SSO). Stores the
//             credential in Profile.PK; --env-key seeds the EK label map.
//   - list    Print the profiles table (NAME, URL, PK presence, EK count).
//   - show    Print one profile's URL + pk + ek map. --reveal opts
//             into the full plaintext unmask for the named profile
//             only (D-05 — masked-by-default is the contract).
//   - use     Set `default:` to <name>. Refuses unknown names.
//   - remove  Delete a profile. Active default removal needs --force
//             AND clears `default:` so subsequent commands don't silently
//             route to an unintended profile.
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
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// newConfigCmd returns a fresh `ach config` parent cobra.Command with
// all 6 children registered. Factory shape (per 06-03 Pattern P2)
// keeps tests hermetic — each subtest constructs an isolated tree.
func newConfigCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "config",
		Short: "Manage ~/.config/ach/config.yaml (profiles registry)",
		Long: `Manage the local CLI configuration at ~/.config/ach/config.yaml.

In synthetic mode (ACH_BASE_URL + ACH_API_KEY both set) the on-disk
registry is unused and all children exit 1.
`,
		RunE: helpOrUnknownSubcommand,
	}

	parent.AddCommand(
		newConfigAddCmd(),
		newConfigListCmd(),
		newConfigShowCmd(),
		newConfigUseCmd(),
		newConfigRemoveCmd(),
		newConfigRenameCmd(),
		newConfigRmEKCmd(),
	)
	return parent
}

// newConfigListCmd returns the `ach config list` leaf.
func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the profiles table",
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

// newConfigShowCmd returns the `ach config show [profile]` leaf.
func newConfigShowCmd() *cobra.Command {
	var reveal bool
	c := &cobra.Command{
		Use:     "show [profile]",
		Aliases: []string{"get"},
		Short:   "Print one profile (pk-/ek- masked unless --reveal)",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := configSyntheticGuard("show"); err != nil {
				return err
			}
			f, err := loadConfigForCmd()
			if err != nil {
				return err
			}
			if f == nil || len(f.Profiles) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no profiles configured; run `ach login` or `ach config add`",
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
	c.Flags().BoolVar(&reveal, "reveal", false, "Unmask pk-/ek- for the named profile only")
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
			if f == nil || len(f.Profiles) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no profiles configured; run `ach login` or `ach config add`",
				}
			}
			name := args[0]
			if _, ok := f.Profiles[name]; !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("profile %q not found", name),
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
		Short: "Delete a profile (--force required for active default)",
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
			if f == nil || len(f.Profiles) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no profiles configured; nothing to remove",
				}
			}
			name := args[0]
			if _, ok := f.Profiles[name]; !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("profile %q not found", name),
				}
			}
			if f.Default == name && !force {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("cannot remove active default %q; use --force", name),
				}
			}
			delete(f.Profiles, name)
			if f.Default == name {
				// T-06-04-02 mitigation: clear default so subsequent
				// commands don't silently route to a vanished profile.
				f.Default = ""
			}
			if err := config.Save(path, f); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Required to remove the active default profile")
	return c
}

// newConfigRenameCmd returns the `ach config rename <old> <new>` leaf.
func newConfigRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a profile map key (preserves PK + EK map)",
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
			if f == nil || len(f.Profiles) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "no profiles configured; nothing to rename",
				}
			}
			oldName, newName := args[0], args[1]
			dep, ok := f.Profiles[oldName]
			if !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("profile %q not found", oldName),
				}
			}
			if _, exists := f.Profiles[newName]; exists {
				// T-06-04-03 mitigation: target exists → exit 1; never
				// silently merge.
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("rename target %q already exists; remove it first", newName),
				}
			}
			delete(f.Profiles, oldName)
			f.Profiles[newName] = dep
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

// newConfigAddCmd returns the `ach config add` leaf — the headless
// counterpart to `ach login`. Where login mints a pk- via the browser
// SSO round-trip, `config add` registers a profile from a credential
// the caller ALREADY holds: a pk- copied from a login on another
// machine, or an ek- from `ach env-keys create`. This is the
// agent/CI path — no server contact, no browser.
//
// The --api-key plaintext is stored in Profile.PK, the profile's
// default-bearer slot (hydrate's no-flag credential path reads it).
// PK here means "default bearer for this profile"; an ek- is a valid
// value for a service profile. Per-environment ek- belong in the
// Profile.EK label map (see `--env-key`, added separately).
func newConfigAddCmd() *cobra.Command {
	var (
		flagProfile  string
		flagURL      string
		flagAPIKey   string
		flagEnvKeys  []string
		flagDefault  bool
		flagForce    bool
		flagInsecure bool
	)
	c := &cobra.Command{
		Use:   "add --profile <name> --url <url> --api-key <pk-|ek->",
		Short: "Register a profile from an existing pk-/ek- (no SSO)",
		Long: `Register a profile from a credential you already hold — the headless
counterpart to ach login (which needs a browser SSO round-trip).

Use this on an agent/CI box: mint an ek- with ach env-keys create (or
copy a pk- from a login elsewhere), then seed a working profile here.

  ach config add --profile prod --url https://hub.example --api-key ek-...

The credential is written to Profile.PK (the default bearer used by
ach hydrate when no --api-key/--env-key/ACH_API_KEY/ACH_ENV_KEY is set).
Exits 1 in synthetic mode (ACH_BASE_URL + ACH_API_KEY both set).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigAdd(cmd, flagProfile, flagURL, flagAPIKey, flagEnvKeys, flagDefault, flagForce, flagInsecure)
		},
	}
	c.Flags().StringVar(&flagProfile, "profile", "", "Profile name to create (DNS-1123 label)")
	c.Flags().StringVar(&flagURL, "url", "", "Hub URL (http:// or https://)")
	c.Flags().StringVar(&flagAPIKey, "api-key", "", "Existing pk- or ek- plaintext to store")
	c.Flags().BoolVar(&flagDefault, "default", false, "Set this profile as the default")
	c.Flags().BoolVar(&flagForce, "force", false, "Overwrite an existing profile of the same name")
	c.Flags().BoolVar(&flagInsecure, "insecure", false,
		"Allow a plaintext http:// Hub URL (credentials stored/used unencrypted; localhost still requires this)")
	c.Flags().StringArrayVar(&flagEnvKeys, "env-key", nil,
		"Seed a labelled ek- into profiles.<name>.ek (label=ek-...); repeatable")
	_ = c.MarkFlagRequired("profile")
	_ = c.MarkFlagRequired("url")
	_ = c.MarkFlagRequired("api-key")
	return c
}

// runConfigAdd validates the three required inputs, then creates (or,
// with --force, overwrites) the named profile and saves it 0600. The
// first profile written becomes the default; --default forces it.
func runConfigAdd(
	cmd *cobra.Command, name, url, apiKey string, envKeys []string, setDefault, force, insecure bool,
) error {
	if err := configSyntheticGuard("add"); err != nil {
		return err
	}
	allowInsecure := insecure || config.InsecureFromEnv()
	stdout := cmd.OutOrStdout()

	// Validate name — DNS-1123 label (reuse login's package-level pattern).
	if !profileNamePattern.MatchString(name) {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("profile name %q is invalid; expected DNS-1123 label (lower-case [a-z0-9-])", name),
		}
	}
	// Validate URL scheme — config.Save is the backstop (ErrInvalidURLScheme,
	// exit 8), but give a friendlier exit-1 message up front.
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return &exit.CodedError{Code: exit.General, Msg: "url must be http:// or https://"}
	}
	// Validate credential shape — pk- or ek-, canonical 29-char length.
	if _, err := keys.ClassifyBearer(apiKey); err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("--api-key is not a valid pk-/ek- bearer: %v", err),
			Wrapped: err,
		}
	}
	// Parse --env-key label=ek- specs. Each value MUST be an ek- bearer
	// (a pk- is the profile default, not a per-environment key).
	ekMap := map[string]string{}
	for _, spec := range envKeys {
		label, ek, ok := strings.Cut(spec, "=")
		if !ok {
			// No '=' at all: spec is a bare label (no secret) — safe to quote.
			return &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("--env-key %q: missing '=' separator; expected label=ek-...", spec),
			}
		}
		if label == "" {
			// spec is "=<value>"; do NOT quote spec — it carries the ek- plaintext.
			return &exit.CodedError{
				Code: exit.General,
				Msg:  "--env-key: label must not be empty; expected label=ek-...",
			}
		}
		prefix, err := keys.ClassifyBearer(ek)
		if err != nil || prefix != keys.PrefixEk {
			return &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("--env-key %q value is not a valid ek- bearer", label),
			}
		}
		ekMap[label] = ek
	}
	// G19: refuse a plaintext http:// URL unless the user opted into insecure
	// transport (--insecure flag OR ACH_INSECURE env). localhost is NOT exempt
	// (decision B). SaveInsecure below is the backstop with the same gate.
	if err := config.ValidateSecureURL(url, allowInsecure); err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}

	path, err := config.Path()
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.Load(path)
	if err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		file = &config.File{}
	}
	if file.Profiles == nil {
		file.Profiles = map[string]*config.Profile{}
	}
	if _, exists := file.Profiles[name]; exists && !force {
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf("profile %q already exists; pass --force to overwrite "+
				"or run `ach config remove %s` first", name, name),
		}
	}

	// On --force overwrite, URL + PK are replaced and the existing EK
	// label map is preserved; any --env-key passed this invocation is
	// then merged on top, OVERRIDING a pre-existing entry of the same
	// label (last-write-wins per label).
	dep := &config.Profile{URL: url, PK: apiKey}
	if existing := file.Profiles[name]; existing != nil {
		dep.EK = existing.EK
	}
	if len(ekMap) > 0 {
		if dep.EK == nil {
			dep.EK = map[string]string{}
		}
		for label, ek := range ekMap {
			dep.EK[label] = ek
		}
	}
	file.Profiles[name] = dep
	if setDefault || file.Default == "" {
		file.Default = name
	}
	if err := config.SaveInsecure(path, file, allowInsecure); err != nil {
		return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	_, _ = fmt.Fprintf(stdout, "added profile %s (%s)\n", name, config.Mask(apiKey))
	return nil
}

// newConfigRmEKCmd returns the `ach config rm-ek <label>` leaf — removes a
// single ek_ label from the active (or --profile) profile's saved key map.
// Use after `keys revoke <ekid_…>` to drop the now-dead local entry (the
// profile stores the ek_ plaintext, not the key id, so revoke can't match it).
func newConfigRmEKCmd() *cobra.Command {
	var flagProfile string
	cmd := &cobra.Command{
		Use:   "rm-ek <label>",
		Short: "Remove one saved environment-key (ek_) label from a profile",
		Long: `Delete a single ek_ label from the active profile's saved keys.

Use this after 'keys revoke <ekid_…>' to drop the now-dead local label
(the server revoke cannot match the local label automatically, since the
profile stores the ek_ plaintext, not its key id).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			label := args[0]
			cfgPath, err := config.Path()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			file, err := config.Load(cfgPath)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			name, prof, err := config.ResolveActive(file, flagProfile, "")
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			if _, ok := prof.EK[label]; !ok {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("no ek label %q in profile %s", label, name),
				}
			}
			delete(prof.EK, label)
			if err := config.Save(cfgPath, file); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed ek label %q from profile %s\n", label, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&flagProfile, "profile", "", "Override profile selection")
	return cmd
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

// configSyntheticGuard delegates to the centralized 06-07 helper. The
// `sub` argument is preserved in the local signature so call sites
// stay self-documenting (`configSyntheticGuard("list")` etc.) — the
// shared helper's message already names the gate (`ach config is not
// available in synthetic mode...`), so the sub label is no longer
// folded into the rendered string.
func configSyntheticGuard(_ string) error {
	return synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateConfig})
}

func init() {
	rootCmd.AddCommand(newConfigCmd())
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// credFlags are the credential flags every network command carries.
// Insecure is not registered here: only commands with their own --insecure
// (env hydrate) set it, to load an http:// profile.
type credFlags struct {
	Profile  string
	Key      string
	Verbose  bool
	Insecure bool
}

// registerCredFlags wires --profile, --key and --verbose.
func registerCredFlags(cmd *cobra.Command, f *credFlags) {
	cmd.Flags().StringVar(&f.Profile, "profile", "", "Use this profile instead of the active one")
	cmd.Flags().StringVar(&f.Key, "key", "",
		"Authenticate with a key: a name saved in the profile, or a raw ek-… key")
	cmd.Flags().BoolVar(&f.Verbose, "verbose", false, "Dump request headers to stderr (credentials redacted)")
}

// cred is a resolved credential. Profile, File and Path are nil/"" in
// synthetic mode (there is no config file).
type cred struct {
	BaseURL, Bearer, ProfileName string
	Profile                      *config.Profile
	File                         *config.File
	Path                         string
}

// resolveCred picks the Hub URL + bearer for a network command:
//
//   - synthetic mode (ACH_URL + ACH_KEY): both from env; a raw --key
//     overrides ACH_KEY.
//   - otherwise the profile (--profile → ACH_PROFILE → default → sole one),
//     and the bearer --key → ACH_KEY → the profile's own credential (OAuth
//     access token, else its Key). A key value is raw when it is an ek-…,
//     else a name looked up in the profile's saved keys.
func resolveCred(ctx context.Context, f credFlags, gate synthetic.Gate) (cred, error) {
	if err := synthetic.GuardCommand(synthetic.Params{Gate: gate, ProfileFlag: f.Profile, KeyFlag: f.Key}); err != nil {
		return cred{}, err
	}
	if synthetic.IsActive() {
		bearer := f.Key
		if bearer == "" {
			bearer = synthetic.Getenv("ACH_KEY")
		}
		return cred{BaseURL: synthetic.Getenv("ACH_URL"), Bearer: bearer, ProfileName: synthetic.SyntheticProfileLabel}, nil
	}
	path, err := config.Path()
	if err != nil {
		return cred{}, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	file, err := config.LoadWithInsecure(path, nil, f.Insecure || config.InsecureFromEnv())
	if err != nil {
		return cred{}, &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
	}
	if file == nil {
		return cred{}, &exit.CodedError{Code: exit.General, Msg: "no profile configured; run `ach-cli login`"}
	}
	name, prof, err := config.ResolveActive(file, f.Profile, os.Getenv("ACH_PROFILE"))
	if err != nil {
		return cred{}, &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("%v; run `ach-cli login`", err), Wrapped: err}
	}
	c := cred{BaseURL: prof.URL, ProfileName: name, Profile: prof, File: file, Path: path}
	key := f.Key
	if key == "" {
		key = os.Getenv("ACH_KEY")
	}
	switch {
	case key == "":
		if c.Bearer, err = profileBearer(ctx, file, path, prof); err != nil {
			return cred{}, err
		}
		if c.Bearer == "" {
			return cred{}, &exit.CodedError{Code: exit.General,
				Msg: fmt.Sprintf("profile %q has no credential; run `ach-cli login`", name)}
		}
	case synthetic.IsRawKey(key):
		c.Bearer = key
	default:
		saved, ok := prof.Keys[key]
		if !ok {
			hint := "it has no saved keys (`ach-cli keys create <env>` saves one)"
			if len(prof.Keys) > 0 {
				hint = "saved keys: " + strings.Join(slices.Sorted(maps.Keys(prof.Keys)), ", ")
			}
			return cred{}, &exit.CodedError{Code: exit.General,
				// The value is not echoed: it may be a mistyped secret.
				Msg: fmt.Sprintf("--key / ACH_KEY names no key saved in profile %q; %s", name, hint)}
		}
		c.Bearer = saved.Key
	}
	return c, nil
}

// outputFlag is the -o table|json[|yaml] flag.
type outputFlag struct {
	v       string
	allowed []string
}

// registerOutputFlag wires -o (default "table") and rejects any value
// outside allowed before RunE.
func registerOutputFlag(cmd *cobra.Command, o *outputFlag, allowed ...string) {
	o.allowed = allowed
	list := strings.Join(allowed, "|")
	cmd.Flags().StringVarP(&o.v, "output", "o", "table", "Output format: "+list)
	prev := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if !slices.Contains(o.allowed, o.v) {
			return &exit.CodedError{Code: exit.General, Msg: "-o must be one of " + list}
		}
		if prev != nil {
			return prev(c, args)
		}
		return nil
	}
}

// writeJSON prints v as indented JSON (the -o json form).
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: "marshal json: " + err.Error()}
	}
	_, _ = fmt.Fprintf(w, "%s\n", b)
	return nil
}

// SPDX-License-Identifier: Apache-2.0

// `ach-cli content fetch <kind> <name>` — a low-level debug command that
// streams a single content artifact straight from the Content Service to
// stdout (or --output), with NO extraction and NO adapter projection. It is
// the credential-resolution + x-ach-key/x-ach-environment path that `env
// hydrate` uses, reduced to a raw byte dump (G6).

package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/synthetic"
	"github.com/ackstorm/ach/internal/keys"
)

// contentFetchKinds is the closed set of content kinds the Content Service
// serves on /content/<kind>/<name> (Hub §15.2 content block).
var contentFetchKinds = map[string]struct{}{
	"prompt":   {},
	"plugin":   {},
	"artifact": {},
	"skill":    {},
}

// contentFetchFlags carries the credential + output flags for `content fetch`.
type contentFetchFlags struct {
	Profile     string
	APIKey      string
	EnvKey      string
	Environment string
	Output      string
	Verbose     bool
}

// newContentCmd returns the `content` parent with its `fetch` child.
func newContentCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "content",
		Short: "Low-level content debugging (raw artifact fetch)",
		Long: `Low-level helpers for inspecting the Content Service directly.

These are debug tools — they bypass adapter projection and write raw bytes.
For normal workspace setup use 'ach-cli env hydrate'.`,
		RunE: helpOrUnknownSubcommand,
	}
	parent.AddCommand(newContentFetchCmd())
	return parent
}

// newContentFetchCmd returns `content fetch <kind> <name>`.
func newContentFetchCmd() *cobra.Command {
	f := &contentFetchFlags{}
	cmd := &cobra.Command{
		Use:   "fetch <kind> <name>",
		Short: "Fetch one content artifact and write its raw bytes to stdout",
		Long: `Stream a single artifact from the Content Service verbatim.

kind ∈ {prompt, plugin, artifact, skill}. No extraction, no adapter
projection — the response body is written as-is to stdout (or --output).

A pk- bearer MUST pass --environment (the Content Service resolves scope from
the x-ach-environment header); an ek- bearer is already Environment-bound.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContentFetch(cmd, args[0], args[1], f)
		},
	}
	cmd.Flags().StringVar(&f.Profile, "profile", "", "Profile name (default: active profile)")
	cmd.Flags().StringVar(&f.APIKey, "api-key", "", "pk- bearer (overrides profile/env)")
	cmd.Flags().StringVar(&f.EnvKey, "env-key", "", "ek- bearer (overrides profile/env)")
	cmd.Flags().StringVar(&f.Environment, "environment", "", "Target Environment (required for a pk- bearer)")
	cmd.Flags().StringVarP(&f.Output, "output", "o", "", "Write to this file instead of stdout")
	cmd.Flags().BoolVar(&f.Verbose, "verbose", false, "Dump request headers to stderr (x-ach-key redacted)")
	return cmd
}

func runContentFetch(cmd *cobra.Command, kind, name string, f *contentFetchFlags) error {
	ctx := cmd.Context()

	if _, ok := contentFetchKinds[kind]; !ok {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("kind must be one of: prompt, plugin, artifact, skill; got: %s", kind),
		}
	}
	if name == "" {
		return &exit.CodedError{Code: exit.General, Msg: "name is required"}
	}

	// CLI-07 synthetic gate — content fetch is a pk-/ek- read, allowed in
	// synthetic mode (same disposition as hydrate).
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateHydrate,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}

	baseURL, bearer, err := resolveEnvKeysBearer(f.Profile, f.APIKey, f.EnvKey)
	if err != nil {
		return err
	}

	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: adminHTTPClient,
		Verbose:    f.Verbose,
		Stderr:     cmd.ErrOrStderr(),
	}

	// A pk- bearer must carry the target Environment in x-ach-environment (the
	// Content Service returns 400 missing_environment otherwise). An ek- binds
	// its own Environment, so the header is omitted.
	bearerPrefix, _ := keys.ClassifyBearer(bearer)
	if bearerPrefix == keys.PrefixPk {
		if f.Environment == "" {
			return &exit.CodedError{
				Code: exit.General,
				Msg:  "--environment is required for a pk- bearer",
			}
		}
		if err := validateEnvHeaderValue(f.Environment); err != nil {
			return &exit.CodedError{
				Code: exit.General,
				Msg:  fmt.Sprintf("invalid environment %q: %v", f.Environment, err),
			}
		}
		hc.ExtraHeaders = http.Header{"x-ach-environment": {f.Environment}}
	}

	resp, err := hc.DoRaw(ctx, http.MethodGet, "/content/"+kind+"/"+name, nil)
	if err != nil {
		return err // *ServerError → cobra renders the envelope + exit-code map
	}
	defer func() { _ = resp.Body.Close() }()

	out := cmd.OutOrStdout()
	if f.Output != "" {
		file, createErr := os.Create(f.Output)
		if createErr != nil {
			return &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("create output file %q: %v", f.Output, createErr),
				Wrapped: createErr,
			}
		}
		defer func() { _ = file.Close() }()
		out = file
	}
	if _, copyErr := io.Copy(out, resp.Body); copyErr != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("stream content body: %v", copyErr),
			Wrapped: copyErr,
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newContentCmd())
}

// SPDX-License-Identifier: Apache-2.0

// `ach-cli env fetch <env> <kind> <name>` — a low-level debug command that
// streams a single content artifact straight from the Content Service to
// stdout (or --file), with NO extraction and NO adapter projection. It is
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
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// contentFetchKinds is the closed set of content kinds the Content Service
// serves on /content/<kind>/<name> (Hub §15.2 content block).
var contentFetchKinds = map[string]struct{}{
	"prompt":   {},
	"plugin":   {},
	"artifact": {},
	"skill":    {},
}

// newEnvFetchCmd returns `env fetch <env> <kind> <name>`.
func newEnvFetchCmd() *cobra.Command {
	var (
		f    credFlags
		file string
	)
	cmd := &cobra.Command{
		Use:   "fetch <env> <kind> <name>",
		Short: "Fetch one content artifact and write its raw bytes to stdout",
		Long: `Stream a single artifact from the Content Service verbatim (a debug tool).

kind ∈ {prompt, plugin, artifact, skill}. No extraction, no adapter
projection — the response body is written as-is to stdout (or --file).
For normal workspace setup use 'ach-cli env hydrate'.

The Environment is always sent as x-ach-environment; with an ek- key it
must be the key's own Environment.

Example:
  ach-cli env fetch frontend-dev plugin code-review --file code-review.tar.gz`,
		Args:          cobra.ExactArgs(3),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvFetch(cmd, args[0], args[1], args[2], f, file)
		},
	}
	registerCredFlags(cmd, &f)
	cmd.Flags().StringVar(&file, "file", "", "Write to this file instead of stdout")
	return cmd
}

func runEnvFetch(cmd *cobra.Command, env, kind, name string, f credFlags, file string) error {
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
	if err := validateEnvHeaderValue(env); err != nil {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("invalid environment %q: %v", env, err),
		}
	}

	c, err := resolveCred(ctx, f, synthetic.GateAPI)
	if err != nil {
		return err
	}
	hc := newAPIClient(c.BaseURL, c.Bearer, envHTTPClient, f.Verbose, cmd.ErrOrStderr())
	hc.ExtraHeaders = http.Header{"x-ach-environment": {env}}

	resp, err := hc.DoRaw(ctx, http.MethodGet, "/content/"+kind+"/"+name, nil)
	if err != nil {
		return err // *ServerError → cobra renders the envelope + exit-code map
	}
	defer func() { _ = resp.Body.Close() }()

	out := cmd.OutOrStdout()
	if file != "" {
		fh, createErr := os.Create(file)
		if createErr != nil {
			return &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("create output file %q: %v", file, createErr),
				Wrapped: createErr,
			}
		}
		defer func() { _ = fh.Close() }()
		out = fh
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

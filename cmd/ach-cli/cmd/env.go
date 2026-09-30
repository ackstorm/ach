// SPDX-License-Identifier: Apache-2.0

// `ach env` is the read-only environments surface (CLI-12 / spec
// §5.5). Two children:
//
//   - list      GET /platform/environments, paginating next_cursor
//               automatically. --limit caps the per-page request.
//   - describe  Two-call: paginate /environments to find the row,
//               then POST /platform/hydrate {environment:<name>} for
//               the runtime + context manifest. --metadata-only
//               skips the second call. A 403 unauthorized_team on
//               the hydrate call exits 0 with `(unavailable)` markers
//               (CLI-12 graceful admin fallback).
//
// Both are read-only and work in synthetic mode (ACH_URL + ACH_KEY)
// through resolveCred.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// envHTTPClient is a test-only package-level seam — same pattern as
// whoamiHTTPClient. nil → fresh stdlib client at Do time. Tests
// targeting httptest.NewTLSServer set this to the test server's
// TLS-trusting client so env list/describe can reach the ephemeral
// cert without bloating the package API with per-call hooks.
var envHTTPClient *http.Client

// defaultEnvListLimit mirrors the server-side default (100 per
// internal/platformapi/environments handler). Surfaced as a constant
// so the flag definition and the env-side default agree.
const defaultEnvListLimit = 100

// pathEnvironments is the environments list endpoint.
const pathEnvironments = "/platform/environments"

// newEnvCmd returns a fresh `ach env` parent cobra.Command with
// the 2 children registered.
func newEnvCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "env",
		Short: "Inspect environments visible to the active credential",
		Long: `Inspect environments visible to the active credential.

env list + describe are read-only and synthetic-mode friendly.
describe gracefully degrades on 403 unauthorized_team — printing
'(unavailable)' for runtime + context and exiting 0.
`,
		RunE: helpOrUnknownSubcommand,
	}
	parent.AddCommand(
		newEnvListCmd(),
		newEnvDescribeCmd(),
		newHydrateCmd(),
		newEnvStatusCmd(),
		newEnvSaveCmd(),
		newUninstallCmd(),
	)
	return parent
}

// newEnvListCmd returns the `ach env list` leaf.
func newEnvListCmd() *cobra.Command {
	var (
		flagLimit int
		f         credFlags
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List environments visible to the active credential",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hc, err := buildEnvHTTPClient(cmd, f)
			if err != nil {
				return err
			}
			items, err := paginateEnvironments(cmd.Context(), hc, flagLimit)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatEnvList(items))
			return nil
		},
	}
	c.Flags().IntVar(&flagLimit, "limit", defaultEnvListLimit, "Per-page limit (server cap is 500)")
	registerCredFlags(c, &f)
	return c
}

// newEnvDescribeCmd returns the `ach env describe <name>` leaf.
func newEnvDescribeCmd() *cobra.Command {
	var (
		flagMetadataOnly bool
		f                credFlags
	)
	c := &cobra.Command{
		Use:   "describe <name>",
		Short: "Show one environment's runtime + context manifest",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// C4: friendly missing-arg hint before any credential resolution.
			if len(args) == 0 {
				return &exit.CodedError{
					Code: exit.General,
					Msg: "missing environment.\n  Usage: ach env describe <name>\n" +
						"  Run 'ach env list' to see available environments.",
				}
			}
			name := args[0]
			hc, err := buildEnvHTTPClient(cmd, f)
			if err != nil {
				return err
			}
			view, err := findEnvironmentByName(cmd.Context(), hc, name)
			if err != nil {
				return err
			}

			if flagMetadataOnly {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatEnvDescribe(view, nil, false))
				return nil
			}

			h, err := callHydrate(cmd.Context(), hc, name)
			if err != nil {
				// CLI-12 graceful admin fallback: 403 unauthorized_team
				// → exit 0 with `(unavailable)` markers.
				var sErr *httpclient.ServerError
				if errors.As(err, &sErr) && sErr.Status == http.StatusForbidden && sErr.Code == "unauthorized_team" {
					_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatEnvDescribe(view, nil, false))
					return nil
				}
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), render.FormatEnvDescribe(view, h, true))
			return nil
		},
	}
	c.Flags().BoolVar(&flagMetadataOnly, "metadata-only", false,
		"Skip the /platform/hydrate call (faster, env metadata only)")
	registerCredFlags(c, &f)
	return c
}

// buildEnvHTTPClient resolves the credential and builds the client wired
// to envHTTPClient (test-seam aware).
func buildEnvHTTPClient(cmd *cobra.Command, f credFlags) (*httpclient.Client, error) {
	c, err := resolveCred(cmd.Context(), f, synthetic.GateAPI)
	if err != nil {
		return nil, err
	}
	return newAPIClient(c.BaseURL, c.Bearer, envHTTPClient, f.Verbose, cmd.ErrOrStderr()), nil
}

// paginateEnvironments calls GET /platform/environments repeatedly,
// following next_cursor until exhausted. Returns the accumulated
// items. The first request carries ?limit=<limit>; subsequent
// requests carry both ?limit + ?cursor=<prev_next_cursor>.
func paginateEnvironments(ctx context.Context, hc *httpclient.Client, limit int) ([]render.EnvView, error) {
	return fetchAll[render.EnvView](ctx, hc, "", func(c string) string { return buildEnvListPath(limit, c) })
}

// buildEnvListPath composes the GET /platform/environments URL with
// ?limit + (optionally) ?cursor query parameters. Cursor is URL-
// escaped because the server emits opaque base64-encoded values.
func buildEnvListPath(limit int, cursor string) string {
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	return pathEnvironments + "?" + v.Encode()
}

// findEnvironmentByName paginates /platform/environments looking for
// a row whose name matches. Returns the EnvView when found; returns
// a CodedError{General} with "not found" when exhausted without a
// match.
func findEnvironmentByName(ctx context.Context, hc *httpclient.Client, name string) (render.EnvView, error) {
	var (
		cursor string
		limit  = defaultEnvListLimit
	)
	for {
		path := buildEnvListPath(limit, cursor)
		var resp page[render.EnvView]
		if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return render.EnvView{}, err
		}
		for _, e := range resp.Items {
			if e.Name == name {
				return e, nil
			}
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return render.EnvView{}, &exit.CodedError{
		Code: exit.General,
		Msg:  fmt.Sprintf("environment %q not found", name),
	}
}

// callHydrate POSTs /platform/hydrate {environment:<name>} and
// decodes into render.HydrateView. The HydrateView field tags match
// the wire JSON keys verbatim so the decode is a trivial round-trip.
func callHydrate(ctx context.Context, hc *httpclient.Client, name string) (*render.HydrateView, error) {
	body := struct {
		Environment string `json:"environment"`
	}{Environment: name}
	resp, err := hc.DoRaw(ctx, http.MethodPost, "/platform/hydrate", body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	dec := json.NewDecoder(resp.Body)
	var h render.HydrateView
	if decodeErr := dec.Decode(&h); decodeErr != nil {
		return nil, fmt.Errorf("decode hydrate response: %w", decodeErr)
	}
	return &h, nil
}

func init() {
	rootCmd.AddCommand(newEnvCmd())
}

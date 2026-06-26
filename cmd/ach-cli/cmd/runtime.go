// SPDX-License-Identifier: Apache-2.0

// `ach-cli runtime` surfaces the admin runtime catalog — the set of
// models, MCP servers, A2A agents, and teams known to the ACH platform API.
// Four single-kind list views plus a combined catalog view:
//
//   - ach-cli runtime models list   → GET /platform/admin/runtime/models
//   - ach-cli runtime mcp list      → GET /platform/admin/runtime/mcp-servers
//   - ach-cli runtime a2a list      → GET /platform/admin/runtime/a2a-agents
//   - ach-cli runtime teams list    → GET /platform/admin/runtime/teams
//   - ach-cli runtime catalog       → GET /platform/admin/runtime/catalog
//
// Each command accepts -o table|json and the standard admin credential
// flags (--profile, --api-key, --env-key, --verbose). All endpoints
// require an admin-allowlisted pk- (same auth surface as `ach admin`).

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// runtimeItem is the per-row shape returned by the single-kind list
// endpoints (/runtime/models, /runtime/mcp-servers, /runtime/a2a-agents).
type runtimeItem struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// runtimeListResp is the envelope from the single-kind list endpoints.
type runtimeListResp struct {
	Items []runtimeItem `json:"items"`
}

// runtimeCatalogResp is the envelope from GET /platform/admin/runtime/catalog.
type runtimeCatalogResp struct {
	Models     []runtimeItem `json:"models"`
	MCPServers []runtimeItem `json:"mcpServers"`
	A2AAgents  []runtimeItem `json:"a2aAgents"`
	Teams      []runtimeItem `json:"teams"`
}

// newRuntimeCmd returns the `ach-cli runtime` parent command with its five
// children: models, mcp, a2a, teams (each with a `list` leaf), and catalog.
func newRuntimeCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect the admin runtime catalog (models, MCP servers, A2A agents, teams)",
		RunE:  helpOrUnknownSubcommand,
	}
	parent.AddCommand(
		newRuntimeKindCmd("models", "/platform/admin/runtime/models", "List available models"),
		newRuntimeKindCmd("mcp", "/platform/admin/runtime/mcp-servers", "List available MCP servers"),
		newRuntimeKindCmd("a2a", "/platform/admin/runtime/a2a-agents", "List available A2A agents"),
		newRuntimeKindCmd("teams", "/platform/admin/runtime/teams", "List available teams"),
		newRuntimeCatalogCmd(),
	)
	return parent
}

// newRuntimeKindCmd returns a single-kind parent (e.g. `runtime models`)
// with a `list` child that fetches path and renders the result.
func newRuntimeKindCmd(use, path, short string) *cobra.Command {
	parent := &cobra.Command{
		Use:   use,
		Short: short,
		RunE:  helpOrUnknownSubcommand,
	}
	var f adminCredFlags
	var output string
	list := &cobra.Command{
		Use:           "list",
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRuntimeList(cmd.Context(), cmd, path, output, &f)
		},
	}
	registerAdminCredFlags(list, &f, false)
	list.Flags().StringVarP(&output, "output", "o", "table", "Output format: table|json")
	parent.AddCommand(list)
	return parent
}

// newRuntimeCatalogCmd returns `ach-cli runtime catalog` which fetches
// all kinds in one call and renders a merged table (or raw JSON).
func newRuntimeCatalogCmd() *cobra.Command {
	var f adminCredFlags
	var output string
	cmd := &cobra.Command{
		Use:           "catalog",
		Short:         "List the full runtime catalog (all kinds)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			return runRuntimeCatalog(c.Context(), c, output, &f)
		},
	}
	registerAdminCredFlags(cmd, &f, false)
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table|json")
	return cmd
}

// runRuntimeList fetches path (a single-kind endpoint) and renders the result.
func runRuntimeList(ctx context.Context, cmd *cobra.Command, path, output string, f *adminCredFlags) error {
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateAdmin,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}
	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
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
	var resp runtimeListResp
	if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	if output == outputJSON {
		return writeRuntimeJSON(cmd.OutOrStdout(), resp.Items)
	}
	return writeRuntimeTable(cmd.OutOrStdout(), resp.Items)
}

// runRuntimeCatalog fetches /platform/admin/runtime/catalog and renders all kinds.
func runRuntimeCatalog(ctx context.Context, cmd *cobra.Command, output string, f *adminCredFlags) error {
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:        synthetic.GateAdmin,
		APIKeyFlag:  f.APIKey,
		EnvKeyFlag:  f.EnvKey,
		ProfileFlag: f.Profile,
	}); err != nil {
		return err
	}
	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
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
	var resp runtimeCatalogResp
	if err := hc.Do(ctx, http.MethodGet, "/platform/admin/runtime/catalog", nil, &resp); err != nil {
		return err
	}
	if output == outputJSON {
		return writeRuntimeJSON(cmd.OutOrStdout(), resp)
	}
	all := make([]runtimeItem, 0, len(resp.Models)+len(resp.MCPServers)+len(resp.A2AAgents)+len(resp.Teams))
	all = append(all, resp.Models...)
	all = append(all, resp.MCPServers...)
	all = append(all, resp.A2AAgents...)
	all = append(all, resp.Teams...)
	return writeRuntimeTable(cmd.OutOrStdout(), all)
}

// writeRuntimeTable renders items as a tab-separated table with KIND / NAME / STATUS columns.
func writeRuntimeTable(w io.Writer, items []runtimeItem) error {
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KIND\tNAME\tSTATUS")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Kind, it.Name, it.Status)
	}
	return tw.Flush()
}

// writeRuntimeJSON marshals v as indented JSON followed by a newline.
func writeRuntimeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func init() {
	rootCmd.AddCommand(newRuntimeCmd())
}

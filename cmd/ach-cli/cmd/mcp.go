// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/ackstorm/ach/internal/cli/render"
)

func init() { rootCmd.AddCommand(newMCPCmd(), newA2ACmd()) }

func newMCPCmd() *cobra.Command {
	c := &cobra.Command{Use: "mcp", Short: "Discover and call MCP tools through ACH", RunE: helpOrUnknownSubcommand}
	c.AddCommand(newMCPListCmd(), newMCPSearchCmd(), newMCPDescribeCmd(), newMCPCallCmd(), newMCPLoginCmd())
	return c
}

type mcpToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type mcpServerSummary struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Tools       []mcpToolSummary `json:"tools,omitempty"`
}

type mcpQueryFailure struct {
	Server string `json:"server"`
	Error  string `json:"error"`
}

type mcpInventory struct {
	Servers []mcpServerSummary `json:"servers"`
	Errors  []mcpQueryFailure  `json:"errors,omitempty"`
}

func (v mcpInventory) write(cmd *cobra.Command, o outputFlag) error {
	if o.v == outputJSON {
		if err := writeJSON(cmd.OutOrStdout(), v); err != nil {
			return err
		}
	} else {
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "SERVER\tTOOL\tDESCRIPTION")
		for _, s := range v.Servers {
			_, _ = fmt.Fprintf(tw, "%s\t\t%s\n", s.Name, shortDescription(s.Description))
			for _, tool := range s.Tools {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, tool.Name, tool.Description)
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(v.Errors) > 0 {
		var reasons []string
		for _, e := range v.Errors {
			reasons = append(reasons, e.Server+": "+e.Error)
		}
		return fmt.Errorf("partial MCP inventory: %s", strings.Join(reasons, "; "))
	}
	return nil
}

func summarizeTool(tool *mcp.Tool) mcpToolSummary {
	return mcpToolSummary{Name: tool.Name, Description: shortDescription(tool.Description)}
}

func shortDescription(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	text := []rune(strings.Join(strings.Fields(first), " "))
	if len(text) > 160 {
		return string(text[:159]) + "…"
	}
	return string(text)
}

func newMCPListCmd() *cobra.Command {
	var f credFlags
	var o outputFlag
	var expand bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List accessible MCP servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPInventory(cmd, f, o, expand, "")
		},
	}
	cmd.Flags().BoolVar(&expand, "tools", false, "Include tools grouped by server")
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

func newMCPSearchCmd() *cobra.Command {
	var f credFlags
	var o outputFlag
	cmd := &cobra.Command{
		Use:   "search <words...>",
		Short: "Find servers and tools by name and description",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(strings.Fields(strings.Join(args, " ")), " ")
			if query == "" {
				return errors.New("enter at least one search word")
			}
			return runMCPInventory(cmd, f, o, true, query)
		},
	}
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

func runMCPInventory(cmd *cobra.Command, f credFlags, o outputFlag, expand bool, query string) error {
	c, err := resolveCred(cmd.Context(), f)
	if err != nil {
		return err
	}
	servers, err := runtimeCatalog(cmd.Context(), c, f.Verbose, "mcp")
	if err != nil {
		return err
	}
	inventory := mcpInventory{Servers: []mcpServerSummary{}}
	for _, s := range servers {
		if strings.EqualFold(query, s.Name) {
			inventory.Servers = append(inventory.Servers, mcpServerSummary{Name: s.Name, Description: s.Description})
			return inventory.write(cmd, o)
		}
	}
	type entry struct {
		summary mcpServerSummary
		show    bool
		err     error
	}
	entries := make([]entry, len(servers))
	var jobs errgroup.Group
	jobs.SetLimit(6)
	for i, s := range servers {
		e := &entries[i]
		e.summary = mcpServerSummary{Name: s.Name, Description: s.Description}
		e.show = query == "" || render.MatchTerms(query, s.Name, s.Description)
		if expand {
			jobs.Go(func() error {
				tools, err := readMCPTools(cmd.Context(), c, f.Verbose, s.Name)
				e.err = err
				for _, tool := range tools {
					if query == "" || render.MatchTerms(query, s.Name, s.Description, tool.Name, tool.Description) {
						e.summary.Tools = append(e.summary.Tools, summarizeTool(tool))
						e.show = true
					}
				}
				return nil
			})
		}
	}
	_ = jobs.Wait()
	for _, e := range entries {
		if e.show {
			inventory.Servers = append(inventory.Servers, e.summary)
		}
		if e.err != nil {
			inventory.Errors = append(inventory.Errors, mcpQueryFailure{Server: e.summary.Name, Error: e.err.Error()})
		}
	}
	return inventory.write(cmd, o)
}

const mcpDiscoveryTimeout = 30 * time.Second

func readMCPTools(ctx context.Context, c cred, verbose bool, name string) ([]*mcp.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpDiscoveryTimeout)
	defer cancel()
	cs, err := openToolSession(ctx, c, verbose, "mcp", name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cs.Close() }()
	return listRuntimeTools(ctx, cs, name)
}

func newMCPDescribeCmd() *cobra.Command {
	var f credFlags
	var o outputFlag
	cmd := &cobra.Command{
		Use:   "describe <server> [tool]",
		Short: "Show a server's tools or a tool's complete input schema",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCred(cmd.Context(), f)
			if err != nil {
				return err
			}
			servers, err := runtimeCatalog(cmd.Context(), c, f.Verbose, "mcp")
			if err != nil {
				return err
			}
			idx := slices.IndexFunc(servers, func(s runtimeTarget) bool { return s.Name == args[0] })
			if idx < 0 {
				return fmt.Errorf("MCP server %q is unavailable to this credential", args[0])
			}
			tools, err := readMCPTools(cmd.Context(), c, f.Verbose, args[0])
			if err != nil {
				return err
			}
			if len(args) == 2 {
				tool := matchMCPTool(tools, args[1])
				if tool == nil {
					return fmt.Errorf("tool %q is unavailable or ambiguous on %s", args[1], args[0])
				}
				if o.v == outputJSON {
					return writeJSON(cmd.OutOrStdout(), tool)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n%s\n\nInput schema\n", args[0], tool.Name, tool.Description)
				return writeJSON(cmd.OutOrStdout(), tool.InputSchema)
			}
			summary := mcpServerSummary{Name: args[0], Description: servers[idx].Description}
			for _, tool := range tools {
				summary.Tools = append(summary.Tools, summarizeTool(tool))
			}
			if o.v == outputJSON {
				return writeJSON(cmd.OutOrStdout(), summary)
			}
			return (mcpInventory{Servers: []mcpServerSummary{summary}}).write(cmd, o)
		},
	}
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

// Gateways may prefix a tool with its server and a dot or slash.
func matchMCPTool(tools []*mcp.Tool, name string) *mcp.Tool {
	var match *mcp.Tool
	count := 0
	for _, tool := range tools {
		if name == tool.Name {
			return tool
		}
		if strings.HasSuffix(tool.Name, "."+name) || strings.HasSuffix(tool.Name, "/"+name) {
			match = tool
			count++
		}
	}
	if count == 1 {
		return match
	}
	return nil
}

func newMCPCallCmd() *cobra.Command {
	var f credFlags
	var o outputFlag
	cmd := &cobra.Command{
		Use:   "call <server> <tool> [json-object]",
		Short: "Call one MCP tool with an optional JSON object",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments := json.RawMessage("{}")
			if len(args) == 3 {
				s := strings.TrimSpace(args[2])
				if !strings.HasPrefix(s, "{") || !json.Valid([]byte(s)) {
					return errors.New("tool arguments must be a valid JSON object")
				}
				arguments = json.RawMessage(s)
			}
			c, err := resolveCred(cmd.Context(), f)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), mcpDiscoveryTimeout)
			defer cancel()
			cs, err := openToolSession(ctx, c, f.Verbose, "mcp", args[0])
			if err != nil {
				return err
			}
			defer func() { _ = cs.Close() }()
			tools, err := listRuntimeTools(ctx, cs, args[0])
			if err != nil {
				return err
			}
			name := args[1]
			if tool := matchMCPTool(tools, name); tool != nil {
				name = tool.Name
			}
			result, err := callRuntimeTool(cmd.Context(), cs, name, arguments)
			if err != nil {
				return err
			}
			return writeToolResult(cmd, o, result)
		},
	}
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

func writeToolResult(cmd *cobra.Command, o outputFlag, result *runtimeToolResult) error {
	if o.v == outputJSON {
		if err := writeJSON(cmd.OutOrStdout(), result.Raw); err != nil {
			return err
		}
	} else {
		var lines []string
		for _, part := range result.Content {
			if item, ok := part.(*mcp.TextContent); ok {
				lines = append(lines, item.Text)
			} else {
				lines = nil
				break
			}
		}
		if len(lines) > 0 {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n")); err != nil {
				return err
			}
		} else if err := writeJSON(cmd.OutOrStdout(), result.Raw); err != nil {
			return err
		}
	}
	if result.IsError {
		return errors.New("MCP tool returned an error")
	}
	return nil
}

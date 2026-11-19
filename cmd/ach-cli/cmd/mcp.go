// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
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
	reference   string
}

type mcpServerSummary struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Tools       []mcpToolSummary `json:"tools,omitempty"`
}

type mcpQueryFailure struct {
	Server string `json:"server"`
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}

type mcpInventory struct {
	Servers   []mcpServerSummary `json:"servers"`
	Errors    []mcpQueryFailure  `json:"errors,omitempty"`
	attempted int
}

func (v mcpInventory) write(cmd *cobra.Command, o outputFlag) error {
	allFailed := v.attempted > 0 && len(v.Errors) == v.attempted
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
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, tool.reference, tool.Description)
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if cmd.Name() == "search" && len(v.Servers) == 0 && !allFailed {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No matches. Try fewer words, for example `ach-cli mcp search contacts`.")
		}
	}
	var skipped, failures []string
	for _, e := range v.Errors {
		if e.Reason == mcpAuthRequiredStatus {
			skipped = append(skipped, e.Server)
		} else {
			failures = append(failures, e.Server+": "+e.Error)
		}
	}
	if len(skipped) > 0 {
		noun := "servers"
		if len(skipped) == 1 {
			noun = "server"
		}
		summary := fmt.Sprintf("%d %s skipped, sign-in needed: %s", len(skipped), noun, strings.Join(skipped, ", "))
		if !allFailed && len(failures) == 0 {
			_, err := fmt.Fprintln(cmd.ErrOrStderr(), summary)
			return err
		}
		failures = append(failures, summary)
	}
	if len(failures) > 0 {
		return fmt.Errorf("partial MCP inventory: %s", strings.Join(failures, "; "))
	}
	return nil
}

// Show the split form if the dotted reference resolves to a different server or tool.
func (s *mcpServerSummary) setToolReferences(servers []runtimeTarget, tools []*mcp.Tool) {
	wireNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		wireNames[tool.Name] = true
	}
	for i, tool := range s.Tools {
		ref := tool.Name
		if !strings.HasPrefix(ref, s.Name+".") {
			ref = s.Name + "." + ref
		}
		target, suffix, _ := resolveMCPReference(servers, ref, true, true)
		if suffix == "" || target.Name != s.Name || (ref != tool.Name && wireNames[ref]) {
			ref = s.Name + " " + tool.Name
		}
		s.Tools[i].reference = ref
	}
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
	var expand, refresh bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List accessible MCP servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPInventory(cmd, f, o, expand, "", refresh)
		},
	}
	cmd.Flags().BoolVar(&expand, "tools", false, "Include tools grouped by server")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Refresh the five-minute tool inventory cache when listing tools")
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

func newMCPSearchCmd() *cobra.Command {
	var f credFlags
	var o outputFlag
	var refresh bool
	cmd := &cobra.Command{
		Use:   "search <words...>",
		Short: "Find servers and tools by name and description",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(strings.Fields(strings.Join(args, " ")), " ")
			if query == "" {
				return errors.New("enter at least one search word")
			}
			return runMCPInventory(cmd, f, o, true, query, refresh)
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Refresh the five-minute tool inventory cache when searching tools")
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

func runMCPInventory(cmd *cobra.Command, f credFlags, o outputFlag, expand bool, query string, refresh bool) error {
	c, err := resolveCred(cmd.Context(), f)
	if err != nil {
		return err
	}
	servers, err := runtimeCatalog(cmd.Context(), c, f.Verbose, "mcp")
	if err != nil {
		return err
	}
	inventory := mcpInventory{Servers: []mcpServerSummary{}, attempted: len(servers)}
	for _, s := range servers {
		if strings.EqualFold(query, s.Name) {
			inventory.Servers = append(inventory.Servers, mcpServerSummary{Name: s.Name, Description: s.Description})
			return inventory.write(cmd, o)
		}
	}
	type entry struct {
		summary mcpServerSummary
		score   int
		err     error
	}
	terms := mcpSearchTerms(query)
	entries := make([]entry, len(servers))
	var jobs errgroup.Group
	jobs.SetLimit(6)
	for i, s := range servers {
		e := &entries[i]
		e.summary = mcpServerSummary{Name: s.Name, Description: s.Description}
		if expand {
			jobs.Go(func() error {
				tools, err := cachedMCPTools(cmd.Context(), c, f.Verbose, s.Name, refresh)
				e.err = err
				if query != "" {
					e.summary.Tools, e.score = matchingMCPTools(s, tools, terms)
				} else {
					for _, tool := range tools {
						e.summary.Tools = append(e.summary.Tools, summarizeTool(tool))
					}
				}
				if o.v != outputJSON {
					e.summary.setToolReferences(servers, tools)
				}
				return nil
			})
		}
	}
	_ = jobs.Wait()
	if query != "" {
		slices.SortStableFunc(entries, func(a, b entry) int { return cmp.Compare(b.score, a.score) })
	}
	for _, e := range entries {
		if query == "" || e.score > 0 {
			inventory.Servers = append(inventory.Servers, e.summary)
		}
		if e.err != nil {
			failure := mcpQueryFailure{Server: e.summary.Name, Error: e.err.Error()}
			if errors.Is(e.err, errMCPAuthRequired) {
				failure.Reason = mcpAuthRequiredStatus
			}
			inventory.Errors = append(inventory.Errors, failure)
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
		Use:   "describe <server> [tool] | <server.tool>",
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
			server, toolSuffix, err := resolveMCPReference(servers, args[0], true, len(args) == 1)
			if err != nil {
				return err
			}
			tools, err := readMCPTools(cmd.Context(), c, f.Verbose, server.Name)
			if err != nil {
				return err
			}
			if len(args) == 2 || toolSuffix != "" {
				name := args[len(args)-1]
				tool, err := matchMCPTool(tools, name, toolSuffix)
				if err != nil {
					return err
				}
				if tool == nil {
					return fmt.Errorf("tool %q is unavailable on %s", name, server.Name)
				}
				if o.v == outputJSON {
					return writeJSON(cmd.OutOrStdout(), tool)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n%s\n\nInput schema\n", server.Name, tool.Name, tool.Description)
				return writeJSON(cmd.OutOrStdout(), tool.InputSchema)
			}
			summary := mcpServerSummary{Name: server.Name, Description: server.Description}
			for _, tool := range tools {
				summary.Tools = append(summary.Tools, summarizeTool(tool))
			}
			if o.v == outputJSON {
				return writeJSON(cmd.OutOrStdout(), summary)
			}
			summary.setToolReferences(servers, tools)
			return (mcpInventory{Servers: []mcpServerSummary{summary}}).write(cmd, o)
		},
	}
	registerCredFlags(cmd, &f)
	registerOutputFlag(cmd, &o, "table", "json")
	return cmd
}

// A server name may contain dots. The longest visible prefix owns a qualified tool.
func resolveMCPReference(
	servers []runtimeTarget, name string, allowServer, allowTool bool,
) (runtimeTarget, string, error) {
	var best runtimeTarget
	for _, server := range servers {
		if allowServer && server.Name == name {
			return server, "", nil
		}
		prefix := server.Name + "."
		if allowTool && len(server.Name) > len(best.Name) && len(name) > len(prefix) && strings.HasPrefix(name, prefix) {
			best = server
		}
	}
	if best.Name != "" {
		return best, strings.TrimPrefix(name, best.Name+"."), nil
	}
	return runtimeTarget{}, "", fmt.Errorf("MCP server %q is unavailable to this credential", name)
}

// Gateways may prefix a tool with its server and a dot or slash.
func matchMCPTool(tools []*mcp.Tool, name, toolSuffix string) (*mcp.Tool, error) {
	if toolSuffix != "" {
		for _, tool := range tools {
			if tool.Name == name {
				return tool, nil
			}
		}
		name = toolSuffix
	}
	var match *mcp.Tool
	count := 0
	for _, tool := range tools {
		if name == tool.Name {
			return tool, nil
		}
		if strings.HasSuffix(tool.Name, "."+name) || strings.HasSuffix(tool.Name, "/"+name) {
			match = tool
			count++
		}
	}
	if count > 1 {
		return nil, fmt.Errorf("tool %q is ambiguous; use its full name", name)
	}
	return match, nil
}

var errMCPArguments = errors.New("tool arguments must be a valid JSON object")

func readMCPArguments(cmd *cobra.Command, arg string) (json.RawMessage, error) {
	if arg == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read tool arguments from stdin: %w", err)
		}
		arg = string(data)
	}
	raw := json.RawMessage(strings.TrimSpace(arg))
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return nil, errMCPArguments
	}
	return raw, nil
}

func splitMCPCallArgs(cmd *cobra.Command, args []string) ([]string, json.RawMessage, error) {
	names := args
	arguments := json.RawMessage("{}")
	if len(args) == 3 || (len(args) == 2 && strings.Contains(args[0], ".") &&
		(args[1] == "-" || strings.HasPrefix(strings.TrimSpace(args[1]), "{"))) {
		last := len(args) - 1
		var err error
		arguments, err = readMCPArguments(cmd, args[last])
		if err != nil {
			return nil, nil, err
		}
		names = args[:last]
	}
	if len(names) == 1 && !strings.Contains(names[0], ".") {
		return nil, nil, errors.New("specify a server and tool or a qualified server.tool name")
	}
	return names, arguments, nil
}

func newMCPCallCmd() *cobra.Command {
	var f credFlags
	var o outputFlag
	cmd := &cobra.Command{
		Use:   "call <server> <tool> [json-object|-] | <server.tool> [json-object|-]",
		Short: "Call one MCP tool with an optional JSON object (use - for stdin)",
		Args:  cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			names, arguments, err := splitMCPCallArgs(cmd, args)
			if err != nil {
				return err
			}
			c, err := resolveCred(cmd.Context(), f)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), mcpDiscoveryTimeout)
			defer cancel()
			servers, err := runtimeCatalog(ctx, c, f.Verbose, "mcp")
			if err != nil {
				return err
			}
			server, toolSuffix, err := resolveMCPReference(servers, names[0], len(names) == 2, len(names) == 1)
			if err != nil {
				// A JSON scalar can also be a tool name on a dotted server.
				if len(names) == 2 && len(args) == 2 && strings.Contains(args[0], ".") && json.Valid([]byte(args[1])) {
					return errMCPArguments
				}
				return err
			}
			name := names[len(names)-1]
			cs, err := openToolSession(ctx, c, f.Verbose, "mcp", server.Name)
			if err != nil {
				return err
			}
			defer func() { _ = cs.Close() }()
			tools, err := listRuntimeTools(ctx, cs, server.Name)
			if err != nil {
				return err
			}
			tool, err := matchMCPTool(tools, name, toolSuffix)
			if err != nil {
				return err
			}
			if tool != nil {
				name = tool.Name
			} else if toolSuffix != "" {
				name = toolSuffix
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

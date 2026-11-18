// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/render"
)

func newA2ACmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   a2aKind,
		Short: "Discover agents and send them messages",
		RunE:  helpOrUnknownSubcommand,
	}
	list := &cobra.Command{Use: "list", Short: "List agents visible to the active credential", Args: cobra.NoArgs}
	search := &cobra.Command{
		Use: "search <words...>", Short: "Find agents by name, description or skill",
		Args: cobra.MinimumNArgs(1),
	}
	for _, c := range []*cobra.Command{list, search} {
		var f credFlags
		var out outputFlag
		registerCredFlags(c, &f)
		registerOutputFlag(c, &out, "table", "json")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			cr, err := resolveCred(cmd.Context(), f)
			if err != nil {
				return err
			}
			targets, err := runtimeCatalog(cmd.Context(), cr, f.Verbose, a2aKind)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				query := strings.Join(args, " ")
				targets = slices.DeleteFunc(targets, func(t runtimeTarget) bool {
					return !render.MatchTerms(query, t.Name, t.Description, strings.Join(t.Skills, " "))
				})
			}
			if out.v == outputJSON {
				return writeJSON(cmd.OutOrStdout(), targets)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tDESCRIPTION\tSKILLS")
			for _, t := range targets {
				description := strings.Join(strings.Fields(t.Description), " ")
				skills := strings.Join(strings.Fields(strings.Join(t.Skills, ", ")), " ")
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", t.Name, description, skills)
			}
			return w.Flush()
		}
	}
	parent.AddCommand(list, search, newA2ACallCmd())
	return parent
}

func newA2ACallCmd() *cobra.Command {
	var f credFlags
	var out outputFlag
	var contextID string
	c := &cobra.Command{
		Use: "call <agent> <message>", Short: "Send a message to an agent",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("message cannot be empty")
			}
			cr, err := resolveCred(cmd.Context(), f)
			if err != nil {
				return err
			}
			cs, err := openToolSession(cmd.Context(), cr, f.Verbose, a2aKind, args[0])
			if err != nil {
				return err
			}
			defer func() { _ = cs.Close() }()
			in := map[string]string{"message": args[1]}
			if contextID != "" {
				in["context_id"] = contextID
			}
			raw, err := json.Marshal(in)
			if err != nil {
				return err
			}
			result, err := callRuntimeTool(cmd.Context(), cs, "send_message", raw)
			if err != nil {
				return err
			}
			return writeToolResult(cmd, out, result)
		},
	}
	c.Flags().StringVar(&contextID, "context-id", "", "Continue the conversation with this context ID")
	registerCredFlags(c, &f)
	registerOutputFlag(c, &out, "table", "json")
	return c
}

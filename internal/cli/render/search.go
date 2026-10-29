// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"strings"
	"text/tabwriter"
)

// CatalogView is the slice of one GET /platform/environments row that
// `ach-cli env search` reads: the Environment's own name + description and
// the NAMES of what it serves. Names only — the server stores no skill or
// plugin description, so neither does the search.
type CatalogView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Context     struct {
		Prompts   []string `json:"prompts,omitempty"`
		Plugins   []string `json:"plugins,omitempty"`
		Artifacts []string `json:"artifacts,omitempty"`
		Skills    []string `json:"skills,omitempty"`
	} `json:"context"`
	Runtime struct {
		Models     []string `json:"models,omitempty"`
		MCPServers []string `json:"mcpServers,omitempty"`
		A2AAgents  []string `json:"a2aAgents,omitempty"`
	} `json:"runtime"`
}

// SearchHit is one match: an item (or the Environment itself, Kind
// "environment") and the Environment that serves it.
type SearchHit struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
}

// Search matches query against every Environment's name + description and
// every item name, case-insensitively. A query of several words matches
// only where every word appears.
func Search(envs []CatalogView, query string) []SearchHit {
	terms := strings.Fields(strings.ToLower(query))
	matches := func(s string) bool {
		s = strings.ToLower(s)
		for _, t := range terms {
			if !strings.Contains(s, t) {
				return false
			}
		}
		return len(terms) > 0
	}
	var hits []SearchHit
	for _, e := range envs {
		if matches(e.Name + " " + e.Description) {
			hits = append(hits, SearchHit{Kind: "environment", Name: e.Name, Environment: e.Name})
		}
		for _, group := range []struct {
			kind  string
			names []string
		}{
			{"plugin", e.Context.Plugins},
			{"skill", e.Context.Skills},
			{"prompt", e.Context.Prompts},
			{"artifact", e.Context.Artifacts},
			{"mcp", e.Runtime.MCPServers},
			{"model", e.Runtime.Models},
			{"a2a", e.Runtime.A2AAgents},
		} {
			for _, n := range group.names {
				if matches(n) {
					hits = append(hits, SearchHit{Kind: group.kind, Name: n, Environment: e.Name})
				}
			}
		}
	}
	return hits
}

// FormatSearch renders the `ach-cli env search` table, plus the install
// command when a plugin or skill matched.
func FormatSearch(hits []SearchHit) string {
	if len(hits) == 0 {
		return "No matches.\n"
	}
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KIND\tNAME\tENVIRONMENT")
	installable := false
	for _, h := range hits {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", h.Kind, h.Name, h.Environment)
		installable = installable || h.Kind == "plugin" || h.Kind == "skill"
	}
	_ = tw.Flush()
	if installable {
		sb.WriteString("\nInstall one: ach-cli env hydrate <environment> --only <kind>/<name>\n")
	}
	return sb.String()
}

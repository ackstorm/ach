// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"cmp"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpSearchTerms(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var terms []string
	for _, word := range words {
		word = strings.Trim(word, ".,?!")
		if len(words) > 1 {
			switch word {
			case "a", "an", "and", "are", "at", "by", "for", "from", "in", "is", "of", "on", "the", "to", "what", "who", "with":
				continue
			}
		}
		if word != "" && !slices.Contains(terms, word) {
			terms = append(terms, word)
		}
	}
	return terms
}

func mcpMatchScore(terms []string, server runtimeTarget, tool *mcp.Tool) (score int, toolMatch bool) {
	serverName := strings.ToLower(server.Name)
	serverDescription := strings.ToLower(server.Description)
	var name, localName, description string
	if tool != nil {
		name = strings.ToLower(tool.Name)
		// A gateway-qualified name repeats the server on every tool.
		localName = strings.TrimPrefix(strings.TrimPrefix(name, serverName+"."), serverName+"/")
		description = strings.ToLower(tool.Description)
	}
	var words, nameHits int
	for _, term := range terms {
		inServer := strings.Contains(serverName, term)
		inTool := strings.Contains(name, term)
		inToolDescription := strings.Contains(description, term)
		if inTool && (!inServer || strings.Contains(localName, term)) || inToolDescription {
			toolMatch = true
		}
		if inServer || inTool {
			nameHits++
		} else if !inToolDescription && !strings.Contains(serverDescription, term) {
			continue
		}
		words++
	}
	// One more matching word outweighs every possible name tie-breaker.
	return words*(len(terms)+1) + nameHits, toolMatch
}

func matchingMCPTools(server runtimeTarget, tools []*mcp.Tool, terms []string) ([]mcpToolSummary, int) {
	best, _ := mcpMatchScore(terms, server, nil)
	matches := make([]mcpToolSummary, 0, len(tools))
	scores := make(map[string]int)
	for _, tool := range tools {
		score, toolMatch := mcpMatchScore(terms, server, tool)
		if !toolMatch {
			continue
		}
		best = max(best, score)
		scores[tool.Name] = score
		matches = append(matches, summarizeTool(tool))
	}
	slices.SortFunc(matches, func(a, b mcpToolSummary) int {
		return cmp.Or(cmp.Compare(scores[b.Name], scores[a.Name]), strings.Compare(a.Name, b.Name))
	})
	return matches, best
}

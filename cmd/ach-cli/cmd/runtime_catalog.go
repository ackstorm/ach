// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/render"
	"github.com/ackstorm/ach/internal/keys"
)

const a2aKind = "a2a"

type runtimeTarget struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      []string `json:"skills,omitempty"`
}

// The named gateway routes enforce Environment grants in addition to the caller's
// LiteLLM permissions. Only names present in both inventories are callable.
func runtimeCatalog(ctx context.Context, c cred, verbose bool, kind string) ([]runtimeTarget, error) {
	hc := newAPIClient(c.BaseURL, c.Bearer, envHTTPClient, verbose, os.Stderr)
	grants, err := runtimeGrants(ctx, hc, c.Bearer, kind)
	if err != nil {
		return nil, err
	}
	targets := []runtimeTarget{}
	if len(grants) == 0 {
		return targets, nil
	}
	path, wrapper := "/v1/mcp/server", "servers"
	if kind == a2aKind {
		path, wrapper = "/v1/agents?health_check=false", "agents"
	}
	var raw json.RawMessage
	if err := hc.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) > 0 && raw[0] == '{' {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		raw = obj[wrapper]
		if raw == nil {
			raw = obj["data"]
		}
	}
	var rows []struct {
		ServerName  string `json:"server_name"`
		Description string `json:"description"`
		AgentName   string `json:"agent_name"`
		Card        struct {
			Description string `json:"description"`
			Skills      []any  `json:"skills"`
		} `json:"agent_card_params"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode %s catalog: %w", kind, err)
	}
	for _, row := range rows {
		t := runtimeTarget{Name: row.ServerName, Description: row.Description}
		if kind == a2aKind {
			t = runtimeTarget{Name: row.AgentName, Description: row.Card.Description}
			for _, skill := range row.Card.Skills {
				if label := agentSkillLabel(skill); label != "" {
					t.Skills = append(t.Skills, label)
				}
			}
		}
		if grants[t.Name] {
			targets = append(targets, t)
			delete(grants, t.Name)
		}
	}
	slices.SortFunc(targets, func(a, b runtimeTarget) int { return strings.Compare(a.Name, b.Name) })
	return targets, nil
}

func agentSkillLabel(skill any) string {
	switch s := skill.(type) {
	case string:
		return s
	case map[string]any:
		name, _ := s["name"].(string)
		if name == "" {
			name, _ = s["id"].(string)
		}
		if description, _ := s["description"].(string); name != "" && description != "" {
			return name + ": " + description
		}
		return name
	}
	return ""
}

func runtimeGrants(ctx context.Context, hc *httpclient.Client, bearer, kind string) (map[string]bool, error) {
	prefix, err := classifyBearer(bearer)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	if prefix == keys.PrefixEk {
		v, err := fetchEnvVersion(ctx, hc, "")
		if err != nil {
			return nil, err
		}
		for item := range v.Items {
			if name, ok := strings.CutPrefix(item, kind+"/"); ok {
				names[name] = true
			}
		}
		return names, nil
	}
	envs, err := fetchAll[render.CatalogView](ctx, hc, "", func(cursor string) string {
		return buildEnvListPath(defaultEnvListLimit, cursor, false)
	})
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		members := env.Runtime.MCPServers
		if kind == a2aKind {
			members = env.Runtime.A2AAgents
		}
		for _, name := range members {
			names[name] = true
		}
	}
	return names, nil
}

// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ackstorm/ach/internal/cli/adapter/route"
)

// agentAllowlist is the only set of Claude agent frontmatter keys that reach
// a Codex custom-agent file. It is deliberately an allowlist: anything else
// (notably mcp_servers) would let a plugin inject arbitrary Codex config.
// Field names: https://developers.openai.com/codex/subagents
var agentAllowlist = []string{"name", "description", "model", "model_reasoning_effort", "sandbox_mode"}

// codexAgentTOML rewrites a Claude agent markdown file into a Codex
// custom-agent TOML document: allowlisted frontmatter keys, a name that
// falls back to the file's base name, and the body as developer_instructions.
// The result is file-owned, so no merge keys are returned.
func codexAgentTOML(srcRel string, in []byte) ([]byte, []string, error) {
	fm, body, _ := route.SplitFrontmatter(in)
	meta := map[string]any{}
	if len(strings.TrimSpace(string(fm))) > 0 {
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return nil, nil, fmt.Errorf("codex: codexAgentTOML %s: invalid frontmatter: %w", srcRel, err)
		}
	}
	doc := map[string]any{}
	for _, k := range agentAllowlist {
		if v := meta[k]; v != nil {
			doc[k] = v
		}
	}
	if _, ok := doc["name"]; !ok {
		base := filepath.Base(srcRel)
		doc["name"] = strings.TrimSuffix(base, filepath.Ext(base))
	}
	doc["developer_instructions"] = string(body)
	out, err := route.CanonicalTOML(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("codex: codexAgentTOML %s: %w", srcRel, err)
	}
	return out, nil, nil
}

// envRef matches a header value that is exactly one ${env:NAME} reference.
var envRef = regexp.MustCompile(`^\$\{env:([A-Z_][A-Z0-9_]*)\}$`)

// bearerEnvRef matches an Authorization value of exactly "Bearer ${env:NAME}".
var bearerEnvRef = regexp.MustCompile(`^Bearer \$\{env:([A-Z_][A-Z0-9_]*)\}$`)

// codexMCPSurgery converts a Claude MCP JSON file into Codex [mcp_servers]
// tables. Server keys pass through, except that timeout becomes
// startup_timeout_sec and headers are split into bearer_token_env_var,
// env_http_headers and http_headers. A literal Authorization value is
// dropped so a secret is never written into the Codex config. Schema:
// https://developers.openai.com/codex/config-reference
func codexMCPSurgery(srcRel string, in []byte) ([]byte, []string, error) {
	var top struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(in, &top); err != nil {
		return nil, nil, fmt.Errorf("codex: codexMCPSurgery %s: invalid JSON: %w", srcRel, err)
	}
	servers := map[string]any{}
	keys := make([]string, 0, len(top.MCPServers))
	for id, raw := range top.MCPServers {
		var src map[string]any
		if err := json.Unmarshal(raw, &src); err != nil {
			return nil, nil, fmt.Errorf("codex: codexMCPSurgery %s: server %q: invalid JSON: %w", srcRel, id, err)
		}
		dst := map[string]any{}
		for k, v := range src {
			switch k {
			case "headers": // split below
			case "timeout":
				dst["startup_timeout_sec"] = v
			default:
				dst[k] = v
			}
		}
		envHdrs, litHdrs := map[string]any{}, map[string]any{}
		hdrs, _ := src["headers"].(map[string]any)
		for name, rawVal := range hdrs {
			val, ok := rawVal.(string)
			if !ok {
				continue
			}
			if m := bearerEnvRef.FindStringSubmatch(val); name == "Authorization" && m != nil {
				dst["bearer_token_env_var"] = m[1]
			} else if m := envRef.FindStringSubmatch(val); m != nil {
				envHdrs[name] = m[1]
			} else if name != "Authorization" {
				litHdrs[name] = val
			}
		}
		if len(envHdrs) > 0 {
			dst["env_http_headers"] = envHdrs
		}
		if len(litHdrs) > 0 {
			dst["http_headers"] = litHdrs
		}
		servers[id] = dst
		keys = append(keys, "mcp_servers."+id)
	}
	sort.Strings(keys)
	out, err := route.CanonicalTOML(map[string]any{"mcp_servers": servers})
	if err != nil {
		return nil, nil, fmt.Errorf("codex: codexMCPSurgery %s: %w", srcRel, err)
	}
	return out, keys, nil
}

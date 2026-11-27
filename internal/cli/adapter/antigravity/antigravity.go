// SPDX-License-Identifier: Apache-2.0

// Package antigravity is the Antigravity CLI (`agy`) adapter. Everything it
// reads in a project lives under .agents/: MCP servers in
// .agents/mcp_config.json, skills in .agents/skills, subagents in
// .agents/agents. It has no command directory, so plugin commands are
// reported as dropped.
// Contract: https://antigravity.google/docs/{mcp,skills,subagents}.
package antigravity

import (
	"context"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

const (
	canonicalID   = "antigravity"
	mcpConfigPath = ".agents/mcp_config.json"
)

type Adapter struct{}

func init() {
	adapter.Register(&Adapter{})
}

func (a *Adapter) ID() string { return canonicalID }

func (a *Adapter) Aliases() []string { return []string{"agy"} }

// Detect ignores .agents/skills and AGENTS.md: other tools read them too.
func (a *Adapter) Detect(root string) (adapter.Match, error) {
	signals := []adapter.Signal{
		{Path: filepath.Join(root, ".agents", "mcp_config.json"), Reason: "found .agents/mcp_config.json"},
		{Path: filepath.Join(root, ".agents", "agents"), Reason: "found .agents/agents/ directory"},
		{Path: filepath.Join(root, ".agents", "rules"), Reason: "found .agents/rules/ directory"},
		// These two match only when root is $HOME (--global); a fresh agy install
		// has the antigravity-cli directory but no mcp_config.json.
		{Path: filepath.Join(root, ".gemini", "antigravity-cli"), Reason: "found .gemini/antigravity-cli/ directory"},
		{Path: filepath.Join(root, ".gemini", "config", "mcp_config.json"), Reason: "found .gemini/config/mcp_config.json"},
	}
	return adapter.DetectFromSignals(canonicalID, signals), nil
}

func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("antigravity: RenderRuntime called with nil manifest or runtime block")
	}
	content, keys, err := adapter.RenderMCPServersJSON(m, adapter.CredentialFromContext(ctx), "serverUrl")
	if err != nil {
		return nil, fmt.Errorf("antigravity: %w", err)
	}
	return []adapter.FileWrite{{Path: mcpConfigPath, Content: content, Merge: adapter.MergeDeep, Keys: keys}}, nil
}

// Plugin MCP entries are merged verbatim: Antigravity accepts `url` as well
// as `serverUrl` for a remote server.
func mcpDeepKeys(srcRel string, in []byte) ([]byte, []string, error) {
	return route.MCPDeepKeys(canonicalID, srcRel, in, "mcpServers")
}

// subagentFrontmatter keeps only name and description. Antigravity's other
// fields (tools, model) take its own values, and an unmapped tool name can
// hang the subagent.
func subagentFrontmatter(srcRel string, in []byte) ([]byte, []string, error) {
	fm, body, found := route.SplitFrontmatter(in)
	if !found {
		return in, nil, nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, nil, fmt.Errorf("antigravity: parse %q frontmatter: %w", srcRel, err)
	}
	kept := map[string]any{}
	for _, k := range []string{"name", "description"} {
		if v, ok := doc[k]; ok {
			kept[k] = v
		}
	}
	out, err := route.EncodeFrontmatterDoc(kept, body)
	if err != nil {
		return nil, nil, fmt.Errorf("antigravity: encode %q: %w", srcRel, err)
	}
	return out, nil, nil
}

func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "skills/**/*", ToGlob: ".agents/skills/**/*", Merge: adapter.MergeReplace, Prefix: route.PrefixSkill, Sep: "-"},
		{FromGlob: "agents/**/*.md", ToGlob: ".agents/agents/**/*.md", Merge: adapter.MergeReplace, Transform: subagentFrontmatter},
		{FromGlob: "mcp/**/*", ToGlob: mcpConfigPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
		{FromGlob: ".mcp.json", ToGlob: mcpConfigPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
	}
}

// SPDX-License-Identifier: Apache-2.0

// Package cursor is the Cursor adapter (the editor and its `agent` CLI share
// one configuration). MCP servers deep-merge into .cursor/mcp.json; skills go
// to the cross-tool .agents/skills; commands and subagents are copied
// verbatim, since Cursor reads Claude-format files as they are.
// Contract: https://cursor.com/docs/{mcp,skills,subagents}.
package cursor

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

const (
	canonicalID = "cursor"
	mcpJSONPath = ".cursor/mcp.json"
)

type Adapter struct{}

func init() {
	adapter.Register(&Adapter{})
}

func (a *Adapter) ID() string { return canonicalID }

func (a *Adapter) Aliases() []string { return []string{"cursor-agent"} }

func (a *Adapter) Detect(root string) (adapter.Match, error) {
	signals := []adapter.Signal{
		{Path: filepath.Join(root, ".cursor"), Reason: "found .cursor/ directory"},
		{Path: filepath.Join(root, ".cursor", "mcp.json"), Reason: "found .cursor/mcp.json"},
		{Path: filepath.Join(root, ".cursor", "rules"), Reason: "found .cursor/rules/ directory"},
	}
	return adapter.DetectFromSignals(canonicalID, signals), nil
}

func (a *Adapter) RenderRuntime(ctx context.Context, m *manifest.Manifest, _ *state.File) ([]adapter.FileWrite, error) {
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("cursor: RenderRuntime called with nil manifest or runtime block")
	}
	content, keys, err := adapter.RenderMCPServersJSON(m, adapter.CredentialFromContext(ctx), "url")
	if err != nil {
		return nil, fmt.Errorf("cursor: %w", err)
	}
	return []adapter.FileWrite{{Path: mcpJSONPath, Content: content, Merge: adapter.MergeDeep, Keys: keys}}, nil
}

// ponytail: plugin MCP entries are merged verbatim. Cursor interpolates
// ${env:VAR}, not ${VAR}; rewrite the reference here if a plugin ships one.
func mcpDeepKeys(srcRel string, in []byte) ([]byte, []string, error) {
	return route.MCPDeepKeys(canonicalID, srcRel, in, "mcpServers")
}

func (a *Adapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".cursor/commands/**/*.md", Merge: adapter.MergeReplace, Prefix: route.PrefixCommand, Sep: "-"},
		{FromGlob: "skills/**/*", ToGlob: ".agents/skills/**/*", Merge: adapter.MergeReplace, Prefix: route.PrefixSkill, Sep: "-"},
		{FromGlob: "agents/**/*.md", ToGlob: ".cursor/agents/**/*.md", Merge: adapter.MergeReplace},
		{FromGlob: "mcp/**/*", ToGlob: mcpJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
		{FromGlob: ".mcp.json", ToGlob: mcpJSONPath, Merge: adapter.MergeDeep, Transform: mcpDeepKeys},
	}
}

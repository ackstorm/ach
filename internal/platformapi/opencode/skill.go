// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	_ "embed"
	"strings"
)

//go:embed skill/SKILL.md
var skillTemplate string

// genaiSkill is ACH's own OpenCode skill: how an agent uses the gateway
// (enable + sign in to MCP servers, ach-cli, ek_ keys). Static per
// deployment; only {{base}} and {{provider}} are filled in. The MCP list is
// NOT embedded: the config already delivers it (disabled) and the skill
// reads it with `opencode mcp list`.
func genaiSkill(baseURL, provider string) map[string]any {
	md := strings.NewReplacer("{{base}}", strings.TrimRight(baseURL, "/"), "{{provider}}", provider).Replace(skillTemplate)
	return map[string]any{"name": "genai-api", "version": sha(md), "files": map[string]string{"SKILL.md": md}}
}

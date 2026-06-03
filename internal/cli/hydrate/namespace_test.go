// SPDX-License-Identifier: Apache-2.0

package hydrate

import "testing"

func TestNamespaceLeaf(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		plugin string
		want   string
	}{
		{"agent file", ".claude/agents/cloud-architect.md", "cloud-infra", ".claude/agents/cloud-infra-cloud-architect.md"},
		{"command file", ".codex/prompts/deploy.md", "ci-tools", ".codex/prompts/ci-tools-deploy.md"},
		{"skill marker dir", ".claude/skills/optimize/SKILL.md", "acme", ".claude/skills/acme-optimize/SKILL.md"},
		{"nested skill marker", ".claude/skills/optimize/extra/x.md", "acme", ".claude/skills/acme-optimize/extra/x.md"},
		{"skip when leaf==plugin", ".claude/agents/code-review.md", "code-review", ".claude/agents/code-review.md"},
		{"skip when skilldir==plugin", ".claude/skills/acme/SKILL.md", "acme", ".claude/skills/acme/SKILL.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := namespaceLeaf(c.path, c.plugin)
			if got != c.want {
				t.Errorf("namespaceLeaf(%q,%q) = %q want %q", c.path, c.plugin, got, c.want)
			}
		})
	}
}

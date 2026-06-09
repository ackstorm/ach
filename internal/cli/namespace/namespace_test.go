// SPDX-License-Identifier: Apache-2.0

package namespace_test

import (
	"testing"

	"github.com/ackstorm/ach/internal/cli/namespace"
)

func TestLeaf(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		plugin string
		want   string
	}{
		{"plain leaf", ".claude/commands/review.md", "codex", ".claude/commands/codex-review.md"},
		{"nested plain leaf", ".opencode/commands/git/sync.md", "dev", ".opencode/commands/git/dev-sync.md"},
		{"skill segment prefixed", ".claude/skills/pdf/SKILL.md", "tools", ".claude/skills/tools-pdf/SKILL.md"},
		{"skill ref file prefixed on name", ".claude/skills/pdf/refs/a.md", "tools", ".claude/skills/tools-pdf/refs/a.md"},
		{"redundant leaf skipped", ".claude/commands/codex.md", "codex", ".claude/commands/codex.md"},
		{"redundant skill name skipped", ".claude/skills/pdf/SKILL.md", "pdf", ".claude/skills/pdf/SKILL.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := namespace.Leaf(c.path, c.plugin)
			if got != c.want {
				t.Errorf("Leaf(%q, %q) = %q; want %q", c.path, c.plugin, got, c.want)
			}
		})
	}
}

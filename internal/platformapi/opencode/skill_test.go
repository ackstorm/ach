// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"strings"
	"testing"
)

func TestAPISkill(t *testing.T) {
	s := apiSkill("https://ach.test/", "ackstorm", "ackstorm")
	md := s["files"].(map[string]string)["SKILL.md"]
	if s["name"] != "ackstorm-api" || !strings.HasPrefix(md, "---\nname: ackstorm-api\n") ||
		!strings.Contains(md, "ach-cli login https://ach.test\n") ||
		!strings.Contains(md, "`https://ach.test/v1`") || !strings.Contains(md, "opencode auth login ackstorm") ||
		!strings.Contains(md, "opencode auth login https://ach.test`") ||
		!strings.Contains(md, "ach-cli env hydrate ackstorm -g --target opencode") ||
		strings.Contains(md, "{{") {
		t.Fatalf("%v", s)
	}
	if s["version"] != sha(md) || len(md) > 256<<10 {
		t.Fatalf("version %v size %d", s["version"], len(md))
	}

	// No default Environment: its step is cut, the rest is unchanged.
	bare := apiSkill("https://ach.test/", "ackstorm", "")["files"].(map[string]string)["SKILL.md"]
	if strings.Contains(bare, "baseline") || strings.Contains(bare, "{{") || !strings.Contains(bare, "one folder per Environment") {
		t.Fatal(bare)
	}
}

func TestSkillName(t *testing.T) {
	for p, want := range map[string]string{"ackstorm": "ackstorm-api", "ai-platform": "ai-platform-api", strings.Repeat("a", 60): strings.Repeat("a", 60) + "-api"} {
		if got, err := SkillName(p); err != nil || got != want {
			t.Fatalf("%q: %q %v", p, got, err)
		}
	}
	for _, p := range []string{strings.Repeat("a", 61), "a-", "a--b"} {
		if _, err := SkillName(p); err == nil {
			t.Fatalf("%q: want error", p)
		}
	}
}

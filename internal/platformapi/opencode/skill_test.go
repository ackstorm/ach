// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"strings"
	"testing"
)

func TestGenaiSkill(t *testing.T) {
	s := genaiSkill("https://ach.test/", "acme")
	md := s["files"].(map[string]string)["SKILL.md"]
	if s["name"] != "genai-api" || !strings.HasPrefix(md, "---\nname: genai-api\n") ||
		!strings.Contains(md, "ach-cli login --base-url https://ach.test ") ||
		!strings.Contains(md, "`https://ach.test/v1`") || !strings.Contains(md, "opencode auth login -p acme") ||
		strings.Contains(md, "{{") {
		t.Fatalf("%v", s)
	}
	if s["version"] != sha(md) || len(md) > 256<<10 {
		t.Fatalf("version %v size %d", s["version"], len(md))
	}
}

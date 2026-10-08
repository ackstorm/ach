// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed skill/SKILL.md
var skillTemplate string

// skillNameRE is the OpenCode plugin's SKILL_NAME rule (opencode-oidc-provider
// index.mjs): a skill whose name fails it, or is over 64 chars, is silently
// dropped on the user's machine.
var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// SkillName is the name of ACH's OpenCode skill for provider: "<provider>-api".
// The provider rule (≤64 chars, any '-' placement) is looser than the plugin's
// skill-name rule, so platform-api refuses to start on an error here.
func SkillName(provider string) (string, error) {
	name := provider + "-api"
	if len(name) > 64 || !skillNameRE.MatchString(name) {
		return "", fmt.Errorf("skill name %q: want kebab-case (%s), at most 64 chars", name, skillNameRE)
	}
	return name, nil
}

// The block between these markers is the "hydrate the default Environment"
// step; it is cut when no default Environment is configured.
const defaultEnvOpen, defaultEnvClose = "{{#default_env}}\n", "{{/default_env}}\n"

// apiSkill is ACH's own OpenCode skill: an onboarding walkthrough (ach-cli,
// Environments, MCP, ek_ keys). Static per deployment; only {{base}},
// {{provider}}, {{skill}} and {{default_env}} are filled in. defaultEnv is
// genai.defaultEnvironment ("" cuts its step).
func apiSkill(baseURL, provider, defaultEnv string) map[string]any {
	name, _ := SkillName(provider) // validated at platform-api start
	before, rest, _ := strings.Cut(skillTemplate, defaultEnvOpen)
	block, after, _ := strings.Cut(rest, defaultEnvClose)
	if defaultEnv == "" {
		block = ""
	}
	md := strings.NewReplacer("{{base}}", strings.TrimRight(baseURL, "/"), "{{provider}}", provider,
		"{{skill}}", name, "{{default_env}}", defaultEnv).Replace(before + block + after)
	return map[string]any{"name": name, "version": sha(md), "files": map[string]string{"SKILL.md": md}}
}

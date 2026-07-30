// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"path/filepath"
	"testing"
)

// clearAll neutralizes every config-dir env var this package reads, so a test
// case is unaffected by the developer's own shell (XDG_CONFIG_HOME in
// particular is commonly set).
func clearAll(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME",
		"PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME",
	} {
		t.Setenv(v, "")
	}
}

const testHome = "/home/u"

// TestRemapGlobalPath_NoEnv pins today's behavior: with no override set, only
// opencode's .opencode/ -> .config/opencode/ substitution applies and every
// other path passes through untouched.
func TestRemapGlobalPath_NoEnv(t *testing.T) {
	clearAll(t)
	cases := []struct{ adapterID, in, want string }{
		{"opencode", ".opencode/opencode.json", ".config/opencode/opencode.json"},
		{"opencode", ".opencode/skills/a/b.md", ".config/opencode/skills/a/b.md"},
		{"opencode", "AGENTS.md", "AGENTS.md"},
		{"claude-code", ".claude/settings.json", ".claude/settings.json"},
		{"claude-code", ".mcp.json", ".mcp.json"},
		{"codex", ".codex/config.toml", ".codex/config.toml"},
		{"codex", ".agents/skills/a/b.md", ".agents/skills/a/b.md"},
		{"gemini-cli", ".gemini/settings.json", ".gemini/settings.json"},
		{"pimono", ".pi/agent/skills/a.md", ".pi/agent/skills/a.md"},
		{"pimono", ".pi/mcp.json", ".pi/mcp.json"},
		{"unknown-adapter", ".claude/settings.json", ".claude/settings.json"},
	}
	for _, tc := range cases {
		if got := RemapGlobalPath(tc.adapterID, testHome, tc.in); got != tc.want {
			t.Errorf("RemapGlobalPath(%q, %q) = %q; want %q", tc.adapterID, tc.in, got, tc.want)
		}
	}
}

// TestRemapGlobalPath_EnvSet covers each adapter's own var, including the two
// PARENT-shaped ones (gemini appends .gemini, opencode appends opencode).
func TestRemapGlobalPath_EnvSet(t *testing.T) {
	cases := []struct{ name, adapterID, envVar, envVal, in, want string }{
		{"claude dir-itself", "claude-code", "CLAUDE_CONFIG_DIR", "/p/claude",
			".claude/skills/a/b.md", "/p/claude/skills/a/b.md"},
		{"codex dir-itself", "codex", "CODEX_HOME", "/p/codex",
			".codex/config.toml", "/p/codex/config.toml"},
		{"gemini parent", "gemini-cli", "GEMINI_CLI_HOME", "/p/gem",
			".gemini/settings.json", "/p/gem/.gemini/settings.json"},
		{"opencode parent", "opencode", "XDG_CONFIG_HOME", "/p/xdg",
			".opencode/opencode.json", "/p/xdg/opencode/opencode.json"},
		{"pi dir-itself", "pimono", "PI_CODING_AGENT_DIR", "/p/pi",
			".pi/agent/skills/a.md", "/p/pi/skills/a.md"},
		// Value is cleaned, so no ".." survives into the destination.
		{"cleaned", "codex", "CODEX_HOME", "/p/x/../codex",
			".codex/config.toml", "/p/codex/config.toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAll(t)
			t.Setenv(tc.envVar, tc.envVal)
			if got := RemapGlobalPath(tc.adapterID, testHome, tc.in); got != tc.want {
				t.Errorf("= %q; want %q", got, tc.want)
			}
		})
	}
}

// TestRemapGlobalPath_EnvIgnored: an empty or relative value is NOT usable and
// must fall back to the no-override behavior (O-1 fail-safe).
func TestRemapGlobalPath_EnvIgnored(t *testing.T) {
	cases := []struct{ name, envVal, want string }{
		{"empty", "", ".claude/settings.json"},
		{"relative", "relative/dir", ".claude/settings.json"},
		{"dot-relative", "./x", ".claude/settings.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAll(t)
			t.Setenv("CLAUDE_CONFIG_DIR", tc.envVal)
			got := RemapGlobalPath("claude-code", testHome, ".claude/settings.json")
			if got != tc.want {
				t.Errorf("= %q; want %q", got, tc.want)
			}
		})
	}
}

// TestRemapGlobalPath_OnlyPrefixedPathsRedirect proves redirection is keyed on
// the path PREFIX, not the adapter: CODEX_HOME must not capture the cross-tool
// .agents/ convention dir, nor CLAUDE_CONFIG_DIR the project-root files.
func TestRemapGlobalPath_OnlyPrefixedPathsRedirect(t *testing.T) {
	clearAll(t)
	t.Setenv("CODEX_HOME", "/p/codex")
	t.Setenv("CLAUDE_CONFIG_DIR", "/p/claude")
	cases := []struct{ adapterID, in, want string }{
		{"codex", ".agents/skills/a/b.md", ".agents/skills/a/b.md"},
		{"codex", ".codex/prompts/x.md", "/p/codex/prompts/x.md"},
		{"claude-code", ".mcp.json", ".mcp.json"},
		{"claude-code", "CLAUDE.md", "CLAUDE.md"},
		{"claude-code", ".claude/agents/x.md", "/p/claude/agents/x.md"},
	}
	for _, tc := range cases {
		if got := RemapGlobalPath(tc.adapterID, testHome, tc.in); got != tc.want {
			t.Errorf("RemapGlobalPath(%q, %q) = %q; want %q", tc.adapterID, tc.in, got, tc.want)
		}
	}
}

// TestRemapGlobalPath_SameDirStaysRelative is the anti-churn invariant: when the
// override resolves to the very location the $HOME-relative form already names,
// the RELATIVE form is returned.
func TestRemapGlobalPath_SameDirStaysRelative(t *testing.T) {
	clearAll(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(testHome, ".config"))
	got := RemapGlobalPath("opencode", testHome, ".opencode/opencode.json")
	if want := ".config/opencode/opencode.json"; got != want {
		t.Errorf("= %q; want %q (relative, no churn)", got, want)
	}

	clearAll(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(testHome, ".claude"))
	got = RemapGlobalPath("claude-code", testHome, ".claude/settings.json")
	if want := ".claude/settings.json"; got != want {
		t.Errorf("= %q; want %q (relative, no churn)", got, want)
	}
}

// TestResolveDest: an absolute (redirected) destination bypasses the root join;
// a relative one joins as before.
func TestResolveDest(t *testing.T) {
	if got, want := ResolveDest("/home/u", ".claude/x.md"), "/home/u/.claude/x.md"; got != want {
		t.Errorf("relative: = %q; want %q", got, want)
	}
	if got, want := ResolveDest("/home/u", "/p/claude/x.md"), "/p/claude/x.md"; got != want {
		t.Errorf("absolute: = %q; want %q", got, want)
	}
}

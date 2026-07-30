// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"os"
	"path/filepath"
	"strings"
)

// globalScope describes how one adapter's --global destinations resolve.
//
// Each supported agent CLI exposes its own config-dir environment variable, and
// they are not the same shape: CLAUDE_CONFIG_DIR / CODEX_HOME /
// PI_CODING_AGENT_DIR name the config dir itself, while GEMINI_CLI_HOME and
// XDG_CONFIG_HOME name a parent under which the tool creates its own subdir
// (.gemini and opencode respectively). envSuffix encodes that difference.
type globalScope struct {
	envVar     string
	fromPrefix string
	envSuffix  string
	fallback   string
}

// globalScopes is the closed per-adapter table, keyed by canonical adapter ID.
var globalScopes = map[string]globalScope{
	"claude-code": {envVar: "CLAUDE_CONFIG_DIR", fromPrefix: ".claude/"},
	"codex":       {envVar: "CODEX_HOME", fromPrefix: ".codex/"},
	"gemini-cli":  {envVar: "GEMINI_CLI_HOME", fromPrefix: ".gemini/", envSuffix: ".gemini"},
	"pimono":      {envVar: "PI_CODING_AGENT_DIR", fromPrefix: ".pi/agent/"},
	"opencode": {
		envVar:     "XDG_CONFIG_HOME",
		fromPrefix: ".opencode/",
		envSuffix:  "opencode",
		fallback:   ".config/opencode/",
	},
}

// RemapGlobalPath adjusts a workspace-relative path for --global scope, given
// home (the $HOME the caller would otherwise join against).
//
// The result remains relative to home for the default location, or becomes an
// absolute path when the adapter's config-dir environment variable redirects it.
// Callers must resolve the result with ResolveDest rather than bare
// filepath.Join.
func RemapGlobalPath(adapterID, home, path string) string {
	s, ok := globalScopes[adapterID]
	if !ok || !strings.HasPrefix(path, s.fromPrefix) {
		return path
	}
	rest := strings.TrimPrefix(path, s.fromPrefix)

	relative := path
	if s.fallback != "" {
		relative = s.fallback + rest
	}

	dir := configDir(s.envVar)
	if dir == "" {
		return relative
	}
	abs := filepath.Join(dir, s.envSuffix, rest)
	if home != "" && abs == filepath.Join(home, relative) {
		return relative
	}
	return abs
}

// configDir returns a usable absolute config directory, or "" when the
// variable is unset, empty, or relative. Relative values are deliberately
// ignored because the agent CLI resolves them against its own working
// directory, not ach-cli's.
func configDir(envVar string) string {
	v := strings.TrimSpace(os.Getenv(envVar))
	if v == "" || !filepath.IsAbs(v) {
		return ""
	}
	return filepath.Clean(v)
}

// ResolveDest joins a recorded destination against root, passing an already
// absolute destination (an env-var-redirected --global destination) through
// unchanged.
func ResolveDest(root, dest string) string {
	if filepath.IsAbs(dest) {
		return dest
	}
	return filepath.Join(root, dest)
}

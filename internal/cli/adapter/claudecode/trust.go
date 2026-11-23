// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FolderUntrusted reports that Claude Code's own config records dir as not
// trusted. Claude Code keeps trust per exact folder, a trusted parent does not
// count, and in an untrusted folder it drops a project plugin marketplace
// silently. False when the config cannot be read: nothing is known then.
func FolderUntrusted(dir string) bool {
	cfgDir := os.Getenv("CLAUDE_CONFIG_DIR")
	path := filepath.Join(cfgDir, ".claude.json")
	if cfgDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		path = filepath.Join(home, ".claude.json")
	}
	b, err := os.ReadFile(path) // #nosec G304 — Claude Code's own config file
	if err != nil {
		return false
	}
	var cfg struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return !cfg.Projects[abs].HasTrustDialogAccepted
}

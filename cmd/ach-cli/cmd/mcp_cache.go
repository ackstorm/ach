// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ackstorm/ach/internal/cli/state"
)

const mcpCacheTTL = 5 * time.Minute

type mcpCacheEntry struct {
	Credential   string           `json:"credentialFingerprint"`
	Expires      time.Time        `json:"expires"`
	Tools        []mcpToolSummary `json:"tools"`
	AuthRequired bool             `json:"authRequired,omitempty"`
}

// One slot per profile and target bounds disk use as OAuth credentials rotate.
func mcpCachePath(c cred, name string) string {
	dir := os.Getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(dir) {
		var err error
		dir, err = os.UserCacheDir()
		if err != nil {
			return ""
		}
	}
	key := strings.Join([]string{strings.TrimRight(c.BaseURL, "/"), c.ProfileName, name}, "\x00")
	return filepath.Join(dir, "ach", "mcp", fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
}

func cachedMCPTools(ctx context.Context, c cred, verbose bool, name string, refresh bool) ([]*mcp.Tool, error) {
	path := mcpCachePath(c, name)
	if path == "" {
		return readMCPTools(ctx, c, verbose, name)
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(c.Bearer)))
	if refresh {
		_ = os.Remove(path)
	} else if data, err := os.ReadFile(path); err == nil {
		var entry mcpCacheEntry
		if json.Unmarshal(data, &entry) == nil && entry.Credential == fingerprint && time.Now().Before(entry.Expires) {
			tools := make([]*mcp.Tool, 0, len(entry.Tools))
			for _, tool := range entry.Tools {
				tools = append(tools, &mcp.Tool{Name: tool.Name, Description: tool.Description})
			}
			if entry.AuthRequired {
				return tools, mcpAuthRequired(name)
			}
			return tools, nil
		}
	}

	tools, err := readMCPTools(ctx, c, verbose, name)
	signIn := errors.Is(err, errMCPAuthRequired)
	if err == nil || signIn {
		entry := mcpCacheEntry{Credential: fingerprint, Expires: time.Now().Add(mcpCacheTTL), AuthRequired: signIn}
		for _, tool := range tools {
			entry.Tools = append(entry.Tools, mcpToolSummary{Name: tool.Name, Description: tool.Description})
		}
		if data, err := json.Marshal(entry); err == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = state.WriteAtomic(path, data, 0o600)
		}
	}
	return tools, err
}

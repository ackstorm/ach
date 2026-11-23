// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/merge"
)

// OpenCode 2 loads a plugin folder listed in the `plugins` array of its config.
const nativeArrayKey = "plugins"

// NativeSupports mirrors how OpenCode 2 resolves a local plugin folder: through
// package.json `exports`, else a root `server` or `index` file. `main` alone
// is not used for a folder.
func (a *Adapter) NativeSupports(dir string) bool {
	if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil { // #nosec G304 — a file of the plugin ach-cli extracted
		var pkg struct {
			Exports json.RawMessage `json:"exports"`
		}
		if json.Unmarshal(raw, &pkg) == nil && len(pkg.Exports) > 0 {
			return true
		}
	}
	for _, name := range []string{"server.js", "server.ts", "index.js", "index.ts"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func (a *Adapter) nativeConfig(p adapter.NativePlugin) string {
	rel := configJSONPath
	if p.Global {
		rel = adapter.RemapGlobalPath(a.ID(), p.ToolRoot, rel)
	}
	return adapter.ResolveDest(p.ToolRoot, rel)
}

func (a *Adapter) NativeRegister(_ context.Context, p adapter.NativePlugin) (string, error) {
	err := adapter.EditJSON(a.nativeConfig(p), func(doc map[string]any) (bool, error) {
		return merge.EnsureArrayEntry(doc, nativeArrayKey, p.Dir())
	})
	if err != nil {
		return "", err
	}
	return "OpenCode loads " + p.Name + " when its service starts (opencode service restart)", nil
}

func (a *Adapter) NativeUnregister(_ context.Context, p adapter.NativePlugin) error {
	return adapter.EditJSON(a.nativeConfig(p), func(doc map[string]any) (bool, error) {
		return merge.RemoveArrayEntry(doc, nativeArrayKey, p.Dir())
	})
}

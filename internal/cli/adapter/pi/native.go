// SPDX-License-Identifier: Apache-2.0

package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/merge"
)

// pi loads a local package in place from the `packages` array of its settings.
const (
	nativeSettingsPath = ".pi/settings.json"
	nativeArrayKey     = "packages"
)

// NativeSupports reports a package.json carrying a `pi` block.
func (a *Adapter) NativeSupports(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json")) // #nosec G304 — a file of the plugin ach-cli extracted
	if err != nil {
		return false
	}
	var pkg struct {
		Pi json.RawMessage `json:"pi"`
	}
	return json.Unmarshal(raw, &pkg) == nil && len(pkg.Pi) > 0
}

func (a *Adapter) nativeSettings(p adapter.NativePlugin) string {
	rel := nativeSettingsPath
	if p.Global {
		rel = adapter.RemapGlobalPath(a.ID(), p.ToolRoot, rel)
	}
	return adapter.ResolveDest(p.ToolRoot, rel)
}

func (a *Adapter) NativeRegister(_ context.Context, p adapter.NativePlugin) (string, error) {
	err := adapter.EditJSON(a.nativeSettings(p), func(doc map[string]any) (bool, error) {
		return merge.EnsureArrayEntry(doc, nativeArrayKey, p.Dir())
	})
	if err != nil || p.Global {
		return "", err
	}
	return "pi loads " + p.Name + " only in a trusted project (pi -a)", nil
}

func (a *Adapter) NativeUnregister(_ context.Context, p adapter.NativePlugin) error {
	return adapter.EditJSON(a.nativeSettings(p), func(doc map[string]any) (bool, error) {
		return merge.RemoveArrayEntry(doc, nativeArrayKey, p.Dir())
	})
}

// SPDX-License-Identifier: Apache-2.0

package antigravity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Antigravity loads every plugin folder under .agents/plugins (workspace) or
// its per-user plugin directory, so installing is placing a copy there. A
// symlink is not used: Antigravity did not load one reliably.
const (
	nativeDirPrefix = ".agents/plugins/"
	// nativeMarker tells a copy ach-cli placed from a folder the user made.
	nativeMarker = ".ach-native"
)

// NativeSupports reports an Antigravity plugin manifest.
func (a *Adapter) NativeSupports(dir string) bool {
	for _, rel := range []string{filepath.Join(".antigravity-plugin", "plugin.json"), "plugin.json"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return true
		}
	}
	return false
}

func (a *Adapter) nativeDest(p adapter.NativePlugin) (string, error) {
	rel := nativeDirPrefix + p.Name
	if p.Global {
		return adapter.ResolveDest(p.ToolRoot, adapter.RemapGlobalPath(a.ID(), p.ToolRoot, rel)), nil
	}
	dest := adapter.ResolveDest(p.ToolRoot, rel)
	return dest, state.CheckContained(p.ToolRoot, dest)
}

func placedByAch(dest string) bool {
	_, err := os.Stat(filepath.Join(dest, nativeMarker))
	return err == nil
}

func (a *Adapter) NativeRegister(_ context.Context, p adapter.NativePlugin) (string, error) {
	dest, err := a.nativeDest(p)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dest); err == nil {
		if !placedByAch(dest) {
			return "", fmt.Errorf("%s exists and ach-cli did not put it there: remove it or rename the plugin", dest)
		}
		if !p.Changed {
			return "", nil
		}
	}
	if err := adapter.CopyTree(p.Dir(), dest); err != nil {
		return "", err
	}
	return "", os.WriteFile(filepath.Join(dest, nativeMarker), []byte(p.Dir()+"\n"), 0o600)
}

func (a *Adapter) NativeUnregister(_ context.Context, p adapter.NativePlugin) error {
	dest, err := a.nativeDest(p)
	if err != nil || !placedByAch(dest) {
		return err
	}
	return os.RemoveAll(dest)
}

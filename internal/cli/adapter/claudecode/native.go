// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Claude Code loads a plugin only through a marketplace. The marketplace is
// p.Root, declared in settings as a directory source, with the listing below.
const nativeListing = ".claude-plugin/marketplace.json"

func (a *Adapter) NativeSupports(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	return err == nil
}

func (a *Adapter) nativeSettings(p adapter.NativePlugin) string {
	rel := settingsJSONPath
	if p.Global {
		rel = adapter.RemapGlobalPath(a.ID(), p.ToolRoot, rel)
	}
	return adapter.ResolveDest(p.ToolRoot, rel)
}

func (a *Adapter) NativeRegister(_ context.Context, p adapter.NativePlugin) (string, error) {
	names, err := p.Names()
	if err != nil {
		return "", err
	}
	if err := writeNativeListing(p, names); err != nil {
		return "", err
	}
	mkt := p.Marketplace()
	source := map[string]any{"source": map[string]any{"source": "directory", "path": p.Root}}
	err = adapter.EditJSON(a.nativeSettings(p), func(doc map[string]any) (bool, error) {
		known, err := adapter.JSONObject(doc, "extraKnownMarketplaces")
		if err != nil {
			return false, err
		}
		enabled, err := adapter.JSONObject(doc, "enabledPlugins")
		if err != nil {
			return false, err
		}
		key := p.Name + "@" + mkt
		if reflect.DeepEqual(known[mkt], source) && enabled[key] == true {
			return false, nil
		}
		known[mkt], enabled[key] = source, true
		return true, nil
	})
	if err != nil || p.Global {
		return "", err
	}
	return "Claude Code loads " + p.Name + " once you trust this folder", nil
}

func (a *Adapter) NativeUnregister(_ context.Context, p adapter.NativePlugin) error {
	names, err := p.Names()
	if err != nil {
		return err
	}
	rest := names[:0:0]
	for _, n := range names {
		if n != p.Name {
			rest = append(rest, n)
		}
	}
	mkt := p.Marketplace()
	err = adapter.EditJSON(a.nativeSettings(p), func(doc map[string]any) (bool, error) {
		changed := false
		drop := func(key, entry string) {
			obj, ok := doc[key].(map[string]any)
			if !ok {
				return
			}
			if _, has := obj[entry]; has {
				delete(obj, entry)
				changed = true
			}
			if len(obj) == 0 {
				delete(doc, key)
			}
		}
		drop("enabledPlugins", p.Name+"@"+mkt)
		if len(rest) == 0 {
			drop("extraKnownMarketplaces", mkt)
		}
		return changed, nil
	})
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return os.RemoveAll(filepath.Join(p.Root, filepath.Dir(nativeListing)))
	}
	return writeNativeListing(p, rest)
}

func writeNativeListing(p adapter.NativePlugin, names []string) error {
	type entry struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	listing := struct {
		Name    string            `json:"name"`
		Owner   map[string]string `json:"owner"`
		Plugins []entry           `json:"plugins"`
	}{Name: p.Marketplace(), Owner: map[string]string{"name": "ach"}}
	for _, n := range names {
		listing.Plugins = append(listing.Plugins, entry{Name: n, Source: "./plugins/" + n})
	}
	out, err := json.MarshalIndent(listing, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(p.Root, nativeListing)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return state.WriteAtomic(path, append(out, '\n'), 0o644)
}

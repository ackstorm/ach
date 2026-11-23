// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Codex loads a plugin from its own per-user cache, filled by `codex plugin
// add` from a marketplace. A project .codex/config.toml is not read for
// marketplaces or plugins, so there is no file to write: the commands do it.
const (
	nativeBin     = "codex"
	nativeListing = ".agents/plugins/marketplace.json"
)

func (a *Adapter) NativeSupports(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".codex-plugin", "plugin.json"))
	return err == nil
}

func (a *Adapter) NativeRegister(ctx context.Context, p adapter.NativePlugin) (string, error) {
	names, err := p.Names()
	if err != nil {
		return "", err
	}
	if err := writeNativeListing(p, names); err != nil {
		return "", err
	}
	if !p.Changed {
		return "", nil
	}
	if err := adapter.RunTool(ctx, nativeBin, "plugin", "marketplace", "add", p.Root); err != nil {
		return "", err
	}
	if err := adapter.RunTool(ctx, nativeBin, "plugin", "add", p.Name+"@"+p.Marketplace()); err != nil {
		return "", err
	}
	return "Codex installs plugins for your user: " + p.Name + " is active in every folder, and Codex may ask you to approve its hooks", nil
}

func (a *Adapter) NativeUnregister(ctx context.Context, p adapter.NativePlugin) error {
	rest, err := p.Others()
	if err != nil {
		return err
	}
	mkt := p.Marketplace()
	if err := adapter.RunTool(ctx, nativeBin, "plugin", "remove", p.Name+"@"+mkt); err != nil {
		return err
	}
	if len(rest) > 0 {
		return writeNativeListing(p, rest)
	}
	// `marketplace remove` exits 1 for a name Codex no longer has, so its
	// failure is not an error here.
	_ = adapter.RunTool(ctx, nativeBin, "plugin", "marketplace", "remove", mkt)
	return os.RemoveAll(filepath.Join(p.Root, ".agents"))
}

func writeNativeListing(p adapter.NativePlugin, names []string) error {
	type source struct {
		Source string `json:"source"`
		Path   string `json:"path"`
	}
	type entry struct {
		Name     string            `json:"name"`
		Source   source            `json:"source"`
		Policy   map[string]string `json:"policy"`
		Category string            `json:"category"`
	}
	listing := struct {
		Name      string            `json:"name"`
		Interface map[string]string `json:"interface"`
		Plugins   []entry           `json:"plugins"`
	}{Name: p.Marketplace(), Interface: map[string]string{"displayName": "ACH " + p.Env}}
	for _, n := range names {
		listing.Plugins = append(listing.Plugins, entry{
			Name:     n,
			Source:   source{Source: "local", Path: "./plugins/" + n},
			Policy:   map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
			Category: "Productivity",
		})
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

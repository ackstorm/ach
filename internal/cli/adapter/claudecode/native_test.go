// SPDX-License-Identifier: Apache-2.0

package claudecode_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/claudecode"
)

func nativePlugin(t *testing.T, name string) adapter.NativePlugin {
	t.Helper()
	ws := t.TempDir()
	p := adapter.NativePlugin{Env: "dev", Name: name, ToolRoot: ws,
		Root: filepath.Join(ws, ".ach", "dev", "native", "claude-code")}
	if err := os.MkdirAll(filepath.Join(p.Dir(), ".claude-plugin"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), ".claude-plugin", "plugin.json"), []byte(`{"name":"`+name+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func TestNative_RegisterWritesTheTwoKeysAndTheListing(t *testing.T) {
	a := &claudecode.Adapter{}
	p := nativePlugin(t, "memory")
	if !a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = false for a tree with .claude-plugin/plugin.json")
	}
	if a.NativeSupports(t.TempDir()) {
		t.Fatal("NativeSupports = true for an empty tree")
	}
	settings := filepath.Join(p.ToolRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	note, err := a.NativeRegister(context.Background(), p)
	if err != nil {
		t.Fatalf("NativeRegister: %v", err)
	}
	if note == "" {
		t.Error("project scope returned no note; an untrusted folder loads nothing and says nothing")
	}
	doc := readJSON(t, settings)
	if doc["model"] != "opus" {
		t.Error("the user's own key was lost")
	}
	mkt := p.Marketplace()
	source := doc["extraKnownMarketplaces"].(map[string]any)[mkt].(map[string]any)["source"].(map[string]any)
	if source["source"] != "directory" || source["path"] != p.Root {
		t.Errorf("marketplace source = %v; want directory at %s", source, p.Root)
	}
	if doc["enabledPlugins"].(map[string]any)["memory@"+mkt] != true {
		t.Errorf("enabledPlugins = %v; want memory@%s", doc["enabledPlugins"], mkt)
	}
	listing := readJSON(t, filepath.Join(p.Root, ".claude-plugin", "marketplace.json"))
	if listing["name"] != mkt {
		t.Errorf("listing name = %v; want %s", listing["name"], mkt)
	}
	entry := listing["plugins"].([]any)[0].(map[string]any)
	if entry["name"] != "memory" || entry["source"] != "./plugins/memory" {
		t.Errorf("listing entry = %v", entry)
	}

	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatalf("NativeUnregister: %v", err)
	}
	doc = readJSON(t, settings)
	if len(doc) != 1 || doc["model"] != "opus" {
		t.Errorf("after unregister settings = %v; want only the user's key", doc)
	}
	if _, err := os.Stat(filepath.Join(p.Root, ".claude-plugin")); !os.IsNotExist(err) {
		t.Error("the listing of an empty marketplace was left behind")
	}
}

func TestNative_UnregisterKeepsTheMarketplaceWhileAnotherPluginRemains(t *testing.T) {
	a := &claudecode.Adapter{}
	p := nativePlugin(t, "memory")
	other := p
	other.Name = "other"
	if err := os.MkdirAll(filepath.Join(other.Dir(), ".claude-plugin"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, x := range []adapter.NativePlugin{p, other} {
		if _, err := a.NativeRegister(context.Background(), x); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, filepath.Join(p.ToolRoot, ".claude", "settings.json"))
	mkt := p.Marketplace()
	if _, ok := doc["extraKnownMarketplaces"].(map[string]any)[mkt]; !ok {
		t.Error("the marketplace was dropped while another plugin still needs it")
	}
	enabled := doc["enabledPlugins"].(map[string]any)
	if _, gone := enabled["memory@"+mkt]; gone {
		t.Error("the removed plugin is still enabled")
	}
	if enabled["other@"+mkt] != true {
		t.Error("the remaining plugin lost its entry")
	}
	listing := readJSON(t, filepath.Join(p.Root, ".claude-plugin", "marketplace.json"))
	if n := len(listing["plugins"].([]any)); n != 1 {
		t.Errorf("listing holds %d plugins; want 1", n)
	}
}

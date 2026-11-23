// SPDX-License-Identifier: Apache-2.0

package opencode_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/opencode"
)

func TestNative_RegistersTheFolderAsAPlugin(t *testing.T) {
	a := &opencode.Adapter{}
	ws := t.TempDir()
	p := adapter.NativePlugin{Env: "dev", Name: "memory", ToolRoot: ws,
		Root: filepath.Join(ws, ".ach", "dev", "native", "opencode")}
	if err := os.MkdirAll(p.Dir(), 0o750); err != nil {
		t.Fatal(err)
	}
	// `main` alone is not how OpenCode 2 resolves a local plugin folder.
	if err := os.WriteFile(filepath.Join(p.Dir(), "package.json"), []byte(`{"name":"memory","main":"x.js"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = true for a package.json without exports")
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), "package.json"), []byte(`{"name":"memory","exports":{".":"./x.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = false for a package.json with exports")
	}

	config := filepath.Join(ws, ".opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(config), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"plugins":["someone-elses-plugin"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	note, err := a.NativeRegister(context.Background(), p)
	if err != nil {
		t.Fatalf("NativeRegister: %v", err)
	}
	if !strings.Contains(note, "opencode service restart") {
		t.Errorf("note = %q; want the restart command", note)
	}
	read := func() []string {
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ Plugins []string }
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Plugins
	}
	if got := read(); len(got) != 2 || got[1] != p.Dir() {
		t.Fatalf("plugins = %v; want the user's entry then %s", got, p.Dir())
	}
	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatalf("NativeUnregister: %v", err)
	}
	if got := read(); len(got) != 1 || got[0] != "someone-elses-plugin" {
		t.Fatalf("after unregister plugins = %v; want only the user's entry", got)
	}
}

func TestNative_SupportsARootEntryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte("export default {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !(&opencode.Adapter{}).NativeSupports(dir) {
		t.Fatal("NativeSupports = false for a folder with index.ts")
	}
}

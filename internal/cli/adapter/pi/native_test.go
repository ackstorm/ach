// SPDX-License-Identifier: Apache-2.0

package pi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/pi"
)

func TestNative_RegistersTheFolderAsAPackage(t *testing.T) {
	a := &pi.Adapter{}
	ws := t.TempDir()
	p := adapter.NativePlugin{Env: "dev", Name: "memory", ToolRoot: ws,
		Root: filepath.Join(ws, ".ach", "dev", "native", "pi")}
	if err := os.MkdirAll(p.Dir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = true without package.json")
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), "package.json"), []byte(`{"name":"memory","pi":{"extensions":["./x.js"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = false for a package.json with a pi key")
	}

	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatalf("NativeRegister: %v", err)
	}
	settings := filepath.Join(ws, ".pi", "settings.json")
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Packages []string }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Packages) != 1 || doc.Packages[0] != p.Dir() {
		t.Fatalf("packages = %v; want [%s]", doc.Packages, p.Dir())
	}
	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatalf("NativeUnregister: %v", err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Error("settings.json was left behind holding nothing")
	}
}

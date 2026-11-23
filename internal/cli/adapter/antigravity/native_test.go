// SPDX-License-Identifier: Apache-2.0

package antigravity_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/antigravity"
)

func nativePlugin(t *testing.T) adapter.NativePlugin {
	t.Helper()
	ws := t.TempDir()
	p := adapter.NativePlugin{Env: "dev", Name: "memory", ToolRoot: ws, Changed: true,
		Root: filepath.Join(ws, ".ach", "dev", "native", "antigravity")}
	if err := os.MkdirAll(filepath.Join(p.Dir(), "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), "scripts", "start.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNative_PlacesACopyWhereAntigravityScans(t *testing.T) {
	a := &antigravity.Adapter{}
	p := nativePlugin(t)
	if a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = true without a plugin manifest")
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), "plugin.json"), []byte(`{"name":"memory"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = false with plugin.json")
	}

	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatalf("NativeRegister: %v", err)
	}
	dest := filepath.Join(p.ToolRoot, ".agents", "plugins", "memory")
	info, err := os.Stat(filepath.Join(dest, "scripts", "start.sh"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("start.sh = %v, %v; want it copied with its executable bit", info, err)
	}

	// An unchanged plugin already in place is left alone.
	if err := os.WriteFile(filepath.Join(dest, "probe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p.Changed = false
	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "probe")); err != nil {
		t.Error("an unchanged plugin was copied again")
	}
	// A changed one is replaced.
	p.Changed = true
	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "probe")); !os.IsNotExist(err) {
		t.Error("a changed plugin kept a file of the previous copy")
	}

	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatalf("NativeUnregister: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("the copy was left behind")
	}
	if _, err := os.Stat(filepath.Join(p.ToolRoot, ".agents")); !os.IsNotExist(err) {
		t.Error(".agents was left behind although it held only this plugin")
	}
}

func TestNative_UnregisterKeepsWhatElseLivesUnderAgents(t *testing.T) {
	a := &antigravity.Adapter{}
	p := nativePlugin(t)
	if err := os.WriteFile(filepath.Join(p.Dir(), "plugin.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(p.ToolRoot, ".agents", "skills", "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("a sibling skill was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ToolRoot, ".agents", "plugins")); !os.IsNotExist(err) {
		t.Error("the empty .agents/plugins was left behind")
	}
}

func TestNative_NeverTouchesAFolderItDidNotPlace(t *testing.T) {
	a := &antigravity.Adapter{}
	p := nativePlugin(t)
	dest := filepath.Join(p.ToolRoot, ".agents", "plugins", "memory")
	if err := os.MkdirAll(dest, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "mine.txt"), []byte("the user's own plugin"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := a.NativeRegister(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "did not put it there") {
		t.Fatalf("NativeRegister over the user's folder = %v; want a refusal", err)
	}
	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "mine.txt")); err != nil {
		t.Errorf("the user's own plugin folder was removed: %v", err)
	}
}

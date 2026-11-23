// SPDX-License-Identifier: Apache-2.0

package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/codex"
)

// recordTool replaces adapter.RunTool and returns the commands it saw.
func recordTool(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prev := adapter.RunTool
	adapter.RunTool = func(_ context.Context, bin string, args ...string) error {
		calls = append(calls, bin+" "+strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { adapter.RunTool = prev })
	return &calls
}

func TestNative_InstallsThroughCodexItself(t *testing.T) {
	a := &codex.Adapter{}
	calls := recordTool(t)
	ws := t.TempDir()
	p := adapter.NativePlugin{Env: "dev", Name: "memory", ToolRoot: ws, Changed: true,
		Root: filepath.Join(ws, ".ach", "dev", "native", "codex")}
	if err := os.MkdirAll(filepath.Join(p.Dir(), ".codex-plugin"), 0o750); err != nil {
		t.Fatal(err)
	}
	if a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = true without .codex-plugin/plugin.json")
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), ".codex-plugin", "plugin.json"), []byte(`{"name":"memory"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.NativeSupports(p.Dir()) {
		t.Fatal("NativeSupports = false with .codex-plugin/plugin.json")
	}

	note, err := a.NativeRegister(context.Background(), p)
	if err != nil {
		t.Fatalf("NativeRegister: %v", err)
	}
	if !strings.Contains(note, "every folder") {
		t.Errorf("note = %q; want it to say the install is user-wide", note)
	}
	mkt := p.Marketplace()
	want := []string{"codex plugin marketplace add " + p.Root, "codex plugin add memory@" + mkt}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands =\n%s\nwant\n%s", strings.Join(*calls, "\n"), strings.Join(want, "\n"))
	}
	raw, err := os.ReadFile(filepath.Join(p.Root, ".agents", "plugins", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Name    string
		Plugins []struct {
			Name   string
			Source struct{ Source, Path string }
		}
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Name != mkt || len(listing.Plugins) != 1 || listing.Plugins[0].Source.Path != "./plugins/memory" || listing.Plugins[0].Source.Source != "local" {
		t.Fatalf("listing = %+v", listing)
	}

	// An unchanged tree costs no command.
	*calls = nil
	p.Changed = false
	if _, err := a.NativeRegister(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("an unchanged plugin ran %v", *calls)
	}

	*calls = nil
	if err := a.NativeUnregister(context.Background(), p); err != nil {
		t.Fatalf("NativeUnregister: %v", err)
	}
	want = []string{"codex plugin remove memory@" + mkt, "codex plugin marketplace remove " + mkt}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands =\n%s\nwant\n%s", strings.Join(*calls, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(filepath.Join(p.Root, ".agents")); !os.IsNotExist(err) {
		t.Error("the listing of an empty marketplace was left behind")
	}
}

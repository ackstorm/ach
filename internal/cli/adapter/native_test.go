// SPDX-License-Identifier: Apache-2.0

package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

func TestNativePlugin_MarketplaceIsUniquePerRoot(t *testing.T) {
	a := adapter.NativePlugin{Env: "team.dev", Name: "p", Root: "/w1/.ach/team.dev/native/codex"}
	b := adapter.NativePlugin{Env: "team.dev", Name: "p", Root: "/w2/.ach/team.dev/native/codex"}
	if a.Marketplace() == b.Marketplace() {
		t.Fatalf("two roots share the marketplace name %q", a.Marketplace())
	}
	if !strings.HasPrefix(a.Marketplace(), "ach-team-dev-") {
		t.Errorf("Marketplace() = %q; want prefix ach-team-dev-", a.Marketplace())
	}
	again := a
	if a.Marketplace() != again.Marketplace() {
		t.Error("Marketplace() is not stable")
	}
}

func TestNativePlugin_NamesListsPluginFolders(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"zeta", "alpha"} {
		if err := os.MkdirAll(filepath.Join(root, "plugins", n), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	got, err := adapter.NativePlugin{Root: root}.Names()
	if err != nil || strings.Join(got, ",") != "alpha,zeta" {
		t.Fatalf("Names() = %v, %v; want [alpha zeta]", got, err)
	}
	none, err := adapter.NativePlugin{Root: filepath.Join(root, "missing")}.Names()
	if err != nil || len(none) != 0 {
		t.Fatalf("Names() on a missing root = %v, %v; want none", none, err)
	}
}

func TestEditJSON_WritesOnlyOnChangeAndDropsAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	set := func(doc map[string]any) (bool, error) {
		if doc["k"] == "v" {
			return false, nil
		}
		doc["k"] = "v"
		return true, nil
	}
	if err := adapter.EditJSON(path, set); err != nil {
		t.Fatalf("first EditJSON: %v", err)
	}
	before, _ := os.Stat(path)
	if err := adapter.EditJSON(path, set); err != nil {
		t.Fatalf("second EditJSON: %v", err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("an unchanged document was rewritten")
	}
	del := func(doc map[string]any) (bool, error) { delete(doc, "k"); return true, nil }
	if err := adapter.EditJSON(path, del); err != nil {
		t.Fatalf("delete EditJSON: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("an empty document was left on disk (stat err = %v)", err)
	}
	// Nothing to change in a file that does not exist: no folder is created.
	absent := filepath.Join(t.TempDir(), "nodir", "settings.json")
	if err := adapter.EditJSON(absent, func(map[string]any) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(absent)); !os.IsNotExist(err) {
		t.Error("EditJSON created a folder for a no-op")
	}
}

func TestCopyTree_MakesAnExactCopy(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	if err := os.MkdirAll(filepath.Join(src, "hooks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "hooks", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "stale.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := adapter.CopyTree(src, dst); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "hooks", "run.sh"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh = %v, %v; want it copied with its executable bit", info, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.txt")); !os.IsNotExist(err) {
		t.Error("a file of the previous copy survived")
	}
}

func TestJSONObject_RefusesANonObject(t *testing.T) {
	doc := map[string]any{"enabledPlugins": "oops"}
	if _, err := adapter.JSONObject(doc, "enabledPlugins"); err == nil {
		t.Fatal("JSONObject accepted a string where an object is expected")
	}
	obj, err := adapter.JSONObject(doc, "other")
	if err != nil || obj == nil {
		t.Fatalf("JSONObject on an absent key = %v, %v", obj, err)
	}
	obj["x"] = true
	if doc["other"].(map[string]any)["x"] != true {
		t.Error("JSONObject did not attach the new object to the document")
	}
}

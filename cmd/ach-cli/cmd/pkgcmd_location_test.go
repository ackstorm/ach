// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// mustPlugin runs `ach-cli local plugin <args>` and fails t on error.
func mustPlugin(t *testing.T, args ...string) {
	t.Helper()
	if out, err := runPluginCmd(args...); err != nil {
		t.Fatalf("plugin %v: %v (output: %s)", args, err, out)
	}
}

// TestPluginCmd_SameRefTwoLocations: the same plugin and target installed in
// two folders and with -g are three installs; uninstalling one leaves the
// others' files and records in place.
func TestPluginCmd_SameRefTwoLocations(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	pluginURL := makeDirectPluginRepo(t)
	seedRepo(t, store.RepoEntry{
		Name:     "fix",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	dirA, dirB := t.TempDir(), t.TempDir()
	cmdFile := filepath.Join(".claude", "commands", "fix", "x.md")

	mustPlugin(t, "install", "fix@fix", "--target", "claude", "--dir", dirA)
	mustPlugin(t, "install", "fix@fix", "--target", "claude", "--dir", dirB)
	mustPlugin(t, "install", "fix@fix", "--target", "claude", "-g")

	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(installed.Installed) != 3 {
		t.Fatalf("installed.json has %d entries, want 3: %+v", len(installed.Installed), installed.Installed)
	}

	mustPlugin(t, "uninstall", "fix@fix", "--dir", dirA)

	if _, err := os.Stat(filepath.Join(dirA, cmdFile)); !os.IsNotExist(err) {
		t.Errorf("folder A file still present (err=%v)", err)
	}
	for _, p := range []string{filepath.Join(dirB, cmdFile), filepath.Join(home, cmdFile)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed by the folder A uninstall: %v", p, err)
		}
	}
	installed, err = store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(installed.Installed) != 2 {
		t.Fatalf("installed.json has %d entries after uninstall, want 2: %+v",
			len(installed.Installed), installed.Installed)
	}
	for _, e := range installed.Installed {
		if !e.Global && e.Dir != dirB {
			t.Errorf("unexpected record left: %+v", e)
		}
	}

	mustPlugin(t, "uninstall", "fix@fix", "-g")
	if _, err := os.Stat(filepath.Join(home, cmdFile)); !os.IsNotExist(err) {
		t.Errorf("global file still present (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, cmdFile)); err != nil {
		t.Errorf("folder B file removed by the -g uninstall: %v", err)
	}
}

// TestLocationHolds: a record matches only its own location; a record that
// carries no location matches every location.
func TestLocationHolds(t *testing.T) {
	a := location{dir: "/a"}
	g := location{global: true}
	cases := []struct {
		name string
		e    store.InstalledEntry
		l    location
		want bool
	}{
		{"same dir", store.InstalledEntry{Dir: "/a"}, a, true},
		{"other dir", store.InstalledEntry{Dir: "/b"}, a, false},
		{"global record, dir", store.InstalledEntry{Global: true}, a, false},
		{"global record, global", store.InstalledEntry{Global: true}, g, true},
		{"dir record, global", store.InstalledEntry{Dir: "/a"}, g, false},
		{"no location, dir", store.InstalledEntry{}, a, true},
		{"no location, global", store.InstalledEntry{}, g, true},
	}
	for _, c := range cases {
		if got := c.l.holds(c.e); got != c.want {
			t.Errorf("%s: holds = %v, want %v", c.name, got, c.want)
		}
	}
}

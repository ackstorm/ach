// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// seedState writes a per-platform state file under .ach/<env>/state-<platform>.json
// with the given environment + adapter id, the two fields deriveManifest reads.
func seedState(t *testing.T, cwd, env, platform string) {
	t.Helper()
	dir := filepath.Join(cwd, ".ach", env)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schemaVersion":"3","environment":"` + env +
		`","adapter":{"id":"` + platform + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "state-"+platform+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveManifest_GroupsAndSorts(t *testing.T) {
	cwd := t.TempDir()
	seedState(t, cwd, "team-shared", "codex")
	seedState(t, cwd, "team-shared", "claude-code")
	seedState(t, cwd, "project-x", "claude-code")

	m, err := deriveManifest(cwd)
	if err != nil {
		t.Fatalf("deriveManifest: %v", err)
	}
	if len(m.Environments) != 2 {
		t.Fatalf("want 2 envs, got %+v", m.Environments)
	}
	if m.Environments[0].Name != "project-x" || m.Environments[1].Name != "team-shared" {
		t.Fatalf("envs not sorted: %+v", m.Environments)
	}
	ts := m.Environments[1].Targets // team-shared
	if len(ts) != 2 || ts[0] != "claude-code" || ts[1] != "codex" {
		t.Fatalf("team-shared targets wrong/unsorted: %v", ts)
	}
}

func TestDeriveManifest_NothingHydrated(t *testing.T) {
	if _, err := deriveManifest(t.TempDir()); !errors.Is(err, errNothingHydrated) {
		t.Fatalf("want errNothingHydrated, got %v", err)
	}
}

func TestEnvSave_WritesFile(t *testing.T) {
	cwd := t.TempDir()
	seedState(t, cwd, "demo", "claude-code")

	// Run the command with cwd switched to the temp workspace.
	restore := chdir(t, cwd)
	defer restore()

	if _, _, _, err := executeCommand(t, newEnvSaveCmd()); err != nil {
		t.Fatalf("env save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "ach.yaml")); err != nil {
		t.Fatalf("ach.yaml not written: %v", err)
	}
}

// chdir switches the process cwd for the test and returns a restore func.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(prev) }
}

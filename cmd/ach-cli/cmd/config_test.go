// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// configTestEnv resets XDG_CONFIG_HOME → t.TempDir() and clears the
// synthetic-mode env vars so each subtest runs hermetically.
func configTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ACH_BASE_URL", "")
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_PROFILE", "")
	return dir
}

// seedConfigFile writes a config.yaml with the supplied File contents
// under the test XDG home.
func seedConfigFile(t *testing.T, dir string, f *config.File) string {
	t.Helper()
	path := filepath.Join(dir, "ach", "config.yaml")
	if err := config.Save(path, f); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return path
}

// executeConfig runs a fresh `ach config <sub>` invocation with the
// given args and returns stdout, stderr, exit code, raw error.
// Delegates to the shared executeCommand helper (helpers_test.go).
func executeConfig(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	return executeCommand(t, newConfigCmd(), args...)
}

// TestConfig_List_Empty asserts `ach config list` exits 0 + prints
// "No profiles configured" on a fresh empty config dir.
func TestConfig_List_Empty(t *testing.T) {
	configTestEnv(t)
	stdout, _, code, err := executeConfig(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "No profiles configured") {
		t.Errorf("missing 'No profiles configured'; stdout: %s", stdout)
	}
}

// TestConfig_List_TwoProfiles asserts the table renders with the
// CURRENT "*" marker on the active default row.
func TestConfig_List_TwoProfiles(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example", PK: "pk_aaaaaaaaaaaaaaaaaaaaaawxyz"},
			"stg":  {URL: "https://stg.example"},
		},
	})
	stdout, _, code, err := executeConfig(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "CURRENT") {
		t.Errorf("missing CURRENT column header; stdout: %s", stdout)
	}
	var prodMarked bool
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "prod") && strings.HasPrefix(strings.TrimSpace(line), "*") {
			prodMarked = true
		}
	}
	if !prodMarked {
		t.Errorf("default 'prod' row missing '*' marker; stdout: %s", stdout)
	}
	if !strings.Contains(stdout, "stg") {
		t.Errorf("missing 'stg' row; stdout: %s", stdout)
	}
}

// TestConfig_Show_Masked asserts no --reveal hides the full pk/ek
// plaintext (CLI-04).
func TestConfig_Show_Masked(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {
				URL: "https://prod.example",
				PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
				EK:  map[string]string{"demo": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234"},
			},
		},
	})
	stdout, _, code, err := executeConfig(t, "show", "prod")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if strings.Contains(stdout, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz") {
		t.Errorf("CLI-04 leak: full pk plaintext present; stdout: %s", stdout)
	}
	if strings.Contains(stdout, "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234") {
		t.Errorf("CLI-04 leak: full ek plaintext present; stdout: %s", stdout)
	}
	if !strings.Contains(stdout, "pk-****wxyz") {
		t.Errorf("missing masked pk tail; stdout: %s", stdout)
	}
}

// TestConfig_Show_Reveal asserts --reveal emits the full plaintext.
func TestConfig_Show_Reveal(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example", PK: "pk_aaaaaaaaaaaaaaaaaaaaaawxyz"},
		},
	})
	stdout, _, code, err := executeConfig(t, "show", "prod", "--reveal")
	if err != nil {
		t.Fatalf("show --reveal: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "pk_aaaaaaaaaaaaaaaaaaaaaawxyz") {
		t.Errorf("--reveal: missing full pk plaintext; stdout: %s", stdout)
	}
}

// TestConfig_Use_SetsDefault asserts `ach config use <name>` updates
// default: + persists.
func TestConfig_Use_SetsDefault(t *testing.T) {
	dir := configTestEnv(t)
	path := seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example"},
			"stg":  {URL: "https://stg.example"},
		},
	})
	_, _, code, err := executeConfig(t, "use", "stg")
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Default != "stg" {
		t.Errorf("default = %q; want stg", f.Default)
	}
}

// TestConfig_Use_UnknownName asserts exit 1 when the name is not in
// the registry.
func TestConfig_Use_UnknownName(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example"},
		},
	})
	_, _, code, err := executeConfig(t, "use", "nonexistent")
	if err == nil {
		t.Fatal("expected error on unknown name")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
}

// TestConfig_Remove_DefaultWithoutForce asserts removing the active
// default WITHOUT --force exits 1.
func TestConfig_Remove_DefaultWithoutForce(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example"},
			"stg":  {URL: "https://stg.example"},
		},
	})
	_, _, code, err := executeConfig(t, "remove", "prod")
	if err == nil {
		t.Fatal("expected error removing default without --force")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err missing '--force' hint: %q", err.Error())
	}
}

// TestConfig_Remove_DefaultWithForce asserts removing the active
// default WITH --force succeeds AND clears default:.
func TestConfig_Remove_DefaultWithForce(t *testing.T) {
	dir := configTestEnv(t)
	path := seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example"},
			"stg":  {URL: "https://stg.example"},
		},
	})
	_, _, code, err := executeConfig(t, "remove", "prod", "--force")
	if err != nil {
		t.Fatalf("remove --force: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := f.Profiles["prod"]; ok {
		t.Errorf("profile 'prod' should be removed")
	}
	if f.Default != "" {
		t.Errorf("default = %q; want '' (cleared after default removal)", f.Default)
	}
}

// TestConfig_Remove_NonDefault asserts removing a non-default
// profile works without --force.
func TestConfig_Remove_NonDefault(t *testing.T) {
	dir := configTestEnv(t)
	path := seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example"},
			"stg":  {URL: "https://stg.example"},
		},
	})
	_, _, code, err := executeConfig(t, "remove", "stg")
	if err != nil {
		t.Fatalf("remove stg: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := f.Profiles["stg"]; ok {
		t.Errorf("'stg' should be removed")
	}
	if f.Default != "prod" {
		t.Errorf("default = %q; want prod (unchanged)", f.Default)
	}
}

// TestConfig_Rename_PreservesPKAndEK asserts the rename preserves
// PK + EK map AND updates default: when it was pointing at <old>.
func TestConfig_Rename_PreservesPKAndEK(t *testing.T) {
	dir := configTestEnv(t)
	path := seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {
				URL: "https://prod.example",
				PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
				EK:  map[string]string{"demo": "ek_aaaaaaaaaaaaaaaaaaaaa1234"},
			},
		},
	})
	_, _, code, err := executeConfig(t, "rename", "prod", "prod-v2")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := f.Profiles["prod"]; ok {
		t.Errorf("old key 'prod' should be gone")
	}
	dep, ok := f.Profiles["prod-v2"]
	if !ok {
		t.Fatal("new key 'prod-v2' missing")
	}
	if dep.PK != "pk_aaaaaaaaaaaaaaaaaaaaaawxyz" {
		t.Errorf("PK lost during rename; got %q", dep.PK)
	}
	if dep.EK["demo"] != "ek_aaaaaaaaaaaaaaaaaaaaa1234" {
		t.Errorf("EK map lost during rename; got %v", dep.EK)
	}
	if f.Default != "prod-v2" {
		t.Errorf("default not updated; got %q want prod-v2", f.Default)
	}
}

// TestConfig_Rename_TargetExists asserts rename to an existing name
// is rejected with exit 1.
func TestConfig_Rename_TargetExists(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example"},
			"stg":  {URL: "https://stg.example"},
		},
	})
	_, _, code, err := executeConfig(t, "rename", "prod", "stg")
	if err == nil {
		t.Fatal("expected error renaming to existing target")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
}

// TestConfig_SyntheticMode_Exit1 asserts every config sub exits 1
// when synthetic-mode env vars are set (ACH_BASE_URL + ACH_API_KEY).
func TestConfig_SyntheticMode_Exit1(t *testing.T) {
	configTestEnv(t)
	t.Setenv("ACH_BASE_URL", "https://synth.example")
	t.Setenv("ACH_API_KEY", "pk_synthetic_test_key_aaaaaaaaaa")

	subcommands := [][]string{
		{"add", "--profile", "p", "--url", "https://x.example", "--api-key", validEK},
		{"list"},
		{"show", "prod"},
		{"use", "prod"},
		{"remove", "prod"},
		{"rename", "prod", "stg"},
	}
	for _, sub := range subcommands {
		_, _, code, err := executeConfig(t, sub...)
		if err == nil {
			t.Errorf("subcommand %v: expected synthetic-mode rejection", sub)
			continue
		}
		if code != exit.General {
			t.Errorf("subcommand %v: code = %d; want 1", sub, code)
		}
	}
}

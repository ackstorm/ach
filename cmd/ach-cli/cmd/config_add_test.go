// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// validEK is a canonical ek- bearer (ek- + 64 base64url chars, 67 total).
const validEK = "ek-fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// validPK is a canonical pk- bearer (pk- + 64 base64url chars, 67 total).
const validPK = "pk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestConfigAdd_WritesProfile_FirstBecomesDefault(t *testing.T) {
	dir := configTestEnv(t)
	stdout, _, code, err := executeConfig(t, "add",
		"--profile", "demo",
		"--url", "https://hub.example",
		"--api-key", validEK)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit = %d; want 0", code)
	}
	if !strings.Contains(stdout, "added profile demo") {
		t.Errorf("missing success line; stdout: %q", stdout)
	}
	// pk_/ek_ must appear ONLY masked (config.Mask), never in full.
	if strings.Contains(stdout, validEK) {
		t.Errorf("full ek_ leaked to stdout: %q", stdout)
	}
	f, err := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dep, ok := f.Profiles["demo"]
	if !ok {
		t.Fatalf("profile 'demo' not written; file: %+v", f)
	}
	if dep.URL != "https://hub.example" || dep.PK != validEK {
		t.Errorf("profile = %+v; want url=https://hub.example pk=%s", dep, validEK)
	}
	if f.Default != "demo" {
		t.Errorf("Default = %q; want 'demo' (first profile auto-defaults)", f.Default)
	}
}

func TestConfigAdd_DefaultFlag_FlipsDefault(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default:  "prod",
		Profiles: map[string]*config.Profile{"prod": {URL: "https://prod.example", PK: validPK}},
	})
	_, _, code, err := executeConfig(t, "add",
		"--profile", "stg", "--url", "https://stg.example",
		"--api-key", validEK, "--default")
	if err != nil || code != exit.OK {
		t.Fatalf("add: code=%d err=%v", code, err)
	}
	f, _ := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	if f.Default != "stg" {
		t.Errorf("Default = %q; want 'stg' (--default flips it)", f.Default)
	}
}

func TestConfigAdd_NotDefaultByDefault_WhenOthersExist(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Default:  "prod",
		Profiles: map[string]*config.Profile{"prod": {URL: "https://prod.example", PK: validPK}},
	})
	_, _, code, err := executeConfig(t, "add",
		"--profile", "stg", "--url", "https://stg.example", "--api-key", validEK)
	if err != nil || code != exit.OK {
		t.Fatalf("add: code=%d err=%v", code, err)
	}
	f, _ := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	if f.Default != "prod" {
		t.Errorf("Default = %q; want unchanged 'prod' (no --default, others exist)", f.Default)
	}
}

func TestConfigAdd_DuplicateWithoutForce_Exit1(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Profiles: map[string]*config.Profile{"prod": {URL: "https://prod.example", PK: validPK}},
	})
	_, _, code, err := executeConfig(t, "add",
		"--profile", "prod", "--url", "https://x.example", "--api-key", validEK)
	if err == nil {
		t.Fatal("want error on duplicate without --force")
	}
	if code != exit.General {
		t.Errorf("exit = %d; want 1", code)
	}
}

func TestConfigAdd_DuplicateWithForce_Overwrites_PreservesEK(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://old.example", PK: validPK, EK: map[string]string{"team-a": validEK}},
		},
	})
	_, _, code, err := executeConfig(t, "add",
		"--profile", "prod", "--url", "https://new.example", "--api-key", validEK, "--force")
	if err != nil || code != exit.OK {
		t.Fatalf("add --force: code=%d err=%v", code, err)
	}
	f, _ := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	dep := f.Profiles["prod"]
	if dep.URL != "https://new.example" || dep.PK != validEK {
		t.Errorf("overwrite failed: %+v", dep)
	}
	if dep.EK["team-a"] != validEK {
		t.Errorf("EK map should survive --force overwrite; got: %+v", dep.EK)
	}
}

func TestConfigAdd_InvalidInputs_Exit1(t *testing.T) {
	cases := []struct {
		name              string
		profile, url, key string
	}{
		{"bad-name-upper", "Prod", "https://x.example", validEK},
		{"bad-name-underscore", "pr_od", "https://x.example", validEK},
		{"bad-url-scheme", "prod", "ftp://x.example", validEK},
		{"bad-key-prefix", "prod", "https://x.example", "zz_abcdefghijklmnopqrstuvwxyz"},
		{"bad-key-length", "prod", "https://x.example", "ek_short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configTestEnv(t)
			_, _, code, err := executeConfig(t, "add",
				"--profile", tc.profile, "--url", tc.url, "--api-key", tc.key)
			if err == nil {
				t.Fatal("want validation error")
			}
			if code != exit.General {
				t.Errorf("exit = %d; want 1", code)
			}
		})
	}
}

// TestConfigAdd_RefusesHTTP_ByDefault asserts G19 decision B: `config add`
// with a plaintext http:// URL (localhost included) is refused with exit 1
// and the error cites the ACH_INSECURE opt-in.
func TestConfigAdd_RefusesHTTP_ByDefault(t *testing.T) {
	configTestEnv(t)
	_, _, code, err := executeConfig(t, "add",
		"--profile", "dev", "--url", "http://localhost:8080", "--api-key", validEK)
	if code != exit.General {
		t.Fatalf("add(http): code=%d, want 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "ACH_INSECURE") {
		t.Fatalf("error should cite the ACH_INSECURE opt-in; got %v", err)
	}
}

// TestConfigAdd_AllowsHTTP_WithInsecureFlag asserts --insecure writes the
// http:// profile (exit 0).
func TestConfigAdd_AllowsHTTP_WithInsecureFlag(t *testing.T) {
	configTestEnv(t)
	_, _, code, err := executeConfig(t, "add",
		"--profile", "dev", "--url", "http://localhost:8080", "--api-key", validEK, "--insecure")
	if err != nil || code != exit.OK {
		t.Fatalf("add(http,--insecure): code=%d err=%v", code, err)
	}
}

// TestConfigAdd_AllowsHTTP_WithInsecureEnv asserts ACH_INSECURE=1 writes it.
func TestConfigAdd_AllowsHTTP_WithInsecureEnv(t *testing.T) {
	configTestEnv(t)
	t.Setenv("ACH_INSECURE", "1")
	_, _, code, err := executeConfig(t, "add",
		"--profile", "dev", "--url", "http://localhost:8080", "--api-key", validEK)
	if err != nil || code != exit.OK {
		t.Fatalf("add(http,env): code=%d err=%v", code, err)
	}
}

func TestConfigAdd_SyntheticMode_Exit1(t *testing.T) {
	configTestEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.example")
	t.Setenv("ACH_API_KEY", validEK)
	_, _, code, err := executeConfig(t, "add",
		"--profile", "prod", "--url", "https://x.example", "--api-key", validPK)
	if err == nil {
		t.Fatal("want synthetic-mode rejection")
	}
	if code != exit.General {
		t.Errorf("exit = %d; want 1 (synthetic mode)", code)
	}
}

func TestConfigAdd_EnvKeys_PopulatesEKMap(t *testing.T) {
	dir := configTestEnv(t)
	_, _, code, err := executeConfig(t, "add",
		"--profile", "svc", "--url", "https://hub.example", "--api-key", validPK,
		"--env-key", "team-a="+validEK,
		"--env-key", "team-b="+validEK)
	if err != nil || code != exit.OK {
		t.Fatalf("add: code=%d err=%v", code, err)
	}
	f, _ := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	dep := f.Profiles["svc"]
	if dep.EK["team-a"] != validEK || dep.EK["team-b"] != validEK {
		t.Errorf("EK map = %+v; want team-a + team-b", dep.EK)
	}
}

func TestConfigAdd_EnvKeys_Invalid_Exit1(t *testing.T) {
	cases := []struct {
		name string
		spec string
	}{
		{"no-equals", "team-a"},
		{"empty-label", "=" + validEK},
		{"value-not-ek", "team-a=" + validPK}, // pk_ rejected; must be ek_
		{"value-malformed", "team-a=ek_short"},
		{"value-pk-wrong-length", "team-a=pk_short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configTestEnv(t)
			_, _, code, err := executeConfig(t, "add",
				"--profile", "svc", "--url", "https://hub.example",
				"--api-key", validPK, "--env-key", tc.spec)
			if err == nil {
				t.Fatal("want --env-key validation error")
			}
			if code != exit.General {
				t.Errorf("exit = %d; want 1", code)
			}
		})
	}
}

func TestConfigAdd_ForceEnvKey_OverridesMatchingLabel(t *testing.T) {
	dir := configTestEnv(t)
	oldEK := "ek_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedConfigFile(t, dir, &config.File{
		Profiles: map[string]*config.Profile{
			"svc": {URL: "https://old.example", PK: validPK, EK: map[string]string{"team-a": oldEK, "team-b": validEK}},
		},
	})
	_, _, code, err := executeConfig(t, "add",
		"--profile", "svc", "--url", "https://new.example", "--api-key", validPK,
		"--env-key", "team-a="+validEK, "--force")
	if err != nil || code != exit.OK {
		t.Fatalf("add --force: code=%d err=%v", code, err)
	}
	f, _ := config.Load(filepath.Join(dir, "ach", "config.yaml"))
	dep := f.Profiles["svc"]
	if dep.EK["team-a"] != validEK {
		t.Errorf("team-a should be overridden to new ek; got %q", dep.EK["team-a"])
	}
	if dep.EK["team-b"] != validEK {
		t.Errorf("team-b (untouched) should be preserved; got %q", dep.EK["team-b"])
	}
}

func TestConfigParentHelp_MentionsAdd(t *testing.T) {
	configTestEnv(t)
	stdout, _, _, _ := executeConfig(t) // bare `config` prints help
	if !strings.Contains(stdout, "add") {
		t.Errorf("config help should list the 'add' child; stdout: %q", stdout)
	}
}

func TestConfigUse_UnknownProfile_Exit1(t *testing.T) {
	dir := configTestEnv(t)
	seedConfigFile(t, dir, &config.File{
		Profiles: map[string]*config.Profile{"prod": {URL: "https://prod.example", PK: validPK}},
	})
	_, _, code, err := executeConfig(t, "use", "ghost")
	if err == nil || code != exit.General {
		t.Fatalf("want exit 1 on unknown profile; code=%d err=%v", code, err)
	}
}

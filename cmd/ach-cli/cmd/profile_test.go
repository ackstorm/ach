// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

func executeProfile(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	return executeCommand(t, newProfileCmd(), args...)
}

// seedProfiles writes two profiles: prod (session, default, one saved key)
// and ci (machine key).
func seedProfiles(t *testing.T) string {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	prod := oauthProfile("https://prod.example")
	prod.Keys = map[string]config.SavedKey{"laptop": {ID: "ekid_lap", Key: testEKSaved}}
	if err := config.Save(path, &config.File{Default: "prod", Profiles: map[string]*config.Profile{
		"prod": prod, "ci": {URL: "https://ci.example", Key: testEK},
	}}); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadFile(t *testing.T, path string) *config.File {
	t.Helper()
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestProfileList(t *testing.T) {
	credTestEnv(t)
	seedProfiles(t)
	out, _, _, err := executeProfile(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Split(strings.TrimSpace(out), "\n")
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	want := []string{"CURRENT NAME URL AUTH KEYS", "ci https://ci.example key 0", "* prod https://prod.example session 1"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestProfileShow(t *testing.T) {
	credTestEnv(t)
	seedProfiles(t)
	out, _, _, err := executeProfile(t, "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"URL      https://prod.example", "Auth     session", "laptop  ek-****save  (ekid_lap)"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, testEKSaved) {
		t.Error("key leaked without --reveal")
	}
	out, _, _, err = executeProfile(t, "show", "ci", "--reveal")
	if err != nil || !strings.Contains(out, "Auth     key "+testEK) {
		t.Fatalf("reveal: %v\n%s", err, out)
	}
	if _, _, code, err := executeProfile(t, "show", "ghost"); err == nil || code != exit.General {
		t.Fatalf("unknown: code=%d err=%v", code, err)
	}
}

func TestProfileAdd(t *testing.T) {
	credTestEnv(t)
	path, _ := config.Path()
	out, _, _, err := executeProfile(t, "add", "ci", "--url", "https://ci.example", "--key", testEK)
	if err != nil || !strings.Contains(out, "ek-****abcd") {
		t.Fatalf("add: %v %q", err, out)
	}
	f := loadFile(t, path)
	if f.Default != "ci" || f.Profiles["ci"].Key != testEK || f.Profiles["ci"].URL != "https://ci.example" {
		t.Fatalf("saved: %+v", f.Profiles["ci"])
	}

	pk := "pk-" + strings.Repeat("A", 64)
	_, _, code, err := executeProfile(t, "add", "other", "--url", "https://x.example", "--key", pk)
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "--key must be an environment key (ek-…)") {
		t.Fatalf("pk: code=%d err=%v", code, err)
	}
	_, _, code, err = executeProfile(t, "add", "ci", "--url", "https://ci.example", "--key", testEK)
	if code != exit.General || err == nil {
		t.Fatalf("existing without --force: code=%d err=%v", code, err)
	}
	_, _, _, err = executeProfile(t, "add", "ci", "--url", "https://ci2.example", "--key", testEKSaved, "--force")
	if err != nil {
		t.Fatal(err)
	}
	if got := loadFile(t, path).Profiles["ci"]; got.URL != "https://ci2.example" || got.Key != testEKSaved {
		t.Fatalf("--force: %+v", got)
	}
	for _, removed := range []string{"--pk", "--api-key", "--env-key"} {
		if _, _, _, err := executeProfile(t, "add", "x", "--url", "https://x", removed, testEK); err == nil ||
			!strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: err = %v; want unknown flag", removed, err)
		}
	}
}

func TestProfileUseRenameRemove(t *testing.T) {
	credTestEnv(t)
	path := seedProfiles(t)

	if _, _, _, err := executeProfile(t, "use", "ci"); err != nil || loadFile(t, path).Default != "ci" {
		t.Fatalf("use: %v", err)
	}
	if _, _, _, err := executeProfile(t, "rename", "ci", "build"); err != nil {
		t.Fatal(err)
	}
	if f := loadFile(t, path); f.Default != "build" || f.Profiles["build"].Key != testEK || f.Profiles["ci"] != nil {
		t.Fatalf("rename: %+v", f)
	}
	if _, _, code, err := executeProfile(t, "remove", "build"); err == nil || code != exit.General {
		t.Fatalf("remove default without --force: code=%d err=%v", code, err)
	}
	out, _, _, err := executeProfile(t, "remove", "build", "--force")
	if err != nil || !strings.Contains(out, "default reassigned to prod") {
		t.Fatalf("remove --force: %v %q", err, out)
	}
	if f := loadFile(t, path); f.Default != "prod" || f.Profiles["build"] != nil {
		t.Fatalf("after remove: %+v", f)
	}
}

func TestProfileRmEKRemoved(t *testing.T) {
	credTestEnv(t)
	if _, _, _, err := executeProfile(t, "rm-ek", "x"); err == nil {
		t.Fatal("rm-ek must be gone")
	}
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/hydrate"
)

// testCmd returns a minimal cobra.Command with stdout/stderr wired to
// buffers and a background context, so runHydrateWorkspace / runHydrateEngine
// can render their summaries without polluting the real process streams.
func testCmd(t *testing.T) *cobra.Command {
	t.Helper()
	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetContext(context.Background())
	return c
}

// seedState writes a per-platform state file under .ach/<env>/state-<platform>.json
// with the given environment + adapter id, the two fields hydratedEnvs reads.
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

// stubEngine replaces hydrateRunFn for the test, recording each env/platform
// it is asked to hydrate and failing the envs named in failFor.
func stubEngine(t *testing.T, failFor map[string]bool, seen *[]hydrate.Opts) {
	t.Helper()
	prev := hydrateRunFn
	hydrateRunFn = func(_ context.Context, opts hydrate.Opts) (hydrate.Result, error) {
		*seen = append(*seen, opts)
		if failFor[opts.Environment] {
			return hydrate.Result{}, &os.PathError{Op: "hydrate", Path: opts.Environment, Err: os.ErrPermission}
		}
		return hydrate.Result{Environment: opts.Environment, PlatformID: opts.Platform,
			Models: []string{"a.fast", opts.Environment + ".smart"}}, nil
	}
	t.Cleanup(func() { hydrateRunFn = prev })
}

func TestHydratedEnvs_GroupsAndSorts(t *testing.T) {
	cwd := t.TempDir()
	seedState(t, cwd, "team-shared", "codex")
	seedState(t, cwd, "team-shared", "claude-code")
	seedState(t, cwd, "project-x", "claude-code")

	envs, err := hydratedEnvs(cwd)
	if err != nil {
		t.Fatalf("hydratedEnvs: %v", err)
	}
	if len(envs) != 2 || envs[0].name != "project-x" || envs[1].name != "team-shared" {
		t.Fatalf("envs not grouped/sorted: %+v", envs)
	}
	if ts := envs[1].targets; len(ts) != 2 || ts[0] != "claude-code" || ts[1] != "codex" {
		t.Fatalf("team-shared targets wrong/unsorted: %v", ts)
	}
}

// A bare hydrate re-hydrates every env recorded under .ach/ with its recorded
// targets, best-effort: a failing env does not stop the next one.
func TestRunHydrateWorkspace_BestEffort(t *testing.T) {
	cwd := t.TempDir()
	seedState(t, cwd, "a", "opencode")
	seedState(t, cwd, "b", "claude-code")
	t.Cleanup(chdir(t, cwd))

	var seen []hydrate.Opts
	stubEngine(t, map[string]bool{"a": true}, &seen)

	err := runHydrateWorkspace(testCmd(t), hydrateInputs{output: cwd}, "https://hub.example", "pk_x")
	if err == nil {
		t.Fatal("want non-zero exit because env a failed, got nil")
	}
	if len(seen) != 2 || seen[0].Environment != "a" || seen[0].Platform != "opencode" ||
		seen[1].Environment != "b" || seen[1].Platform != "claude-code" {
		t.Fatalf("both envs should be attempted in order with their targets: %+v", seen)
	}
}

func TestRunHydrateWorkspace_NothingHydrated(t *testing.T) {
	cwd := t.TempDir()
	t.Cleanup(chdir(t, cwd))
	err := runHydrateWorkspace(testCmd(t), hydrateInputs{output: cwd}, "https://hub.example", "pk_x")
	if err == nil || !strings.Contains(err.Error(), "environment clone") {
		t.Fatalf("empty folder should point at environment clone, got %v", err)
	}
}

// A named hydrate without --target updates the targets the Environment is
// already hydrated for, in project scope and under --global; --target wins,
// and an Environment not hydrated there falls back to autodetection.
func TestResolvePlatforms_RecordedTargets(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	seedState(t, cwd, "proj", "opencode")
	seedState(t, cwd, "proj", "claude-code")
	seedState(t, cwd, "proj", "cursor") // no adapter: skipped
	// What `environment uninstall --only-runtime` leaves: no adapter section.
	if err := os.WriteFile(filepath.Join(cwd, ".ach", "proj", "state-pi.json"),
		[]byte(`{"schemaVersion":"3","environment":"proj"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	seedState(t, home, "glob", "codex")
	seedState(t, home, "glob", "pi")

	for _, tc := range []struct {
		name string
		in   hydrateInputs
		env  string
		want string
	}{
		{"project", hydrateInputs{output: cwd}, "proj", "claude-code,opencode,pi"},
		{"global", hydrateInputs{global: true}, "glob", "codex,pi"},
		{"--target wins", hydrateInputs{output: cwd, platform: []string{"pi"}}, "proj", "pi"},
		{"ACH_TARGET wins", hydrateInputs{output: cwd, envTarget: "codex"}, "proj", "codex"},
	} {
		got, err := resolvePlatformsOrAutodetect(tc.in, tc.env, &bytes.Buffer{})
		if err != nil || strings.Join(got, ",") != tc.want {
			t.Errorf("%s: got %v, %v; want %s", tc.name, got, err, tc.want)
		}
	}
	// Not hydrated in this scope: autodetect, which finds nothing in an empty folder.
	if got, err := resolvePlatformsOrAutodetect(hydrateInputs{output: cwd}, "glob", &bytes.Buffer{}); err == nil {
		t.Errorf("unrecorded Environment: want the autodetect error, got %v", got)
	}
}

// A bare `hydrate -g` updates the Environments recorded under ~/.ach, never
// the ones of the folder it runs in.
func TestRunHydrateWorkspace_Global(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	seedState(t, home, "glob", "codex")
	seedState(t, cwd, "proj", "claude-code")
	t.Cleanup(chdir(t, cwd))

	var seen []hydrate.Opts
	stubEngine(t, nil, &seen)
	if err := runHydrateWorkspace(testCmd(t), hydrateInputs{global: true}, "https://hub.example", "pk_x"); err != nil {
		t.Fatalf("runHydrateWorkspace -g: %v", err)
	}
	if len(seen) != 1 || seen[0].Environment != "glob" || seen[0].Platform != "codex" || !seen[0].Global {
		t.Fatalf("want only glob/codex hydrated globally, got %+v", seen)
	}
}

// The bare form ends with the same Models line as a named hydrate, once for
// every Environment it updated.
func TestRunHydrateWorkspace_ModelsLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedState(t, home, "one", "codex")
	seedState(t, home, "two", "claude-code")
	t.Cleanup(chdir(t, t.TempDir()))

	var seen []hydrate.Opts
	stubEngine(t, nil, &seen)
	run := func(models bool) string {
		t.Helper()
		cmd := testCmd(t)
		var out bytes.Buffer
		cmd.SetOut(&out)
		in := hydrateInputs{global: true, models: models}
		if err := runHydrateWorkspace(cmd, in, "https://hub.example", "pk-"+strings.Repeat("x", 64)); err != nil {
			t.Fatalf("runHydrateWorkspace: %v", err)
		}
		return out.String()
	}
	if got := run(true); !strings.Contains(got, ""+
		"      Models routed through ACH; choose one with codex -m <model> / claude --model <model>\n"+
		"             a.fast, one.smart, two.smart\n") {
		t.Fatalf("--models:\n%s", got)
	}
	if got := run(false); !strings.Contains(got, "      Models 3 available, not routed — add --models\n") {
		t.Fatalf("no --models:\n%s", got)
	}
}

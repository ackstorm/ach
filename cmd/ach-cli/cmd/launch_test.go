// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// fakeTool puts an executable `claude` alone on PATH that records its cwd and
// args into the returned file and exits 3.
func fakeTool(t *testing.T) string {
	t.Helper()
	bin, out := t.TempDir(), filepath.Join(t.TempDir(), "out")
	script := "#!/bin/sh\npwd > \"$LAUNCH_OUT\"\necho \"$@\" >> \"$LAUNCH_OUT\"\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("LAUNCH_OUT", out)
	return out
}

func TestLaunch_HydratesThenRunsTool(t *testing.T) {
	setupClone(t, false)
	out := fakeTool(t)
	dir := t.TempDir()

	_, stderr, code, _ := executeCommand(t, newLaunchCmd(),
		"claude", "--environment", "aws-rw", "--dir", dir, "--", "--resume", "x")
	if code != exit.Code(3) {
		t.Fatalf("exit code = %d, want the tool's 3", code)
	}
	envs, err := hydratedEnvs(dir)
	if err != nil || len(envs) != 1 || envs[0].targets[0] != "claude-code" {
		t.Fatalf("hydrated %+v, %v; want aws-rw for claude-code", envs, err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("tool did not run: %v", err)
	}
	if want := dir + "\n--resume x\n"; string(got) != want {
		t.Fatalf("tool saw %q, want %q", got, want)
	}
	if !strings.Contains(stderr, "Launching claude --resume x") {
		t.Fatalf("stderr missing Launching line:\n%s", stderr)
	}
}

// Nothing runs — neither hydrate nor the tool — when the tool cannot start
// or the environment is missing; a failed hydrate never starts the tool.
func TestLaunch_Refusals(t *testing.T) {
	cases := []struct {
		name, want string
		failEngine bool
		args       []string
	}{
		{"unknown tool", `unknown tool "cursor"`, false, []string{"cursor", "--environment", "e"}},
		{"not on PATH", `"codex" not found on PATH`, false, []string{"codex", "--environment", "e"}},
		{"no environment", "pass --environment", false, []string{"claude"}},
		{"hydrate fails", "boom", true, []string{"claude", "--environment", "e"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd := setupClone(t, tc.failEngine)
			out := fakeTool(t)
			t.Setenv("ACH_ENVIRONMENT", "")
			_, _, _, err := executeCommand(t, newLaunchCmd(), tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(out); err == nil {
				t.Fatal("tool ran")
			}
			if envs, _ := hydratedEnvs(cwd); len(envs) != 0 {
				t.Fatalf("hydrated %+v", envs)
			}
		})
	}
}

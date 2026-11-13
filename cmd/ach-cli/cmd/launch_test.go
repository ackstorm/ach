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
// args into the returned file, and its ANTHROPIC_* environment into
// <file>.env, and exits 3.
func fakeTool(t *testing.T) string {
	t.Helper()
	return fakeNamedTool(t, "claude")
}

func fakeNamedTool(t *testing.T, name string) string {
	t.Helper()
	bin, out := t.TempDir(), filepath.Join(t.TempDir(), "out")
	script := "#!/bin/sh\npwd > \"$LAUNCH_OUT\"\necho \"$@\" >> \"$LAUNCH_OUT\"\n" +
		"echo \"$ANTHROPIC_BASE_URL|$ANTHROPIC_CUSTOM_HEADERS\" > \"$LAUNCH_OUT.env\"\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("LAUNCH_OUT", out)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "")
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

// With no environment named, launch updates what the folder already holds
// for the launched tool, then starts it there.
func TestLaunch_NoEnvironmentUsesHydratedFolder(t *testing.T) {
	cwd := setupClone(t, false)
	out := fakeTool(t)
	t.Setenv("ACH_ENVIRONMENT", "")
	seedState(t, cwd, "aws-rw", "codex")

	_, _, code, _ := executeCommand(t, newLaunchCmd(), "claude")
	if code != exit.Code(3) {
		t.Fatalf("exit code = %d, want the tool's 3", code)
	}
	envs, err := hydratedEnvs(cwd)
	if err != nil || len(envs) != 1 || strings.Join(envs[0].targets, ",") != "claude-code,codex" {
		t.Fatalf("hydrated %+v, %v; want aws-rw for claude-code and codex", envs, err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("tool did not run: %v", err)
	}
	if want := cwd + "\n\n"; string(got) != want {
		t.Fatalf("tool saw %q, want %q", got, want)
	}
}

// Nothing runs — neither hydrate nor the tool — when the tool cannot start
// or the folder holds no environment; a failed hydrate never starts the tool.
func TestLaunch_Refusals(t *testing.T) {
	cases := []struct {
		name, want string
		failEngine bool
		args       []string
	}{
		{"unknown tool", `unknown tool "cursor"`, false, []string{"cursor", "--environment", "e"}},
		{"not on PATH", `"codex" not found on PATH`, false, []string{"codex", "--environment", "e"}},
		{"models for claude", "--models writes apiKeyHelper", false, []string{"claude", "--models", "--environment", "e"}},
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

// A folder with no Environment starts the tool as it is, without hydrating.
func TestLaunch_NoEnvironmentStartsTool(t *testing.T) {
	cwd := setupClone(t, false)
	out := fakeNamedTool(t, "pi")
	t.Setenv("ACH_ENVIRONMENT", "")

	_, stderr, code, _ := executeCommand(t, newLaunchCmd(), "pi")
	if code != exit.Code(3) {
		t.Fatalf("exit code = %d, want the tool's 3", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("tool did not run: %v", err)
	}
	if envs, _ := hydratedEnvs(cwd); len(envs) != 0 {
		t.Fatalf("hydrated %+v", envs)
	}
	if !strings.Contains(stderr, "starting without hydrating") || strings.Contains(stderr, "models via") {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

// claude starts behind the launch proxy: the shell's gateway variables are
// replaced (with a warning) by the proxy's local address.
func TestLaunch_ClaudeGoesThroughProxy(t *testing.T) {
	setupClone(t, false)
	out := fakeTool(t)
	t.Setenv("ACH_ENVIRONMENT", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.example")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "x-litellm-api-key: Bearer ek-static")

	_, stderr, code, _ := executeCommand(t, newLaunchCmd(), "claude")
	if code != exit.Code(3) {
		t.Fatalf("exit code = %d, want the tool's 3\n%s", code, stderr)
	}
	env, err := os.ReadFile(out + ".env")
	if err != nil {
		t.Fatalf("tool did not run: %v", err)
	}
	if !strings.HasPrefix(string(env), "http://127.0.0.1:") || !strings.HasSuffix(string(env), "|\n") {
		t.Fatalf("tool env = %q, want the proxy address and no custom headers", env)
	}
	for _, want := range []string{
		"ignoring ANTHROPIC_BASE_URL", "ignoring ANTHROPIC_CUSTOM_HEADERS", "Claude login kept",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// A Claude settings file that sets ANTHROPIC_BASE_URL would bypass the proxy:
// claude does not start.
func TestLaunch_ClaudeSettingsBaseURLStops(t *testing.T) {
	cwd := setupClone(t, false)
	out := fakeTool(t)
	t.Setenv("ACH_ENVIRONMENT", "")
	settings := filepath.Join(cwd, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := executeCommand(t, newLaunchCmd(), "claude")
	if err == nil || !strings.Contains(err.Error(), "settings.local.json sets env.ANTHROPIC_BASE_URL") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("tool ran")
	}
}

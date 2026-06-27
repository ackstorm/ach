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

	"github.com/ackstorm/ach/internal/cli/gitignore"
	"github.com/ackstorm/ach/internal/cli/hydrate"
)

// testCmd returns a minimal cobra.Command with stdout/stderr wired to
// buffers and a background context, so runHydrateManifest / runHydrateEngine
// can render their summaries without polluting the real process streams.
func testCmd(t *testing.T) *cobra.Command {
	t.Helper()
	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetContext(context.Background())
	return c
}

func writeManifest(t *testing.T, cwd, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cwd, "ach.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubEngine replaces hydrateRunFn for the test, recording each env it is
// asked to hydrate and failing the envs named in failFor.
func stubEngine(t *testing.T, failFor map[string]bool, seen *[]string) {
	t.Helper()
	prev := hydrateRunFn
	hydrateRunFn = func(_ context.Context, opts hydrate.Opts) (hydrate.Result, error) {
		*seen = append(*seen, opts.Environment)
		if failFor[opts.Environment] {
			return hydrate.Result{}, &os.PathError{Op: "hydrate", Path: opts.Environment, Err: os.ErrPermission}
		}
		return hydrate.Result{Environment: opts.Environment, PlatformID: opts.Platform}, nil
	}
	t.Cleanup(func() { hydrateRunFn = prev })
}

func TestRunHydrateManifest_BestEffort(t *testing.T) {
	cwd := t.TempDir()
	writeManifest(t, cwd, "version: 1\nenvironments:\n"+
		"  - name: a\n    targets: [claude-code]\n"+
		"  - name: b\n    targets: [claude-code]\n")
	restore := chdir(t, cwd)
	defer restore()

	var seen []string
	stubEngine(t, map[string]bool{"b": true}, &seen)

	in := hydrateInputs{output: cwd}
	err := runHydrateManifest(testCmd(t), in, "https://hub.example", "pk_x")
	if err == nil {
		t.Fatal("want non-zero exit because env b failed, got nil")
	}
	if len(seen) != 2 || seen[0] != "a" || seen[1] != "b" {
		t.Fatalf("both envs should be attempted in order: %v", seen)
	}
}

func TestRunHydrateManifest_AllOK(t *testing.T) {
	cwd := t.TempDir()
	writeManifest(t, cwd, "version: 1\nenvironments:\n  - name: a\n    targets: [claude-code]\n")
	restore := chdir(t, cwd)
	defer restore()

	var seen []string
	stubEngine(t, nil, &seen)

	if err := runHydrateManifest(testCmd(t), hydrateInputs{output: cwd}, "https://hub.example", "pk_x"); err != nil {
		t.Fatalf("all-ok should exit zero: %v", err)
	}
}

func TestRunHydrateManifest_NoManifest_RequiredArgError(t *testing.T) {
	cwd := t.TempDir()
	restore := chdir(t, cwd)
	defer restore()
	err := runHydrateManifest(testCmd(t), hydrateInputs{output: cwd}, "https://hub.example", "pk_x")
	if err == nil || !strings.Contains(err.Error(), "ach.yaml") {
		t.Fatalf("absent manifest should error mentioning ach.yaml, got %v", err)
	}
}

func TestGitignore_DoesNotIgnoreAchYaml(t *testing.T) {
	// ach.yaml is a committed file at the workspace root; the hydrate-managed
	// gitignore block (step12bGitignore) seeds only ".ach/" + the top-level dir
	// of every render-written file — ach.yaml is never a written target, so it
	// is never an entry. Drive gitignore.Ensure with that exact representative
	// seed and prove the managed block never ignores the committed manifest,
	// and that an on-disk ach.yaml is left untouched. Guards against a future
	// change that sweeps cwd files into the block.
	dir := t.TempDir()
	writeManifest(t, dir, "version: 1\nenvironments:\n  - name: a\n")
	entries := []string{".ach/", gitignore.TopLevelEntry(".claude/agents/x.md")}
	if _, err := gitignore.Ensure(dir, entries); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Contains(string(raw), "ach.yaml") {
		t.Fatalf("managed .gitignore must never ignore the committed ach.yaml:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "ach.yaml")); err != nil {
		t.Fatalf("ach.yaml clobbered: %v", err)
	}
}

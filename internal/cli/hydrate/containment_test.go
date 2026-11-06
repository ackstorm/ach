// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

const (
	containEnv      = "demo"
	containPlatform = "gemini-cli"
)

func hashStr(s string) string { return hash.HashBytes([]byte(s)) }

func mcpManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   containEnv,
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{{ID: "srv", Endpoint: "http://localhost:1/mcp/srv"}},
		},
		Context: &manifest.ContextBlock{},
	}
}

// containCommit is a production-wired commit for the Gemini target rooted at
// ws, with only the network fetch replaced.
func containCommit(t *testing.T, ws string, mutate func(*Opts)) (*commit, error) {
	t.Helper()
	opts := Opts{
		Environment: containEnv,
		Platform:    containPlatform,
		Output:      ws,
		Bearer:      "ek-fake",
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
	}
	if mutate != nil {
		mutate(&opts)
	}
	c, err := newCommit(opts)
	if err != nil {
		return nil, err
	}
	_, c.adapter = NewWiring(nil, containPlatform, extract.DefaultLimits(), false, opts.Force, opts.Global, conflict.Namespace)
	c.fetcher = func(context.Context, string) (*manifest.Manifest, error) { return mcpManifest(), nil }
	return c, nil
}

func containRun(t *testing.T, ws string, mutate func(*Opts)) error {
	t.Helper()
	c, err := containCommit(t, ws, mutate)
	if err != nil {
		return err
	}
	_, err = c.run(context.Background())
	return err
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSaveState(t *testing.T, ws string, f *state.File) {
	t.Helper()
	f.SchemaVersion, f.Environment = "3", containEnv
	p, err := state.ResolvePlatformPath(ws, containEnv, containPlatform, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(p, f); err != nil {
		t.Fatal(err)
	}
}

func assertBody(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s holds %v, want nothing written there", dir, entries)
	}
}

func TestRun_SymlinkedToolDirRefused(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	mustSymlink(t, outside, filepath.Join(ws, ".gemini"))

	err := containRun(t, ws, nil)
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("run = %v, want ErrNotContained", err)
	}
	assertEmptyDir(t, outside)
}

func TestRun_DryRunRefusesSymlinkedToolDir(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	mustSymlink(t, outside, filepath.Join(ws, ".gemini"))

	err := containRun(t, ws, func(o *Opts) { o.DryRun = true })
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("dry run = %v, want ErrNotContained", err)
	}
	assertEmptyDir(t, outside)
}

// TestRun_SymlinkStayingInsideWorkspaceAllowed: only a link leaving the
// workspace is refused.
func TestRun_SymlinkStayingInsideWorkspaceAllowed(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, "shared", filepath.Join(ws, ".gemini"))

	if err := containRun(t, ws, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "shared", "settings.json")); err != nil {
		t.Fatalf("settings.json not written through the inner link: %v", err)
	}
}

func TestRun_SymlinkedWorkspacePathsRefused(t *testing.T) {
	envDir := filepath.Join(".ach", containEnv)
	tests := []struct {
		name string
		link string
	}{
		{"ach root", ".ach"},
		{"env dir", envDir},
		{"lock", filepath.Join(envDir, "lock")},
		{"state file", filepath.Join(envDir, "state-"+containPlatform+".json")},
		{"artifact cache", filepath.Join(envDir, "artifact")},
		{"prompt cache", filepath.Join(envDir, "prompt")},
		{"runtime mirror", filepath.Join(envDir, "runtime-"+containPlatform)},
		{"gitignore", ".gitignore"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			mustWrite(t, filepath.Join(outside, "keep"), "x")
			mustSymlink(t, outside, filepath.Join(ws, tc.link))

			err := containRun(t, ws, nil)
			if !errors.Is(err, state.ErrNotContained) {
				t.Fatalf("run = %v, want ErrNotContained", err)
			}
			var ce *exit.CodedError
			if !errors.As(err, &ce) || ce.Code != exit.General {
				t.Fatalf("run = %#v, want exit.General", err)
			}
			entries, rerr := os.ReadDir(outside)
			if rerr != nil || len(entries) != 1 {
				t.Fatalf("outside changed: %v %v", entries, rerr)
			}
			assertBody(t, filepath.Join(outside, "keep"), "x")
		})
	}
}

// TestRun_DanglingLockLinkCreatesNothing: opening the lock would create the
// link's target.
func TestRun_DanglingLockLinkCreatesNothing(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	mustSymlink(t, filepath.Join(outside, "created"), filepath.Join(ws, ".ach", containEnv, "lock"))

	if err := containRun(t, ws, nil); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("run = %v, want ErrNotContained", err)
	}
	assertEmptyDir(t, outside)
}

func TestRun_LegacyStateNotMigratedThroughSymlinkedEnvDir(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(outside, "state-pimono.json"), "x")
	if err := os.Mkdir(filepath.Join(ws, ".ach"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(ws, ".ach", containEnv))

	_, err := containCommit(t, ws, func(o *Opts) { o.Platform = "pi" })
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("newCommit = %v, want ErrNotContained", err)
	}
	assertBody(t, filepath.Join(outside, "state-pimono.json"), "x")
}

func TestRun_TmpSymlinkLeavesTargetIntact(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		ws, outside := t.TempDir(), t.TempDir()
		mustWrite(t, filepath.Join(outside, "keep"), "x")
		tmp := filepath.Join(ws, ".ach", containEnv, "tmp")
		mustSymlink(t, outside, tmp)

		if err := containRun(t, ws, func(o *Opts) { o.DryRun = dryRun }); err != nil {
			t.Fatalf("run (dry=%v): %v", dryRun, err)
		}
		assertBody(t, filepath.Join(outside, "keep"), "x")
		if info, err := os.Lstat(tmp); err == nil && info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("tmp link survived (dry=%v)", dryRun)
		}
	}
}

func TestRun_UnsafeStateTargetsRefused(t *testing.T) {
	tests := []struct {
		name   string
		target func(victim string) string
		hash   func(body string) string
		force  bool
	}{
		{name: "absolute", target: func(v string) string { return v }, hash: hashStr},
		{name: "dotdot", target: func(v string) string { return filepath.Join("..", filepath.Base(filepath.Dir(v)), "victim") }, hash: hashStr},
		{name: "empty hash", target: func(v string) string { return v }, hash: func(string) string { return "" }},
		{name: "force", target: func(v string) string { return v }, hash: hashStr, force: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			victim := filepath.Join(outside, "victim")
			mustWrite(t, victim, "x")
			mustSaveState(t, ws, &state.File{
				Plugins: []state.FileEntry{{Target: tc.target(victim), Hash: tc.hash("x"), Merge: "replace"}},
			})

			err := containRun(t, ws, func(o *Opts) { o.Sync, o.Force = true, tc.force })
			if !errors.Is(err, state.ErrNotContained) {
				t.Fatalf("run = %v, want ErrNotContained", err)
			}
			assertBody(t, victim, "x")
		})
	}
}

// relativeWorkspace returns "repo" as the workspace, the working directory
// being its parent, plus a directory outside it.
func relativeWorkspace(t *testing.T) (ws, abs, outside string) {
	t.Helper()
	parent := t.TempDir()
	abs = filepath.Join(parent, "repo")
	if err := os.Mkdir(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)
	return "repo", abs, t.TempDir()
}

func TestRun_RelativeDir_SymlinksRefused(t *testing.T) {
	envDir := filepath.Join(".ach", containEnv)
	for _, link := range []string{
		".ach",
		envDir,
		filepath.Join(envDir, "lock"),
		filepath.Join(envDir, "artifact"),
		".gitignore",
		".gemini",
	} {
		t.Run(link, func(t *testing.T) {
			ws, abs, outside := relativeWorkspace(t)
			mustSymlink(t, outside, filepath.Join(abs, link))

			if err := containRun(t, ws, nil); !errors.Is(err, state.ErrNotContained) {
				t.Fatalf("run = %v, want ErrNotContained", err)
			}
			assertEmptyDir(t, outside)
		})
	}
}

func TestRun_RelativeDir_DanglingLockCreatesNothing(t *testing.T) {
	ws, abs, outside := relativeWorkspace(t)
	mustSymlink(t, filepath.Join(outside, "created"), filepath.Join(abs, ".ach", containEnv, "lock"))

	if err := containRun(t, ws, nil); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("run = %v, want ErrNotContained", err)
	}
	assertEmptyDir(t, outside)
}

func TestRun_RelativeDir_TmpSymlinkLeavesTargetIntact(t *testing.T) {
	ws, abs, outside := relativeWorkspace(t)
	mustWrite(t, filepath.Join(outside, "keep"), "x")
	tmp := filepath.Join(abs, ".ach", containEnv, "tmp")
	mustSymlink(t, outside, tmp)

	if err := containRun(t, ws, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertBody(t, filepath.Join(outside, "keep"), "x")
	if info, err := os.Lstat(tmp); err == nil && info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("tmp link survived")
	}
}

func TestRun_RelativeDir_SyncRefusesSymlinkedParent(t *testing.T) {
	ws, abs, outside := relativeWorkspace(t)
	mustWrite(t, filepath.Join(outside, "x.md"), "x")
	mustSymlink(t, outside, filepath.Join(abs, ".claude"))
	mustSaveState(t, abs, &state.File{
		Plugins: []state.FileEntry{{Target: ".claude/x.md", Hash: hashStr("x"), Merge: "replace"}},
	})

	err := containRun(t, ws, func(o *Opts) { o.Sync = true })
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("run = %v, want ErrNotContained", err)
	}
	assertBody(t, filepath.Join(outside, "x.md"), "x")
}

func TestRun_RelativeDir_UnsafeStateTargetRefused(t *testing.T) {
	ws, abs, outside := relativeWorkspace(t)
	victim := filepath.Join(outside, "victim")
	mustWrite(t, victim, "x")
	mustSaveState(t, abs, &state.File{
		Plugins: []state.FileEntry{{Target: victim, Hash: hashStr("x"), Merge: "replace"}},
	})

	err := containRun(t, ws, func(o *Opts) { o.Sync = true })
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("run = %v, want ErrNotContained", err)
	}
	assertBody(t, victim, "x")
}

// TestRun_RelativeDir_SoundWorkspaceHydrates: the checks do not refuse a
// relative workspace with nothing wrong in it, including a repeat run.
func TestRun_RelativeDir_SoundWorkspaceHydrates(t *testing.T) {
	ws, abs, _ := relativeWorkspace(t)
	for run := range 2 {
		if err := containRun(t, ws, func(o *Opts) { o.Sync = true }); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if _, err := os.Stat(filepath.Join(abs, ".gemini", "settings.json")); err != nil {
		t.Fatalf("settings.json not in the workspace: %v", err)
	}
}

// TestRun_Global_SymlinkedDirStillWorks: dotfile managers link ~/.gemini.
func TestRun_Global_SymlinkedDirStillWorks(t *testing.T) {
	home, dotfiles := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_CLI_HOME", "")
	mustSymlink(t, dotfiles, filepath.Join(home, ".gemini"))
	global := func(o *Opts) { o.Global, o.Output, o.Sync = true, "", true }

	for run := range 2 {
		if err := containRun(t, "", global); err != nil {
			t.Fatalf("global run %d: %v", run, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dotfiles, "settings.json")); err != nil {
		t.Fatalf("settings.json not in the linked dir: %v", err)
	}
}

func TestRun_Global_StateTargets(t *testing.T) {
	home, cfg, outside := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_CLI_HOME", cfg)
	statePath, err := state.ResolvePlatformPath("", containEnv, containPlatform, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim")
	mustWrite(t, victim, "x")
	global := func(o *Opts) { o.Global, o.Output, o.Sync = true, "", true }
	save := func(target string) {
		t.Helper()
		f := &state.File{SchemaVersion: "3", Environment: containEnv,
			Plugins: []state.FileEntry{{Target: target, Merge: "replace"}}}
		if err := state.Save(statePath, f); err != nil {
			t.Fatal(err)
		}
	}

	save(victim)
	if err := containRun(t, "", global); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("absolute outside the config root: %v, want ErrNotContained", err)
	}
	assertBody(t, victim, "x")

	save(filepath.Join("..", filepath.Base(outside), "victim"))
	if err := containRun(t, "", global); !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("dotdot: %v, want ErrNotContained", err)
	}
	assertBody(t, victim, "x")

	redirected := filepath.Join(cfg, ".gemini", "skills", "a.md")
	mustWrite(t, redirected, "x")
	save(redirected)
	if err := containRun(t, "", global); err != nil {
		t.Fatalf("absolute under the config root: %v", err)
	}
}

func TestSync_SymlinkedParentRefusedBeforeAnyDelete(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(outside, "x.md"), "x")
	mustSymlink(t, outside, filepath.Join(ws, ".gemini"))
	mustWrite(t, filepath.Join(ws, ".claude", "own.md"), "o")
	prev := &state.File{Plugins: []state.FileEntry{
		{Target: ".claude/own.md", Hash: hashStr("o"), Merge: "replace"},
		{Target: ".gemini/x.md", Hash: hashStr("x"), Merge: "replace"},
	}}
	achDir := filepath.Join(ws, ".ach", containEnv)

	_, err := Sync(prev, &state.File{}, achDir, ws, SyncOptions{})
	if !errors.Is(err, state.ErrNotContained) {
		t.Fatalf("Sync = %v, want ErrNotContained", err)
	}
	assertBody(t, filepath.Join(outside, "x.md"), "x")
	assertBody(t, filepath.Join(ws, ".claude", "own.md"), "o")

	if _, err := Sync(prev, &state.File{}, achDir, ws, SyncOptions{Global: true}); err != nil {
		t.Fatalf("Sync under --global: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "x.md")); !os.IsNotExist(err) {
		t.Fatalf("--global follows the link it was told to: %v", err)
	}
}

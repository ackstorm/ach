// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestCheckContained(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, "real", filepath.Join(root, "inside"))
	symlink(t, filepath.Join(root, "real"), filepath.Join(root, "inside-abs"))
	symlink(t, "inside", filepath.Join(root, "chain"))
	symlink(t, "missing/leaf", filepath.Join(root, "dangling-inside"))
	symlink(t, outside, filepath.Join(root, "out-abs"))
	symlink(t, filepath.Join("..", filepath.Base(outside)), filepath.Join(root, "out-rel"))
	symlink(t, filepath.Join(outside, "not-yet"), filepath.Join(root, "dangling-out"))
	symlink(t, "out-abs", filepath.Join(root, "chain-out"))
	symlink(t, "../..", filepath.Join(root, "real", "sub", "up"))
	symlink(t, "loop-b", filepath.Join(root, "loop-a"))
	symlink(t, "loop-a", filepath.Join(root, "loop-b"))

	tests := []struct {
		name string
		path string
		want error
	}{
		{"root itself", root, nil},
		{"missing leaf", filepath.Join(root, "real", "nope"), nil},
		{"missing chain", filepath.Join(root, "a", "b", "c"), nil},
		{"relative path is not joined to root", filepath.Join("real", "sub"), state.ErrNotContained},
		{"below a regular file", filepath.Join(root, "file", "x"), nil},
		{"inside link", filepath.Join(root, "inside", "sub", "x"), nil},
		{"inside absolute link", filepath.Join(root, "inside-abs", "x"), nil},
		{"inside link chain", filepath.Join(root, "chain", "x"), nil},
		{"dangling link inside", filepath.Join(root, "dangling-inside"), nil},
		{"link back to root", filepath.Join(root, "real", "sub", "up", "file"), nil},
		{"outside absolute link", filepath.Join(root, "out-abs", "x"), state.ErrNotContained},
		{"outside relative link", filepath.Join(root, "out-rel", "x"), state.ErrNotContained},
		{"outside link as leaf", filepath.Join(root, "out-abs"), state.ErrNotContained},
		{"dangling link outside", filepath.Join(root, "dangling-out"), state.ErrNotContained},
		{"link chain outside", filepath.Join(root, "chain-out", "x"), state.ErrNotContained},
		{"link loop", filepath.Join(root, "loop-a", "x"), state.ErrNotContained},
		{"dotdot", filepath.Join(root, "..", "x"), state.ErrNotContained},
		{"relative dotdot", "../x", state.ErrNotContained},
		{"absolute elsewhere", filepath.Join(outside, "x"), state.ErrNotContained},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := state.CheckContained(root, tc.path)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckContained(%q) = %v, want %v", tc.path, err, tc.want)
			}
		})
	}
}

// TestCheckContained_SymlinkedRoot: the root is the user's own choice, so a
// link in the path leading to it is not an escape.
func TestCheckContained_SymlinkedRoot(t *testing.T) {
	actual := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(actual, "a"), filepath.Join(actual, "inside"))
	if err := state.CheckContained(alias, filepath.Join(alias, "inside", "x")); err != nil {
		t.Fatalf("CheckContained through a symlinked root: %v", err)
	}
}

// TestCheckContained_RelativeRoot: --dir may be relative, and then root and
// path are both relative to the working directory, already joined.
func TestCheckContained_RelativeRoot(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "repo", "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, outside, filepath.Join(parent, "repo", ".claude"))
	symlink(t, filepath.Join(outside, "not-yet"), filepath.Join(parent, "repo", "lock"))
	symlink(t, "real", filepath.Join(parent, "repo", "inside"))
	t.Chdir(filepath.Join(parent, "repo", "real"))

	tests := []struct {
		name string
		root string
		path string
		want error
	}{
		{"missing leaf", "../../repo", "../../repo/.ach/x/lock", nil},
		{"root itself", "..", "..", nil},
		{"inside link", "..", "../inside/x", nil},
		{"dotdot root, outside link", "..", "../.claude/settings.json", state.ErrNotContained},
		{"named root, outside link", "../../repo", "../../repo/.claude/settings.json", state.ErrNotContained},
		{"named root, dangling link", "../../repo", "../../repo/lock", state.ErrNotContained},
		{"dotdot root, dotdot path", "..", "../../x", state.ErrNotContained},
		{"absolute path, outside link", "..", filepath.Join(parent, "repo", ".claude", "x"), state.ErrNotContained},
		{"absolute root, relative path", filepath.Join(parent, "repo"), "../.claude/x", state.ErrNotContained},
		{"absolute elsewhere", "..", filepath.Join(outside, "x"), state.ErrNotContained},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := state.CheckContained(tc.root, tc.path); !errors.Is(err, tc.want) {
				t.Fatalf("CheckContained(%q, %q) = %v, want %v", tc.root, tc.path, err, tc.want)
			}
		})
	}

	t.Run("plain name", func(t *testing.T) {
		t.Chdir(parent)
		if err := state.CheckContained("repo", "repo/.claude/x"); !errors.Is(err, state.ErrNotContained) {
			t.Fatalf("outside link under a named root = %v, want ErrNotContained", err)
		}
		if err := state.CheckContained("repo", "repo/.ach/x"); err != nil {
			t.Fatalf("missing leaf under a named root: %v", err)
		}
	})
	t.Run("dot", func(t *testing.T) {
		t.Chdir(filepath.Join(parent, "repo"))
		if err := state.CheckContained(".", ".claude/x"); !errors.Is(err, state.ErrNotContained) {
			t.Fatalf("outside link under . = %v, want ErrNotContained", err)
		}
	})
}

func TestCheckContained_MissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	if err := state.CheckContained(root, filepath.Join(root, ".ach", "x")); err != nil {
		t.Fatalf("CheckContained under a missing root: %v", err)
	}
}

func TestValidateTargets(t *testing.T) {
	cfg := t.TempDir()
	tests := []struct {
		name     string
		f        *state.File
		absRoots []string
		want     error
	}{
		{name: "nil file", f: nil},
		{name: "relative", f: &state.File{Plugins: []state.FileEntry{{Target: ".claude/a.md"}}}},
		{name: "inner dotdot stays inside", f: &state.File{Skills: []state.FileEntry{{Target: ".claude/x/../a.md"}}}},
		{
			name: "absolute",
			f:    &state.File{Plugins: []state.FileEntry{{Target: "/etc/x", Hash: "h"}}},
			want: state.ErrNotContained,
		},
		{
			name: "dotdot",
			f:    &state.File{Adapter: state.AdapterSection{Files: []state.FileEntry{{Target: "../x", Hash: "h"}}}},
			want: state.ErrNotContained,
		},
		{
			name: "empty hash has no exemption",
			f:    &state.File{RuntimeFiles: []state.FileEntry{{Target: "a/../../x"}}},
			want: state.ErrNotContained,
		},
		{
			name: "every bucket is walked",
			f:    &state.File{Prompts: []state.FileEntry{{Target: "/x"}}},
			want: state.ErrNotContained,
		},
		{
			name: "empty target",
			f:    &state.File{Artifacts: []state.FileEntry{{}}},
			want: state.ErrNotContained,
		},
		{
			name:     "absolute under an allowed root",
			f:        &state.File{Skills: []state.FileEntry{{Target: filepath.Join(cfg, "skills", "a.md")}}},
			absRoots: []string{cfg},
		},
		{
			name:     "absolute outside the allowed roots",
			f:        &state.File{Skills: []state.FileEntry{{Target: filepath.Join(cfg, "..", "x")}}},
			absRoots: []string{cfg},
			want:     state.ErrNotContained,
		},
		{
			name:     "a relative allowed root admits no absolute target",
			f:        &state.File{Skills: []state.FileEntry{{Target: filepath.Join(cfg, "a.md")}}},
			absRoots: []string{"relative-home"},
			want:     state.ErrNotContained,
		},
		{
			name:     "an allowed root still refuses dotdot",
			f:        &state.File{Skills: []state.FileEntry{{Target: "../x"}}},
			absRoots: []string{cfg},
			want:     state.ErrNotContained,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := state.ValidateTargets(tc.f, tc.absRoots...); !errors.Is(err, tc.want) {
				t.Fatalf("ValidateTargets = %v, want %v", err, tc.want)
			}
		})
	}
}

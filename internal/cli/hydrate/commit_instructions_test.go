// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// instructionExtractor stages each requested Instruction as
// <dest>/instruction/<name>/README.md.
type instructionExtractor map[string]string

func (e instructionExtractor) ExtractContent(_ context.Context, ref manifest.ContentRef, dest string, _ *state.File) (ExtractResult, error) {
	dir := filepath.Join(dest, "instruction", ref.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ExtractResult{}, err
	}
	return ExtractResult{}, os.WriteFile(filepath.Join(dir, "README.md"), []byte(e[ref.Name]), 0o644)
}

// instructionCommit builds a commit for platform whose manifest lists the
// Instructions in bodies, rendered by the real dispatcher.
func instructionCommit(t *testing.T, platform, toolRoot string, bodies map[string]string) (*commit, *fakeStateStore) {
	t.Helper()
	c, store, _ := newTestCommit(t)
	c.opts.Platform = platform
	c.toolRoot = toolRoot
	c.extractor = instructionExtractor(bodies)
	c.adapter = &adapterDispatcherImpl{platformID: platform, conflict: conflict.Namespace}
	refs := make([]manifest.ContentRef, 0, len(bodies))
	for n := range bodies {
		refs = append(refs, manifest.ContentRef{Name: n, DownloadURL: "http://local/content/instruction/" + n})
	}
	c.fetcher = func(context.Context, string) (*manifest.Manifest, error) {
		return &manifest.Manifest{SchemaVersion: "v1alpha1", Environment: "demo", Runtime: &manifest.RuntimeBlock{},
			Context: &manifest.ContextBlock{Instructions: refs}}, nil
	}
	return c, store
}

func TestCommit_Instructions_PrunedWithoutSync(t *testing.T) {
	root := t.TempDir()
	c, store := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rule := filepath.Join(root, ".claude/rules/ach-a.md")
	if readFile(t, rule) != "A\n" {
		t.Fatal("rule not written")
	}

	saved := store.savedFile
	c2, store2 := instructionCommit(t, "claude-code", root, map[string]string{})
	store2.loadFn = func(string) (*state.File, error) { return saved, nil }
	if _, err := c2.run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if exists(rule) {
		t.Error("an Instruction that left the Environment must be pruned without --sync")
	}
	if len(store2.savedFile.Instructions) != 0 {
		t.Errorf("state.Instructions = %+v", store2.savedFile.Instructions)
	}
}

func TestCommit_Instructions_MissingFileRewritten(t *testing.T) {
	root := t.TempDir()
	c, store := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
	if _, err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(root, ".claude/rules/ach-a.md")
	if err := os.Remove(rule); err != nil {
		t.Fatal(err)
	}
	saved := store.savedFile
	c2, store2 := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
	store2.loadFn = func(string) (*state.File, error) { return saved, nil }
	if _, err := c2.run(context.Background()); err != nil {
		t.Fatalf("re-hydrate: %v", err)
	}
	if readFile(t, rule) != "A\n" {
		t.Error("deleted rule not rewritten")
	}
}

func TestCommit_Instructions_SharedFolder(t *testing.T) {
	root := t.TempDir()
	first, _ := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
	if _, err := first.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	same, _ := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
	if _, err := same.run(context.Background()); err != nil {
		t.Fatalf("identical text must be adopted: %v", err)
	}
	other, _ := instructionCommit(t, "claude-code", root, map[string]string{"a": "different\n"})
	_, err := other.run(context.Background())
	var ce *exit.CodedError
	if !errors.As(err, &ce) || ce.Code != exit.CollisionRefuse {
		t.Fatalf("different text: err = %v, want CollisionRefuse", err)
	}
}

// Uninstalling one of two Environments sharing an OpenCode folder keeps the
// loader while a sibling's file remains and drops it with the last one.
func TestCommit_Instructions_OpencodeLoaderLifecycle(t *testing.T) {
	root := t.TempDir()
	envA, storeA := instructionCommit(t, "opencode", root, map[string]string{"a": "A\n"})
	if _, err := envA.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	envB, storeB := instructionCommit(t, "opencode", root, map[string]string{"b": "B\n"})
	if _, err := envB.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".opencode/opencode.json")
	if !strings.Contains(readFile(t, cfg), ".opencode/ach/*.md") {
		t.Fatal("loader entry missing")
	}

	uninstall := func(f *state.File) {
		t.Helper()
		if _, err := Sync(f, BuildScopedEmpty(f, true, false), t.TempDir(), root, SyncOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := TidyInstructionLoader("opencode", root, false); err != nil {
			t.Fatal(err)
		}
	}
	uninstall(storeA.savedFile)
	if exists(filepath.Join(root, ".opencode/ach/a.md")) || !exists(filepath.Join(root, ".opencode/ach/b.md")) {
		t.Fatal("only the first Environment's file must go")
	}
	if !strings.Contains(readFile(t, cfg), ".opencode/ach/*.md") {
		t.Fatal("loader entry must stay while a sibling's file remains")
	}
	uninstall(storeB.savedFile)
	if exists(cfg) {
		t.Errorf("opencode.json should be deleted: %s", readFile(t, cfg))
	}
}

// A sibling Environment of the same folder and target that still records an
// Instruction keeps its file when this Environment drops it.
func TestCommit_Instructions_SiblingFileKept(t *testing.T) {
	root := t.TempDir()
	for _, sync := range []bool{false, true} {
		root := filepath.Join(root, map[bool]string{false: "plain", true: "sync"}[sync])
		a, storeA := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
		a.achDir = filepath.Join(root, ".ach", "envA")
		if _, err := a.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := state.Save(filepath.Join(root, ".ach", "envA", "state-claude-code.json"), storeA.savedFile); err != nil {
			t.Fatal(err)
		}
		b, storeB := instructionCommit(t, "claude-code", root, map[string]string{"a": "A\n"})
		b.achDir = filepath.Join(root, ".ach", "envB")
		if _, err := b.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		saved := storeB.savedFile
		b2, storeB2 := instructionCommit(t, "claude-code", root, map[string]string{})
		b2.achDir = filepath.Join(root, ".ach", "envB")
		b2.opts.Sync = sync
		storeB2.loadFn = func(string) (*state.File, error) { return saved, nil }
		if _, err := b2.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !exists(filepath.Join(root, ".claude/rules/ach-a.md")) {
			t.Errorf("sync=%v: a sibling's file was deleted", sync)
		}
		if len(storeB2.savedFile.Instructions) != 0 {
			t.Errorf("sync=%v: B state still lists %+v", sync, storeB2.savedFile.Instructions)
		}
	}
}

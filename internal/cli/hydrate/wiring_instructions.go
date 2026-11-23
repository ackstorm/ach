// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/merge"
	"github.com/ackstorm/ach/internal/cli/state"
)

// projectInstructions writes every extracted Instruction for the target tool,
// then makes sure the tool's loader exists. Each Instruction extracts to
// <stage>/instruction/<name>/<one file>.
func (d *adapterDispatcherImpl) projectInstructions(ad adapter.Adapter, s *state.File, stageRoot, toolRoot string, result *RenderResult) error {
	root := filepath.Join(stageRoot, "instruction")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("adapter %s read instruction root: %w", d.platformID, err)
	}
	it, ok := ad.(adapter.InstructionTarget)
	if !ok {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, ent := range entries {
		name := ent.Name()
		if verr := validatePluginName(name); verr != nil {
			return fmt.Errorf("adapter %s instruction %q: %w", d.platformID, name, verr)
		}
		body, err := readSingleFile(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("adapter %s instruction %s: %w", d.platformID, name, err)
		}
		for _, fw := range it.InstructionFiles(name, body, d.global) {
			if d.global {
				fw.Path = adapter.RemapGlobalPath(d.platformID, toolRoot, fw.Path)
			}
			var prior *state.FileEntry
			if s != nil {
				prior = findEntry(s.Instructions, fw.Path)
			}
			entry, err := d.publishFile(fw, prior, toolRoot)
			if err != nil {
				return err
			}
			entry.Source = name
			result.ProjectedInstructionFiles = append(result.ProjectedInstructionFiles, entry)
		}
	}
	if l := it.InstructionLoader(d.global); l != nil && !d.dryRun {
		written, err := ensureLoader(d.platformID, toolRoot, d.global, *l)
		result.InstructionLoaderWritten = written
		return err
	}
	return nil
}

// readSingleFile returns the content of the only regular file in dir.
func readSingleFile(dir string) ([]byte, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var path string
	for _, f := range files {
		if f.Type().IsRegular() {
			if path != "" {
				return nil, fmt.Errorf("more than one file in %s", dir)
			}
			path = filepath.Join(dir, f.Name())
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no file in %s", dir)
	}
	return os.ReadFile(path) // #nosec G304 — the one file ach-cli extracted into its own stage
}

// resolveLoader remaps the loader's paths for --global and resolves them
// against toolRoot, refusing a project path that leaves the workspace.
func resolveLoader(platformID, toolRoot string, global bool, l adapter.InstructionLoader) (dir, path string, err error) {
	dirRel, pathRel := l.Dir+"/", l.Path
	if global {
		dirRel = adapter.RemapGlobalPath(platformID, toolRoot, dirRel)
		pathRel = adapter.RemapGlobalPath(platformID, toolRoot, pathRel)
	}
	dir, path = adapter.ResolveDest(toolRoot, dirRel), adapter.ResolveDest(toolRoot, pathRel)
	if !global {
		for _, p := range []string{dir, path} {
			if err := state.CheckContained(toolRoot, p); err != nil {
				return "", "", err
			}
		}
	}
	return dir, path, nil
}

// loaderEntry is the array entry for dir. It is absolute because OpenCode
// searches a relative one only up to the git root, missing an Environment
// folder above the project.
func loaderEntry(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(abs, "*.md")), nil
}

// hookCommand prints every *.md of the absolute dir abs. Codex approves a
// hook by a hash of its command, so the command must not change while the
// folder stays put.
func hookCommand(abs string) string {
	return "cat '" + strings.ReplaceAll(abs, "'", `'\''`) + "'/*.md"
}

// loaderEdit returns the JSON edits that add and remove l's entry for dir,
// and a text found in the file while that entry is there; "" means the file
// must be parsed to tell. A hook command is JSON-escaped in the file, so it
// has no such text, and hooks.json is plain JSON that parses safely.
func loaderEdit(l adapter.InstructionLoader, dir string) (ensure, remove func(map[string]any) (bool, error), marker string, err error) {
	if l.HookEvent != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, nil, "", err
		}
		cmd := hookCommand(abs)
		handler := map[string]any{"type": "command", "command": cmd, "additionalContextLimit": l.HookContextLimit}
		return func(doc map[string]any) (bool, error) { return merge.EnsureHookCommand(doc, l.HookEvent, handler) },
			func(doc map[string]any) (bool, error) { return merge.RemoveHookCommand(doc, l.HookEvent, cmd) },
			"", nil
	}
	entry, err := loaderEntry(dir)
	if err != nil {
		return nil, nil, "", err
	}
	return func(doc map[string]any) (bool, error) { return merge.EnsureArrayEntry(doc, l.ArrayKey, entry) },
		func(doc map[string]any) (bool, error) { return merge.RemoveArrayEntry(doc, l.ArrayKey, entry) },
		entry, nil
}

// ensureLoader creates or completes the loader and reports whether it wrote.
func ensureLoader(platformID, toolRoot string, global bool, l adapter.InstructionLoader) (bool, error) {
	dir, path, err := resolveLoader(platformID, toolRoot, global, l)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}
	if l.ArrayKey == "" && l.HookEvent == "" {
		cur, err := os.ReadFile(path) // #nosec G304 — a tool config file ach-cli manages for the user
		if err == nil && bytes.Equal(cur, l.Content) {
			return false, nil
		}
		return true, state.WriteAtomic(path, l.Content, 0o644)
	}
	ensure, _, _, err := loaderEdit(l, dir)
	if err != nil {
		return false, err
	}
	written := false
	err = state.RewriteFile(path, 0o600, func(cur []byte) ([]byte, bool, error) {
		doc, err := merge.ParseDoc(cur, false)
		if err != nil {
			return nil, false, err
		}
		changed, err := ensure(doc)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		if !changed {
			return nil, false, nil
		}
		written = true
		out, err := merge.EncodeDoc(doc, false)
		return out, true, err
	})
	return written && err == nil, err
}

// WithoutSiblingInstructions returns f without the instruction rows a sibling
// Environment of the same folder and platform also records, so pruning never
// deletes a file the sibling still needs.
func WithoutSiblingInstructions(f *state.File, achDir, platformID string) *state.File {
	foreign := state.ForeignInstructionTargets(achDir, platformID)
	if f == nil || len(foreign) == 0 {
		return f
	}
	out := *f
	out.Instructions = nil
	for _, e := range f.Instructions {
		if !foreign[e.Target] {
			out.Instructions = append(out.Instructions, e)
		}
	}
	return &out
}

// TidyInstructionLoader removes the target's loader when its instruction
// folder holds no *.md. It runs after pruning, on hydrate and uninstall; a
// sibling Environment's file keeps the loader in place.
func TidyInstructionLoader(platformID, toolRoot string, global bool) error {
	ad, ok := adapter.Lookup(platformID)
	if !ok {
		return nil
	}
	it, ok := ad.(adapter.InstructionTarget)
	if !ok {
		return nil
	}
	l := it.InstructionLoader(global)
	if l == nil {
		return nil
	}
	dir, path, err := resolveLoader(platformID, toolRoot, global, *l)
	if err != nil {
		return err
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*.md")); len(names) > 0 {
		return nil
	}
	if l.ArrayKey == "" && l.HookEvent == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	_, remove, marker, err := loaderEdit(*l, dir)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path) // #nosec G304 — a tool config file ach-cli manages for the user
	if errors.Is(err, os.ErrNotExist) || (err == nil && marker != "" && !bytes.Contains(raw, []byte(marker))) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	empty := false
	err = state.RewriteFile(path, 0o600, func(cur []byte) ([]byte, bool, error) {
		doc, err := merge.ParseDoc(cur, false)
		if err != nil {
			return nil, false, err
		}
		changed, err := remove(doc)
		if err != nil || !changed {
			return nil, false, err
		}
		empty = len(doc) == 0
		out, err := merge.EncodeDoc(doc, false)
		return out, true, err
	})
	if err == nil && empty {
		err = os.Remove(path)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

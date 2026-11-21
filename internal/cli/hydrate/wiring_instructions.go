// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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
		result.InstructionNotes = append(result.InstructionNotes,
			fmt.Sprintf("%s does not load Environment instructions", d.platformID))
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	wrote := false
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
			wrote = true
		}
	}
	if !wrote {
		result.InstructionWarnings = append(result.InstructionWarnings,
			fmt.Sprintf("%s has no global instructions file; instructions are delivered only in a project", d.platformID))
		return nil
	}
	if l := it.InstructionLoader(d.global); l != nil && !d.dryRun {
		return ensureLoader(d.platformID, toolRoot, d.global, *l)
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
	return os.ReadFile(path)
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

func ensureLoader(platformID, toolRoot string, global bool, l adapter.InstructionLoader) error {
	_, path, err := resolveLoader(platformID, toolRoot, global, l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if l.ArrayKey == "" {
		cur, err := os.ReadFile(path)
		if err == nil && bytes.Equal(cur, l.Content) {
			return nil
		}
		return state.WriteAtomic(path, l.Content, 0o644)
	}
	return state.RewriteFile(path, 0o600, func(cur []byte) ([]byte, bool, error) {
		doc, err := merge.ParseDoc(cur, false)
		if err != nil {
			return nil, false, err
		}
		changed, err := merge.EnsureArrayEntry(doc, l.ArrayKey, l.Entry)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		if !changed {
			return nil, false, nil
		}
		out, err := merge.EncodeDoc(doc, false)
		return out, true, err
	})
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
	if l.ArrayKey == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	empty := false
	err = state.RewriteFile(path, 0o600, func(cur []byte) ([]byte, bool, error) {
		doc, err := merge.ParseDoc(cur, false)
		if err != nil {
			return nil, false, err
		}
		changed, err := merge.RemoveArrayEntry(doc, l.ArrayKey, l.Entry)
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
	return err
}

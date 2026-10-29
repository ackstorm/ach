// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/state"
)

// Op is what a hydrate/uninstall did to one item, git-style.
type Op byte

const (
	OpAdd    Op = '+'
	OpUpdate Op = '~'
	OpRemove Op = '-'
)

// ItemChange is one user-facing item (plugin, skill, mcp server…) that a run
// added, updated or removed. Files is the item's file count (0 when unknown).
type ItemChange struct {
	Op         Op
	Kind, Name string
	Files      int
}

// StateItem is one user-facing item recorded in state, with its files' hashes.
type StateItem struct {
	Kind, Name string
	Files      map[string]string // target → hash
}

// Runtime item kinds, as runtimeKindForKey names them.
const (
	kindMCP = "mcp"
	kindA2A = "a2a"
)

var kindRank = map[string]int{kindMCP: 0, kindA2A: 1, kindPlugin: 2, kindSkill: 3, kindPrompt: 4, kindArtifact: 5}

func isRuntimeKind(k string) bool { return k == kindMCP || k == kindA2A }

// ItemsOf groups a state file's entries into items. MCP/A2A servers come from
// the adapter's deep-merge keys; their shared file hash cannot be attributed to
// one server, so they are only ever added or removed, never updated.
func ItemsOf(f *state.File) []StateItem {
	if f == nil {
		return nil
	}
	byKey := map[[2]string]*StateItem{}
	add := func(kind, name, target, hash string) {
		k := [2]string{kind, name}
		it := byKey[k]
		if it == nil {
			it = &StateItem{Kind: kind, Name: name, Files: map[string]string{}}
			byKey[k] = it
		}
		it.Files[target] = hash
	}
	for kind, bucket := range map[string][]state.FileEntry{
		kindPlugin: f.Plugins, kindSkill: f.Skills, kindPrompt: f.Prompts, kindArtifact: f.Artifacts,
	} {
		for _, e := range bucket {
			name := e.Source
			if name == "" {
				name = filepath.Base(e.Target)
			}
			add(kind, name, e.Target, e.Hash)
		}
	}
	for _, e := range f.Adapter.Files {
		for _, k := range e.Keys {
			if kind := runtimeKindForKey(k); kind != "" {
				add(kind, k[strings.IndexByte(k, '.')+1:], k, "")
			}
		}
	}
	out := make([]StateItem, 0, len(byKey))
	for _, it := range byKey {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindRank[out[i].Kind] < kindRank[out[j].Kind]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// DiffItems compares two states item by item.
func DiffItems(prev, next *state.File) (changes []ItemChange, unchanged int) {
	before := map[[2]string]StateItem{}
	for _, it := range ItemsOf(prev) {
		before[[2]string{it.Kind, it.Name}] = it
	}
	for _, it := range ItemsOf(next) {
		k := [2]string{it.Kind, it.Name}
		old, ok := before[k]
		delete(before, k)
		switch {
		case !ok:
			changes = append(changes, ItemChange{OpAdd, it.Kind, it.Name, fileCount(it)})
		case !sameFiles(old.Files, it.Files):
			changes = append(changes, ItemChange{OpUpdate, it.Kind, it.Name, fileCount(it)})
		default:
			unchanged++
		}
	}
	for _, it := range before {
		changes = append(changes, ItemChange{OpRemove, it.Kind, it.Name, fileCount(it)})
	}
	sortChanges(changes)
	return changes, unchanged
}

func fileCount(it StateItem) int {
	if isRuntimeKind(it.Kind) {
		return 0
	}
	return len(it.Files)
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func sortChanges(c []ItemChange) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Kind != c[j].Kind {
			return kindRank[c[i].Kind] < kindRank[c[j].Kind]
		}
		return c[i].Name < c[j].Name
	})
}

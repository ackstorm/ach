// SPDX-License-Identifier: Apache-2.0

// Package route is the generic recursive-glob projection engine that
// every per-adapter rule table (Phases 2-5) consumes. It owns the
// declarative Rule type, the Project walk/classify/drop function, the
// shared canonical JSON/TOML encoders (encode.go), and the D-06
// RuleProvider seam.
//
// Project decomposes a Claude-format plugin source tree into its
// canonical resource kinds (rules/, commands/, agents/, skills/, …) and
// routes each kind to a per-adapter destination via a recursive **-glob
// remap that preserves nested subdirs (CONTEXT.md D-01 pure
// kind-routing, D-03 declarative []Rule). It returns []adapter.FileWrite
// (NEVER writing to disk — D-05; the dispatcher publishes via
// publishRuntimeFile in plan 02) plus a deduped, sorted []string of
// dropped top-level kinds (WIRE-03 / D-12).
//
// Phase-1 behavior on both provenance arms (D-02) is verbatim
// passthrough copy of source bytes; the per-adapter format CONVERSIONS
// (e.g. codex .md→.toml, claude model-regex rewrite) land in Phases 2-3
// on top of this same mechanism.
package route

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

// Rule is the declarative routing entry per CONTEXT.md D-03. Each
// adapter exposes a static []Rule table (see RuleProvider); the shared
// engine performs the recursive ** walk, target remap, and provenance
// branch. Adapters become mostly data, not code — the table maps 1:1
// onto the OPENPACKAGE-MAPPING from→to rows.
type Rule struct {
	// FromGlob is the source anchor (e.g. "rules/**/*.md"). The FIRST
	// path element (the anchor segment before the first "**") is the
	// source kind matched against a plugin tree's top-level component.
	FromGlob string

	// ToGlob is the destination anchor (e.g. ".claude/rules/**/*.md").
	// The recursive-glob remap strips FromGlob's anchor, prepends
	// ToGlob's anchor, and preserves the remaining subpath suffix
	// verbatim (Phase 1: no extension remap — that seam lands in
	// Phase 3 for codex .md→.toml).
	ToGlob string

	// Merge classifies how the projected file combines with any
	// pre-existing on-disk content. Reuses the LOCKED adapter.MergeKind
	// enum (NEVER redefined here) so the publish/sync machinery treats
	// projected and runtime files identically.
	Merge adapter.MergeKind

	// ProvenanceGate is the optional D-02 provenance signal. "" means
	// the rule applies to any source; a non-empty value (e.g.
	// "claude-plugin") gates the rule to a matching source provenance —
	// the passthrough-vs-transform branch point. Phase-1 behavior on
	// both arms is verbatim passthrough copy; Phases 2-3 fill the
	// transform arm.
	ProvenanceGate string

	// Transform is the D-03 per-file conversion seam. When nil the engine
	// copies the matched file's bytes verbatim and emits Keys=nil — exactly
	// the Phase-1 passthrough. When non-nil, Project calls it per matched
	// file with the plugin-tree-relative, forward-slashed srcRel and the
	// raw source bytes; the returned out bytes become FileWrite.Content and
	// the returned keys become FileWrite.Keys (the dotted paths MergeDeep
	// deep-merges, e.g. "mcpServers.<id>"). A non-nil error aborts Project,
	// wrapped with the offending file path.
	//
	// Phase 2's ONLY non-nil Transform is mcpDeepKeys: it returns in
	// unchanged plus keys=["mcpServers.<id>"…] (no byte conversion — D-04:
	// every projected file's Hash equals its source hash, so no per-file
	// pre-conversion hash is threaded here). Phase 3 fills the real
	// conversion arm (codex .md→.toml, opencode tools[]→{}).
	Transform func(srcRel string, in []byte) (out []byte, keys []string, err error)
}

// RuleProvider is the D-06 seam. Adapters implement a concrete
// ProjectionRules() []Rule method, and plan 02's dispatcher
// type-asserts the adapter to route.RuleProvider to obtain the rule
// table. The locked adapter.Adapter interface is deliberately NOT
// mutated: Rule.Merge imports adapter.MergeKind, so adding a
// ProjectionRules() []route.Rule method to adapter.Adapter would create
// an import cycle (adapter → route → adapter). Keeping the seam in the
// route package avoids the cycle entirely.
type RuleProvider interface {
	ProjectionRules() []Rule
}

// droppedSet is the shared dedup-and-append primitive (promoted from
// codex's TransformPlugin) D-12 needs: a top-level source kind with no
// matching Rule is recorded exactly ONCE across the whole walk.
type droppedSet struct {
	seen map[string]bool
	out  []string
}

func newDroppedSet() *droppedSet {
	return &droppedSet{seen: map[string]bool{}}
}

func (d *droppedSet) add(name string) {
	if d.seen[name] {
		return
	}
	d.seen[name] = true
	d.out = append(d.out, name)
}

// globAnchor returns the leading literal segments of a glob, i.e. the
// path prefix before the first "**" (or before the first "*" if no
// "**"). For "rules/**/*.md" → "rules"; for ".claude/rules/**/*.md" →
// ".claude/rules".
func globAnchor(glob string) string {
	parts := strings.Split(filepath.ToSlash(glob), "/")
	anchor := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.Contains(p, "*") {
			break
		}
		anchor = append(anchor, p)
	}
	return strings.Join(anchor, "/")
}

// resolveRecursiveGlobTarget computes the destination rel path for a
// source rel path matched by a {FromGlob, ToGlob} pair, preserving
// nested subdirs (the OPENPACKAGE-MAPPING §"ACH gap" #2
// resolveRecursiveGlobTargetRelativePath port). It strips FromGlob's
// anchor segment from srcRel, prepends ToGlob's anchor, and preserves
// the remaining suffix verbatim (Phase 1 passthrough form; extension
// remap is the Phase-3 seam).
//
// T-01-01 guard: the computed dest is asserted to stay within the
// destination root — not absolute, no leading "/", and no ".." segment.
// On violation it returns an error rather than emitting an escaping
// path. (Defense-in-depth: SAFE-01/02 already rejected ../, symlinks,
// and absolute paths at extract time, but the remap is a NEW
// path-construction surface.)
func resolveRecursiveGlobTarget(fromGlob, toGlob, srcRel string) (string, error) {
	srcRel = filepath.ToSlash(srcRel)
	fromAnchor := globAnchor(fromGlob)
	toAnchor := globAnchor(toGlob)

	// Strip the FromGlob anchor segment; the remaining suffix is the
	// nested subpath to preserve under the ToGlob anchor.
	suffix := srcRel
	if fromAnchor != "" {
		trimmed := strings.TrimPrefix(srcRel, fromAnchor)
		suffix = strings.TrimPrefix(trimmed, "/")
	}

	var dest string
	switch {
	case toAnchor == "":
		dest = suffix
	case !strings.Contains(toGlob, "*"):
		// Concrete destination (ToGlob carries no wildcard): an N→1
		// collapse — every matched source maps to the single ToGlob file
		// (deep-merged downstream, e.g. mcp/**/* → .claude/settings.json).
		// The source suffix MUST NOT be appended or the merge target
		// becomes a bogus subpath (settings.json/<name>) and never fires.
		dest = toAnchor
	case suffix == "":
		dest = toAnchor
	default:
		dest = toAnchor + "/" + suffix
	}
	dest = filepath.ToSlash(filepath.Clean(dest))

	// T-01-01 path-traversal guard on the constructed destination.
	if filepath.IsAbs(dest) || strings.HasPrefix(dest, "/") {
		return "", fmt.Errorf("route: remapped target %q is absolute (escapes destination root)", dest)
	}
	for _, seg := range strings.Split(dest, "/") {
		if seg == ".." {
			return "", fmt.Errorf("route: remapped target %q contains a %q segment (escapes destination root)", dest, "..")
		}
	}
	return dest, nil
}

// matchRule returns the first Rule whose source kind matches topLevel
// AND whose ProvenanceGate is satisfied by source. The source kind is
// the FromGlob anchor's first segment, compared exactly against
// topLevel (the source tree's first path element) — mirroring the codex
// classify-on-parts[0] discipline. A rule with ProvenanceGate=="" applies
// to any source; a gated rule applies only when source == gate.
func matchRule(rules []Rule, topLevel, source string) (Rule, bool) {
	for _, r := range rules {
		anchorFirst := topLevel
		if a := globAnchor(r.FromGlob); a != "" {
			anchorFirst = strings.SplitN(a, "/", 2)[0]
		}
		if anchorFirst != topLevel {
			continue
		}
		if r.ProvenanceGate != "" && r.ProvenanceGate != source {
			continue
		}
		return r, true
	}
	return Rule{}, false
}

// Project walks the plugin source tree at src, classifies each regular
// file by its top-level source kind (parts[0]), routes matched kinds to
// per-adapter destinations via the recursive-glob remap, and records
// unrouted kinds in a deduped dropped set. The source argument carries
// the D-02 provenance signal threaded to ProvenanceGate matching.
//
// Project NEVER writes to disk (D-05): it returns []adapter.FileWrite
// (the dispatcher publishes each via publishRuntimeFile in plan 02). The
// returned []FileWrite is sorted by Path and the dropped []string is
// sorted so emitted order is byte-stable (VER-03 idempotence).
//
// Phase-1 behavior on both provenance arms is verbatim passthrough copy
// of the source bytes; the per-adapter transform arm is a documented
// stub Phases 2-3 fill.
func Project(rules []Rule, src, source string) ([]adapter.FileWrite, []string, error) {
	if src == "" {
		return nil, nil, fmt.Errorf("route: Project requires non-empty src")
	}

	var fws []adapter.FileWrite
	dropped := newDroppedSet()

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("route: rel(%q, %q): %w", src, path, err)
		}
		if rel == "." {
			// Skip the src root itself; Project returns FileWrites (no
			// disk write), so there is no dst dir to MkdirAll.
			return nil
		}

		// Classify on the FIRST path element exactly as the adapter
		// walks do.
		parts := strings.Split(filepath.ToSlash(rel), "/")
		topLevel := parts[0]

		rule, ok := matchRule(rules, topLevel, source)
		if !ok {
			// No matching rule → record the top-level kind once and skip
			// recursion into the unrouted dir.
			dropped.add(topLevel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Matched a rule. Directories carry no bytes; recurse into them.
		if d.IsDir() {
			return nil
		}

		// Regular files only — symlinks, devices, FIFOs are rejected by
		// the SAFE-02 safe-extract layer before Project sees the tree.
		// Defensive skip for non-regular entries.
		if !d.Type().IsRegular() {
			return nil
		}

		dest, err := resolveRecursiveGlobTarget(rule.FromGlob, rule.ToGlob, rel)
		if err != nil {
			return err
		}

		content, err := os.ReadFile(path) //nolint:gosec // path is under the caller-validated src root
		if err != nil {
			return fmt.Errorf("route: read %q: %w", path, err)
		}

		// D-03 transform seam: nil → verbatim passthrough (Keys=nil,
		// byte-identical to Phase 1). Non-nil → convert bytes + contribute
		// the dotted Keys[] for MergeDeep. The transform sees the
		// plugin-tree-relative, forward-slashed srcRel (the existing rel).
		var keys []string
		if rule.Transform != nil {
			srcRel := filepath.ToSlash(rel)
			out, tkeys, terr := rule.Transform(srcRel, content)
			if terr != nil {
				return fmt.Errorf("route: transform %q: %w", srcRel, terr)
			}
			content = out
			keys = tkeys
		}

		fws = append(fws, adapter.FileWrite{
			Path:    dest,
			Content: content,
			Merge:   rule.Merge,
			Keys:    keys,
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Stable ordering for byte-stable emission (VER-03).
	sort.Slice(fws, func(i, j int) bool { return fws[i].Path < fws[j].Path })
	sort.Strings(dropped.out)

	return fws, dropped.out, nil
}

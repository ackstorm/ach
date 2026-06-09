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
	"github.com/ackstorm/ach/internal/cli/hash"
)

// projectMaxFileBytes is the defense-in-depth per-file ceiling Project asserts
// before slurping a matched file into memory (WR-08). It is deliberately
// generous — 512 MiB, at/above the SAFE-02 per-artifact uncompressed cap — so
// it never fires on a legitimate plugin/artifact resource; it exists solely to
// stop a reusable-engine caller NOT gated by the extract bomb-defense limits
// from os.ReadFile'ing a pathological multi-GiB file. The SAFE-02 safe-extract
// layer remains the PRIMARY bomb defense.
const projectMaxFileBytes int64 = 512 * 1024 * 1024

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

// droppedSet is the shared dedup-and-append primitive D-12 needs: a
// top-level source kind with no matching Rule is recorded exactly ONCE
// across the whole walk.
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
// remapSuffixExt swaps the trailing extension of suffix to the ToGlob's
// literal extension WHEN the ToGlob's final path segment is a `*.<ext>`
// wildcard basename (a wildcard stem with a fixed extension, e.g.
// ".codex/agents/**/*.toml" → ".toml"). This is the Phase-3 extension-remap
// seam (D-23: codex agents/**/*.md → .codex/agents/**/*.toml). When the
// ToGlob basename carries no extension (e.g. "**/*"), or its extension already
// matches the suffix's extension, the suffix passes through unchanged. Only the
// trailing extension of the basename is rewritten — directory segments of the
// nested suffix are preserved verbatim.
func remapSuffixExt(toGlob, suffix string) string {
	if suffix == "" {
		return suffix
	}
	toBase := filepath.Base(filepath.ToSlash(toGlob))
	// Only a `*.<ext>` wildcard basename triggers the swap. A bare "*" (no
	// dot) or a concrete basename is not an extension-remap target.
	if !strings.HasPrefix(toBase, "*") {
		return suffix
	}
	toExt := filepath.Ext(toBase) // ".toml" for "*.toml"; "" for "*"
	if toExt == "" || toExt == "." {
		return suffix
	}
	srcExt := filepath.Ext(suffix) // honor the source basename's extension
	if srcExt == toExt {
		return suffix
	}
	return strings.TrimSuffix(suffix, srcExt) + toExt
}

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
		// Extension-remap seam (Phase 3, D-23): when the ToGlob's final
		// segment is a `*.<ext>` wildcard basename whose extension differs
		// from the source suffix's extension (e.g. ToGlob
		// `.codex/agents/**/*.toml` vs source `foo.md`), swap the suffix's
		// trailing extension to the ToGlob's literal extension. The swap
		// rewrites ONLY the trailing `.ext` of the basename — it never
		// inserts a path segment, so the T-01-01 guard below still holds.
		dest = toAnchor + "/" + remapSuffixExt(toGlob, suffix)
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
//
// Terminal-extension disambiguation: when several rules share a top-level
// anchor but differ on a "*.<ext>" terminal segment (e.g. gemini's
// commands/**/*.md → .toml converter AND commands/**/*.toml passthrough), a
// FILE is matched to the rule whose terminal extension equals the file's. The
// anchor-only classification alone would always return the FIRST such rule and
// the WR-03 terminal-extension guard would then silently drop every file of the
// OTHER extension (this is the bug that dropped a plugin's native gemini-format
// commands/*.toml). isFile=false (directories, anchor-only probes) keeps the
// permissive first-anchor match so Project still recurses into the kind dir.
func matchRule(rules []Rule, topLevel, rel, source string, isFile bool) (Rule, bool) {
	var fallback Rule
	var haveFallback bool
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
		// For a concrete file, a rule whose FromGlob terminal is "*.<ext>" only
		// applies when the file carries that extension. A rule with no terminal
		// extension wildcard (skills/**/*, agents/**/*) matches any extension.
		if isFile {
			if base := filepath.Base(filepath.ToSlash(r.FromGlob)); strings.HasPrefix(base, "*.") {
				if filepath.Ext(rel) != filepath.Ext(base) {
					// Remember the first anchor match as a fallback so an
					// unexpected extension still routes (and the existing WR-03
					// guard inside Project decides drop), preserving prior
					// behavior for trees with no extension-specific rule.
					if !haveFallback {
						fallback, haveFallback = r, true
					}
					continue
				}
			}
		}
		return r, true
	}
	if haveFallback {
		return fallback, true
	}
	return Rule{}, false
}

// ProjectResult is the structured return of Project. FileWrites is the
// sorted projected-file list; KeptByKind tallies how many regular files
// were projected per source component kind (e.g. {"commands":12,"agents":8})
// for the hydrate success summary; Dropped is the deduped+sorted set of
// KNOWN component kinds present in the source tree that this adapter's rule
// table has no destination for. Metadata, docs, and unrecognized top-levels
// are silently skipped and never appear in Dropped.
type ProjectResult struct {
	FileWrites []adapter.FileWrite
	KeptByKind map[string]int
	Dropped    []string
}

// Project walks the plugin source tree at src, classifies each regular
// file by its top-level source kind (parts[0]), routes matched kinds to
// per-adapter destinations via the recursive-glob remap, and records
// unrouted kinds in a deduped dropped set. The source argument carries
// the D-02 provenance signal threaded to ProvenanceGate matching.
//
// Project NEVER writes to disk (D-05): it returns a ProjectResult whose
// FileWrites is sorted by Path and Dropped is sorted so emitted order is
// byte-stable (VER-03 idempotence).
//
// Phase-1 behavior on both provenance arms is verbatim passthrough copy
// of the source bytes; the per-adapter transform arm is a documented
// stub Phases 2-3 fill.
func Project(rules []Rule, src, source string) (ProjectResult, error) {
	if src == "" {
		return ProjectResult{}, fmt.Errorf("route: Project requires non-empty src")
	}

	var fws []adapter.FileWrite
	dropped := newDroppedSet()
	kept := map[string]int{}
	// keptSeen dedups the per-kind count to distinct COMPONENTS, not files.
	// A skill is a directory (skills/<name>/SKILL.md + helpers) — counting
	// every regular file inflated "N skills" (e.g. 35 for 12 dirs). We key
	// on the second path segment (the component name under each kind dir);
	// agents/commands are single files (agents/<name>.md) so their second
	// segment is the filename — still one increment per component.
	keptSeen := map[string]map[string]struct{}{}

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

		rule, ok := matchRule(rules, topLevel, rel, source, !d.IsDir())
		if !ok {
			// Only KNOWN component kinds are reported as dropped (so the user
			// learns the target lacks support); metadata, docs, and
			// unrecognized top-levels are skipped silently to keep the
			// warning surface focused.
			if KnownComponentKinds[topLevel] {
				dropped.add(topLevel)
			}
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

		// WR-03 terminal-extension enforcement: matchRule classifies on the
		// FromGlob anchor's FIRST segment only, so the `.md` in
		// "agents/**/*.md" is otherwise decorative — any file under agents/
		// (image.png, README, notes.txt) would be routed through the
		// converting Transform. When the FromGlob terminal segment is a
		// "*.<ext>" wildcard, skip files whose extension differs so a
		// spec-violating plugin's non-.md content never reaches codexAgentTOML
		// / opencodeAgentTools (binary → developer_instructions / silent
		// wrong-output). D-01 plugin source is Claude-vanilla, so this only
		// guards a malformed tree, fail-fast rather than corrupt-through.
		if base := filepath.Base(filepath.ToSlash(rule.FromGlob)); strings.HasPrefix(base, "*.") {
			if filepath.Ext(rel) != filepath.Ext(base) {
				return nil
			}
		}

		dest, err := resolveRecursiveGlobTarget(rule.FromGlob, rule.ToGlob, rel)
		if err != nil {
			return err
		}

		// WR-08 defense-in-depth size cap: the SAFE-02 safe-extract layer is
		// the PRIMARY bomb defense (per-archive uncompressed cap before
		// Project ever sees the tree), but Project is a reusable engine also
		// invoked directly in tests / by future callers over a tree that may
		// NOT be gated by extract limits. Lstat the entry and reject anything
		// past projectMaxFileBytes before os.ReadFile loads it fully, so the
		// engine's safety no longer depends on an invariant it never asserts.
		// The ceiling is generous (>= the largest legitimate plugin/artifact
		// resource) so it never fires on real input — it only stops a
		// pathological multi-GiB file from being slurped into memory.
		if fi, statErr := os.Lstat(path); statErr == nil {
			if fi.Size() > projectMaxFileBytes {
				return fmt.Errorf("route: file %q is %d bytes, exceeds projection size cap %d (defense-in-depth)", rel, fi.Size(), projectMaxFileBytes)
			}
		}

		content, err := os.ReadFile(path) //nolint:gosec // path is under the caller-validated src root
		if err != nil {
			return fmt.Errorf("route: read %q: %w", path, err)
		}

		// D-23 SourceHash capture: hash the SOURCE bytes BEFORE any Transform
		// overwrite. For a nil-Transform (passthrough) rule the source bytes
		// equal the emitted Content, so SourceHash == downstream Hash (the
		// Phase-1/2 invariant). For a converting Transform the emitted bytes
		// differ, so SourceHash != Hash — recorded into state.FileEntry by
		// publishFile.
		srcHash := hash.HashBytes(content)

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

		// Count distinct components per kind, not files. The component name
		// is the second path segment (skills/<name>/…, agents/<name>.md); a
		// matched component file always has one (its rule anchors under the
		// kind dir), but fall back to topLevel defensively.
		component := topLevel
		if len(parts) >= 2 {
			component = parts[1]
		}
		if keptSeen[topLevel] == nil {
			keptSeen[topLevel] = map[string]struct{}{}
		}
		if _, dup := keptSeen[topLevel][component]; !dup {
			keptSeen[topLevel][component] = struct{}{}
			kept[topLevel]++
		}
		fws = append(fws, adapter.FileWrite{
			Path:       dest,
			Content:    content,
			SourceHash: srcHash,
			Merge:      rule.Merge,
			Keys:       keys,
		})
		return nil
	})
	if err != nil {
		return ProjectResult{}, err
	}

	// Stable ordering for byte-stable emission (VER-03).
	sort.Slice(fws, func(i, j int) bool { return fws[i].Path < fws[j].Path })
	sort.Strings(dropped.out)

	return ProjectResult{
		FileWrites: fws,
		KeptByKind: kept,
		Dropped:    dropped.out,
	}, nil
}

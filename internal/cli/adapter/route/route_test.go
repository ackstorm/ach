// SPDX-License-Identifier: Apache-2.0

package route

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
)

// writeTree materializes a map of rel-path -> contents under a fresh
// temp dir and returns the dir. Parent dirs are created as needed.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	return root
}

// TestProject_NestedSubdirPreserved is the must-have: a recursive **
// glob remap preserves nested subdirs.
func TestProject_NestedSubdirPreserved(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/foo/bar.md": "RULE BODY",
	})
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(pr.Dropped) != 0 {
		t.Fatalf("expected no drops, got %v", pr.Dropped)
	}
	if len(pr.FileWrites) != 1 {
		t.Fatalf("expected 1 FileWrite, got %d (%v)", len(pr.FileWrites), pr.FileWrites)
	}
	got := pr.FileWrites[0]
	if got.Path != ".claude/rules/foo/bar.md" {
		t.Errorf("Path: got %q, want %q", got.Path, ".claude/rules/foo/bar.md")
	}
	if string(got.Content) != "RULE BODY" {
		t.Errorf("Content: got %q, want %q", string(got.Content), "RULE BODY")
	}
	if got.Merge != adapter.MergeReplace {
		t.Errorf("Merge: got %v, want %v", got.Merge, adapter.MergeReplace)
	}
}

// TestProject_DropDedup: an unrouted top-level kind appears exactly once
// in the dropped slice even when multiple files exist under it.
func TestProject_DropDedup(t *testing.T) {
	src := writeTree(t, map[string]string{
		"hooks/a.json":  "{}",
		"hooks/b.json":  "{}",
		"rules/keep.md": "x",
	})
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(pr.FileWrites) != 1 {
		t.Fatalf("expected 1 routed file, got %d", len(pr.FileWrites))
	}
	want := []string{"hooks"}
	if !reflect.DeepEqual(pr.Dropped, want) {
		t.Errorf("dropped: got %v, want %v", pr.Dropped, want)
	}
}

// TestProject_Deterministic: output []FileWrite is sorted by Path and
// dropped []string is sorted; two runs yield identical slices.
func TestProject_Deterministic(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/z.md":    "z",
		"rules/a.md":    "a",
		"rules/m/n.md":  "n",
		"hooks/x.json":  "{}",
		"unknown/y.txt": "y",
	})
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	pr1, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project run 1: %v", err)
	}
	pr2, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project run 2: %v", err)
	}

	if !reflect.DeepEqual(pr1.FileWrites, pr2.FileWrites) {
		t.Errorf("FileWrites not deterministic:\n run1=%v\n run2=%v", pr1.FileWrites, pr2.FileWrites)
	}
	if !reflect.DeepEqual(pr1.Dropped, pr2.Dropped) {
		t.Errorf("dropped not deterministic:\n run1=%v\n run2=%v", pr1.Dropped, pr2.Dropped)
	}

	// Assert FileWrites are sorted by Path.
	for i := 1; i < len(pr1.FileWrites); i++ {
		if pr1.FileWrites[i-1].Path > pr1.FileWrites[i].Path {
			t.Errorf("FileWrites not sorted by Path at %d: %q > %q", i, pr1.FileWrites[i-1].Path, pr1.FileWrites[i].Path)
		}
	}
	// Assert dropped is sorted.
	for i := 1; i < len(pr1.Dropped); i++ {
		if pr1.Dropped[i-1] > pr1.Dropped[i] {
			t.Errorf("dropped not sorted at %d: %q > %q", i, pr1.Dropped[i-1], pr1.Dropped[i])
		}
	}
}

// TestProject_EmptyDirTree: a dir-only tree yields no FileWrite, no panic.
func TestProject_EmptyDirTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rules", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: ".claude/rules/**/*.md", Merge: adapter.MergeReplace},
	}

	pr, err := Project(rules, root, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(pr.FileWrites) != 0 {
		t.Errorf("expected no FileWrites for dir-only tree, got %v", pr.FileWrites)
	}
	if len(pr.Dropped) != 0 {
		t.Errorf("expected no drops for dir-only tree, got %v", pr.Dropped)
	}
}

// TestProject_ProvenanceGate: a rule gated "claude-plugin" matches only
// when source == "claude-plugin"; Phase-1 both arms copy bytes verbatim.
func TestProject_ProvenanceGate(t *testing.T) {
	files := map[string]string{
		"agents/x.md": "AGENT BODY",
	}

	gated := []Rule{
		{FromGlob: "agents/**/*.md", ToGlob: ".claude/agents/**/*.md", Merge: adapter.MergeReplace, ProvenanceGate: "claude-plugin"},
	}

	// source == gate: rule applies.
	srcMatch := writeTree(t, files)
	prMatch, err := Project(gated, srcMatch, "claude-plugin")
	if err != nil {
		t.Fatalf("Project (match): %v", err)
	}
	if len(prMatch.FileWrites) != 1 {
		t.Fatalf("gated rule with matching source: expected 1 FileWrite, got %d", len(prMatch.FileWrites))
	}
	if string(prMatch.FileWrites[0].Content) != "AGENT BODY" {
		t.Errorf("gated arm content not verbatim: got %q", string(prMatch.FileWrites[0].Content))
	}

	// source != gate: rule does NOT apply, agents dropped.
	srcMiss := writeTree(t, files)
	prMiss, err := Project(gated, srcMiss, "other-source")
	if err != nil {
		t.Fatalf("Project (miss): %v", err)
	}
	if len(prMiss.FileWrites) != 0 {
		t.Fatalf("gated rule with non-matching source: expected 0 FileWrites, got %d", len(prMiss.FileWrites))
	}
	if len(prMiss.Dropped) != 1 || prMiss.Dropped[0] != "agents" {
		t.Errorf("expected agents dropped on gate miss, got %v", prMiss.Dropped)
	}

	// Ungated rule + verbatim assertion: content identical to gated arm.
	ungated := []Rule{
		{FromGlob: "agents/**/*.md", ToGlob: ".claude/agents/**/*.md", Merge: adapter.MergeReplace},
	}
	srcUngated := writeTree(t, files)
	prUngated, err := Project(ungated, srcUngated, "anything")
	if err != nil {
		t.Fatalf("Project (ungated): %v", err)
	}
	if len(prUngated.FileWrites) != 1 {
		t.Fatalf("ungated rule: expected 1 FileWrite, got %d", len(prUngated.FileWrites))
	}
	if string(prUngated.FileWrites[0].Content) != string(prMatch.FileWrites[0].Content) {
		t.Errorf("Phase-1 transform-vs-passthrough arms differ: gated=%q ungated=%q",
			string(prMatch.FileWrites[0].Content), string(prUngated.FileWrites[0].Content))
	}
}

// TestProject_TraversalGuard (T-01-01): a crafted ToGlob that would
// produce a dest path escaping the destination root makes Project error.
func TestProject_TraversalGuard(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/evil.md": "x",
	})
	// ToGlob anchored on a parent-escape: the recursive-glob remap will
	// produce a dest beginning with "../", which the guard must reject.
	rules := []Rule{
		{FromGlob: "rules/**/*.md", ToGlob: "../escape/**/*.md", Merge: adapter.MergeReplace},
	}

	_, err := Project(rules, src, "")
	if err == nil {
		t.Fatalf("expected traversal guard error for ToGlob escaping dest root, got nil")
	}
}

// TestProject_TransformNilIsVerbatim (D-03): a rule with Transform==nil
// produces FileWrite.Content == raw source bytes and FileWrite.Keys == nil,
// byte-identical to the Phase-1 passthrough output (regression guard).
func TestProject_TransformNilIsVerbatim(t *testing.T) {
	src := writeTree(t, map[string]string{
		"mcp/server.json": `{"mcpServers":{"x":{}}}`,
	})
	rules := []Rule{
		{FromGlob: "mcp/**/*", ToGlob: ".claude/settings.json", Merge: adapter.MergeDeep},
	}

	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(pr.FileWrites) != 1 {
		t.Fatalf("expected 1 FileWrite, got %d (%v)", len(pr.FileWrites), pr.FileWrites)
	}
	got := pr.FileWrites[0]
	// N→1 collapse: a concrete (wildcard-free) ToGlob is the merge target
	// verbatim — the source suffix must NOT be appended (regression guard
	// for the settings.json/<name> bogus-subpath bug).
	if got.Path != ".claude/settings.json" {
		t.Errorf("Path: got %q, want %q (N→1 collapse, no suffix append)", got.Path, ".claude/settings.json")
	}
	if string(got.Content) != `{"mcpServers":{"x":{}}}` {
		t.Errorf("Content: got %q, want verbatim raw bytes", string(got.Content))
	}
	if got.Keys != nil {
		t.Errorf("Keys: got %v, want nil for nil-Transform path", got.Keys)
	}
}

// TestProject_TerminalExtensionEnforced (WR-03): when a rule's FromGlob ends
// in a "*.<ext>" wildcard, files under the matching kind dir whose extension
// differs are skipped — never routed through the converting Transform. Guards
// a spec-violating plugin that drops binary/non-.md content under agents/.
func TestProject_TerminalExtensionEnforced(t *testing.T) {
	src := writeTree(t, map[string]string{
		"agents/real.md":   "---\nname: a\n---\nbody",
		"agents/image.png": "\x89PNG\x00binary",
		"agents/README":    "no extension",
	})
	transformCalls := 0
	rules := []Rule{
		{
			FromGlob: "agents/**/*.md",
			ToGlob:   ".codex/agents/**/*.toml",
			Merge:    adapter.MergeReplace,
			Transform: func(_ string, in []byte) ([]byte, []string, error) {
				transformCalls++
				return in, nil, nil
			},
		},
	}

	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	// Only the .md file should be routed/transformed.
	if len(pr.FileWrites) != 1 {
		t.Fatalf("expected 1 FileWrite (.md only), got %d: %v", len(pr.FileWrites), pr.FileWrites)
	}
	if transformCalls != 1 {
		t.Errorf("Transform called %d times; want 1 (only the .md file)", transformCalls)
	}
	if got := pr.FileWrites[0].Path; got != ".codex/agents/real.toml" {
		t.Errorf("Path = %q, want .codex/agents/real.toml", got)
	}
}

// TestProject_TerminalExtensionDisambiguatesSameAnchor proves that when two
// rules share a top-level anchor but differ on a "*.<ext>" terminal (gemini's
// commands/**/*.md converter AND commands/**/*.toml passthrough), a FILE routes
// to the rule whose terminal extension matches its own — NOT unconditionally to
// the FIRST anchor match. Regression guard for the bug where a plugin's native
// gemini-format commands/*.toml were silently dropped: matchRule returned the
// .md rule for every file under commands/, and the WR-03 terminal-extension
// guard then dropped the .toml ones (the .toml passthrough rule was dead code).
func TestProject_TerminalExtensionDisambiguatesSameAnchor(t *testing.T) {
	src := writeTree(t, map[string]string{
		"commands/from-md.md":    "claude command body",
		"commands/native.toml":   "prompt = \"native\"\n",
		"commands/sub/deep.toml": "prompt = \"deep\"\n",
	})
	mdCalls := 0
	rules := []Rule{
		// .md → .toml with a converting Transform (must NOT see the .toml files).
		{
			FromGlob: "commands/**/*.md",
			ToGlob:   ".gemini/commands/**/*.toml",
			Merge:    adapter.MergeReplace,
			Transform: func(_ string, in []byte) ([]byte, []string, error) {
				mdCalls++
				return []byte("converted"), nil, nil
			},
		},
		// native .toml passthrough (verbatim) — previously shadowed/dead.
		{FromGlob: "commands/**/*.toml", ToGlob: ".gemini/commands/**/*.toml", Merge: adapter.MergeReplace},
	}

	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if mdCalls != 1 {
		t.Errorf("md Transform called %d times; want 1 (only commands/from-md.md)", mdCalls)
	}
	got := map[string]string{}
	for _, fw := range pr.FileWrites {
		got[fw.Path] = string(fw.Content)
	}
	// The .md converts; both .toml files pass through verbatim (NOT dropped).
	if v, ok := got[".gemini/commands/from-md.toml"]; !ok || v != "converted" {
		t.Errorf(".md route: got %q (present=%v), want converted", v, ok)
	}
	if v, ok := got[".gemini/commands/native.toml"]; !ok || v != "prompt = \"native\"\n" {
		t.Errorf(".toml passthrough native: got %q (present=%v), want verbatim (the dropped-command bug)", v, ok)
	}
	if v, ok := got[".gemini/commands/sub/deep.toml"]; !ok || v != "prompt = \"deep\"\n" {
		t.Errorf(".toml passthrough nested: got %q (present=%v), want verbatim", v, ok)
	}
	if len(pr.FileWrites) != 3 {
		t.Fatalf("expected 3 FileWrites (1 converted + 2 verbatim), got %d: %v", len(pr.FileWrites), pr.FileWrites)
	}
}

// TestProject_TransformApplied (D-03): a rule whose Transform returns fixed
// bytes + keys ["mcpServers.x"] yields a FileWrite carrying those Content/Keys.
func TestProject_TransformApplied(t *testing.T) {
	src := writeTree(t, map[string]string{
		"mcp/server.json": "RAW INPUT",
	})
	rules := []Rule{
		{
			FromGlob: "mcp/**/*",
			ToGlob:   ".claude/settings.json",
			Merge:    adapter.MergeDeep,
			Transform: func(_ string, _ []byte) ([]byte, []string, error) {
				return []byte("OUT"), []string{"mcpServers.x"}, nil
			},
		},
	}

	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(pr.FileWrites) != 1 {
		t.Fatalf("expected 1 FileWrite, got %d (%v)", len(pr.FileWrites), pr.FileWrites)
	}
	got := pr.FileWrites[0]
	if got.Path != ".claude/settings.json" {
		t.Errorf("Path: got %q, want %q (N→1 collapse, no suffix append)", got.Path, ".claude/settings.json")
	}
	if string(got.Content) != "OUT" {
		t.Errorf("Content: got %q, want %q (transform output)", string(got.Content), "OUT")
	}
	if !reflect.DeepEqual(got.Keys, []string{"mcpServers.x"}) {
		t.Errorf("Keys: got %v, want %v (transform keys)", got.Keys, []string{"mcpServers.x"})
	}
}

// TestProject_TransformReceivesSrcRel (D-03): Transform receives srcRel ==
// the plugin-tree-relative path of the matched file, forward-slashed.
func TestProject_TransformReceivesSrcRel(t *testing.T) {
	src := writeTree(t, map[string]string{
		"mcp/nested/server.json": "x",
	})
	var seen string
	rules := []Rule{
		{
			FromGlob: "mcp/**/*",
			ToGlob:   ".claude/settings.json",
			Merge:    adapter.MergeDeep,
			Transform: func(srcRel string, in []byte) ([]byte, []string, error) {
				seen = srcRel
				return in, nil, nil
			},
		},
	}

	if _, err := Project(rules, src, ""); err != nil {
		t.Fatalf("Project: %v", err)
	}
	if seen != "mcp/nested/server.json" {
		t.Errorf("Transform srcRel: got %q, want plugin-tree-relative %q", seen, "mcp/nested/server.json")
	}
}

// TestProject_TransformErrorAborts (D-03): a Transform returning a non-nil
// error makes Project return a non-nil error mentioning the file path.
func TestProject_TransformErrorAborts(t *testing.T) {
	src := writeTree(t, map[string]string{
		"mcp/server.json": "x",
	})
	rules := []Rule{
		{
			FromGlob: "mcp/**/*",
			ToGlob:   ".claude/settings.json",
			Merge:    adapter.MergeDeep,
			Transform: func(_ string, _ []byte) ([]byte, []string, error) {
				return nil, nil, errors.New("boom")
			},
		},
	}

	sentinel := errors.New("boom")
	rules[0].Transform = func(_ string, _ []byte) ([]byte, []string, error) {
		return nil, nil, sentinel
	}
	_, err := Project(rules, src, "")
	if err == nil {
		t.Fatalf("expected Project to abort on Transform error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error %v does not wrap the Transform's sentinel error", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "mcp/server.json") {
		t.Errorf("error %q does not name the offending file path", msg)
	}
}

// TestProject_DropsOnlyKnownKinds asserts that metadata dirs (.claude-plugin,
// .codex-plugin), doc files (README.md), and unrecognized top-levels
// (random-dir) are silently skipped — only known component kinds (hooks)
// appear in Dropped.
func TestProject_DropsOnlyKnownKinds(t *testing.T) {
	src := writeTree(t, map[string]string{
		"hooks/pre.sh":               "#!/bin/sh\n",
		".claude-plugin/plugin.json": "{}",
		".codex-plugin/x.json":       "{}",
		"README.md":                  "# docs\n",
		"random-dir/x.txt":           "x",
		"skills/a/SKILL.md":          "s",
	})

	rules := []Rule{
		{FromGlob: "skills/**/*", ToGlob: ".x/skills/**/*", Merge: adapter.MergeReplace},
	}
	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project = %v", err)
	}
	if got := pr.Dropped; len(got) != 1 || got[0] != "hooks" {
		t.Fatalf("Dropped = %v; want exactly [hooks] (metadata/docs/unknown must be silent)", got)
	}
}

// TestProject_KeptByKind: KeptByKind tallies distinct COMPONENTS per kind,
// not files. A multi-file skill (SKILL.md + helpers + nested refs) counts
// ONCE; two skill dirs count as two. Regression guard for the "35 skills
// for 12 dirs" inflation — the pre-fix per-file tally would report skills=4
// here (4 files under skills/) instead of 2 components.
func TestProject_KeptByKind(t *testing.T) {
	src := writeTree(t, map[string]string{
		"commands/a.md":         "a",
		"commands/b.md":         "b",
		"skills/s/SKILL.md":     "s", // skill "s" is multi-file …
		"skills/s/helper.py":    "h", // … these extra files must NOT …
		"skills/s/ref/notes.md": "n", // … inflate the skills count.
		"skills/t/SKILL.md":     "t", // a second, distinct skill component
	})
	rules := []Rule{
		{FromGlob: "commands/**/*", ToGlob: ".x/commands/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".x/skills/**/*", Merge: adapter.MergeReplace},
	}
	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project = %v", err)
	}
	if pr.KeptByKind["commands"] != 2 || pr.KeptByKind["skills"] != 2 {
		t.Fatalf("KeptByKind = %v; want commands=2 skills=2 (distinct components, not files)", pr.KeptByKind)
	}
}

// TestResolveTarget_NestedAndGuard exercises the remap helper directly.
func TestResolveTarget_NestedAndGuard(t *testing.T) {
	dest, err := resolveRecursiveGlobTarget("rules/**/*.md", ".claude/rules/**/*.md", "rules/foo/bar.md")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dest != ".claude/rules/foo/bar.md" {
		t.Errorf("dest: got %q, want %q", dest, ".claude/rules/foo/bar.md")
	}

	// Absolute ToGlob anchor must be rejected.
	if _, err := resolveRecursiveGlobTarget("rules/**/*.md", "/etc/**/*.md", "rules/foo/bar.md"); err == nil {
		t.Errorf("expected error for absolute dest anchor")
	}

	// `..` in the resulting dest must be rejected.
	if _, err := resolveRecursiveGlobTarget("rules/**/*.md", "../x/**/*.md", "rules/foo/bar.md"); err == nil {
		t.Errorf("expected error for `..` dest segment")
	}
}

// TestResolveTarget_ExtensionRemap exercises the Phase-3 extension-remap
// seam: when the ToGlob basename is a `*.<ext>` wildcard whose extension
// differs from the source suffix, the source suffix's extension is swapped
// to the ToGlob's literal extension (D-23 codex .md→.toml). When the ToGlob
// basename carries no extension (e.g. `**/*`) the suffix passes through
// unchanged. The concrete N→1 collapse arm is untouched.
func TestResolveTarget_ExtensionRemap(t *testing.T) {
	cases := []struct {
		name             string
		from, to, srcRel string
		want             string
	}{
		{
			name: "md to toml flat", from: "agents/**/*.md",
			to: ".codex/agents/**/*.toml", srcRel: "agents/foo.md",
			want: ".codex/agents/foo.toml",
		},
		{
			name: "md to toml nested suffix preserved", from: "agents/**/*.md",
			to: ".codex/agents/**/*.toml", srcRel: "agents/sub/bar.md",
			want: ".codex/agents/sub/bar.toml",
		},
		{
			name: "no ext in toglob basename means no swap", from: "skills/**/*",
			to: ".agents/skills/**/*", srcRel: "skills/x/y.sh",
			want: ".agents/skills/x/y.sh",
		},
		{
			name: "concrete N to 1 collapse unchanged", from: "mcp/**/*",
			to: ".codex/config.toml", srcRel: "mcp/github/server.json",
			want: ".codex/config.toml",
		},
		{
			name: "same ext is a no-op swap", from: "rules/**/*.md",
			to: ".claude/rules/**/*.md", srcRel: "rules/foo/bar.md",
			want: ".claude/rules/foo/bar.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRecursiveGlobTarget(tc.from, tc.to, tc.srcRel)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("dest: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProject_SourceHash asserts the D-23 SourceHash capture: a nil-Transform
// (passthrough) rule records SourceHash == HashBytes(emitted content), while a
// Transform that alters bytes records SourceHash == HashBytes(source bytes) !=
// HashBytes(emitted content).
func TestProject_SourceHash(t *testing.T) {
	src := writeTree(t, map[string]string{
		"rules/a.md":  "SOURCE-A\n",
		"agents/b.md": "SOURCE-B\n",
	})

	passthrough := Rule{
		FromGlob: "rules/**/*.md",
		ToGlob:   ".claude/rules/**/*.md",
		Merge:    adapter.MergeReplace,
	}
	convert := Rule{
		FromGlob: "agents/**/*.md",
		ToGlob:   ".codex/agents/**/*.md",
		Merge:    adapter.MergeReplace,
		Transform: func(_ string, in []byte) ([]byte, []string, error) {
			return []byte("CONVERTED-" + string(in)), nil, nil
		},
	}

	pr, err := Project([]Rule{passthrough, convert}, src, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	byPath := map[string]adapter.FileWrite{}
	for _, fw := range pr.FileWrites {
		byPath[fw.Path] = fw
	}

	pf, ok := byPath[".claude/rules/a.md"]
	if !ok {
		t.Fatalf("passthrough file missing; got %+v", pr.FileWrites)
	}
	if pf.SourceHash == "" {
		t.Errorf("passthrough SourceHash empty")
	}
	if want := hash.HashBytes(pf.Content); pf.SourceHash != want {
		t.Errorf("passthrough SourceHash: got %q, want %q", pf.SourceHash, want)
	}

	cf, ok := byPath[".codex/agents/b.md"]
	if !ok {
		t.Fatalf("converted file missing; got %+v", pr.FileWrites)
	}
	if cf.SourceHash == hash.HashBytes(cf.Content) {
		t.Errorf("converted SourceHash must differ from emitted Hash; both %q", cf.SourceHash)
	}
	if want := hash.HashBytes([]byte("SOURCE-B\n")); cf.SourceHash != want {
		t.Errorf("converted SourceHash: got %q, want %q (source bytes)", cf.SourceHash, want)
	}
}

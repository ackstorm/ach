// SPDX-License-Identifier: Apache-2.0

// Package adapter_test holds the cross-adapter ProjectionRules conformance
// gate (VER-01, Phase 06). It is the spec-drift tripwire: every per-adapter
// ProjectionRules() route table is pinned — FromGlob, ToGlob, Merge, and the
// per-adapter drop set — against a literal expected-table fixture transcribed
// from .planning/research/OPENPACKAGE-MAPPING.md. Editing a route ToGlob or
// Merge without updating the mapping doc breaks this test, so the canonical
// spec document and the Go ports cannot diverge undetected.
//
// The five adapter subpackages are named-imported and instantiated directly via
// their exported Adapter type. The route-table assertions construct the adapters
// directly rather than walking adapter.Iter() — the registry is reset between
// tests by the in-package registry_test.go (resetForTesting), so a registry walk
// from this external package would race that reset and see an empty set.
// Registration-on-import stays asserted by each subpackage's own
// TestRegistry_RegistersOnImport.
package adapter_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"

	// Named-import all five adapters: instantiate each Adapter directly for the
	// route-table assertions. The import also fires init() Register, but
	// registration is asserted by each subpackage's own test, not here.
	"github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	"github.com/ackstorm/ach/internal/cli/adapter/codex"
	"github.com/ackstorm/ach/internal/cli/adapter/gemini"
	"github.com/ackstorm/ach/internal/cli/adapter/opencode"
	"github.com/ackstorm/ach/internal/cli/adapter/pimono"
)

// adapters maps each canonical id to a freshly-constructed adapter instance.
// Constructing directly (not via adapter.Iter) sidesteps the registry-reset
// race the in-package registry_test.go introduces into this shared test binary.
func adapters() map[string]route.RuleProvider {
	return map[string]route.RuleProvider{
		"claude-code": &claudecode.Adapter{},
		"codex":       &codex.Adapter{},
		"gemini-cli":  &gemini.Adapter{},
		"opencode":    &opencode.Adapter{},
		"pimono":      &pimono.Adapter{},
	}
}

// wantRule is one literal route row transcribed from OPENPACKAGE-MAPPING.md.
// hasXform records only whether a non-nil Transform is wired (we cannot compare
// function identity across packages; the per-adapter package tests already pin
// the Transform's behavior — here we only gate the route shape + merge classes).
type wantRule struct {
	to       string
	merge    adapter.MergeKind
	hasXform bool
}

// adapterConformance is one per-adapter expected table + drop set, transcribed
// literally from the OPENPACKAGE-MAPPING.md section named in `mappingSection`.
type adapterConformance struct {
	id             string
	mappingSection string
	// rules is keyed by FromGlob → expected {ToGlob, Merge, hasXform}.
	rules map[string]wantRule
	// rowCount is the literal closed-set row count for this adapter's table
	// (must equal len(rules)); it ties the subtest to the mapping section so a
	// silently-added/removed route trips the count assertion.
	rowCount int
	// wantDropped is the EXACT, sorted drop set route.Project records for a
	// plugin tree carrying every canonical source kind (see fullTree).
	wantDropped []string
}

// fullTree is a plugin source tree carrying EVERY canonical OpenPackage source
// kind so route.Project's drop-set classification is exercised for all kinds at
// once. Each adapter's wantDropped is the complement of its routed kinds.
var fullTree = map[string]string{
	"rules/style.md":       "rule\n",
	"commands/grunt.md":    "# grunt\n",
	"agents/cave.md":       "---\nname: cave\n---\nhi\n",
	"skills/fire/skill.md": "skill\n",
	"prompts/intro.md":     "# intro\n",
	"mcp/servers.json":     `{"mcpServers":{"svc":{"url":"https://svc"}}}`,
	"AGENTS.md":            "agents prose\n",
	"hooks/pre.sh":         "#!/bin/sh\n",
}

// conformanceTables is the literal expected fixture, one entry per adapter id.
// Transcribed from .planning/research/OPENPACKAGE-MAPPING.md — keep 1:1 with
// the per-adapter §sections; a divergence here OR in an adapter's
// ProjectionRules() breaks the gate.
var conformanceTables = []adapterConformance{
	{
		id:             "claude-code",
		mappingSection: "OPENPACKAGE-MAPPING.md §claude-code (drops = NONE at field level; FMT-03 CUT)",
		rowCount:       6,
		rules: map[string]wantRule{
			"rules/**/*":    {to: ".claude/rules/**/*", merge: adapter.MergeReplace, hasXform: false},
			"commands/**/*": {to: ".claude/commands/**/*", merge: adapter.MergeReplace, hasXform: false},
			"agents/**/*":   {to: ".claude/agents/**/*", merge: adapter.MergeReplace, hasXform: false},
			"skills/**/*":   {to: ".claude/skills/**/*", merge: adapter.MergeReplace, hasXform: false},
			"AGENTS.md":     {to: "CLAUDE.md", merge: adapter.MergeComposite, hasXform: false},
			"mcp/**/*":      {to: ".claude/settings.json", merge: adapter.MergeDeep, hasXform: true},
		},
		// claude routes rules/commands/agents/skills/AGENTS.md/mcp; only
		// prompts/ + hooks/ have no rule → dropped.
		wantDropped: []string{"hooks", "prompts"},
	},
	{
		id:             "codex",
		mappingSection: "OPENPACKAGE-MAPPING.md §codex (FMT-01 field-lift, FMT-02 header surgery)",
		rowCount:       4,
		rules: map[string]wantRule{
			"commands/**/*.md": {to: ".codex/prompts/**/*.md", merge: adapter.MergeReplace, hasXform: false},
			"skills/**/*":      {to: ".agents/skills/**/*", merge: adapter.MergeReplace, hasXform: false},
			"agents/**/*.md":   {to: ".codex/agents/**/*.toml", merge: adapter.MergeReplace, hasXform: true},
			"mcp/**/*":         {to: ".codex/config.toml", merge: adapter.MergeDeep, hasXform: true},
		},
		// codex routes commands/skills/agents/mcp; rules/, prompts/, AGENTS.md,
		// hooks/ have no rule → dropped.
		wantDropped: []string{"AGENTS.md", "hooks", "prompts", "rules"},
	},
	{
		id:             "gemini-cli",
		mappingSection: "OPENPACKAGE-MAPPING.md §gemini-cli (ACH-original; drop hooks; AGENTS.md→GEMINI.md)",
		rowCount:       6,
		rules: map[string]wantRule{
			"agents/**/*":   {to: ".gemini/agents/**/*", merge: adapter.MergeReplace, hasXform: false},
			"prompts/**/*":  {to: ".gemini/prompts/**/*", merge: adapter.MergeReplace, hasXform: false},
			"commands/**/*": {to: ".gemini/commands/**/*", merge: adapter.MergeReplace, hasXform: false},
			"skills/**/*":   {to: ".gemini/skills/**/*", merge: adapter.MergeReplace, hasXform: false},
			"AGENTS.md":     {to: "GEMINI.md", merge: adapter.MergeComposite, hasXform: false},
			"mcp/**/*":      {to: ".gemini/settings.json", merge: adapter.MergeDeep, hasXform: true},
		},
		// gemini routes agents/prompts/commands/skills/AGENTS.md/mcp; only
		// rules/ + hooks/ have no rule → dropped.
		wantDropped: []string{"hooks", "rules"},
	},
	{
		id:             "opencode",
		mappingSection: "OPENPACKAGE-MAPPING.md §opencode (FMT-04 tools-object, D-21 mcp rename)",
		rowCount:       4,
		rules: map[string]wantRule{
			"commands/**/*":  {to: ".opencode/commands/**/*", merge: adapter.MergeReplace, hasXform: false},
			"agents/**/*.md": {to: ".opencode/agents/**/*.md", merge: adapter.MergeReplace, hasXform: true},
			"skills/**/*":    {to: ".opencode/skills/**/*", merge: adapter.MergeReplace, hasXform: false},
			"mcp/**/*":       {to: ".opencode/opencode.json", merge: adapter.MergeDeep, hasXform: true},
		},
		// opencode routes commands/agents/skills/mcp; rules/, prompts/,
		// AGENTS.md, hooks/ have no rule → dropped.
		wantDropped: []string{"AGENTS.md", "hooks", "prompts", "rules"},
	},
	{
		id:             "pimono",
		mappingSection: "OPENPACKAGE-MAPPING.md §pimono (global-only passthrough + D-33 .pi/mcp.json deep)",
		rowCount:       3,
		rules: map[string]wantRule{
			"commands/**/*.md": {to: ".pi/agent/prompts/**/*", merge: adapter.MergeReplace, hasXform: false},
			"skills/**/*":      {to: ".pi/agent/skills/**/*", merge: adapter.MergeReplace, hasXform: false},
			"mcp/**/*":         {to: ".pi/mcp.json", merge: adapter.MergeDeep, hasXform: true},
		},
		// pimono routes commands/skills/mcp; rules/, agents/, prompts/,
		// AGENTS.md, hooks/ have no rule → dropped (D-33: mcp NOT dropped).
		wantDropped: []string{"AGENTS.md", "agents", "hooks", "prompts", "rules"},
	},
}

// TestProjectionRules_Conformance is the cross-adapter spec-drift gate. For each
// of the five adapters it (1) asserts the ProjectionRules() route table FromGlob
// / ToGlob / Merge + Transform-presence against the literal expected fixture
// transcribed from OPENPACKAGE-MAPPING.md, and (2) asserts the EXACT drop set
// route.Project records over a plugin tree carrying every canonical source kind.
func TestProjectionRules_Conformance(t *testing.T) {
	all := adapters()

	for _, want := range conformanceTables {
		want := want
		t.Run(want.id, func(t *testing.T) {
			rp, ok := all[want.id]
			if !ok {
				t.Fatalf("adapter %q has no constructor entry in adapters()", want.id)
			}
			rules := rp.ProjectionRules()

			// Row-count closed-set check (ties the subtest to the mapping section).
			if len(rules) != want.rowCount {
				t.Errorf("%s: ProjectionRules row count = %d, want %d (per %s)",
					want.id, len(rules), want.rowCount, want.mappingSection)
			}

			// Per-row FromGlob / ToGlob / Merge / Transform-presence assertions.
			byFrom := map[string]route.Rule{}
			for _, r := range rules {
				if _, dup := byFrom[r.FromGlob]; dup {
					t.Errorf("%s: duplicate FromGlob %q", want.id, r.FromGlob)
				}
				byFrom[r.FromGlob] = r
			}
			for from, wr := range want.rules {
				r, ok := byFrom[from]
				if !ok {
					t.Errorf("%s: missing route FromGlob=%q (per %s)", want.id, from, want.mappingSection)
					continue
				}
				if r.ToGlob != wr.to {
					t.Errorf("%s: route %q ToGlob = %q, want %q (per %s)", want.id, from, r.ToGlob, wr.to, want.mappingSection)
				}
				if r.Merge != wr.merge {
					t.Errorf("%s: route %q Merge = %v, want %v (per %s)", want.id, from, r.Merge, wr.merge, want.mappingSection)
				}
				if (r.Transform != nil) != wr.hasXform {
					t.Errorf("%s: route %q Transform present = %v, want %v (per %s)", want.id, from, r.Transform != nil, wr.hasXform, want.mappingSection)
				}
			}
			// No unexpected extra rows.
			for from := range byFrom {
				if _, ok := want.rules[from]; !ok {
					t.Errorf("%s: unexpected route FromGlob=%q not in OPENPACKAGE-MAPPING expected table", want.id, from)
				}
			}

			// Drop-set assertion via the real route.Project engine.
			src := writeTree(t, fullTree)
			_, dropped, err := route.Project(rules, src, "")
			if err != nil {
				t.Fatalf("%s: route.Project: %v", want.id, err)
			}
			gotDropped := append([]string(nil), dropped...)
			sort.Strings(gotDropped)
			if !reflect.DeepEqual(gotDropped, want.wantDropped) {
				t.Errorf("%s: drop set = %v, want %v (per %s)", want.id, gotDropped, want.wantDropped, want.mappingSection)
			}
		})
	}
}

// writeTree materializes a rel-path → content map under a fresh temp dir and
// returns the root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll %q: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile %q: %v", rel, err)
		}
	}
	return root
}

// Registration-on-import is intentionally NOT asserted here: this external test
// shares a binary with the in-package registry_test.go, whose resetForTesting()
// teardown clears the registry, so an adapter.Lookup/Iter walk from this package
// races that reset. Each subpackage already proves its own init() Register via
// its TestRegistry_RegistersOnImport; this file owns the route-table spec-drift
// gate only.

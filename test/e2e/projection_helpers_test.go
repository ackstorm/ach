//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Per-adapter projection-lifecycle e2e helpers — Plan 06-02 (VER-02).
//
// This file provides the shared, content-derived assertion surface for
// test/e2e/projection_lifecycle_test.go (the per-adapter hydrate → assert-
// native-dirs → drops-warned → state-records → uninstall-preserves-user-keys
// matrix). It reuses the Phase 7 CLI-engine harness primitives verbatim
// (phase7SuiteGuard, phase7AcquirePk, phase7BaseURL, phase7SeedXdgConfig,
// phase7DemoEnvironmentReady, phase7Workspace, phase7RunAchCli,
// phase7StripExitErr, phase7StatePath) — it declares NO new harness primitive
// that duplicates one of those.
//
// Why a descriptor table rather than golden bytes: the demo Environment ships
// the third-party `caveman` plugin (JuliusBrussee/caveman). Its resource shape
// is fixed by the upstream repo, not by an ACH fixture, so the assertions are
// derived from caveman's *actual* top-level kinds (probed against the kept
// cluster at authoring time):
//
//	agents/   — *.md  (cavecrew-builder.md, cavecrew-investigator.md, …)
//	commands/ — *.toml (caveman.toml, caveman-commit.toml, …)
//	skills/   — skills/<name>/SKILL.md (+ README.md, scripts/)
//	.claude-plugin/, src/, LICENSE, README.md  — NON-resource top-levels
//
// caveman ships NO top-level rules/, mcp/, prompts/, hooks/, or AGENTS.md. The
// descriptors therefore track ONLY the agents/commands/skills kinds the demo
// actually projects. If the demo Environment's caveman plugin shape evolves
// (a new kind lands, a kind is removed), update the per-adapter `nativeDirs`
// + `mustNotProject` + `drops` fields below; the matrix is intentionally
// scoped to "what the demo Environment serves", not "every kind an adapter
// could route" (Plan 06-02 <action>: do NOT fabricate fixture files the
// cluster does not serve).
//
// Per-adapter routing is the authoritative ProjectionRules() globs in
// internal/cli/adapter/<id>/<id>.go, applied to caveman's kinds:
//
//	claude-code  agents→.claude/agents/  commands→.claude/commands/  skills→.claude/skills/
//	codex        agents(*.md)→.codex/agents/*.toml  skills→.agents/skills/
//	             (commands rule is commands/**/*.md; caveman commands are .toml
//	              → NOT projected by codex, NOT a drop: the `commands` top-level
//	              still matches the rule's first segment, so route.Project does
//	              not add it to the dropped set — the individual .toml files are
//	              skipped by the WR-03 terminal-extension guard.)
//	gemini-cli   agents→.gemini/agents/  commands→.gemini/commands/  skills→.gemini/skills/
//	opencode     commands→.opencode/commands/  agents(*.md)→.opencode/agents/  skills→.opencode/skills/
//	pimono       skills→.pi/agent/skills/
//	             (commands rule is commands/**/*.md; caveman commands are .toml
//	              → NOT projected. NO agents rule → caveman's agents/ is DROPPED:
//	              pimono is the one adapter in this matrix whose drop set
//	              intersects caveman's kinds — `agents` appears in the WIRE-03
//	              end-of-hydration stderr warning.)
//
// The WIRE-03 / D-12 stderr warning format (commit.go warnDropped) is an
// attributed multi-line block, emitted ONLY when a KNOWN component kind has no
// rule for the active platform:
//
//	"warning: platform <id> does not support some plugin components — they were skipped:"
//	"    <kind> (plugins: <name, …>)"
//
// Metadata/docs/unknown top-levels (.claude-plugin, .codex-plugin, src,
// LICENSE, README.md) are now SILENTLY skipped — they never appear in the
// warning (route.KnownComponentKinds gates the drop set). So an adapter that
// routes all of caveman's component kinds (claude-code/codex/gemini/opencode)
// prints NO drop warning at all; only pimono (no `agents` rule) warns, naming
// `agents` + the caveman plugin. assertDropsWarned checks for the presence of
// the kinds we KNOW must (or must not) be dropped, not for an exact full list.

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectionDescriptor captures, per canonical platform id, the projection
// expectations for the demo Environment's caveman plugin.
type projectionDescriptor struct {
	// platformID is the canonical --platform value (claude-code / codex /
	// gemini-cli / opencode / pimono).
	platformID string

	// nativeDirs are the adapter-native destination directories under the
	// workspace root that MUST contain at least one projected file after a
	// caveman hydrate. Each is workspace-relative + forward-slashed.
	nativeDirs []string

	// mustNotProject are verbatim source-relative paths under the workspace
	// root that MUST NOT exist — the SC2 native-routing invariant (a
	// consistently-wrong remap would leak files to <toolRoot>/<kind>/ instead
	// of the adapter's native dir). Mirrors projection_rehydrate_test.go's
	// "<toolRoot>/rules/foo.md" leak check.
	mustNotProject []string

	// coOwnedFile is the adapter's deep-merge / composite co-owned runtime
	// file (workspace-relative). uninstall inverse-merges this file: ACH's
	// keys are subtracted while a pre-seeded user key survives. Derived from
	// each adapter's runtime-path const (claudecode settingsJSONPath, codex
	// configTOMLPath, gemini settingsJSONPath, opencode configJSONPath,
	// pimono mcpJSONPath).
	coOwnedFile string

	// coOwnedKind selects the pre-seed shape + the survivor assertion:
	// "json-mcp"  → {"mcpServers": {"<userKey>": …}} (claude / gemini)
	// "json-mcp-opencode" → {"mcp": {"<userKey>": …}} (opencode)
	// "toml-mcp"  → [mcp_servers.<userKey>] (codex)
	// "json-pi"   → {"mcpServers": {"<userKey>": …}} (pimono .pi/mcp.json)
	coOwnedKind string

	// mustDrop are kind names that MUST appear in the WIRE-03 stderr warning
	// for this adapter on a caveman hydrate (caveman ships the kind but the
	// adapter has no rule for it). Empty for adapters whose drop set does not
	// intersect caveman's kinds.
	mustDrop []string

	// mustNotDrop are kind names that MUST NOT appear in the stderr warning —
	// kinds the adapter actively projects (a regression that started dropping
	// a projected kind would surface here).
	mustNotDrop []string
}

// projectionDescriptors is the per-adapter table. Order mirrors the umbrella
// subtest order in projection_lifecycle_test.go. Kept in one place so a
// fixture-shape change is a single-table edit.
//
// NOTE on coOwnedFile + uninstall scope: uninstall's DEFAULT scope is
// context-only (prompts/plugins/artifacts). The co-owned MCP files below are
// written by the RUNTIME leg, so the lifecycle subtests pre-seed a user key,
// hydrate (ACH lands its keys alongside), then run `uninstall --include-runtime`
// so the inverse-merge actually touches the co-owned file and we can prove the
// user key survives the subtraction.
var projectionDescriptors = []projectionDescriptor{
	{
		platformID:     phase7PlatformClaudeCode, // "claude-code"
		nativeDirs:     []string{".claude/agents", ".claude/commands", ".claude/skills"},
		mustNotProject: []string{"agents", "commands", "skills"},
		coOwnedFile:    ".mcp.json", // claude reads MCP from project-root .mcp.json
		coOwnedKind:    "json-mcp",
		mustDrop:       nil, // caveman's kinds are all routed by claude-code.
		mustNotDrop:    []string{"agents", "commands", "skills"},
	},
	{
		platformID: phase7PlatformCodex, // "codex"
		// codex routes agents(*.md)→.codex/agents/*.toml, commands(*.md)→
		// .codex/prompts/, and skills→.agents/skills/. caveman commands are
		// .toml (not projected), but the demo env's feature-dev plugin ships
		// .md commands → they land under .codex/prompts/.
		nativeDirs:     []string{".codex/agents", ".codex/prompts", ".agents/skills"},
		mustNotProject: []string{"agents", "skills"},
		coOwnedFile:    ".codex/config.toml",
		coOwnedKind:    "toml-mcp",
		// caveman ships none of codex's drop kinds {rules, AGENTS.md, hooks};
		// `commands` matches the rule's first segment so it is NOT dropped.
		mustDrop:    nil,
		mustNotDrop: []string{"agents", "skills"},
	},
	{
		platformID:     phase7PlatformGemini, // "gemini-cli"
		nativeDirs:     []string{".gemini/agents", ".gemini/commands", ".gemini/skills"},
		mustNotProject: []string{"agents", "commands", "skills"},
		coOwnedFile:    ".gemini/settings.json",
		coOwnedKind:    "json-mcp",
		// gemini drops `hooks`; caveman has no top-level hooks/ → no drop.
		mustDrop:    nil,
		mustNotDrop: []string{"agents", "commands", "skills"},
	},
	{
		platformID:     phase7PlatformOpencode, // "opencode"
		nativeDirs:     []string{".opencode/commands", ".opencode/agents", ".opencode/skills"},
		mustNotProject: []string{"agents", "commands", "skills"},
		coOwnedFile:    ".opencode/opencode.json",
		coOwnedKind:    "json-mcp-opencode",
		mustDrop:       nil,
		mustNotDrop:    []string{"agents", "commands", "skills"},
	},
	{
		platformID: phase7PlatformPimono, // "pimono"
		// pimono routes commands(*.md)→.pi/agent/prompts/ and skills→.pi/agent/skills/.
		// caveman commands are .toml → not projected, but the demo env's
		// feature-dev plugin ships .md commands → they land under
		// .pi/agent/prompts/.
		nativeDirs:     []string{".pi/agent/skills", ".pi/agent/prompts"},
		mustNotProject: []string{"skills"},
		coOwnedFile:    ".pi/mcp.json",
		coOwnedKind:    "json-pi",
		// pimono has NO agents rule (D-35 drop set {rules, agents, AGENTS.md});
		// caveman ships agents/ → `agents` is dropped + warned.
		mustDrop: []string{"agents"},
		// skills is routed; .claude-plugin / README.md are now silently
		// skipped metadata — none may appear in the attributed warning.
		mustNotDrop: []string{"skills", ".claude-plugin", "README.md"},
	},
}

// phase7PlatformPimono is the canonical pimono id (the four other ids are
// declared in cli_hydrate_engine_test.go; pimono post-dates that file).
const phase7PlatformPimono = "pimono"

// assertProjectedNativeDirs stats each expected adapter-native dir under
// <output>/ and fails if it is absent OR contains no regular file. It also
// asserts the SC2 native-routing invariant: NO file leaked to the verbatim
// source-relative path <output>/<kind>/ (e.g. <output>/agents/) — a
// consistently-wrong remap would land files there instead of the adapter's
// native dir.
func assertProjectedNativeDirs(t *testing.T, output string, d projectionDescriptor) {
	t.Helper()
	for _, dir := range d.nativeDirs {
		abs := filepath.Join(output, filepath.FromSlash(dir))
		n := countRegularFilesUnder(abs)
		if n == 0 {
			t.Errorf("%s: expected native dir %s to contain >=1 projected file, found %d "+
				"(caveman hydrate should have routed this kind here)", d.platformID, dir, n)
		}
	}
	// SC2 leak check: the verbatim source-relative kind dir must not exist at
	// the workspace root (the projection must remap to the native dir).
	for _, kind := range d.mustNotProject {
		leak := filepath.Join(output, kind)
		if fi, err := os.Stat(leak); err == nil && fi.IsDir() {
			// Only a violation if it actually holds projected files; an empty
			// pre-created dir would be benign, but the engine never creates the
			// verbatim path, so any populated dir here is an SC2 break.
			if countRegularFilesUnder(leak) > 0 {
				t.Errorf("%s: SC2 violation — projected files leaked to verbatim source path %s "+
					"(want them under the adapter-native dir, not <output>/%s/)",
					d.platformID, leak, kind)
			}
		}
	}
}

// assertDropsWarned checks the WIRE-03 / D-12 attributed drop warning. For
// each kind in d.mustDrop it asserts the warning block names the kind; for each
// in d.mustNotDrop it asserts the kind is absent from the block. When d.mustDrop
// is empty there is no "does not support" block at all (metadata/docs are now
// silent), so the negative assertions are vacuously satisfied.
func assertDropsWarned(t *testing.T, platformID string, stderr []byte, d projectionDescriptor) {
	t.Helper()
	s := string(stderr)
	marker := "warning: platform " + platformID + " does not support"

	if len(d.mustDrop) > 0 {
		if !strings.Contains(s, marker) {
			t.Errorf("%s: expected an attributed drop warning (kinds %v) but stderr had none\nstderr=%s",
				platformID, d.mustDrop, stderr)
			return
		}
		body := dropBody(s, marker)
		for _, kind := range d.mustDrop {
			if !strings.Contains(body, kind) {
				t.Errorf("%s: drop warning missing expected kind %q\nbody=%q", platformID, kind, body)
			}
		}
	}

	// Negative invariant: a projected kind (or benign metadata) must never be
	// reported as dropped. Only meaningful when a block exists.
	if strings.Contains(s, marker) {
		body := dropBody(s, marker)
		for _, kind := range d.mustNotDrop {
			if strings.Contains(body, kind) {
				t.Errorf("%s: drop warning wrongly lists kind %q\nbody=%q", platformID, kind, body)
			}
		}
	}
}

// dropBody returns the attributed-warning block: the substring from marker up
// to (but excluding) the next distinct "warning:" line (the MCP-shadow warning
// or pk_ warnings), so kind checks scan only the "does not support" body.
func dropBody(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	rest := s[i:]
	if j := strings.Index(rest[len(marker):], "\nwarning:"); j >= 0 {
		return rest[:len(marker)+j]
	}
	return rest
}

// assertStateRecordsPlugins reads state.json v2 and asserts (a) schemaVersion
// is "2" and (b) the Plugins[] bucket records >=1 entry whose Target lands
// under one of the adapter's expected native dirs. The exact Target set is
// caveman-shape-dependent, so the assertion is "the projected targets are
// recorded under a native dir", not an exact list.
func assertStateRecordsPlugins(t *testing.T, statePath string, d projectionDescriptor) {
	t.Helper()
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("%s: read state.json at %s: %v", d.platformID, statePath, err)
	}
	var doc struct {
		SchemaVersion string `json:"schemaVersion"`
		Plugins       []struct {
			Target string `json:"target"`
			Hash   string `json:"hash"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: parse state.json: %v\nbytes=%s", d.platformID, err, raw)
	}
	if doc.SchemaVersion != "3" {
		t.Errorf("%s: state.json schemaVersion=%q, want \"3\"", d.platformID, doc.SchemaVersion)
	}
	if len(doc.Plugins) == 0 {
		t.Fatalf("%s: state.json Plugins[] is empty — projected caveman resources not recorded\nbytes=%s",
			d.platformID, raw)
	}
	// Every recorded plugin Target must sit under one of the adapter's native
	// dirs (the projection remapped it). At least one is enough to prove the
	// recording wiring; we assert ALL to also catch a stray verbatim Target.
	for _, p := range doc.Plugins {
		if p.Target == "" {
			t.Errorf("%s: state Plugins[] entry has empty Target", d.platformID)
			continue
		}
		under := false
		for _, dir := range d.nativeDirs {
			if strings.HasPrefix(filepath.ToSlash(p.Target), dir+"/") {
				under = true
				break
			}
		}
		if !under {
			t.Errorf("%s: state Plugins[] Target %q not under any native dir %v "+
				"(SC2: projected targets must be recorded at their native path)",
				d.platformID, p.Target, d.nativeDirs)
		}
		if p.Hash == "" {
			t.Errorf("%s: state Plugins[] Target %q has empty Hash", d.platformID, p.Target)
		}
	}
}

// seedCoOwnedUserKey writes a pre-existing USER entry into the adapter's
// co-owned deep-merge file BEFORE hydrate, so the post-uninstall assertion can
// prove the inverse-merge subtracted only ACH's keys and left the user key
// intact. Returns the userKey token the survivor assertion looks for.
//
// Threat-model T-06-04: the seeded server is a placeholder localhost URL with
// NO real credential — the x-ach-key bearer arrives only via the real demo pk_
// flow, never hardcoded here.
func seedCoOwnedUserKey(t *testing.T, output string, d projectionDescriptor) string {
	t.Helper()
	const userKey = "user-personal-e2e"
	target := filepath.Join(output, filepath.FromSlash(d.coOwnedFile))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("%s: seed co-owned mkdir %s: %v", d.platformID, filepath.Dir(target), err)
	}
	var body []byte
	switch d.coOwnedKind {
	case "json-mcp", "json-pi":
		body = []byte(`{
  "mcpServers": {
    "` + userKey + `": {"url": "http://localhost:9/user", "headers": {}}
  }
}
`)
	case "json-mcp-opencode":
		body = []byte(`{
  "mcp": {
    "` + userKey + `": {"type": "remote", "url": "http://localhost:9/user"}
  }
}
`)
	case "toml-mcp":
		body = []byte("[mcp_servers." + userKey + "]\n" +
			"url = \"http://localhost:9/user\"\n")
	default:
		t.Fatalf("%s: unknown coOwnedKind %q", d.platformID, d.coOwnedKind)
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatalf("%s: seed co-owned write %s: %v", d.platformID, target, err)
	}
	return userKey
}

// assertCoOwnedUserKeyPreserved reads the co-owned file AFTER uninstall and
// asserts the pre-seeded user key/section survives (inverse-merge subtracted
// only ACH's keys). When the whole file was removed (file-owned teardown), the
// user key is gone — that is a failure for a co-owned deep-merge file. The
// check is a substring match on the userKey token, robust to the JSON/TOML
// re-encoding the inverse-merge engine performs.
func assertCoOwnedUserKeyPreserved(t *testing.T, output string, d projectionDescriptor, userKey string) {
	t.Helper()
	target := filepath.Join(output, filepath.FromSlash(d.coOwnedFile))
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Errorf("%s: co-owned file %s missing after uninstall (want user key %q preserved): %v",
			d.platformID, d.coOwnedFile, userKey, err)
		return
	}
	if !strings.Contains(string(raw), userKey) {
		t.Errorf("%s: uninstall subtracted the user key %q from co-owned file %s "+
			"(inverse-merge must remove only ACH's keys)\ncontents=%s",
			d.platformID, userKey, d.coOwnedFile, raw)
	}
}

// assertFileOwnedResourcesGone asserts the file-owned projected resources
// (the caveman native-dir files) are removed after uninstall. Co-owned
// deep-merge files are NOT checked here (assertCoOwnedUserKeyPreserved owns
// that path). A clean uninstall leaves each native dir empty (or absent).
func assertFileOwnedResourcesGone(t *testing.T, output string, d projectionDescriptor) {
	t.Helper()
	for _, dir := range d.nativeDirs {
		abs := filepath.Join(output, filepath.FromSlash(dir))
		if n := countRegularFilesUnder(abs); n > 0 {
			t.Errorf("%s: file-owned projected resources still present under %s after uninstall (found %d, want 0)",
				d.platformID, dir, n)
		}
	}
}

// countRegularFilesUnder returns the number of regular files under root,
// recursively. Returns 0 when root is absent.
func countRegularFilesUnder(root string) int {
	var n int
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // absent/partial tree → count what exists
		}
		if info.Mode().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

// snapshotProjectedFiles walks every adapter-native projection dir from the
// descriptor under <output>/, reads each regular file, and returns a
// workspace-relative-path → bytes map. Used by the VER-03 idempotence matrix
// (projection_idempotence_test.go) to capture the projected tree after hydrate
// run 1 and compare it byte-for-byte after run 2.
//
// Keys are forward-slashed and relative to output (so they are stable across
// the two runs against the SAME workspace). The scope is intentionally limited
// to the descriptor's nativeDirs — the demo Environment's caveman plugin only
// projects the kinds the 06-02 descriptor enumerates, so a dir the adapter
// never populates contributes nothing (and is not an error). Co-owned runtime
// deep-merge files (descriptor.coOwnedFile) are NOT snapshotted here: they are
// the RUNTIME leg's concern and carry the live x-ach-key bearer; the projection
// idempotence proof gates the file-owned projected resources, whose byte-no-op
// is what FMT-05 deterministic-encode guarantees.
func snapshotProjectedFiles(t *testing.T, output string, d projectionDescriptor) map[string][]byte {
	t.Helper()
	snap := make(map[string][]byte)
	for _, dir := range d.nativeDirs {
		abs := filepath.Join(output, filepath.FromSlash(dir))
		walkErr := filepath.Walk(abs, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil //nolint:nilerr // absent native dir → nothing to snapshot
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, relErr := filepath.Rel(output, p)
			if relErr != nil {
				return relErr
			}
			b, readErr := os.ReadFile(p) //nolint:gosec // p is under the test temp workspace
			if readErr != nil {
				return readErr
			}
			snap[filepath.ToSlash(rel)] = b
			return nil
		})
		if walkErr != nil {
			t.Fatalf("%s: snapshotProjectedFiles walk %s: %v", d.platformID, dir, walkErr)
		}
	}
	return snap
}

// assertSnapshotsByteIdentical fails if the two projected-file snapshots are not
// byte-for-byte identical. It catches three distinct churn signatures:
//
//  1. a file present in both whose bytes differ (a non-deterministic encode —
//     the load-bearing FMT-05 failure for the codex/opencode TOML/JSON
//     conversions);
//  2. a file present in `before` but MISSING from `after` (the second hydrate
//     dropped a projected resource);
//  3. a file present in `after` but ABSENT from `before` (the second hydrate
//     introduced spurious new output — churn in the other direction).
//
// All mismatches are reported (not fail-fast) so a single run surfaces the full
// drift set. The maps are descriptor-scoped (see snapshotProjectedFiles), so an
// empty `before` AND empty `after` is a benign no-projection match — the caller
// guards against an unexpectedly-empty projection via assertProjectedNativeDirs.
func assertSnapshotsByteIdentical(t *testing.T, before, after map[string][]byte) {
	t.Helper()
	for path, b := range before {
		a, ok := after[path]
		if !ok {
			t.Errorf("re-hydrate dropped projected file %q (present after run 1, missing after run 2)", path)
			continue
		}
		if !bytes.Equal(a, b) {
			t.Errorf("re-hydrate changed projected file %q bytes (FMT-05 non-deterministic encode?):\n"+
				"  run1=%q\n  run2=%q", path, b, a)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("re-hydrate introduced spurious projected file %q (absent after run 1, present after run 2)", path)
		}
	}
}

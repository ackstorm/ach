// SPDX-License-Identifier: Apache-2.0

package pimono

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

func TestPimono_ID(t *testing.T) {
	a := &Adapter{}
	if got := a.ID(); got != "pimono" {
		t.Fatalf("ID() = %q, want %q", got, "pimono")
	}
}

func TestPimono_Aliases(t *testing.T) {
	a := &Adapter{}
	got := a.Aliases()
	want := []string{"pi", "pi-mono"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Aliases() = %v, want %v", got, want)
	}
}

func TestPimono_Detect_NoSignals_ZeroMatch(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "" {
		t.Errorf("Detect(empty root) returned ID=%q, want empty", got.ID)
	}
	if got.Confidence != 0 {
		t.Errorf("Detect(empty root) returned Confidence=%v, want zero", got.Confidence)
	}
}

func TestPimono_Detect_OneSignal_LowConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if err := os.MkdirAll(filepath.Join(tmp, ".pi"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "pimono" {
		t.Errorf("Detect returned ID=%q, want %q", got.ID, "pimono")
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("Detect with 1 signal returned Confidence=%v, want ConfidenceLow", got.Confidence)
	}
	if len(got.Reasons) != 1 {
		t.Errorf("Detect with 1 signal returned %d Reasons, want 1", len(got.Reasons))
	}
}

func TestPimono_Detect_TwoSignals_MediumConfidence(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if err := os.MkdirAll(filepath.Join(tmp, ".pi", "agent"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Confidence != adapter.ConfidenceMedium {
		t.Errorf("Detect with 2 signals returned Confidence=%v, want ConfidenceMedium", got.Confidence)
	}
}

func TestPimono_Detect_HighConfidence_AllSignals(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Local signals: .pi/, .pi/agent/, .pi/mcp.json
	if err := os.MkdirAll(filepath.Join(tmp, ".pi", "agent"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".pi", "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Global signal: $HOME/.pi/mcp.json (WR-03: specific file, not bare dir)
	if err := os.MkdirAll(filepath.Join(fakeHome, ".pi"), 0o755); err != nil {
		t.Fatalf("MkdirAll fakeHome: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeHome, ".pi", "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile fakeHome: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Confidence != adapter.ConfidenceHigh {
		t.Errorf("Detect with 4 signals returned Confidence=%v, want ConfidenceHigh", got.Confidence)
	}
	if len(got.Reasons) < 3 {
		t.Errorf("Detect with 4 signals returned %d Reasons, want >=3", len(got.Reasons))
	}
}

// TestPimono_Detect_GlobalOnly exercises the WR-03 path: the global hint
// ($HOME/.pi/mcp.json) is present but the scanned root has zero local signals.
// Contract: exactly 1 signal → ConfidenceLow, ID set, single reason naming the
// global hint. This is the path most likely to cause a spurious autodetect
// multi-match, so the weakest signal must rank only Low. The probe is the
// specific mcp.json file (NOT the bare ~/.pi dir) so an unrelated scratch
// directory no longer trips it.
func TestPimono_Detect_GlobalOnly(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir() // empty root — zero local signals
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if err := os.MkdirAll(filepath.Join(fakeHome, ".pi"), 0o755); err != nil {
		t.Fatalf("MkdirAll fakeHome: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeHome, ".pi", "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile fakeHome: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "pimono" {
		t.Errorf("global-only Detect ID = %q, want %q", got.ID, "pimono")
	}
	if got.Confidence != adapter.ConfidenceLow {
		t.Errorf("global-only Detect Confidence = %v, want ConfidenceLow", got.Confidence)
	}
	if len(got.Reasons) != 1 {
		t.Fatalf("global-only Detect returned %d Reasons, want exactly 1: %v", len(got.Reasons), got.Reasons)
	}
	if got.Reasons[0] != "found ~/.pi/mcp.json (global mode)" {
		t.Errorf("global-only Detect reason = %q, want global-hint reason", got.Reasons[0])
	}
}

// TestPimono_Detect_GlobalDirOnly locks WR-03's lower-false-positive intent:
// a bare $HOME/.pi DIRECTORY (no mcp.json inside) and an empty root must NOT
// produce a match — the specific-file probe ignores the scratch dir.
func TestPimono_Detect_GlobalDirOnly(t *testing.T) {
	a := &Adapter{}
	tmp := t.TempDir() // empty root — zero local signals
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if err := os.MkdirAll(filepath.Join(fakeHome, ".pi"), 0o755); err != nil {
		t.Fatalf("MkdirAll fakeHome: %v", err)
	}

	got, err := a.Detect(tmp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ID != "" {
		t.Errorf("bare ~/.pi dir Detect returned ID=%q, want empty (no match)", got.ID)
	}
	if got.Confidence != 0 {
		t.Errorf("bare ~/.pi dir Detect returned Confidence=%v, want zero", got.Confidence)
	}
}

// buildManifest constructs a non-nil Manifest with 2 MCP servers — pimono
// has no A2A surface, so A2AAgents is intentionally omitted.
func buildManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			Models: []manifest.ContentRef{
				{ID: "demo-model", Endpoint: "http://localhost:8080/v1"},
			},
			MCPServers: []manifest.ContentRef{
				{ID: "demo-mcp-jwt", Endpoint: "http://localhost:8080/mcp/demo-mcp-jwt"},
				{ID: "demo-mcp-nojwt", Endpoint: "http://localhost:8080/mcp/demo-mcp-nojwt"},
			},
		},
		Context: &manifest.ContextBlock{},
	}
}

func TestPimono_RenderRuntime_McpJSONShape(t *testing.T) {
	a := &Adapter{}
	m := buildManifest()

	writes, err := a.RenderRuntime(context.Background(), m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("RenderRuntime returned %d FileWrites, want 1", len(writes))
	}
	w := writes[0]
	if w.Path != ".pi/mcp.json" {
		t.Errorf("FileWrite.Path = %q, want %q", w.Path, ".pi/mcp.json")
	}
	if w.Merge != adapter.MergeDeep {
		t.Errorf("FileWrite.Merge = %v, want MergeDeep", w.Merge)
	}
	if len(w.Keys) != 2 {
		t.Errorf("FileWrite.Keys count = %d, want 2 (2 mcpServers)", len(w.Keys))
	}
	want := []string{"mcpServers.demo-mcp-jwt", "mcpServers.demo-mcp-nojwt"}
	if !reflect.DeepEqual(w.Keys, want) {
		t.Errorf("FileWrite.Keys = %v, want sorted %v", w.Keys, want)
	}

	var got struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
		A2AAgents map[string]any `json:"a2aAgents"`
	}
	if err := json.Unmarshal(w.Content, &got); err != nil {
		t.Fatalf("json.Unmarshal Content: %v", err)
	}
	if len(got.MCPServers) != 2 {
		t.Errorf("mcpServers map size = %d, want 2", len(got.MCPServers))
	}
	if got.MCPServers["demo-mcp-jwt"].URL != "http://localhost:8080/mcp/demo-mcp-jwt" {
		t.Errorf("MCP url = %q, want endpoint from manifest", got.MCPServers["demo-mcp-jwt"].URL)
	}
	// Pi's HTTP MCP schema is url + headers with NO `type` field (stdio uses
	// command/args; HTTP is inferred from url). Schema:
	// https://github.com/nicobailon/pi-mcp-adapter.
	if got.MCPServers["demo-mcp-jwt"].Type != "" {
		t.Errorf("MCP type must be absent for pi (Pi defines no type key); got %q", got.MCPServers["demo-mcp-jwt"].Type)
	}
	if _, ok := got.MCPServers["demo-mcp-jwt"].Headers["x-ach-key"]; !ok {
		t.Errorf("MCP headers must carry x-ach-key; got %v", got.MCPServers["demo-mcp-jwt"].Headers)
	}
	// pimono has NO a2aAgents surface — the key must be absent entirely.
	if got.A2AAgents != nil {
		t.Errorf("pimono must not emit a2aAgents; got %v", got.A2AAgents)
	}
}

func TestPimono_RenderRuntime_CredentialPropagation(t *testing.T) {
	a := &Adapter{}
	m := buildManifest()

	ctx := adapter.WithCredential(context.Background(), "pk_demo")
	writes, err := a.RenderRuntime(ctx, m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	if !bytes.Contains(writes[0].Content, []byte(`"x-ach-key": "pk_demo"`)) {
		t.Errorf("rendered content missing x-ach-key credential header; got:\n%s", string(writes[0].Content))
	}
}

func TestPimono_RenderRuntime_EmptyRuntime_Idempotent(t *testing.T) {
	a := &Adapter{}
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime:       &manifest.RuntimeBlock{},
		Context:       &manifest.ContextBlock{},
	}

	writes, err := a.RenderRuntime(context.Background(), m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("RenderRuntime returned %d FileWrites, want 1 (deterministic empty shape)", len(writes))
	}
	var got struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(writes[0].Content, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(got.MCPServers) != 0 {
		t.Errorf("empty runtime → mcpServers should be empty, got %d entries", len(got.MCPServers))
	}
	if len(writes[0].Keys) != 0 {
		t.Errorf("empty runtime → Keys should be empty, got %d entries", len(writes[0].Keys))
	}

	// Re-render must be byte-identical (deterministic, re-hydrate idempotent).
	writes2, err := a.RenderRuntime(context.Background(), m, nil)
	if err != nil {
		t.Fatalf("RenderRuntime (second): %v", err)
	}
	if !bytes.Equal(writes[0].Content, writes2[0].Content) {
		t.Errorf("empty-runtime output not deterministic:\nfirst:  %q\nsecond: %q", writes[0].Content, writes2[0].Content)
	}
}

func TestPimono_RenderRuntime_NilManifest_Errors(t *testing.T) {
	a := &Adapter{}
	_, err := a.RenderRuntime(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("RenderRuntime(nil manifest) returned nil error; want error")
	}
}

// TestPimono_ProjectionRules asserts the exactly-3-row table: two MergeReplace
// passthrough rows (commands→prompts, skills→skills) with no Transform, and the
// mcp/**/* MergeDeep row wiring the mcpDeepKeys Transform. NO rules/agents/
// AGENTS.md rows (they fall to route.Project's drop set); NO root catch-all.
func TestPimono_ProjectionRules(t *testing.T) {
	rules := (&Adapter{}).ProjectionRules()
	if len(rules) != 3 {
		t.Fatalf("ProjectionRules returned %d rows, want exactly 3", len(rules))
	}

	type rowFields struct {
		to       string
		merge    adapter.MergeKind
		hasXform bool
	}
	byFrom := map[string]*rowFields{}
	for _, r := range rules {
		if _, dup := byFrom[r.FromGlob]; dup {
			t.Fatalf("ProjectionRules has duplicate FromGlob %q", r.FromGlob)
		}
		byFrom[r.FromGlob] = &rowFields{to: r.ToGlob, merge: r.Merge, hasXform: r.Transform != nil}
	}

	// Row 1: commands/**/*.md → .pi/agent/prompts/**/* (MergeReplace, no Transform).
	cmd, ok := byFrom["commands/**/*.md"]
	if !ok {
		t.Fatalf("ProjectionRules missing commands/**/*.md row")
	}
	if cmd.to != ".pi/agent/prompts/**/*" {
		t.Errorf("commands row ToGlob = %q, want %q", cmd.to, ".pi/agent/prompts/**/*")
	}
	if cmd.merge != adapter.MergeReplace {
		t.Errorf("commands row Merge = %v, want MergeReplace", cmd.merge)
	}
	if cmd.hasXform {
		t.Errorf("commands row must have nil Transform")
	}

	// Row 2: skills/**/* → .pi/agent/skills/**/* (MergeReplace, no Transform).
	sk, ok := byFrom["skills/**/*"]
	if !ok {
		t.Fatalf("ProjectionRules missing skills/**/* row")
	}
	if sk.to != ".pi/agent/skills/**/*" {
		t.Errorf("skills row ToGlob = %q, want %q", sk.to, ".pi/agent/skills/**/*")
	}
	if sk.merge != adapter.MergeReplace {
		t.Errorf("skills row Merge = %v, want MergeReplace", sk.merge)
	}
	if sk.hasXform {
		t.Errorf("skills row must have nil Transform")
	}

	// Row 3: mcp/**/* → .pi/mcp.json (MergeDeep, Transform wired).
	mcp, ok := byFrom["mcp/**/*"]
	if !ok {
		t.Fatalf("ProjectionRules missing mcp/**/* row")
	}
	if mcp.to != ".pi/mcp.json" {
		t.Errorf("mcp row ToGlob = %q, want %q", mcp.to, ".pi/mcp.json")
	}
	if mcp.merge != adapter.MergeDeep {
		t.Errorf("mcp row Merge = %v, want MergeDeep", mcp.merge)
	}
	if !mcp.hasXform {
		t.Errorf("mcp row must wire a non-nil Transform (mcpDeepKeys)")
	}

	// NO rules/agents/AGENTS.md rows; NO OpenPackage root catch-all.
	for _, banned := range []string{"rules/**/*", "agents/**/*", "AGENTS.md", "root/**/*"} {
		if _, ok := byFrom[banned]; ok {
			t.Errorf("ProjectionRules must NOT carry a %q row", banned)
		}
	}
}

// TestPimono_McpDeepKeys_Enumerates: top-level mcpServers keys enumerated
// sorted, input bytes returned unchanged (D-03 byte discipline).
func TestPimono_McpDeepKeys_Enumerates(t *testing.T) {
	in := []byte(`{"mcpServers":{"b":{"type":"http","url":"https://b"},"a":{"type":"http","url":"https://a"}}}`)

	out, keys, err := mcpDeepKeys("mcp/servers.json", in)
	if err != nil {
		t.Fatalf("mcpDeepKeys returned error: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("out bytes differ from in: got %q, want %q (no byte conversion)", out, in)
	}
	want := []string{"mcpServers.a", "mcpServers.b"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want sorted %v", keys, want)
	}
}

// TestPimono_McpDeepKeys_NoA2A: pimono enumerates ONLY mcpServers — an a2aAgents
// branch in the input is NOT enumerated (pimono has no a2a surface).
func TestPimono_McpDeepKeys_NoA2A(t *testing.T) {
	in := []byte(`{"mcpServers":{"srv":{"type":"http"}},"a2aAgents":{"agt":{"type":"http"}}}`)

	out, keys, err := mcpDeepKeys("mcp/x.json", in)
	if err != nil {
		t.Fatalf("mcpDeepKeys returned error: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("out bytes differ from in: got %q", out)
	}
	want := []string{"mcpServers.srv"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v (a2aAgents must NOT be enumerated)", keys, want)
	}
}

func TestPimono_McpDeepKeys_Malformed(t *testing.T) {
	in := []byte(`{"mcpServers": this is not json}`)

	out, keys, err := mcpDeepKeys("mcp/bad.json", in)
	if err == nil {
		t.Fatalf("expected error on malformed JSON, got nil (out=%q keys=%v)", out, keys)
	}
	if out != nil || keys != nil {
		t.Errorf("on error want out==nil keys==nil, got out=%q keys=%v", out, keys)
	}
}

// TestPimono_GlobalOnly_Conformance pins pimono's global-only / passthrough
// nature plus the D-33 .pi/mcp.json deep-merge keys (VER-01, Phase 06), against
// OPENPACKAGE-MAPPING.md §pimono. It asserts, via the REAL route.Project engine:
//
//	(a) the two passthrough globs route verbatim to .pi/agent/prompts/ and
//	    .pi/agent/skills/ as MergeReplace (file-owned, byte-identical);
//	(b) the D-33 mcp/**/* → .pi/mcp.json rule is MergeDeep with mcpDeepKeys
//	    enumerating the top-level mcpServers.<id> keys;
//	(c) the drop set is EXACTLY {AGENTS.md, agents, rules} — mcp is NOT dropped
//	    (D-33 routes it), and there is no root catch-all (D-36).
func TestPimono_GlobalOnly_Conformance(t *testing.T) {
	src := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll %q: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile %q: %v", rel, err)
		}
	}
	const cmdBody = "# grunt command\n"
	const skillBody = "skill body\n"
	mustWrite("commands/grunt.md", cmdBody)
	mustWrite("skills/fire/skill.md", skillBody)
	mustWrite("mcp/servers.json", `{"mcpServers":{"b":{"url":"https://b"},"a":{"url":"https://a"}}}`)
	// Dropped-by-omission kinds (no rule): rules/, agents/, AGENTS.md.
	mustWrite("rules/style.md", "rule\n")
	mustWrite("agents/cave.md", "---\nname: cave\n---\nhi\n")
	mustWrite("AGENTS.md", "agents prose\n")

	rules := (&Adapter{}).ProjectionRules()
	pr, err := route.Project(rules, src, "")
	if err != nil {
		t.Fatalf("route.Project: %v", err)
	}

	byPath := map[string]*adapter.FileWrite{}
	for i := range pr.FileWrites {
		byPath[filepath.ToSlash(pr.FileWrites[i].Path)] = &pr.FileWrites[i]
	}

	// (a) commands → .pi/agent/prompts/ verbatim MergeReplace.
	cmd, ok := byPath[".pi/agent/prompts/grunt.md"]
	if !ok {
		t.Fatalf("no FileWrite for .pi/agent/prompts/grunt.md; got %v", keysOf(byPath))
	}
	if string(cmd.Content) != cmdBody {
		t.Errorf("commands passthrough not byte-identical: got %q, want %q", cmd.Content, cmdBody)
	}
	if cmd.Merge != adapter.MergeReplace {
		t.Errorf("commands Merge = %v, want MergeReplace", cmd.Merge)
	}

	// skills → .pi/agent/skills/ verbatim MergeReplace.
	sk, ok := byPath[".pi/agent/skills/fire/skill.md"]
	if !ok {
		t.Fatalf("no FileWrite for .pi/agent/skills/fire/skill.md; got %v", keysOf(byPath))
	}
	if string(sk.Content) != skillBody {
		t.Errorf("skills passthrough not byte-identical: got %q, want %q", sk.Content, skillBody)
	}
	if sk.Merge != adapter.MergeReplace {
		t.Errorf("skills Merge = %v, want MergeReplace", sk.Merge)
	}

	// (b) D-33 mcp/**/* → .pi/mcp.json MergeDeep with enumerated mcpServers.<id> keys.
	mcp, ok := byPath[".pi/mcp.json"]
	if !ok {
		t.Fatalf("no FileWrite for .pi/mcp.json; got %v", keysOf(byPath))
	}
	if mcp.Merge != adapter.MergeDeep {
		t.Errorf("mcp Merge = %v, want MergeDeep", mcp.Merge)
	}
	if !reflect.DeepEqual(mcp.Keys, []string{"mcpServers.a", "mcpServers.b"}) {
		t.Errorf("mcp Keys = %v, want sorted [mcpServers.a mcpServers.b]", mcp.Keys)
	}

	// (c) drop set EXACTLY {AGENTS.md, agents, rules} — mcp NOT dropped (D-33).
	wantDropped := []string{"AGENTS.md", "agents", "rules"}
	if !reflect.DeepEqual(pr.Dropped, wantDropped) {
		t.Errorf("dropped = %v, want %v (mcp must NOT be dropped — D-33)", pr.Dropped, wantDropped)
	}
}

// keysOf returns the sorted path keys of a FileWrite-by-path map for diagnostics.
func keysOf(m map[string]*adapter.FileWrite) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPimono_RegistersOnImport(t *testing.T) {
	got, ok := adapter.Lookup("pimono")
	if !ok {
		t.Fatal("adapter.Lookup(\"pimono\") returned false; init() did not register")
	}
	if got.ID() != "pimono" {
		t.Errorf("Lookup returned adapter with ID %q, want %q", got.ID(), "pimono")
	}
	for _, alias := range []string{"pi", "pi-mono", "PI", "Pi-Mono"} {
		if _, ok := adapter.Lookup(alias); !ok {
			t.Errorf("adapter.Lookup(%q) returned false; alias did not register", alias)
		}
	}
}

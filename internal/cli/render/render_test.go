// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
)

const (
	testEK1 = "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAabcd"
	testEK2 = "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234"
)

func TestFormatProfileList(t *testing.T) {
	if got := FormatProfileList(&config.File{}); !strings.Contains(got, "No profiles configured") {
		t.Errorf("empty: %q", got)
	}
	got := FormatProfileList(&config.File{Default: "prod", Profiles: map[string]*config.Profile{
		"ci":   {URL: "https://ci.example", Key: testEK1},
		"none": {URL: "https://n.example"},
		"prod": {URL: "https://prod.example", OAuth: &config.OAuthCreds{AccessToken: "a.b.c"},
			Keys: map[string]config.SavedKey{"a": {ID: "ekid_a", Key: testEK1}, "b": {ID: "ekid_b", Key: testEK2}}},
	}})
	want := []string{
		"CURRENT NAME URL AUTH SAVED KEYS",
		"ci https://ci.example key 0",
		"none https://n.example none 0",
		"* prod https://prod.example session 2",
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got:\n%s", got)
	}
	for i, w := range want {
		if l := strings.Join(strings.Fields(lines[i]), " "); l != w {
			t.Errorf("line %d = %q; want %q", i, l, w)
		}
	}
}

func TestFormatProfileShow(t *testing.T) {
	dep := &config.Profile{URL: "https://hub.example", Key: testEK1,
		Keys: map[string]config.SavedKey{"laptop": {ID: "ekid_1", Key: testEK2}}}
	got := FormatProfileShow("ci", dep, false)
	for _, w := range []string{"Profile  ci", "URL      https://hub.example", "Auth     key ek-****abcd", "laptop  ek-****1234  (ekid_1)"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, testEK1) || strings.Contains(got, testEK2) {
		t.Errorf("plaintext leaked without reveal:\n%s", got)
	}
	rev := FormatProfileShow("ci", dep, true)
	if !strings.Contains(rev, testEK1) || !strings.Contains(rev, testEK2) {
		t.Errorf("reveal must print the keys in full:\n%s", rev)
	}
	if s := FormatProfileShow("p", &config.Profile{URL: "https://x", OAuth: &config.OAuthCreds{}}, false); !strings.Contains(s, "Auth     session") || !strings.Contains(s, "(none)") {
		t.Errorf("oauth profile:\n%s", s)
	}
}

// TestFormatEnvList asserts the EnvView slice renders with header + rows.
func TestFormatEnvList(t *testing.T) {
	envs := []EnvView{
		{Name: "demo", Namespace: "ach-system", Status: "Available"},
		{Name: "staging", Namespace: "ach-system", Status: "Pending"},
	}
	got := FormatEnvList(envs)
	// Header columns.
	if !strings.Contains(got, "NAME") {
		t.Errorf("missing 'NAME' header; got: %s", got)
	}
	if strings.Contains(got, "NAMESPACE") {
		t.Errorf("NAMESPACE column is gone (always the release namespace); got: %s", got)
	}
	if !strings.Contains(got, "STATUS") {
		t.Errorf("missing 'STATUS' header; got: %s", got)
	}
	// Rows present.
	if !strings.Contains(got, "demo") {
		t.Errorf("missing 'demo' row; got: %s", got)
	}
	if !strings.Contains(got, "staging") {
		t.Errorf("missing 'staging' row; got: %s", got)
	}
	if !strings.Contains(got, "Available") {
		t.Errorf("missing 'Available' status; got: %s", got)
	}
}

// TestFormatEnvList_Empty asserts the empty-slice branch returns a
// "No environments visible" stub.
func TestFormatEnvList_Empty(t *testing.T) {
	got := FormatEnvList(nil)
	if !strings.Contains(got, "No environments") {
		t.Errorf("expected 'No environments' marker; got: %s", got)
	}
}

// TestFormatEnvDescribe_Available asserts the available=true branch
// renders both Runtime and Context sub-tables AND surfaces the
// per-runtime `endpoint` + per-context `downloadUrl` strings (W3
// canonical hydrate wire-format).
func TestFormatEnvDescribe_Available(t *testing.T) {
	env := EnvView{Name: "demo", Namespace: "ach-system", Status: "Available"}
	h := &HydrateView{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: BlockView{
			Models: []RuntimeItem{
				{Name: "gpt-4", ID: "mdl_gpt4", Endpoint: "https://hub.example/v1"},
			},
			MCPServers: []RuntimeItem{
				{Name: "ctx7", ID: "mcp_ctx7", Endpoint: "https://hub.example/mcp/ctx7"},
			},
		},
		Context: BlockView{
			Plugins: []ContextItem{
				{Name: "caveman", ID: "plg_caveman", DownloadURL: "https://hub.example/content/plugin/caveman"},
			},
		},
	}
	got := FormatEnvDescribe(env, h, true)

	// Per-W3 phase goal: rendered Runtime block surfaces each item's
	// `endpoint` — literal substring check.
	if !strings.Contains(got, "https://hub.example/v1") {
		t.Errorf("missing runtime endpoint 'https://hub.example/v1'; got: %s", got)
	}
	if !strings.Contains(got, "https://hub.example/mcp/ctx7") {
		t.Errorf("missing mcp endpoint 'https://hub.example/mcp/ctx7'; got: %s", got)
	}
	// Per-W3 phase goal: rendered Context block surfaces each item's
	// `downloadUrl` — literal substring check.
	if !strings.Contains(got, "https://hub.example/content/plugin/caveman") {
		t.Errorf("missing context downloadUrl 'https://hub.example/content/plugin/caveman'; got: %s", got)
	}
	// Should NOT have the "(unavailable)" markers.
	if strings.Contains(got, "(unavailable)") {
		t.Errorf("unexpected '(unavailable)' marker in available=true rendering; got: %s", got)
	}
	// Section headers.
	if !strings.Contains(got, "Runtime") {
		t.Errorf("missing 'Runtime' section header; got: %s", got)
	}
	if !strings.Contains(got, "Context") {
		t.Errorf("missing 'Context' section header; got: %s", got)
	}
}

// TestFormatEnvDescribe_Unavailable asserts the available=false branch
// renders the (unavailable) markers per CLI-12 graceful admin fallback.
func TestFormatEnvDescribe_Unavailable(t *testing.T) {
	env := EnvView{Name: "demo", Namespace: "ach-system", Status: "Available"}
	got := FormatEnvDescribe(env, nil, false)

	if !strings.Contains(got, "Runtime: (unavailable)") {
		t.Errorf("missing 'Runtime: (unavailable)' marker; got: %s", got)
	}
	if !strings.Contains(got, "Context: (unavailable)") {
		t.Errorf("missing 'Context: (unavailable)' marker; got: %s", got)
	}
	// Env metadata still present.
	if !strings.Contains(got, "demo") {
		t.Errorf("missing env name 'demo'; got: %s", got)
	}
}

// TestFormatKeyList asserts the table renders with the expected
// columns + deterministic ordering by KeyID ascending (per W7 — both
// 06-05 env-keys list AND 06-08 admin keys list consume this).
func TestFormatAdminKeyList(t *testing.T) {
	rows := []KeyRowView{
		{KeyID: "ekid_b", Type: "ek", OwnerEmail: "b@x", Environment: "demo", Name: "b-key", CreatedAt: "2026-05-01T00:00:00Z"},
		{KeyID: "ekid_a", Type: "ek", OwnerEmail: "a@x", Environment: "demo", Name: "a-key", CreatedAt: "2026-05-02T00:00:00Z"},
	}
	got := FormatAdminKeyList(rows)
	// Header.
	for _, want := range []string{"KEY-ID", "TYPE", "OWNER", "ENVIRONMENT", "NAME", "CREATED"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing header column %q; got: %s", want, got)
		}
	}
	// Both rows present.
	if !strings.Contains(got, "ekid_a") || !strings.Contains(got, "ekid_b") {
		t.Errorf("missing rows; got: %s", got)
	}
	// Deterministic order by KeyID ascending: ekid_a appears before ekid_b.
	idxA := strings.Index(got, "ekid_a")
	idxB := strings.Index(got, "ekid_b")
	if idxA < 0 || idxB < 0 {
		t.Fatalf("rows missing; got: %s", got)
	}
	if idxA > idxB {
		t.Errorf("ordering wrong: ekid_a (%d) should appear before ekid_b (%d); got: %s", idxA, idxB, got)
	}
}

// TestFormatKeyList_Empty moved to ek_test.go (06-05) — single source of
// truth for the empty-slice marker assertion (now strict-matches
// "No keys found").

// TestFormatEnvList_DescriptionTruncated asserts the list shows a truncated,
// single-line description in the DESCRIPTION column.
func TestFormatEnvList_DescriptionTruncated(t *testing.T) {
	out := FormatEnvList([]EnvView{
		{Name: "demo", Namespace: "ach-system", Status: "Available",
			Description: "first line of the description\nsecond line that must not appear"},
	})
	if !strings.Contains(out, "DESCRIPTION") {
		t.Errorf("list missing DESCRIPTION column header:\n%s", out)
	}
	if strings.Contains(out, "second line") {
		t.Errorf("list leaked multi-line description:\n%s", out)
	}
	if !strings.Contains(out, "first line") {
		t.Errorf("list dropped the description first line:\n%s", out)
	}
}

// TestFormatEnvDescribe_ContextHasNoIDColumn_RuntimeEmptyDashed asserts that
// the Context table no longer carries an ID column (it always duplicated NAME)
// and that EVERY empty Runtime cell renders as an em dash. The mcpServer row
// mirrors the real /platform/hydrate shape: the server emits `id` + `endpoint`
// for an mcpServer and leaves `name` EMPTY — so the em dash must fall on the
// NAME cell (the bug v0.5.5 missed: only id/endpoint were dashed, never name).
func TestFormatEnvDescribe_ContextHasNoIDColumn_RuntimeEmptyDashed(t *testing.T) {
	env := EnvView{Name: "demo", Namespace: "ach", Status: "Available"}
	h := &HydrateView{}
	// Real-world mcpServer shape: name empty, id populated, endpoint populated.
	h.Runtime.MCPServers = []RuntimeItem{{Name: "", ID: "mcp-x", Endpoint: "https://h/mcp/mcp-x"}}
	h.Context.Plugins = []ContextItem{{Name: "p@repo", ID: "p@repo", DownloadURL: "https://h/content/plugin/p@repo"}}

	out := FormatEnvDescribe(env, h, true)

	// Context header must NOT carry an ID column anymore.
	if strings.Contains(out, "KIND\tNAME\tID\tDOWNLOADURL") {
		t.Errorf("context table still has ID column:\n%s", out)
	}
	// The populated id/endpoint render verbatim; the EMPTY name renders as an
	// em dash (em dash present + populated id present ⇒ the dash is the name).
	if !strings.Contains(out, "mcp-x") {
		t.Errorf("runtime populated id missing:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("runtime empty name not em-dashed:\n%s", out)
	}
}

// TestFormatEnvDescribe_DescriptionFull asserts describe renders the full
// description in a dedicated block.
func TestFormatEnvDescribe_DescriptionFull(t *testing.T) {
	out := FormatEnvDescribe(
		EnvView{Name: "demo", Namespace: "ach-system", Status: "Available",
			Description: "line one\nline two"},
		nil, false)
	if !strings.Contains(out, "Description:") {
		t.Errorf("describe missing Description block:\n%s", out)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") {
		t.Errorf("describe dropped description content:\n%s", out)
	}
}

// TestFormatEnvDescribe_NotReady_ShowsConditionReasons asserts that non-True
// conditions surface with their reason + message in a "Not ready:" block,
// and that True conditions (e.g. "Synced") are suppressed (U4).
func TestFormatEnvDescribe_NotReady_ShowsConditionReasons(t *testing.T) {
	env := EnvView{
		Name: "demo-unresolved", Namespace: "ach", Status: "SubConditionsNotReady",
		Conditions: []ConditionView{
			{Type: "Available", Status: "False", Reason: "SubConditionsNotReady", Message: "one or more sub-conditions are not ready"},
			{Type: "ExecutionResourcesResolved", Status: "False", Reason: "UnresolvedReferences", Message: "model \"nonexistent-model\" not found"},
			{Type: "AccessGroupSynced", Status: "True", Reason: "Synced", Message: ""},
		},
	}
	out := FormatEnvDescribe(env, nil, false)
	// The two non-True conditions must surface reason + message.
	for _, want := range []string{"ExecutionResourcesResolved", "UnresolvedReferences", "nonexistent-model"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe output missing %q:\n%s", want, out)
		}
	}
	// A healthy (True) condition should NOT clutter the not-ready summary.
	if strings.Contains(out, "Synced") {
		t.Errorf("describe should not list True conditions in the problem summary:\n%s", out)
	}
}

// TestFormatEnvDescribeShowsGuardrails: guardrails render with an em-dash in
// the ENDPOINT column — they are applied by LiteLLM, never called, so the row
// must not invent an endpoint.
func TestFormatEnvDescribeShowsGuardrails(t *testing.T) {
	h := &HydrateView{
		Runtime: BlockView{
			Models:     []RuntimeItem{{ID: "gpt-4", Endpoint: "http://x/v1"}},
			Guardrails: []string{"pii-filter"},
		},
	}
	out := FormatEnvDescribe(EnvView{Name: "demo"}, h, true)
	if !strings.Contains(out, "guardrail") || !strings.Contains(out, "pii-filter") {
		t.Fatalf("guardrail row missing:\n%s", out)
	}
	if strings.Contains(out, "/guardrail/") {
		t.Fatalf("fabricated guardrail endpoint:\n%s", out)
	}
}

// TestFormatEnvDescribe_EmptySectionsSayNone: an Environment with no runtime
// or no context prints "(none)", not a bare table header.
func TestFormatEnvDescribe_EmptySectionsSayNone(t *testing.T) {
	got := FormatEnvDescribe(EnvView{Name: "e"}, &HydrateView{}, true)
	if strings.Count(got, "  (none)") != 2 || strings.Contains(got, "KIND") {
		t.Errorf("empty describe:\n%s", got)
	}
}

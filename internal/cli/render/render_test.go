// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
)

// TestFormatConfigList_Empty asserts the empty-config branch returns
// a stable "No profiles configured" string (consumed verbatim by
// `ach config list` when the registry has zero profiles).
func TestFormatConfigList_Empty(t *testing.T) {
	got := FormatConfigList(&config.File{})
	if !strings.Contains(got, "No profiles configured") {
		t.Errorf("FormatConfigList(empty) = %q; want substring 'No profiles configured'", got)
	}
}

// TestFormatConfigList_TwoProfiles asserts the table renders both
// rows in alphabetical order with the default row marked.
func TestFormatConfigList_TwoProfiles(t *testing.T) {
	f := &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
			"prod": {URL: "https://prod.example", PK: "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
				EK: map[string]string{"a": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAabcd", "b": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAefgh"}},
			"stg": {URL: "https://stg.example"},
		},
	}
	got := FormatConfigList(f)
	if !strings.Contains(got, "prod") {
		t.Errorf("missing 'prod' row; got: %s", got)
	}
	if !strings.Contains(got, "stg") {
		t.Errorf("missing 'stg' row; got: %s", got)
	}
	// CURRENT column header + "*" marker on the default (prod) row.
	if !strings.Contains(got, "CURRENT") {
		t.Errorf("missing CURRENT column header; got: %s", got)
	}
	var prodMarked bool
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "prod") && strings.HasPrefix(strings.TrimSpace(line), "*") {
			prodMarked = true
		}
		if strings.Contains(line, "stg") && strings.HasPrefix(strings.TrimSpace(line), "*") {
			t.Errorf("non-default 'stg' row should not carry '*'; got line: %q", line)
		}
	}
	if !prodMarked {
		t.Errorf("default 'prod' row missing '*' marker; got: %s", got)
	}
	// Alphabetical order: prod before stg.
	if idxProd, idxStg := strings.Index(got, "prod"), strings.Index(got, "stg"); idxProd > idxStg {
		t.Errorf("row order: prod (%d) should come before stg (%d); got: %s", idxProd, idxStg, got)
	}
	// PK column = "yes" when PK present.
	if !strings.Contains(got, "yes") {
		t.Errorf("missing 'yes' for prod PK column; got: %s", got)
	}
	// EK count column = "2" for prod.
	if !strings.Contains(got, "2") {
		t.Errorf("missing EK count 2 for prod; got: %s", got)
	}
}

// TestFormatConfigShow_Masked asserts reveal=false hides the full pk-
// plaintext from the rendered block.
func TestFormatConfigShow_Masked(t *testing.T) {
	dep := &config.Profile{
		URL: "https://hub.example",
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
		EK: map[string]string{
			"demo": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234",
		},
	}
	got := FormatConfigShow("prod", dep, false)
	if strings.Contains(got, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz") {
		t.Errorf("CLI-04 leak: full pk- plaintext visible without --reveal; got: %s", got)
	}
	if strings.Contains(got, "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234") {
		t.Errorf("CLI-04 leak: full ek- plaintext visible without --reveal; got: %s", got)
	}
	if !strings.Contains(got, "pk-****wxyz") {
		t.Errorf("missing masked pk tail 'pk-****wxyz'; got: %s", got)
	}
	if !strings.Contains(got, "ek-****1234") {
		t.Errorf("missing masked ek tail 'ek-****1234'; got: %s", got)
	}
	if !strings.Contains(got, "Profile: prod") {
		t.Errorf("missing 'Profile: prod' header; got: %s", got)
	}
	if !strings.Contains(got, "URL: https://hub.example") {
		t.Errorf("missing URL line; got: %s", got)
	}
	// EK label visible (just the label, not the value).
	if !strings.Contains(got, "demo") {
		t.Errorf("missing EK label 'demo'; got: %s", got)
	}
}

// TestFormatConfigShow_Revealed asserts reveal=true emits the full
// pk-/ek- plaintext for the named profile only (D-05).
func TestFormatConfigShow_Revealed(t *testing.T) {
	dep := &config.Profile{
		URL: "https://hub.example",
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
		EK: map[string]string{
			"demo": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234",
		},
	}
	got := FormatConfigShow("prod", dep, true)
	if !strings.Contains(got, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz") {
		t.Errorf("--reveal: pk plaintext missing from rendered block; got: %s", got)
	}
	if !strings.Contains(got, "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1234") {
		t.Errorf("--reveal: ek plaintext missing from rendered block; got: %s", got)
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
	if !strings.Contains(got, "NAMESPACE") {
		t.Errorf("missing 'NAMESPACE' header; got: %s", got)
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
func TestFormatKeyList(t *testing.T) {
	rows := []KeyRowView{
		{KeyID: "ekid_b", Type: "ek", OwnerEmail: "b@x", Environment: "demo", Name: "b-key", CreatedAt: "2026-05-01T00:00:00Z"},
		{KeyID: "ekid_a", Type: "ek", OwnerEmail: "a@x", Environment: "demo", Name: "a-key", CreatedAt: "2026-05-02T00:00:00Z"},
	}
	got := FormatKeyList(rows)
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

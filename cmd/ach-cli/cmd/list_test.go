// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/render"
)

// writeListState writes a state file under the per-environment
// <dir>/.ach/<environment>/state-claude-code.json layout (env parsed from the body) and
// returns the workspace dir. body is the raw JSON document.
func writeListState(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	var meta struct {
		Environment string `json:"environment"`
	}
	if err := json.Unmarshal([]byte(body), &meta); err != nil || meta.Environment == "" {
		t.Fatalf("writeListState: body must carry a non-empty environment field: %v", err)
	}
	achDir := filepath.Join(dir, ".ach", meta.Environment)
	if err := os.MkdirAll(achDir, 0o755); err != nil {
		t.Fatalf("mkdir .ach/%s: %v", meta.Environment, err)
	}
	if err := os.WriteFile(filepath.Join(achDir, "state-claude-code.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return dir
}

// executeList runs newEnvStatusCmd against a fixed workspace cwd (via the
// listWorkspaceCwd seam) and returns stdout, exit code, raw error.
func executeList(t *testing.T, workspaceCwd string, args ...string) (string, exit.Code, error) {
	t.Helper()
	prev := listWorkspaceCwd
	listWorkspaceCwd = func() (string, error) { return workspaceCwd, nil }
	t.Cleanup(func() { listWorkspaceCwd = prev })

	cmd := newEnvStatusCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(append([]string{"--offline"}, args...))
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return outBuf.String(), exit.OK, nil
	}
	var cErr *exit.CodedError
	if errors.As(err, &cErr) {
		return outBuf.String(), cErr.Code, err
	}
	return outBuf.String(), exit.General, err
}

// TestList_Table asserts a state.json with Plugins + Prompts entries
// renders a table containing each Target and the correct derived KIND.
// Uses --files to get the flat per-file view (the default is grouped).
func TestList_Table(t *testing.T) {
	body := `{
  "schemaVersion": "3",
  "environment": "prod",
  "profile": "default",
  "prompts": [
    {"target": ".claude/prompts/review.md", "hash": "xxh3:1", "sourceHash": "xxh3:1"}
  ],
  "plugins": [
    {"target": ".claude/plugins/lint", "hash": "xxh3:2", "sourceHash": "xxh3:2"}
  ]
}`
	dir := writeListState(t, body)
	out, code, err := executeList(t, dir, "--files")
	if err != nil {
		t.Fatalf("list: unexpected error: %v", err)
	}
	if code != exit.OK {
		t.Fatalf("list: want exit OK, got %d", code)
	}

	for _, want := range []string{
		"KIND", "TARGET", "ENVIRONMENT",
		"prompt", ".claude/prompts/review.md",
		"plugin", ".claude/plugins/lint",
		"prod",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
}

// TestList_MissingState asserts a missing state.json prints the stable
// empty-state message and exits 0 (no panic).
func TestList_MissingState(t *testing.T) {
	dir := t.TempDir() // no .ach/state.json
	out, code, err := executeList(t, dir)
	if err != nil {
		t.Fatalf("list (missing state): unexpected error: %v", err)
	}
	if code != exit.OK {
		t.Fatalf("list (missing state): want exit OK, got %d", code)
	}
	if !strings.Contains(out, "No resources installed") {
		t.Fatalf("missing state: want empty-state message, got:\n%s", out)
	}
}

// TestList_JSON asserts -o json emits valid JSON the test can unmarshal
// back to the entry set, with the correct derived kinds + targets.
func TestList_JSON(t *testing.T) {
	body := `{
  "schemaVersion": "3",
  "environment": "stg",
  "profile": "default",
  "plugins": [
    {"target": ".claude/plugins/a", "hash": "xxh3:1", "sourceHash": "xxh3:1"},
    {"target": ".claude/plugins/b", "hash": "xxh3:2", "sourceHash": "xxh3:2"}
  ],
  "artifacts": [
    {"target": ".ach/artifacts/data", "hash": "xxh3:3", "sourceHash": "xxh3:3"}
  ]
}`
	dir := writeListState(t, body)
	out, code, err := executeList(t, dir, "-o", "json")
	if err != nil {
		t.Fatalf("status -o json: unexpected error: %v", err)
	}
	if code != exit.OK {
		t.Fatalf("status -o json: want exit OK, got %d", code)
	}

	var decoded []render.StateEntryView
	if uerr := json.Unmarshal([]byte(out), &decoded); uerr != nil {
		t.Fatalf("status -o json: output not valid JSON: %v\n%s", uerr, out)
	}
	if len(decoded) != 3 {
		t.Fatalf("status -o json: want 3 entries, got %d:\n%s", len(decoded), out)
	}

	byTarget := map[string]string{}
	for _, e := range decoded {
		byTarget[e.Target] = e.Kind
		if e.Environment != "stg" {
			t.Errorf("entry %q: want env stg, got %q", e.Target, e.Environment)
		}
	}
	if byTarget[".claude/plugins/a"] != "plugin" {
		t.Errorf("derived kind for plugin a: want plugin, got %q", byTarget[".claude/plugins/a"])
	}
	if byTarget[".ach/artifacts/data"] != "artifact" {
		t.Errorf("derived kind for artifact: want artifact, got %q", byTarget[".ach/artifacts/data"])
	}
}

// TestList_OutToBuffer asserts list writes to cmd.OutOrStdout() (the
// injected buffer), never directly to os.Stdout. Uses --files to verify the
// individual path is present (the default grouped view shows KIND/NAME/FILES
// rather than per-file paths).
func TestList_OutToBuffer(t *testing.T) {
	body := `{
  "schemaVersion": "3",
  "environment": "prod",
  "profile": "default",
  "prompts": [
    {"target": ".claude/prompts/x.md", "hash": "xxh3:1", "sourceHash": "xxh3:1"}
  ]
}`
	dir := writeListState(t, body)

	prev := listWorkspaceCwd
	listWorkspaceCwd = func() (string, error) { return dir, nil }
	t.Cleanup(func() { listWorkspaceCwd = prev })

	cmd := newEnvStatusCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	// --files: use detailed view so the individual path appears.
	cmd.SetArgs([]string{"--files"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(outBuf.String(), ".claude/prompts/x.md") {
		t.Fatalf("output did not go to injected buffer; got:\n%s", outBuf.String())
	}
}

// TestStatus_PositionalEnvJSON asserts `environment status demo -o json` reads only
// demo's state (the old `--environment demo --json` output).
func TestStatus_PositionalEnvJSON(t *testing.T) {
	dir := writeListState(t, `{"schemaVersion":"3","environment":"demo",
  "plugins":[{"target":".claude/plugins/a","hash":"xxh3:1","sourceHash":"xxh3:1"}]}`)
	other := filepath.Join(dir, ".ach", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	otherState := filepath.Join(other, "state-claude-code.json")
	if err := os.WriteFile(otherState, []byte(`{"schemaVersion":"3","environment":"other",
  "plugins":[{"target":".claude/plugins/z","hash":"xxh3:9","sourceHash":"xxh3:9"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code, err := executeList(t, dir, "demo", "-o", "json")
	if err != nil || code != exit.OK {
		t.Fatalf("status demo -o json: code=%d err=%v", code, err)
	}
	var decoded []render.StateEntryView
	if uerr := json.Unmarshal([]byte(out), &decoded); uerr != nil {
		t.Fatalf("not JSON: %v\n%s", uerr, out)
	}
	if len(decoded) != 1 || decoded[0].Target != ".claude/plugins/a" || decoded[0].Environment != "demo" {
		t.Fatalf("want only demo's row, got %+v", decoded)
	}
}

// TestStatus_GlobalRequiresEnv asserts `environment status -g` without an env exits 1.
func TestStatus_GlobalRequiresEnv(t *testing.T) {
	_, code, err := executeList(t, t.TempDir(), "-g")
	if err == nil || code != exit.General || !strings.Contains(err.Error(), "an environment is required with -g") {
		t.Fatalf("code=%d err=%v; want exit 1 'an environment is required with -g'", code, err)
	}
}

// TestStatus_RemovedFlags asserts --json, --environment and --target are gone.
func TestStatus_RemovedFlags(t *testing.T) {
	c := newEnvStatusCmd()
	for _, name := range []string{"json", "environment", "target"} {
		if c.Flags().Lookup(name) != nil {
			t.Errorf("--%s must be removed from environment status", name)
		}
	}
}

// TestStatus_DefaultView: items grouped by kind, and a projected file whose
// content no longer matches its recorded hash shows as edited.
func TestStatus_DefaultView(t *testing.T) {
	dir := writeListState(t, `{"schemaVersion":"3","environment":"prod",
  "adapter":{"id":"claude-code"},
  "plugins":[{"target":"a.md","hash":"xxh3:stale","sourceHash":"x","source":"superpowers"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeList(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		" Environment prod (claude-code · project)\n",
		"             ✓ plugin  superpowers\n",
		"             ! edited  a.md\n",
		"        hint ach-cli environment hydrate --force",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// versionServer serves POST /platform/hydrate/version with the given items per
// environment and points the synthetic profile at it.
func versionServer(t *testing.T, items map[string][]map[string]string, calls *int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		var req struct {
			Environment string `json:"environment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environment": req.Environment, "version": "v", "items": items[req.Environment],
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ACH_URL", srv.URL)
	t.Setenv("ACH_KEY", testEK)
	t.Setenv("ACH_INSECURE", "1")
}

func runStatus(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	prev := listWorkspaceCwd
	listWorkspaceCwd = func() (string, error) { return dir, nil }
	t.Cleanup(func() { listWorkspaceCwd = prev })
	cmd := newEnvStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

const recordedState = `{"schemaVersion":"3","environment":"prod","adapter":{"id":"claude-code"},
  "plugins":[{"target":"a.md","hash":"h","sourceHash":"h","source":"keep"}],
  "envVersion":{"version":"old","items":{"plugin/keep":"1","plugin/moved":"1","skill/gone":"x"}}}`

func TestStatus_ListsChangesSinceHydrate(t *testing.T) {
	dir := writeListState(t, recordedState)
	var calls int
	versionServer(t, map[string][]map[string]string{"prod": {
		{"kind": "plugin", "name": "keep", "rev": "1"},
		{"kind": "plugin", "name": "moved", "rev": "2"},
		{"kind": "mcp", "name": "new-mcp"},
	}}, &calls)
	out, err := runStatus(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Checking 1 environment against the server",
		"+ mcp", "new-mcp", "~ plugin", "moved", "- skill", "gone",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "keep") && strings.Contains(out, "~ plugin  keep") {
		t.Errorf("an unchanged item is listed:\n%s", out)
	}
}

func TestStatus_UpToDate(t *testing.T) {
	dir := writeListState(t, `{"schemaVersion":"3","environment":"prod","adapter":{"id":"claude-code"},
  "plugins":[{"target":"a.md","hash":"h","sourceHash":"h","source":"keep"}],
  "envVersion":{"version":"v","items":{"plugin/keep":"1"}}}`)
	var calls int
	versionServer(t, map[string][]map[string]string{"prod": {{"kind": "plugin", "name": "keep", "rev": "1"}}}, &calls)
	out, err := runStatus(t, dir)
	if err != nil || !strings.Contains(out, "prod up to date") {
		t.Fatalf("want 'prod up to date', err=%v:\n%s", err, out)
	}
}

func TestStatus_NoRecordedVersion(t *testing.T) {
	dir := writeListState(t, `{"schemaVersion":"3","environment":"prod","adapter":{"id":"claude-code"},
  "plugins":[{"target":"a.md","hash":"h","sourceHash":"h","source":"keep"}]}`)
	var calls int
	versionServer(t, nil, &calls)
	out, err := runStatus(t, dir)
	if err != nil || !strings.Contains(out, "hydrate once to record its version") {
		t.Fatalf("want the unrecorded hint, err=%v:\n%s", err, out)
	}
}

func TestStatus_LocalModesNeverCallServer(t *testing.T) {
	dir := writeListState(t, recordedState)
	var calls int
	versionServer(t, nil, &calls)
	for _, args := range [][]string{{"--offline"}, {"-o", "json"}, {"--files"}} {
		if _, err := runStatus(t, dir, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if calls != 0 {
		t.Errorf("local modes called the server %d times", calls)
	}
}

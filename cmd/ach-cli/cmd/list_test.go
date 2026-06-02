// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/render"
)

// writeListState writes a v2 state.json under the per-environment
// <dir>/.ach/<environment>/state.json layout (env parsed from the body) and
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
	if err := os.WriteFile(filepath.Join(achDir, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
	return dir
}

// executeList runs newListCmd against a fixed workspace cwd (via the
// listWorkspaceCwd seam) and returns stdout, exit code, raw error.
func executeList(t *testing.T, workspaceCwd string, args ...string) (string, exit.Code, error) {
	t.Helper()
	prev := listWorkspaceCwd
	listWorkspaceCwd = func() (string, error) { return workspaceCwd, nil }
	t.Cleanup(func() { listWorkspaceCwd = prev })

	cmd := newListCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
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
func TestList_Table(t *testing.T) {
	body := `{
  "schemaVersion": "2",
  "environment": "prod",
  "deployment": "default",
  "prompts": [
    {"target": ".claude/prompts/review.md", "hash": "xxh3:1", "sourceHash": "xxh3:1"}
  ],
  "plugins": [
    {"target": ".claude/plugins/lint", "hash": "xxh3:2", "sourceHash": "xxh3:2"}
  ]
}`
	dir := writeListState(t, body)
	out, code, err := executeList(t, dir)
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

// TestList_JSON asserts --json emits valid JSON the test can unmarshal
// back to the entry set, with the correct derived kinds + targets.
func TestList_JSON(t *testing.T) {
	body := `{
  "schemaVersion": "2",
  "environment": "stg",
  "deployment": "default",
  "plugins": [
    {"target": ".claude/plugins/a", "hash": "xxh3:1", "sourceHash": "xxh3:1"},
    {"target": ".claude/plugins/b", "hash": "xxh3:2", "sourceHash": "xxh3:2"}
  ],
  "artifacts": [
    {"target": ".ach/artifacts/data", "hash": "xxh3:3", "sourceHash": "xxh3:3"}
  ]
}`
	dir := writeListState(t, body)
	out, code, err := executeList(t, dir, "--json")
	if err != nil {
		t.Fatalf("list --json: unexpected error: %v", err)
	}
	if code != exit.OK {
		t.Fatalf("list --json: want exit OK, got %d", code)
	}

	var decoded []render.StateEntryView
	if uerr := json.Unmarshal([]byte(out), &decoded); uerr != nil {
		t.Fatalf("list --json: output not valid JSON: %v\n%s", uerr, out)
	}
	if len(decoded) != 3 {
		t.Fatalf("list --json: want 3 entries, got %d:\n%s", len(decoded), out)
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
// injected buffer), never directly to os.Stdout.
func TestList_OutToBuffer(t *testing.T) {
	body := `{
  "schemaVersion": "2",
  "environment": "prod",
  "deployment": "default",
  "prompts": [
    {"target": ".claude/prompts/x.md", "hash": "xxh3:1", "sourceHash": "xxh3:1"}
  ]
}`
	dir := writeListState(t, body)

	prev := listWorkspaceCwd
	listWorkspaceCwd = func() (string, error) { return dir, nil }
	t.Cleanup(func() { listWorkspaceCwd = prev })

	cmd := newListCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	// Empty slice, NOT nil: cobra falls back to os.Args[1:] when args is
	// nil, which would leak go-test flags (-test.v, -test.run) into the
	// list command's flag parser and fail with an unknown-flag error.
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(outBuf.String(), ".claude/prompts/x.md") {
		t.Fatalf("output did not go to injected buffer; got:\n%s", outBuf.String())
	}
}

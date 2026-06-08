// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hydrate"

	// Blank-import all four adapter subpackages so their init() side-
	// effect registrations fire before adapter.Iter() runs. The
	// autodetect path consumes adapter.Iter() — without these imports,
	// the registry is empty and every Autodetect call returns the
	// zero-match arm regardless of on-disk state.
	_ "github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	_ "github.com/ackstorm/ach/internal/cli/adapter/codex"
	_ "github.com/ackstorm/ach/internal/cli/adapter/gemini"
	_ "github.com/ackstorm/ach/internal/cli/adapter/opencode"
)

// withCleanHome scrubs $HOME for the lifetime of t so global-mode
// hints in adapters' Detect (e.g. codex checks $HOME/.codex/) do not
// leak in cross-test signals. The codex adapter contributes a Low-
// confidence signal when $HOME/.codex/ exists; without this scrub a
// test seeding only .claude/ would see two matches.
func withCleanHome(t *testing.T) {
	t.Helper()
	// Point HOME at a fresh empty dir so the codex Detect's
	// $HOME/.codex/ check sees nothing.
	scratch := t.TempDir()
	t.Setenv("HOME", scratch)
}

// TestAutodetect_Zero_ExitsOne_Stderr seeds an empty TempDir — no
// adapter signal anywhere — and asserts:
//   - returned error is *exit.CodedError with Code=General (1).
//   - stderr is empty (no "Detected platform" line emitted).
//   - error message names the closed-set ids the user can pass via
//     --target.
func TestAutodetect_Zero_ExitsOne_Stderr(t *testing.T) {
	withCleanHome(t)
	root := t.TempDir()

	var stderr bytes.Buffer
	id, err := hydrate.Autodetect(root, &stderr)
	if err == nil {
		t.Fatalf("Autodetect(empty dir) returned (id=%q, nil); want CodedError", id)
	}
	if id != "" {
		t.Errorf("Autodetect returned id=%q on zero-match path; want empty", id)
	}

	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("Autodetect error not *exit.CodedError: %T (%v)", err, err)
	}
	if ce.Code != exit.General {
		t.Errorf("CodedError.Code = %d; want General (1)", ce.Code)
	}
	if !strings.Contains(ce.Msg, "no platform detected") {
		t.Errorf("error message missing 'no platform detected': %q", ce.Msg)
	}
	if !strings.Contains(ce.Msg, "--target") {
		t.Errorf("error message missing '--target' prompt: %q", ce.Msg)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr emitted on zero-match path: %q", stderr.String())
	}
}

// TestAutodetect_One_EmitsDetectedLine seeds a TempDir with .claude/
// only — exactly one adapter (claude-code) sees a signal.
//
// Asserts:
//   - returned id == "claude-code" (canonical id from claudecode.canonicalID).
//   - stderr contains "Detected platform: claude-code\n" (ADAPT-02).
//   - returned error is nil.
func TestAutodetect_One_EmitsDetectedLine(t *testing.T) {
	withCleanHome(t)
	root := t.TempDir()

	// Seed .claude/.mcp.json — gives claudecode 2 signals (.claude/
	// dir + .claude/.mcp.json) → ConfidenceMedium.
	mustMkdir(t, filepath.Join(root, ".claude"))
	mustWriteFile(t, filepath.Join(root, ".claude", ".mcp.json"), []byte("{}"))

	var stderr bytes.Buffer
	id, err := hydrate.Autodetect(root, &stderr)
	if err != nil {
		t.Fatalf("Autodetect(.claude/): unexpected error: %v", err)
	}
	if id != "claude-code" {
		t.Errorf("Autodetect returned id=%q; want claude-code", id)
	}
	if !strings.Contains(stderr.String(), "Detected platform: claude-code") {
		t.Errorf("stderr missing 'Detected platform: claude-code': %q", stderr.String())
	}
}

// TestAutodetect_Multi_ListsBoth_ExitsOne seeds a TempDir with BOTH
// .claude/ AND .codex/ — exactly two adapters see signals.
//
// Asserts:
//   - returned error is *exit.CodedError with Code=General.
//   - error message lists both ids (sorted: claude-code, codex).
//   - error message prompts user to pass --target.
//   - no "Detected platform" line on stderr.
func TestAutodetect_Multi_ListsBoth_ExitsOne(t *testing.T) {
	withCleanHome(t)
	root := t.TempDir()

	mustMkdir(t, filepath.Join(root, ".claude"))
	mustWriteFile(t, filepath.Join(root, ".claude", ".mcp.json"), []byte("{}"))
	mustMkdir(t, filepath.Join(root, ".codex"))
	mustWriteFile(t, filepath.Join(root, ".codex", "config.toml"), []byte(""))

	var stderr bytes.Buffer
	id, err := hydrate.Autodetect(root, &stderr)
	if err == nil {
		t.Fatalf("Autodetect(multi) returned (id=%q, nil); want CodedError", id)
	}
	if id != "" {
		t.Errorf("Autodetect returned id=%q on multi-match path; want empty", id)
	}

	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("Autodetect error not *exit.CodedError: %T (%v)", err, err)
	}
	if ce.Code != exit.General {
		t.Errorf("CodedError.Code = %d; want General (1)", ce.Code)
	}
	if !strings.Contains(ce.Msg, "multiple platforms detected") {
		t.Errorf("error message missing 'multiple platforms detected': %q", ce.Msg)
	}
	if !strings.Contains(ce.Msg, "claude-code") {
		t.Errorf("error message missing 'claude-code': %q", ce.Msg)
	}
	if !strings.Contains(ce.Msg, "codex") {
		t.Errorf("error message missing 'codex': %q", ce.Msg)
	}
	if !strings.Contains(ce.Msg, "--target") {
		t.Errorf("error message missing '--target' prompt: %q", ce.Msg)
	}
	// Deterministic order — claude-code precedes codex lexicographically.
	idxClaude := strings.Index(ce.Msg, "claude-code")
	idxCodex := strings.Index(ce.Msg, "codex")
	if idxClaude < 0 || idxCodex < 0 || idxClaude > idxCodex {
		t.Errorf("multi-match list not in sort.Strings order: %q", ce.Msg)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr emitted on multi-match path: %q", stderr.String())
	}
}

// TestResolvePlatform_Canonical asserts the canonical id round-trips
// through ResolvePlatform unchanged.
func TestResolvePlatform_Canonical(t *testing.T) {
	for _, id := range []string{"claude-code", "codex", "gemini-cli", "opencode"} {
		got, err := hydrate.ResolvePlatform(id)
		if err != nil {
			t.Errorf("ResolvePlatform(%q): unexpected error: %v", id, err)
			continue
		}
		if got != id {
			t.Errorf("ResolvePlatform(%q) = %q; want %q", id, got, id)
		}
	}
}

// TestResolvePlatform_Alias asserts a case-folded alias resolves to
// the canonical id.
func TestResolvePlatform_Alias(t *testing.T) {
	// claudecode declares aliases ["claude", "cc"] — both should resolve
	// to "claude-code".
	for _, alias := range []string{"claude", "cc", "Claude", "CC"} {
		got, err := hydrate.ResolvePlatform(alias)
		if err != nil {
			t.Errorf("ResolvePlatform(%q): unexpected error: %v", alias, err)
			continue
		}
		if got != "claude-code" {
			t.Errorf("ResolvePlatform(%q) = %q; want claude-code", alias, got)
		}
	}
}

// TestResolvePlatform_Unknown asserts a typo'd id surfaces a typed
// CodedError naming the registered canonical ids.
func TestResolvePlatform_Unknown(t *testing.T) {
	got, err := hydrate.ResolvePlatform("clade-code")
	if err == nil {
		t.Fatalf("ResolvePlatform(typo) returned (id=%q, nil); want CodedError", got)
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("ResolvePlatform error not *exit.CodedError: %T", err)
	}
	if ce.Code != exit.General {
		t.Errorf("CodedError.Code = %d; want General (1)", ce.Code)
	}
	if !strings.Contains(ce.Msg, "clade-code") {
		t.Errorf("error message missing offending id: %q", ce.Msg)
	}
	if !strings.Contains(ce.Msg, "claude-code") {
		t.Errorf("error message missing closed-set list: %q", ce.Msg)
	}
}

// TestResolvePlatform_Empty asserts an empty id surfaces a typed
// CodedError prompting the closed set.
func TestResolvePlatform_Empty(t *testing.T) {
	_, err := hydrate.ResolvePlatform("")
	if err == nil {
		t.Fatal("ResolvePlatform(\"\") returned nil error; want CodedError")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("ResolvePlatform error not *exit.CodedError: %T", err)
	}
	if ce.Code != exit.General {
		t.Errorf("CodedError.Code = %d; want General (1)", ce.Code)
	}
}

// mustMkdir creates a directory or fails the test.
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// mustWriteFile writes bytes to a file or fails the test.
func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

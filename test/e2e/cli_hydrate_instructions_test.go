//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Instruction delivery e2e — `environment hydrate` writes each Instruction of
// the demo Environment as an always-loaded rules file for every tool that has
// one, and `environment uninstall` removes the files and the loaders again.
// The demo Environment lists instruction-valid (octocat/Hello-World README,
// the 13 bytes `Hello World!`).

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// instructionExpect is one tool's delivery contract. Paths are
// workspace-relative; prefix, when set, is the start of the file content.
type instructionExpect struct {
	id      string
	file    string
	prefix  string
	loader  string // loader file removed by uninstall ("" when none)
	opencfg bool   // .opencode/opencode.json carries the absolute `<dir>/.opencode/ach/*.md` entry
}

var instructionExpects = []instructionExpect{
	{id: "claude-code", file: ".claude/rules/ach-instruction-valid.md"},
	{id: "opencode", file: ".opencode/ach/instruction-valid.md", opencfg: true},
	{id: "cursor", file: ".cursor/rules/ach-instruction-valid.mdc", prefix: "---\nalwaysApply: true"},
	{id: "antigravity", file: ".agents/rules/ach-instruction-valid.md", prefix: "---\ntrigger: always_on"},
	{id: "pi", file: ".pi/ach/instruction-valid.md", loader: ".pi/extensions/ach-instructions.ts"},
}

func TestPhase7InstructionDelivery(t *testing.T) {
	phase7SuiteGuard(t)
	phase7DemoEnvironmentReady(t)

	baseURL := phase7BaseURL()
	creds := phase7AcquirePk(t)

	run := func(t *testing.T, xdg string, args ...string) (string, string) {
		t.Helper()
		stdout, stderr, err := phase7RunAchCli(t, xdg, args...)
		code, runErr := phase7StripExitErr(err)
		if runErr != nil || code != 0 {
			t.Fatalf("%v: exit %d err %v\nstdout=%s\nstderr=%s", args, code, runErr, stdout, stderr)
		}
		return string(stdout), string(stderr)
	}

	for _, ie := range instructionExpects {
		ie := ie
		t.Run(ie.id, func(t *testing.T) {
			phase7SuiteGuard(t)
			output := t.TempDir()
			xdg := phase7SeedXdgConfig(t, baseURL, creds)
			opencodeInstructionEntry := filepath.ToSlash(output) + "/.opencode/ach/*.md"

			run(t, xdg, "env", "hydrate", phase7DemoEnvironment, "--target", ie.id, "--dir", output)

			path := filepath.Join(output, ie.file)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: instruction file %s missing: %v", ie.id, ie.file, err)
			}
			if !bytes.Contains(b, []byte("Hello World!")) {
				t.Errorf("%s: %s lacks the instruction text:\n%s", ie.id, ie.file, b)
			}
			if ie.prefix != "" && !strings.HasPrefix(string(b), ie.prefix) {
				t.Errorf("%s: %s must start with %q:\n%s", ie.id, ie.file, ie.prefix, b)
			}
			if ie.loader != "" {
				if _, err := os.Stat(filepath.Join(output, ie.loader)); err != nil {
					t.Errorf("%s: loader %s missing: %v", ie.id, ie.loader, err)
				}
			}
			if ie.opencfg {
				cfg, err := os.ReadFile(filepath.Join(output, ".opencode/opencode.json"))
				if err != nil || !bytes.Contains(cfg, []byte(opencodeInstructionEntry)) {
					t.Errorf("opencode: opencode.json lacks the %q instructions entry (err %v):\n%s", opencodeInstructionEntry, err, cfg)
				}
			}

			run(t, xdg, "env", "uninstall", phase7DemoEnvironment, "--dir", output)

			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s: %s survived uninstall (err %v)", ie.id, ie.file, err)
			}
			if ie.loader != "" {
				if _, err := os.Stat(filepath.Join(output, ie.loader)); !os.IsNotExist(err) {
					t.Errorf("%s: loader %s survived uninstall (err %v)", ie.id, ie.loader, err)
				}
			}
			if ie.opencfg {
				if cfg, err := os.ReadFile(filepath.Join(output, ".opencode/opencode.json")); err == nil &&
					bytes.Contains(cfg, []byte(opencodeInstructionEntry)) {
					t.Errorf("opencode: instructions entry survived uninstall:\n%s", cfg)
				}
			}
		})
	}

	t.Run("codex", func(t *testing.T) {
		phase7SuiteGuard(t)
		output := t.TempDir()
		xdg := phase7SeedXdgConfig(t, baseURL, creds)

		_, stderr := run(t, xdg, "env", "hydrate", phase7DemoEnvironment, "--target", "codex", "--dir", output)

		if !strings.Contains(stderr, "does not load Environment instructions") {
			t.Errorf("codex: missing the instructions note on stderr:\n%s", stderr)
		}
		for _, p := range []string{".codex/rules", ".codex/instructions", ".agents/rules"} {
			if _, err := os.Stat(filepath.Join(output, p)); !os.IsNotExist(err) {
				t.Errorf("codex: %s must not exist (err %v)", p, err)
			}
		}
	})
}

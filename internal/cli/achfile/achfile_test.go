// SPDX-License-Identifier: Apache-2.0

package achfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/achfile"
)

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	body := "version: 1\n" +
		"environments:\n" +
		"  - name: team-shared\n" +
		"    targets: [claude-code, codex]\n" +
		"  - name: project-x\n"
	if err := os.WriteFile(filepath.Join(dir, "ach.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := achfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Version != 1 || len(m.Environments) != 2 {
		t.Fatalf("got %+v", m)
	}
	if m.Environments[0].Name != "team-shared" ||
		len(m.Environments[0].Targets) != 2 ||
		m.Environments[1].Name != "project-x" ||
		len(m.Environments[1].Targets) != 0 {
		t.Fatalf("entries wrong: %+v", m.Environments)
	}
}

func TestLoad_Absent_IsErrNotExist(t *testing.T) {
	_, err := achfile.Load(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestLoad_RejectsBadContent(t *testing.T) {
	cases := map[string]string{
		"unknown version":  "version: 2\nenvironments:\n  - name: a\n",
		"empty envs":       "version: 1\nenvironments: []\n",
		"missing name":     "version: 1\nenvironments:\n  - targets: [codex]\n",
		"duplicate name":   "version: 1\nenvironments:\n  - name: a\n  - name: a\n",
		"unknown key":      "version: 1\nbogus: true\nenvironments:\n  - name: a\n",
		"unknown entrykey": "version: 1\nenvironments:\n  - name: a\n    bogus: 1\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ach.yaml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := achfile.Load(dir); !errors.Is(err, achfile.ErrParse) {
				t.Fatalf("%s: want ErrParse, got %v", label, err)
			}
		})
	}
}

func TestWriteTo_DeterministicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	m := &achfile.Manifest{
		Version: 1,
		Environments: []achfile.Entry{
			{Name: "zeta", Targets: []string{"codex", "claude-code"}},
			{Name: "alpha"},
		},
	}
	if err := m.WriteTo(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "ach.yaml"))
	got := string(raw)
	// Envs sorted by name (alpha before zeta); targets sorted within an entry.
	idxAlpha, idxZeta := strings.Index(got, "name: alpha"), strings.Index(got, "name: zeta")
	if idxAlpha < 0 || idxZeta < 0 || idxAlpha > idxZeta {
		t.Fatalf("envs not sorted:\n%s", got)
	}
	if !strings.Contains(got, "[claude-code, codex]") {
		t.Fatalf("targets not sorted:\n%s", got)
	}
	// Round-trip: load it back and compare normalized.
	back, err := achfile.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if back.Environments[0].Name != "alpha" || back.Environments[1].Name != "zeta" {
		t.Fatalf("round-trip order wrong: %+v", back.Environments)
	}
}

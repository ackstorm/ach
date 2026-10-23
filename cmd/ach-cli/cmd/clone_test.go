// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/achfile"
	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/hydrate"
)

// setupClone wires a profile + mock hub and stubs the engine: on success it
// seeds the per-platform state the real engine would write (so ach.yaml can be
// derived), on fail it returns an error. Returns a fresh cwd.
func setupClone(t *testing.T, fail bool) string {
	t.Helper()
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	seedConfig(t, dir, "prod", &config.Profile{URL: mock.server.URL, OAuth: testOAuth()})
	swapHTTPClientForTest(t, &hydrateHTTPClient, mock.server.Client())

	prev := hydrateRunFn
	hydrateRunFn = func(_ context.Context, opts hydrate.Opts) (hydrate.Result, error) {
		if fail {
			return hydrate.Result{}, errors.New("boom")
		}
		seedState(t, opts.Output, opts.Environment, opts.Platform)
		return hydrate.Result{Environment: opts.Environment, PlatformID: opts.Platform}, nil
	}
	t.Cleanup(func() { hydrateRunFn = prev })

	cwd := t.TempDir()
	t.Cleanup(chdir(t, cwd))
	return cwd
}

func TestEnvClone_DefaultDirWritesManifest(t *testing.T) {
	cwd := setupClone(t, false)
	if _, _, _, err := executeCommand(t, newEnvCloneCmd(), "aws-rw", "--target", "claude-code"); err != nil {
		t.Fatalf("clone: %v", err)
	}
	m, err := achfile.Load(filepath.Join(cwd, "aws-rw"))
	if err != nil {
		t.Fatalf("ach.yaml: %v", err)
	}
	if len(m.Environments) != 1 || m.Environments[0].Name != "aws-rw" ||
		len(m.Environments[0].Targets) != 1 || m.Environments[0].Targets[0] != "claude-code" {
		t.Fatalf("manifest = %+v", m.Environments)
	}
}

func TestEnvClone_RefusesNonEmptyDir(t *testing.T) {
	cwd := setupClone(t, false)
	dest := filepath.Join(cwd, "taken")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := executeCommand(t, newEnvCloneCmd(), "aws-rw", "taken", "--target", "claude-code")
	if err == nil || !strings.Contains(err.Error(), "not an empty directory") {
		t.Fatalf("want non-empty refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "keep")); err != nil {
		t.Fatalf("existing content touched: %v", err)
	}
}

func TestEnvClone_FailureRemovesCreatedDirOnly(t *testing.T) {
	cwd := setupClone(t, true)
	if _, _, _, err := executeCommand(t, newEnvCloneCmd(), "aws-rw", "--target", "claude-code"); err == nil {
		t.Fatal("want engine error")
	}
	if _, err := os.Stat(filepath.Join(cwd, "aws-rw")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created dir should be removed, stat err = %v", err)
	}

	empty := filepath.Join(cwd, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := executeCommand(t, newEnvCloneCmd(), "aws-rw", "empty", "--target", "claude-code"); err == nil {
		t.Fatal("want engine error")
	}
	if _, err := os.Stat(empty); err != nil {
		t.Fatalf("pre-existing empty dir must be kept: %v", err)
	}
}

func TestEnvClone_RequiresTarget(t *testing.T) {
	cwd := setupClone(t, false)
	t.Setenv("ACH_TARGET", "")
	_, _, _, err := executeCommand(t, newEnvCloneCmd(), "aws-rw")
	if err == nil || !strings.Contains(err.Error(), "--target") {
		t.Fatalf("want --target error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "aws-rw")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing should be created, stat err = %v", err)
	}
}

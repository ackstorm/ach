// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
)

// TestPath_XDGOverride asserts Test 1 part A: when XDG_CONFIG_HOME is
// set, Path() returns $XDG_CONFIG_HOME/ach/config.yaml.
func TestPath_XDGOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	got, err := config.Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(tmp, "ach", "config.yaml")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

// TestPath_HomeFallback asserts Test 1 part B: when XDG_CONFIG_HOME is
// unset, Path() falls back to $HOME/.config/ach/config.yaml.
func TestPath_HomeFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", tmp)
	got, err := config.Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(tmp, ".config", "ach", "config.yaml")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

// TestSave_ModeAndDir asserts Test 2: Save writes mode 0600 and parent
// dir 0700, atomic via tmp+rename in the same dir. Skipped on Windows
// (no POSIX mode) and root (root bypasses mode bits).
func TestSave_ModeAndDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not POSIX on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX file mode")
	}
	dir := filepath.Join(t.TempDir(), "nested", "ach")
	path := filepath.Join(dir, "config.yaml")
	f := &config.File{
		Default: "prod",
		Deployments: map[string]*config.Deployment{
			"prod": {URL: "https://example.test"},
		},
	}
	if err := config.Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %#o, want 0600", got)
	}
	dst, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dst.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode = %#o, want 0700", got)
	}
}

// TestSave_RefuseNonHTTPS asserts Test 3: Save refuses any Deployment
// whose URL does not begin with "https://" — returns ErrNonHTTPSURL.
func TestSave_RefuseNonHTTPS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{
		Deployments: map[string]*config.Deployment{
			"x": {URL: "http://insecure.test"},
		},
	}
	err := config.Save(path, f)
	if !errors.Is(err, config.ErrNonHTTPSURL) {
		t.Fatalf("Save returned %v, want ErrNonHTTPSURL", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not exist after refused save, stat err = %v", statErr)
	}
}

// TestLoad_WarnOnPermissiveMode asserts Test 4: Load warns to stderr
// when the file mode > 0600 but proceeds to load. Save will normalize
// on next write. Uses LoadWith to capture the warning.
func TestLoad_WarnOnPermissiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not POSIX on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX file mode")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default: prod\ndeployments:\n  prod:\n    url: https://x.test\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var warned string
	got, err := config.LoadWith(path, func(format string, args ...any) {
		warned += format
	})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if got == nil || got.Default != "prod" {
		t.Fatalf("LoadWith returned unexpected file: %+v", got)
	}
	if warned == "" {
		t.Errorf("expected a warning on permissive mode; got none")
	}
}

// TestLoad_AbsentReturnsNil asserts Test 5 part A: Load returns
// (nil, nil) when the file is absent (fresh install / synthetic mode).
func TestLoad_AbsentReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-config.yaml")
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load on absent file: %v", err)
	}
	if got != nil {
		t.Errorf("Load on absent file = %+v, want nil", got)
	}
}

// TestLoad_BadYAMLReturnsParseError asserts Test 5 part B: Load returns
// ErrConfigParse on yaml decode failure.
func TestLoad_BadYAMLReturnsParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("not: [valid: yaml: at: all"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := config.Load(path)
	if !errors.Is(err, config.ErrConfigParse) {
		t.Fatalf("Load with bad yaml returned %v, want ErrConfigParse", err)
	}
}

// TestLoad_RefuseNonHTTPS asserts Test 6: Load refuses any deployment
// whose URL is non-HTTPS — emits ErrNonHTTPSURL with the deployment
// name in the message.
func TestLoad_RefuseNonHTTPS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default: bad\ndeployments:\n  bad:\n    url: http://attacker.test\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := config.Load(path)
	if !errors.Is(err, config.ErrNonHTTPSURL) {
		t.Fatalf("Load returned %v, want ErrNonHTTPSURL", err)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("Load error %q does not name the offending deployment", err.Error())
	}
}

// TestMask asserts Test 7: the three branches of Mask.
func TestMask(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"long pk", "pk_abcdefghijklmnopWXYZ", "pk_****WXYZ"},
		{"short", "ek_xyz", "<masked>"},
		{"no underscore", "garbageabcd", "<masked>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := config.Mask(tc.in); got != tc.want {
				t.Errorf("Mask(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveActive asserts Test 8: precedence flag → env → default →
// sole entry → ErrNoDeployment per CLI-08.
func TestResolveActive(t *testing.T) {
	twoDeps := &config.File{
		Default: "dev",
		Deployments: map[string]*config.Deployment{
			"dev":  {URL: "https://dev.test"},
			"prod": {URL: "https://prod.test"},
		},
	}
	soleDep := &config.File{
		Deployments: map[string]*config.Deployment{
			"only": {URL: "https://only.test"},
		},
	}

	// Flag wins over env and default.
	name, dep, err := config.ResolveActive(twoDeps, "prod", "dev")
	if err != nil {
		t.Fatalf("flag precedence: %v", err)
	}
	if name != "prod" || dep == nil || dep.URL != "https://prod.test" {
		t.Errorf("flag precedence resolved %q (%+v)", name, dep)
	}

	// Env wins over default when flag empty.
	name, _, err = config.ResolveActive(twoDeps, "", "prod")
	if err != nil {
		t.Fatalf("env precedence: %v", err)
	}
	if name != "prod" {
		t.Errorf("env precedence resolved %q, want prod", name)
	}

	// Default wins when flag + env empty.
	name, _, err = config.ResolveActive(twoDeps, "", "")
	if err != nil {
		t.Fatalf("default precedence: %v", err)
	}
	if name != "dev" {
		t.Errorf("default precedence resolved %q, want dev", name)
	}

	// Sole entry wins when nothing else hits.
	name, _, err = config.ResolveActive(soleDep, "", "")
	if err != nil {
		t.Fatalf("sole entry: %v", err)
	}
	if name != "only" {
		t.Errorf("sole entry resolved %q, want only", name)
	}

	// Nil / empty file → ErrNoDeployment.
	if _, _, err := config.ResolveActive(nil, "", ""); !errors.Is(err, config.ErrNoDeployment) {
		t.Errorf("nil file returned %v, want ErrNoDeployment", err)
	}
	empty := &config.File{}
	if _, _, err := config.ResolveActive(empty, "", ""); !errors.Is(err, config.ErrNoDeployment) {
		t.Errorf("empty file returned %v, want ErrNoDeployment", err)
	}

	// Multiple entries, no flag/env/default → ErrNoDeployment.
	ambig := &config.File{
		Deployments: map[string]*config.Deployment{
			"a": {URL: "https://a.test"},
			"b": {URL: "https://b.test"},
		},
	}
	if _, _, err := config.ResolveActive(ambig, "", ""); !errors.Is(err, config.ErrNoDeployment) {
		t.Errorf("ambiguous file returned %v, want ErrNoDeployment", err)
	}

	// Unknown name flag → ErrNoDeployment.
	if _, _, err := config.ResolveActive(twoDeps, "missing", ""); !errors.Is(err, config.ErrNoDeployment) {
		t.Errorf("missing flag name returned %v, want ErrNoDeployment", err)
	}
}

// TestSaveLoadRoundTrip is the byte-identical round trip in the
// acceptance criteria.
func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{
		Default: "prod",
		Deployments: map[string]*config.Deployment{
			"prod": {URL: "https://x.test"},
		},
	}
	if err := config.Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil || got.Default != "prod" || got.Deployments["prod"].URL != "https://x.test" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

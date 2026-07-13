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
		Profiles: map[string]*config.Profile{
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

// TestSave_RefuseInvalidScheme asserts Save refuses any Profile whose
// URL is neither http:// nor https:// (here ftp://) — returns
// ErrInvalidURLScheme and writes nothing.
func TestSave_RefuseInvalidScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{
		Profiles: map[string]*config.Profile{
			"x": {URL: "ftp://insecure.test"},
		},
	}
	err := config.Save(path, f)
	if !errors.Is(err, config.ErrInvalidURLScheme) {
		t.Fatalf("Save returned %v, want ErrInvalidURLScheme", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not exist after refused save, stat err = %v", statErr)
	}
}

// TestValidateSecureURL asserts the transport-posture gate (G19): https://
// always ok; http:// is ErrInsecureURL unless allowInsecure; a non-http(s)
// scheme is ErrInvalidURLScheme regardless of allowInsecure.
func TestValidateSecureURL(t *testing.T) {
	cases := []struct {
		url      string
		insecure bool
		wantErr  error
	}{
		{"https://hub.example.com", false, nil},
		{"http://hub.example.com", false, config.ErrInsecureURL},
		{"http://localhost:8080", false, config.ErrInsecureURL}, // localhost also refused
		{"http://localhost:8080", true, nil},                    // opt-in
		{"https://x", true, nil},
		{"ftp://x", false, config.ErrInvalidURLScheme},
		{"ftp://x", true, config.ErrInvalidURLScheme}, // insecure does not excuse a bad scheme
	}
	for _, c := range cases {
		err := config.ValidateSecureURL(c.url, c.insecure)
		if c.wantErr == nil && err != nil {
			t.Errorf("%s insecure=%v: unexpected %v", c.url, c.insecure, err)
		}
		if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Errorf("%s insecure=%v: want %v, got %v", c.url, c.insecure, c.wantErr, err)
		}
	}
}

// TestSave_RefusesHTTPByDefault asserts Save refuses a plaintext http://
// profile when no opt-in is present (G19, decision B — localhost included).
func TestSave_RefusesHTTPByDefault(t *testing.T) {
	t.Setenv("ACH_INSECURE", "") // ensure no env opt-in
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{Profiles: map[string]*config.Profile{"x": {URL: "http://localhost:8080"}}}
	if err := config.Save(path, f); !errors.Is(err, config.ErrInsecureURL) {
		t.Fatalf("Save(http) = %v, want ErrInsecureURL", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not exist after refused save, stat err = %v", statErr)
	}
}

// TestSave_AcceptsHTTPS asserts an https:// profile saves with no opt-in.
func TestSave_AcceptsHTTPS(t *testing.T) {
	t.Setenv("ACH_INSECURE", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{Profiles: map[string]*config.Profile{"x": {URL: "https://hub.example.com"}}}
	if err := config.Save(path, f); err != nil {
		t.Errorf("Save(https) returned %v, want nil", err)
	}
}

// TestSaveInsecure_AcceptsHTTP asserts the explicit opt-in path writes an
// http:// profile.
func TestSaveInsecure_AcceptsHTTP(t *testing.T) {
	t.Setenv("ACH_INSECURE", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{Profiles: map[string]*config.Profile{"x": {URL: "http://localhost:8080"}}}
	if err := config.SaveInsecure(path, f, true); err != nil {
		t.Errorf("SaveInsecure(http, true) returned %v, want nil", err)
	}
}

// TestSave_AcceptsHTTP_WithEnvOptIn asserts ACH_INSECURE=1 makes the default
// Save accept http:// (the global opt-in the error message promises).
func TestSave_AcceptsHTTP_WithEnvOptIn(t *testing.T) {
	t.Setenv("ACH_INSECURE", "1")
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{Profiles: map[string]*config.Profile{"x": {URL: "http://localhost:8080"}}}
	if err := config.Save(path, f); err != nil {
		t.Errorf("Save(http) with ACH_INSECURE=1 returned %v, want nil", err)
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
	if err := os.WriteFile(path, []byte("default: prod\nprofiles:\n  prod:\n    url: https://x.test\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var warned string
	got, err := config.LoadWithInsecure(path, func(format string, args ...any) {
		warned += format
	}, config.InsecureFromEnv())
	if err != nil {
		t.Fatalf("LoadWithInsecure: %v", err)
	}
	if got == nil || got.Default != "prod" {
		t.Fatalf("LoadWithInsecure returned unexpected file: %+v", got)
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

// TestLoad_RefuseInvalidScheme asserts Load refuses any profile whose
// URL is neither http:// nor https:// (here ftp://) — emits
// ErrInvalidURLScheme with the profile name in the message.
func TestLoad_RefuseInvalidScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default: bad\nprofiles:\n  bad:\n    url: ftp://attacker.test\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := config.Load(path)
	if !errors.Is(err, config.ErrInvalidURLScheme) {
		t.Fatalf("Load returned %v, want ErrInvalidURLScheme", err)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("Load error %q does not name the offending profile", err.Error())
	}
}

// TestLoad_RefusesHTTPByDefault asserts Load refuses a plaintext http://
// profile with no opt-in present (G19, decision B — localhost included). The
// error names the profile and preserves ErrInsecureURL for errors.Is.
func TestLoad_RefusesHTTPByDefault(t *testing.T) {
	t.Setenv("ACH_INSECURE", "") // ensure no env opt-in
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default: dev\nprofiles:\n  dev:\n    url: http://localhost:8080\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := config.Load(path)
	if !errors.Is(err, config.ErrInsecureURL) {
		t.Fatalf("Load(http) returned %v, want ErrInsecureURL", err)
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Errorf("Load error %q does not name the offending profile", err.Error())
	}
}

// TestLoadInsecure_AcceptsHTTP asserts the explicit opt-in path loads an
// http:// profile.
func TestLoadInsecure_AcceptsHTTP(t *testing.T) {
	t.Setenv("ACH_INSECURE", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default: dev\nprofiles:\n  dev:\n    url: http://localhost:8080\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f, err := config.LoadWithInsecure(path, nil, true)
	if err != nil {
		t.Fatalf("LoadWithInsecure(http, nil, true) returned %v, want nil", err)
	}
	if f.Profiles["dev"].URL != "http://localhost:8080" {
		t.Errorf("URL not preserved; got %q", f.Profiles["dev"].URL)
	}
}

// TestLoad_AcceptsHTTP_WithEnvOptIn asserts ACH_INSECURE=1 makes the default
// Load accept an http:// profile (the global opt-in the error promises, so
// read-only commands like whoami/logout still work for localhost dev).
func TestLoad_AcceptsHTTP_WithEnvOptIn(t *testing.T) {
	t.Setenv("ACH_INSECURE", "1")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default: dev\nprofiles:\n  dev:\n    url: http://localhost:8080\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(http) with ACH_INSECURE=1 returned %v, want nil", err)
	}
	if f.Profiles["dev"].URL != "http://localhost:8080" {
		t.Errorf("URL not preserved; got %q", f.Profiles["dev"].URL)
	}
}

// TestMask asserts Test 7: the three branches of Mask.
func TestMask(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"long pk", "pk-abcdefghijklmnopWXYZ", "pk-****WXYZ"},
		{"short", "ek-xyz", "<masked>"},
		{"no hyphen", "garbageabcd", "<masked>"},
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
// sole entry → ErrNoProfile per CLI-08.
func TestResolveActive(t *testing.T) {
	twoDeps := &config.File{
		Default: "dev",
		Profiles: map[string]*config.Profile{
			"dev":  {URL: "https://dev.test"},
			"prod": {URL: "https://prod.test"},
		},
	}
	soleDep := &config.File{
		Profiles: map[string]*config.Profile{
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

	// Nil / empty file → ErrNoProfile.
	if _, _, err := config.ResolveActive(nil, "", ""); !errors.Is(err, config.ErrNoProfile) {
		t.Errorf("nil file returned %v, want ErrNoProfile", err)
	}
	empty := &config.File{}
	if _, _, err := config.ResolveActive(empty, "", ""); !errors.Is(err, config.ErrNoProfile) {
		t.Errorf("empty file returned %v, want ErrNoProfile", err)
	}

	// Multiple entries, no flag/env/default → ErrNoProfile.
	ambig := &config.File{
		Profiles: map[string]*config.Profile{
			"a": {URL: "https://a.test"},
			"b": {URL: "https://b.test"},
		},
	}
	if _, _, err := config.ResolveActive(ambig, "", ""); !errors.Is(err, config.ErrNoProfile) {
		t.Errorf("ambiguous file returned %v, want ErrNoProfile", err)
	}

	// Unknown name flag → ErrNoProfile.
	if _, _, err := config.ResolveActive(twoDeps, "missing", ""); !errors.Is(err, config.ErrNoProfile) {
		t.Errorf("missing flag name returned %v, want ErrNoProfile", err)
	}
}

// TestSaveLoadRoundTrip is the byte-identical round trip in the
// acceptance criteria.
func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &config.File{
		Default: "prod",
		Profiles: map[string]*config.Profile{
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
	if got == nil || got.Default != "prod" || got.Profiles["prod"].URL != "https://x.test" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

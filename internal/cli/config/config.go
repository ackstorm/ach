// SPDX-License-Identifier: Apache-2.0

// Package config owns ~/.config/ach/config.yaml — the CLI's local
// trust artifact (Hub §15.4) authorized to hold pk_/ek_ plaintext on
// disk at mode 0600. The schema is CLI spec §3.2 verbatim:
//
//	default: <name>
//	deployments:
//	  <name>:
//	    url:  https://...
//	    pk:   pk_...
//	    ek:
//	      <local-label>: ek_...
//
// Discipline (mirrors internal/cachefs and internal/credhash):
//
//   - stdlib + gopkg.in/yaml.v3 only — NO log, NO log/slog, NO direct
//     os.Stderr writes. Caller-side warnings ride the `warn func(...)`
//     seam injected through LoadWith.
//   - 0700 parent dir / 0600 file mode invariants on every Save.
//   - Atomic publication via tmp+rename in the same dir (TOCTOU-safe).
//   - `url:` must be http:// or https:// — validated on BOTH Load AND
//     Save. http:// is accepted; the command layer warns about plaintext
//     transport when the active deployment is http://.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the §3.2 schema. The yaml tags use ",omitempty" so a fresh
// File round-trips to a minimal yaml document without empty maps.
type File struct {
	Default     string                 `yaml:"default,omitempty"`
	Deployments map[string]*Deployment `yaml:"deployments,omitempty"`
}

// Deployment is one named entry under `deployments:` — a URL plus the
// optional pk_/ek_ map. `url:` is the only required field.
type Deployment struct {
	URL string            `yaml:"url"`
	PK  string            `yaml:"pk,omitempty"`
	EK  map[string]string `yaml:"ek,omitempty"`
}

// Sentinel errors. Callers gate behavior via errors.Is.
var (
	// ErrInvalidURLScheme is returned by Load and Save when any
	// deployment's `url:` is neither http:// nor https:// (empty, ftp,
	// etc.). The error string includes the offending deployment name so
	// the operator can fix it; the URL itself is omitted. http:// is
	// accepted (the command layer warns about plaintext transport).
	ErrInvalidURLScheme = errors.New("config: deployment url must be http:// or https://")

	// ErrConfigParse wraps yaml.v3 decode failures so callers can
	// distinguish "file is corrupt" from "deployment is misconfigured".
	ErrConfigParse = errors.New("config: parse failed")

	// ErrNoDeployment is returned by ResolveActive when no deployment
	// can be selected via flag / env / default / sole entry.
	ErrNoDeployment = errors.New("config: no deployment resolved")

	// ErrFileMode is reserved for callers that want to fail-closed on
	// permissive modes (e.g. a future strict-mode flag). Today Load
	// only warns; Save normalizes back to 0600.
	ErrFileMode = errors.New("config: file mode more permissive than 0600")
)

// Path returns the canonical config file location, honoring
// XDG_CONFIG_HOME when set and falling back to $HOME/.config/ach/.
func Path() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ach", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ach", "config.yaml"), nil
}

// Load reads + parses the config file. Returns (nil, nil) when the
// file is absent (fresh install / synthetic mode). Returns
// ErrConfigParse-wrapped error on yaml decode failure. Returns
// ErrInvalidURLScheme when any deployment's URL is neither http:// nor https://.
//
// Warns to stderr via a default logger seam when the file mode is
// more permissive than 0600 — see LoadWith for a test-friendly
// injectable warning sink.
func Load(path string) (*File, error) {
	return LoadWith(path, defaultWarn)
}

// LoadWith is Load with an injectable warning sink. The `warn`
// closure is called with a Printf-style format + args when the file
// mode exceeds 0600. This seam exists so tests can capture the
// warning without redirecting stderr, and so callers (cobra RunE) can
// route warnings through their own structured logger if they wish.
func LoadWith(path string, warn func(format string, args ...any)) (*File, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// 0600 / 0700 mode discipline. Skip on Windows (no POSIX bits)
	// and on root (which bypasses the check).
	if !skipModeCheck() && st.Mode().Perm() > 0o600 {
		if warn != nil {
			warn("config: file mode %#o is more permissive than 0600 at %s — will normalize on next write", st.Mode().Perm(), path)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %v", ErrConfigParse, err)
	}
	if err := validateDeployments(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Save writes the file to `path` atomically: encode to a sibling
// tmp file in the same dir, chmod 0600, then os.Rename onto the
// target. Ensures the parent dir exists with mode 0700. Refuses to
// write any deployment whose URL is neither http:// nor https://.
func Save(path string, f *File) error {
	if f == nil {
		return errors.New("config: Save called with nil File")
	}
	if err := validateDeployments(f); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Tighten dir mode in case MkdirAll honored an umask wider than 0700.
	_ = os.Chmod(dir, 0o700)

	buf, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Mask returns the display form of a pk_/ek_ plaintext — used by
// `ach config show` (D-05). Shape: "<prefix>_****<last-4>". Returns
// "<masked>" when the input is shorter than 8 chars or contains no
// underscore (defensive: never emit ambiguous fragments).
func Mask(s string) string {
	if len(s) < 8 {
		return "<masked>"
	}
	idx := strings.Index(s, "_")
	if idx < 0 {
		return "<masked>"
	}
	return s[:idx+1] + "****" + s[len(s)-4:]
}

// ResolveActive applies the CLI-08 precedence rule: flag → env →
// file.Default → sole entry → ErrNoDeployment. Returns the resolved
// name + pointer into f.Deployments (never a copy — callers may
// mutate via Save).
func ResolveActive(f *File, flagDeployment, envDeployment string) (string, *Deployment, error) {
	if f == nil || len(f.Deployments) == 0 {
		return "", nil, ErrNoDeployment
	}
	pick := func(name string) (string, *Deployment, error) {
		dep, ok := f.Deployments[name]
		if !ok {
			return "", nil, fmt.Errorf("%w: %q not found in deployments", ErrNoDeployment, name)
		}
		return name, dep, nil
	}
	if flagDeployment != "" {
		return pick(flagDeployment)
	}
	if envDeployment != "" {
		return pick(envDeployment)
	}
	if f.Default != "" {
		return pick(f.Default)
	}
	if len(f.Deployments) == 1 {
		for n, d := range f.Deployments {
			return n, d, nil
		}
	}
	return "", nil, ErrNoDeployment
}

// validateDeployments accepts http:// and https:// on every entry and
// rejects any other scheme (empty, ftp://, ...). http:// is permitted so
// the CLI can target local/internal deployments (e.g. the kind+Helm
// ach-local-gateway on http://localhost:8080); the command layer emits a
// plaintext-transport warning when the active deployment is http://. The
// error names the offending deployment but omits the URL itself (it can
// carry secrets in some pathological misconfigurations).
func validateDeployments(f *File) error {
	for name, dep := range f.Deployments {
		if dep == nil {
			continue
		}
		if strings.HasPrefix(dep.URL, "https://") || strings.HasPrefix(dep.URL, "http://") {
			continue
		}
		return fmt.Errorf("%w: deployment %q (must be http:// or https://)", ErrInvalidURLScheme, name)
	}
	return nil
}

// defaultWarn is the noop warning sink for callers that do not care
// about file-mode warnings. The CLI cobra layer can replace it with a
// stderr printer that respects --no-warnings.
func defaultWarn(_ string, _ ...any) {}

// skipModeCheck reports whether the 0600 mode invariant is
// inapplicable on this platform/process. Mirrors the cachefs test
// discipline (skip on Windows + root).
func skipModeCheck() bool {
	if os.Geteuid() == 0 {
		return true
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return false
}

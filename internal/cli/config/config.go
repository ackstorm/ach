// SPDX-License-Identifier: Apache-2.0

// Package config owns ~/.config/ach/config.yaml — the CLI's local
// trust artifact (Hub §15.4) authorized to hold pk_/ek_ plaintext on
// disk at mode 0600. The schema is CLI spec §3.2 verbatim:
//
//	default: <name>
//	profiles:
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
//   - `url:` must be https:// — validated on BOTH Load AND Save. A plaintext
//     http:// URL is REFUSED by default (G19, decision B; localhost included)
//     unless the caller opts into insecure transport via the --insecure flag
//     or the ACH_INSECURE env var. Any other scheme is always rejected.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the §3.2 schema. The yaml tags use ",omitempty" so a fresh
// File round-trips to a minimal yaml document without empty maps.
type File struct {
	Default  string              `yaml:"default,omitempty"`
	Profiles map[string]*Profile `yaml:"profiles,omitempty"`
}

// Profile is one named entry under `profiles:` — a URL plus the
// optional pk_/ek_ map. `url:` is the only required field.
type Profile struct {
	URL string            `yaml:"url"`
	PK  string            `yaml:"pk,omitempty"`
	EK  map[string]string `yaml:"ek,omitempty"`
}

// ProfileNames returns the configured profile names in sorted order (for
// not-found error messages).
func (f *File) ProfileNames() []string {
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Sentinel errors. Callers gate behavior via errors.Is.
var (
	// ErrInvalidURLScheme is returned by Load and Save when any
	// profile's `url:` is neither http:// nor https:// (empty, ftp,
	// etc.). The error string includes the offending profile name so
	// the operator can fix it; the URL itself is omitted.
	ErrInvalidURLScheme = errors.New("config: profile url must be http:// or https://")

	// ErrInsecureURL is returned for a non-https:// (i.e. plaintext http://)
	// URL when insecure transport was not explicitly opted into via the
	// --insecure flag or ACH_INSECURE env var (G19, decision B). Refusing is
	// the default — the pk_/ek_ bearer would otherwise travel in cleartext.
	// localhost is NOT exempt (the frozen-posture decision).
	ErrInsecureURL = errors.New("config: refusing plaintext http:// — credentials would be sent unencrypted; pass --insecure or set ACH_INSECURE=1 to override")

	// ErrConfigParse wraps yaml.v3 decode failures so callers can
	// distinguish "file is corrupt" from "profile is misconfigured".
	ErrConfigParse = errors.New("config: parse failed")

	// ErrNoProfile is returned by ResolveActive when no profile
	// can be selected via flag / env / default / sole entry.
	ErrNoProfile = errors.New("config: no profile resolved")
)

// EnvInsecureName is the env var that opts into plaintext http:// Hub URLs
// (G19). It is the GLOBAL opt-in: Load/Save honor it so read-only commands
// (whoami, logout, config show) keep working against a localhost dev profile,
// matching the override the ErrInsecureURL message advertises.
const EnvInsecureName = "ACH_INSECURE"

// InsecureFromEnv reports whether ACH_INSECURE opts into plaintext transport.
// Truthy values are 1 / true / yes (case-insensitive); anything else is false.
func InsecureFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvInsecureName))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// ValidateSecureURL enforces the transport posture (G19): https:// is always
// ok; http:// is ErrInsecureURL unless allowInsecure is set; any other scheme
// is ErrInvalidURLScheme regardless of allowInsecure (a bad scheme is never
// "excused" by the insecure opt-in). The rawURL is echoed in the message here
// because callers pass a user-supplied --base-url; validateProfiles wraps this
// to name the profile instead (a stored URL can carry secrets).
func ValidateSecureURL(rawURL string, allowInsecure bool) error {
	switch {
	case strings.HasPrefix(rawURL, "https://"):
		return nil
	case strings.HasPrefix(rawURL, "http://"):
		if allowInsecure {
			return nil
		}
		return fmt.Errorf("%w (url %q)", ErrInsecureURL, rawURL)
	default:
		return fmt.Errorf("%w: %q (must be http:// or https://)", ErrInvalidURLScheme, rawURL)
	}
}

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
// ErrInvalidURLScheme when any profile's URL is neither http:// nor https://.
//
// Warns to stderr via a default logger seam when the file mode is
// more permissive than 0600 — see LoadWith for a test-friendly
// injectable warning sink.
func Load(path string) (*File, error) {
	return LoadWithInsecure(path, defaultWarn, InsecureFromEnv())
}

// LoadInsecure is Load with an explicit insecure opt-in (the flag-aware
// commands compute `--insecure || ACH_INSECURE` and pass it here).
func LoadInsecure(path string, allowInsecure bool) (*File, error) {
	return LoadWithInsecure(path, defaultWarn, allowInsecure)
}

// LoadWith is Load with an injectable warning sink. The `warn`
// closure is called with a Printf-style format + args when the file
// mode exceeds 0600. This seam exists so tests can capture the
// warning without redirecting stderr, and so callers (cobra RunE) can
// route warnings through their own structured logger if they wish.
// The insecure opt-in is resolved from ACH_INSECURE (use LoadWithInsecure
// to also honor a --insecure flag).
func LoadWith(path string, warn func(format string, args ...any)) (*File, error) {
	return LoadWithInsecure(path, warn, InsecureFromEnv())
}

// LoadWithInsecure is the full form: injectable warning sink + explicit
// insecure opt-in. Used by login, which has both a --insecure flag and the
// permissive-mode warning sink.
func LoadWithInsecure(path string, warn func(format string, args ...any), allowInsecure bool) (*File, error) {
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
	if err := validateProfiles(&f, allowInsecure); err != nil {
		return nil, err
	}
	return &f, nil
}

// Save writes the file to `path` atomically: encode to a sibling
// tmp file in the same dir, chmod 0600, then os.Rename onto the
// target. Ensures the parent dir exists with mode 0700. Refuses to
// write any profile whose URL is neither http:// nor https://.
func Save(path string, f *File) error {
	return SaveInsecure(path, f, InsecureFromEnv())
}

// SaveInsecure is Save with an explicit insecure opt-in (the flag-aware
// commands compute `--insecure || ACH_INSECURE` and pass it here). See Save
// for the atomic-write contract.
func SaveInsecure(path string, f *File, allowInsecure bool) error {
	if f == nil {
		return errors.New("config: Save called with nil File")
	}
	if err := validateProfiles(f, allowInsecure); err != nil {
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

// Mask returns the display form of a pk-/ek- plaintext — used by
// `ach config show` (D-05). Shape: "<prefix>-****<last-4>". Returns
// "<masked>" when the input is shorter than 8 chars or contains no
// hyphen (defensive: never emit ambiguous fragments).
//
// The base64url payload may itself contain hyphens, but the prefix
// separator is always the FIRST hyphen (index 2), so strings.Index is
// correct.
func Mask(s string) string {
	if len(s) < 8 {
		return "<masked>"
	}
	idx := strings.Index(s, "-")
	if idx < 0 {
		return "<masked>"
	}
	return s[:idx+1] + "****" + s[len(s)-4:]
}

// ResolveActive applies the CLI-08 precedence rule: flag → env →
// file.Default → sole entry → ErrNoProfile. Returns the resolved
// name + pointer into f.Profiles (never a copy — callers may
// mutate via Save).
func ResolveActive(f *File, flagProfile, envProfile string) (string, *Profile, error) {
	if f == nil || len(f.Profiles) == 0 {
		return "", nil, ErrNoProfile
	}
	pick := func(name string) (string, *Profile, error) {
		dep, ok := f.Profiles[name]
		if !ok {
			return "", nil, fmt.Errorf("%w: %q not found in profiles", ErrNoProfile, name)
		}
		return name, dep, nil
	}
	if flagProfile != "" {
		return pick(flagProfile)
	}
	if envProfile != "" {
		return pick(envProfile)
	}
	if f.Default != "" {
		return pick(f.Default)
	}
	if len(f.Profiles) == 1 {
		for n, d := range f.Profiles {
			return n, d, nil
		}
	}
	return "", nil, ErrNoProfile
}

// validateProfiles enforces the transport posture (G19) on every entry via
// ValidateSecureURL: https:// always ok; http:// only when allowInsecure;
// any other scheme always rejected. The error NAMES the offending profile
// but omits the URL itself (it can carry secrets in some pathological
// misconfigurations) while preserving the sentinel for errors.Is dispatch.
func validateProfiles(f *File, allowInsecure bool) error {
	for name, dep := range f.Profiles {
		if dep == nil {
			continue
		}
		if err := ValidateSecureURL(dep.URL, allowInsecure); err != nil {
			if errors.Is(err, ErrInsecureURL) {
				return fmt.Errorf("%w: profile %q", ErrInsecureURL, name)
			}
			return fmt.Errorf("%w: profile %q (must be http:// or https://)", ErrInvalidURLScheme, name)
		}
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

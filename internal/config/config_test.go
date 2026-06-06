// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/config"
)

// envKey is a never-set sentinel for the "unset" branch of every helper.
// Using a key that no production code reads avoids accidental coupling
// between tests and real env state in CI.
const envKey = "ACH_TEST_CONFIG_KEY_DO_NOT_USE"

func TestEnvOrFallback(t *testing.T) {
	// t.Setenv with empty value still sets the variable to "" on POSIX —
	// equivalent to "unset" for our purposes because EnvOr checks `v != ""`.
	t.Setenv(envKey, "")
	got := config.EnvOr(envKey, "fallback")
	if got != "fallback" {
		t.Fatalf("EnvOr empty: want %q, got %q", "fallback", got)
	}
}

func TestEnvOrSet(t *testing.T) {
	t.Setenv(envKey, "value")
	got := config.EnvOr(envKey, "fallback")
	if got != "value" {
		t.Fatalf("EnvOr set: want %q, got %q", "value", got)
	}
}

func TestMustEnvNonEmptyEmpty(t *testing.T) {
	t.Setenv(envKey, "")
	_, err := config.MustEnvNonEmpty(envKey)
	if err == nil {
		t.Fatalf("MustEnvNonEmpty empty: want error, got nil")
	}
	// Error message must contain the key name for operator debuggability.
	if !strings.Contains(err.Error(), envKey) {
		t.Fatalf("MustEnvNonEmpty empty: error %q must contain key %q", err.Error(), envKey)
	}
}

func TestMustEnvNonEmptySet(t *testing.T) {
	t.Setenv(envKey, "some-value")
	got, err := config.MustEnvNonEmpty(envKey)
	if err != nil {
		t.Fatalf("MustEnvNonEmpty set: unexpected error %v", err)
	}
	if got != "some-value" {
		t.Fatalf("MustEnvNonEmpty set: want %q, got %q", "some-value", got)
	}
}

func TestMustEnvIntPositiveDefault(t *testing.T) {
	t.Setenv(envKey, "")
	got, err := config.MustEnvIntPositive(envKey, 50)
	if err != nil {
		t.Fatalf("MustEnvIntPositive unset: unexpected error %v", err)
	}
	if got != 50 {
		t.Fatalf("MustEnvIntPositive unset: want fallback 50, got %d", got)
	}
}

func TestMustEnvIntPositiveZeroErrors(t *testing.T) {
	t.Setenv(envKey, "0")
	_, err := config.MustEnvIntPositive(envKey, 50)
	if err == nil {
		t.Fatalf("MustEnvIntPositive zero: want error, got nil")
	}
}

func TestMustEnvIntPositiveNegativeErrors(t *testing.T) {
	t.Setenv(envKey, "-5")
	_, err := config.MustEnvIntPositive(envKey, 50)
	if err == nil {
		t.Fatalf("MustEnvIntPositive negative: want error, got nil")
	}
}

func TestMustEnvIntPositiveNonNumericErrors(t *testing.T) {
	t.Setenv(envKey, "abc")
	_, err := config.MustEnvIntPositive(envKey, 50)
	if err == nil {
		t.Fatalf("MustEnvIntPositive non-numeric: want error, got nil")
	}
}

// TestMustEnvIntPositiveValid exercises the happy path so the parse + range
// logic isn't covered only by negative cases.
func TestMustEnvIntPositiveValid(t *testing.T) {
	t.Setenv(envKey, "100")
	got, err := config.MustEnvIntPositive(envKey, 50)
	if err != nil {
		t.Fatalf("MustEnvIntPositive valid: unexpected error %v", err)
	}
	if got != 100 {
		t.Fatalf("MustEnvIntPositive valid: want 100, got %d", got)
	}
}

func TestEnvBoolValid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"1", true},
		{"t", true},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"0", false},
		{"f", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Setenv(envKey, tc.in)
			// fallback set to the opposite of expected so a no-op parse
			// would yield the wrong answer — the test passes only if
			// the value was actually parsed.
			got := config.EnvBool(envKey, !tc.want)
			if got != tc.want {
				t.Fatalf("EnvBool(%q): want %v, got %v", tc.in, tc.want, got)
			}
		})
	}
}

func TestEnvBoolInvalidFallback(t *testing.T) {
	// "yes" is not a strconv.ParseBool truthy value — EnvBool must return
	// the fallback rather than panic or treat it as true.
	t.Setenv(envKey, "yes")
	if got := config.EnvBool(envKey, false); got != false {
		t.Fatalf("EnvBool(yes, false): want false, got %v", got)
	}
	if got := config.EnvBool(envKey, true); got != true {
		t.Fatalf("EnvBool(yes, true): want true, got %v", got)
	}
}

// TestMustEnvBoolUnsetFallback: an unset variable returns the fallback with
// no error (the only non-error fallback path).
func TestMustEnvBoolUnsetFallback(t *testing.T) {
	for _, fb := range []bool{true, false} {
		got, err := config.MustEnvBool(envKey, fb) // envKey is unset here
		if err != nil {
			t.Fatalf("MustEnvBool unset: unexpected err %v", err)
		}
		if got != fb {
			t.Fatalf("MustEnvBool unset: want fallback %v, got %v", fb, got)
		}
	}
}

// TestMustEnvBoolValid: parseable values are returned regardless of fallback.
func TestMustEnvBoolValid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{{"true", true}, {"1", true}, {"t", true}, {"false", false}, {"0", false}, {"FALSE", false}}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Setenv(envKey, tc.in)
			got, err := config.MustEnvBool(envKey, !tc.want)
			if err != nil {
				t.Fatalf("MustEnvBool(%q): unexpected err %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("MustEnvBool(%q): want %v, got %v", tc.in, tc.want, got)
			}
		})
	}
}

// TestMustEnvBoolInvalidErrors: a SET-but-unparseable value is a HARD error —
// the whole point of MustEnvBool vs EnvBool. It must NOT silently return the
// fallback (that fail-open is what enabled the dry-run-typo footgun).
func TestMustEnvBoolInvalidErrors(t *testing.T) {
	for _, bad := range []string{"yes", "tru", "on", "y", "enabled"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv(envKey, bad)
			got, err := config.MustEnvBool(envKey, false)
			if err == nil {
				t.Fatalf("MustEnvBool(%q): want error, got nil (value=%v)", bad, got)
			}
			if got != false {
				t.Fatalf("MustEnvBool(%q): error path must return false, got %v", bad, got)
			}
		})
	}
}

// TestMustEnvDurationAtLeast exercises the eight-case matrix for the
// ACH_ORPHAN_CLEANUP_INTERVAL parser introduced in Plan 02-09 Task 1:
// default fallback / valid above-min / valid at-min boundary / below-min
// rejection / zero / negative / non-parseable / unit-missing.
func TestMustEnvDurationAtLeast(t *testing.T) {
	cases := []struct {
		name      string
		envValue  string
		defaultD  time.Duration
		minD      time.Duration
		wantD     time.Duration
		wantErr   bool
		errSubstr string
	}{
		{"empty uses default", "", 1 * time.Hour, 5 * time.Minute, 1 * time.Hour, false, ""},
		{"valid above min", "10m", 1 * time.Hour, 5 * time.Minute, 10 * time.Minute, false, ""},
		{"valid at min", "5m", 1 * time.Hour, 5 * time.Minute, 5 * time.Minute, false, ""},
		{"below min", "1m", 1 * time.Hour, 5 * time.Minute, 0, true, "below minimum"},
		{"zero", "0s", 1 * time.Hour, 5 * time.Minute, 0, true, "must be positive"},
		{"negative", "-5m", 1 * time.Hour, 5 * time.Minute, 0, true, "must be positive"},
		{"non-parseable", "abc", 1 * time.Hour, 5 * time.Minute, 0, true, "valid Go duration"},
		{"unit-missing", "1234", 1 * time.Hour, 5 * time.Minute, 0, true, "valid Go duration"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(envKey, c.envValue)
			got, err := config.MustEnvDurationAtLeast(envKey, c.defaultD, c.minD)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want err containing %q, got nil", c.errSubstr)
				}
				if !strings.Contains(err.Error(), c.errSubstr) {
					t.Fatalf("err=%q want contains %q", err.Error(), c.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != c.wantD {
				t.Fatalf("got %v want %v", got, c.wantD)
			}
		})
	}
}

func TestEnvIntNonNegDefault(t *testing.T) {
	t.Setenv(envKey, "")
	got, err := config.EnvIntNonNeg(envKey, 7)
	if err != nil {
		t.Fatalf("EnvIntNonNeg unset: unexpected error %v", err)
	}
	if got != 7 {
		t.Fatalf("EnvIntNonNeg unset: want fallback 7, got %d", got)
	}
}

func TestEnvIntNonNegZeroAllowed(t *testing.T) {
	// W3 (REVIEW): 0 is a legitimate value (Redis default DB).
	t.Setenv(envKey, "0")
	got, err := config.EnvIntNonNeg(envKey, 7)
	if err != nil {
		t.Fatalf("EnvIntNonNeg zero: unexpected error %v", err)
	}
	if got != 0 {
		t.Fatalf("EnvIntNonNeg zero: want 0, got %d", got)
	}
}

func TestEnvIntNonNegPositive(t *testing.T) {
	t.Setenv(envKey, "12")
	got, err := config.EnvIntNonNeg(envKey, 0)
	if err != nil {
		t.Fatalf("EnvIntNonNeg positive: unexpected error %v", err)
	}
	if got != 12 {
		t.Fatalf("EnvIntNonNeg positive: want 12, got %d", got)
	}
}

func TestEnvIntNonNegNegativeErrors(t *testing.T) {
	t.Setenv(envKey, "-1")
	_, err := config.EnvIntNonNeg(envKey, 0)
	if err == nil {
		t.Fatalf("EnvIntNonNeg negative: want error, got nil")
	}
	if !strings.Contains(err.Error(), envKey) {
		t.Fatalf("EnvIntNonNeg negative: error %q must contain key %q", err.Error(), envKey)
	}
}

func TestEnvIntNonNegNonNumericErrors(t *testing.T) {
	t.Setenv(envKey, "abc")
	_, err := config.EnvIntNonNeg(envKey, 0)
	if err == nil {
		t.Fatalf("EnvIntNonNeg non-numeric: want error, got nil")
	}
}

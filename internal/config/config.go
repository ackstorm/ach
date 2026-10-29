// SPDX-License-Identifier: Apache-2.0

// Package config provides env-var-first configuration helpers shared by every
// ACH binary (operator, platform-api, forwarder, content-service, ach CLI).
//
// Discretion default (CONTEXT §"Defaults Claude will pick"): "os.Getenv + a
// small validation helper. No viper." This package is that small helper. It
// has no third-party dependencies and no logger of its own — errors are
// returned to the caller which decides how to surface them (typically a
// fatal startup log + os.Exit(1) per D-08/D-09).
//
// The four exported functions cover the production knob surface:
//
//   - EnvOr               — soft default (ACH_NAMESPACE, ACH_CACHE_ROOT,
//     METRICS_BIND_ADDRESS, …).
//   - EnvBool             — soft default booleans (LEADER_ELECT in dev, …).
//   - MustEnvNonEmpty     — fail-fast required strings (ACH_DB_URL,
//     ACH_CREDENTIAL_HASH_PEPPER — D-08, D-09).
//   - MustEnvIntPositive  — fail-fast positive integers
//     (ACH_PLUGIN_MAX_SIZE_MIB — OP-09 forward-compat).
//
// MustEnvNonEmpty is also used to enforce the §16.1 pepper invariant: the
// caller in cmd/operator/main.go (Plan 06) compares the returned value
// against the literal placeholder text that ships in the Helm-rendered
// Secret manifest (Plan 08), refusing to start when the placeholder has
// not been replaced. That comparison lives at the call site so this
// package stays a generic env-var helper.
package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// EnvOr returns the value of the environment variable named key, or fallback
// when the variable is unset or empty. Used for soft-default knobs:
//
//	watchNS := config.EnvOr("ACH_NAMESPACE", "ach-system")
//	cacheRoot := config.EnvOr("ACH_CACHE_ROOT", "/var/cache/ach")
//	metricsAddr := config.EnvOr("METRICS_BIND_ADDRESS", ":8080")
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// EnvBool parses the named environment variable via strconv.ParseBool. Returns
// fallback when the variable is unset/empty OR when the value fails to parse.
// Accepted truthy values: "1", "t", "T", "TRUE", "true", "True". Accepted
// falsy: "0", "f", "F", "FALSE", "false", "False". Any other value yields
// the fallback — callers that need strict parsing should layer their own
// validation.
func EnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// MustEnvBool parses the named environment variable via strconv.ParseBool.
// Unlike EnvBool, a SET-but-unparseable value is a hard error rather than a
// silent fallback. Returns fallback ONLY when the variable is unset/empty.
//
// Use this for safety-critical toggles where a typo silently resolving to the
// unsafe default would be dangerous — e.g. ACH_ORPHAN_CLEANUP_DRY_RUN, the B3
// neutralize for a destructive revoke loop: with EnvBool, `=tru` or `=yes`
// silently disables dry-run and re-enables real revocation. MustEnvBool makes
// the operator fix the manifest instead of failing open. The error names the
// variable but NOT its value (no chance of leaking a secret via logs).
func MustEnvBool(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, errors.New("config: " + key + " must be a boolean (true/false/1/0)")
	}
	return b, nil
}

// MustEnvNonEmpty returns the value of the named environment variable, or an
// error when the variable is unset or empty. The error message contains the
// variable name so the operator can fix the deployment manifest without
// reading source. It does NOT contain the value (no chance of leaking
// secrets via error-bearing logs).
//
// Used at startup for the two D-08 / D-09 invariants:
//
//	dbURL, err := config.MustEnvNonEmpty("ACH_DB_URL")
//	pepper, err := config.MustEnvNonEmpty("ACH_CREDENTIAL_HASH_PEPPER")
//
// On error, the caller (typically cmd/operator/main.go) logs the error
// and calls os.Exit(1) — there is no best-effort/silent-skip behavior on
// a missing required env var.
func MustEnvNonEmpty(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", errors.New("config: " + key + " is empty or unset")
	}
	return v, nil
}

// MustEnvIntPositive parses a positive integer from the named environment
// variable. Returns fallback when the variable is unset/empty; returns an
// error when the value parses to zero, a negative number, or a non-numeric
// string. Used for OP-09's ACH_PLUGIN_MAX_SIZE_MIB knob: per Hub §11 the
// operator refuses to start when the plugin size limit is misconfigured
// (zero, negative, or non-numeric).
//
// The fallback path is intentional — Phase 1 ships the knob at default 50
// (MiB) but the value is unused until Phase 2's plugin tarball extraction
// lands. The contract belongs with the Operator from day one so the
// production deployment is identical between Phase 1 and Phase 2.
func MustEnvIntPositive(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, errors.New("config: " + key + " must be a positive integer")
	}
	return n, nil
}

// EnvIntNonNeg parses a non-negative integer (zero allowed) from the
// named environment variable. Returns fallback when the variable is
// unset/empty; returns an error when the value parses to a negative
// number or a non-numeric string.
//
// The contract differs from MustEnvIntPositive in that 0 is a valid
// value, not an error. Used for env vars whose 0 is a legitimate
// default — e.g. ACH_REDIS_DB=0 selects the default Redis logical
// database (rejecting 0 as invalid would force every Forwarder
// deployment that uses the default DB to omit the env var entirely
// just to dodge the contract mismatch).
func EnvIntNonNeg(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, errors.New("config: " + key + " must be a non-negative integer")
	}
	return n, nil
}

// MustEnvDurationAtLeast parses key as a Go time.Duration string (e.g.
// "1h", "15m", "30s"). Behavior:
//
//   - Unset / empty            → returns (defaultDur, nil).
//   - Parse failure            → returns (0, err) with a config: prefix.
//   - Zero / negative          → returns (0, err) — both are invalid for
//     a cleanup-interval semantic.
//   - Below minDur             → returns (0, err) naming the value and min.
//   - Valid + >= minDur        → returns (parsed, nil).
//
// Used for ACH_ORPHAN_CLEANUP_INTERVAL (default 1h, minimum 5m per OP-15
// / D-15). The caller (cmd/operator/main.go) logs the error and calls
// os.Exit(1) — there is no best-effort/silent-skip on a malformed
// cleanup interval.
func MustEnvDurationAtLeast(key string, defaultDur, minDur time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultDur, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, errors.New("config: " + key + " is not a valid Go duration string (e.g. '15m', '1h')")
	}
	if d <= 0 {
		return 0, errors.New("config: " + key + " must be positive (got " + d.String() + ")")
	}
	if d < minDur {
		return 0, errors.New("config: " + key + "=" + d.String() + " is below minimum " + minDur.String())
	}
	return d, nil
}

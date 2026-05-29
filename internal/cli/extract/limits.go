// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// ResourceKind enumerates the per-resource categories the bomb-defense caps
// route on. Prompts have no archive (single opaque file) and therefore no
// cap; they remain in the enum so callers can pass a `ResourceKind` to
// every helper without a separate code path for prompts. The enum is
// exhaustive — callers using `switch k` on `ResourceKind` should cover all
// three constants.
type ResourceKind string

// ResourceKind constants. Values are the spec §6.4 / §10 lowercase resource
// identifiers ("plugin", "artifact", "prompt") so they double as URL path
// segments in the content service consumer (`GET /content/{kind}/{name}`).
const (
	KindPlugin   ResourceKind = "plugin"
	KindArtifact ResourceKind = "artifact"
	KindPrompt   ResourceKind = "prompt"
)

// Bomb-defense env-var names + default MiB caps + default entry count.
// Defaults match CLI spec §6.4 / PRD D-12 verbatim.
const (
	envPluginMaxMiB   = "ACH_MAX_EXTRACTED_PLUGIN_MIB"
	envArtifactMaxMiB = "ACH_MAX_EXTRACTED_ARTIFACT_MIB"
	envMaxEntries     = "ACH_MAX_ARCHIVE_ENTRIES"

	defaultPluginMaxMiB   = 200
	defaultArtifactMaxMiB = 500
	defaultMaxEntries     = 65536

	mibToBytes = 1024 * 1024
)

// Limits is the per-hydrate bomb-defense configuration the tar extraction
// loop enforces. Byte caps are stored as int64 bytes (MiB resolved at
// LoadLimits) so the streaming counter compares against a single typed
// scalar without re-multiplying per entry.
type Limits struct {
	// MaxExtractedPluginBytes is the per-plugin-archive uncompressed byte
	// cap, sourced from ACH_MAX_EXTRACTED_PLUGIN_MIB × 1 MiB.
	MaxExtractedPluginBytes int64

	// MaxExtractedArtifactBytes is the per-artifact-archive uncompressed
	// byte cap, sourced from ACH_MAX_EXTRACTED_ARTIFACT_MIB × 1 MiB.
	MaxExtractedArtifactBytes int64

	// MaxEntries is the per-archive entry-count cap, sourced from
	// ACH_MAX_ARCHIVE_ENTRIES. The entry-count check fires BEFORE
	// reading the offending entry's body so a billion-file archive
	// never gets the chance to stream.
	MaxEntries int
}

// MaxBytesForKind returns the per-kind uncompressed byte cap. A return
// value of 0 means "no cap" — used for KindPrompt (single opaque file,
// no archive, size-bounded upstream by the Content Service).
func (l Limits) MaxBytesForKind(k ResourceKind) int64 {
	switch k {
	case KindPlugin:
		return l.MaxExtractedPluginBytes
	case KindArtifact:
		return l.MaxExtractedArtifactBytes
	case KindPrompt:
		return 0
	}
	// Unknown kind — fall through to the most-restrictive plugin cap so a
	// future caller passing a stale identifier cannot accidentally bypass
	// bomb defense. The Extract entry point validates the kind separately.
	return l.MaxExtractedPluginBytes
}

// DefaultLimits returns the literal D-12 defaults — used by the dry-run
// path and by tests that exercise the default configuration without
// touching the process environment.
func DefaultLimits() Limits {
	return Limits{
		MaxExtractedPluginBytes:   int64(defaultPluginMaxMiB) * mibToBytes,
		MaxExtractedArtifactBytes: int64(defaultArtifactMaxMiB) * mibToBytes,
		MaxEntries:                defaultMaxEntries,
	}
}

// LoadLimits reads the three bomb-defense env vars at hydrate start.
//
// Behavior matches the operator-side ACH_PLUGIN_MAX_SIZE_MIB discipline
// (OP-09 / Phase 1 internal/config.MustEnvIntPositive): empty/unset values
// take the default; explicit zero, negative, or non-numeric values return
// an error citing the offending variable name. The caller (the hydrate
// orchestrator) treats a non-nil error as a fatal pre-flight failure and
// emits exit 1 — consistent with CLAUDE.md "Claude's Discretion" bomb-cap
// validation guidance.
//
// Error messages contain the variable name but NOT the offending value
// (consistency with internal/config.MustEnvIntPositive — operators read
// the deployment manifest, not the agent's stderr, to find the bad value).
func LoadLimits() (Limits, error) {
	pluginMiB, err := loadPositiveInt(envPluginMaxMiB, defaultPluginMaxMiB)
	if err != nil {
		return Limits{}, err
	}
	artifactMiB, err := loadPositiveInt(envArtifactMaxMiB, defaultArtifactMaxMiB)
	if err != nil {
		return Limits{}, err
	}
	entries, err := loadPositiveInt(envMaxEntries, defaultMaxEntries)
	if err != nil {
		return Limits{}, err
	}
	return Limits{
		MaxExtractedPluginBytes:   int64(pluginMiB) * mibToBytes,
		MaxExtractedArtifactBytes: int64(artifactMiB) * mibToBytes,
		MaxEntries:                entries,
	}, nil
}

// loadPositiveInt reads a positive integer from the named env var. Returns
// fallback when the variable is unset or empty (the soft-default path);
// returns an error when the value is zero, negative, or non-numeric. This
// is the same shape as internal/config.MustEnvIntPositive but kept local
// here so the extract package does not pull a controller-runtime-adjacent
// dependency just for env parsing.
func loadPositiveInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("extract: %s must be a positive integer: %w", key, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("extract: %s must be a positive integer (got %d): %w", key, n, errNotPositive)
	}
	return n, nil
}

// errNotPositive is the sentinel for the zero/negative validation arm of
// loadPositiveInt. It lets callers (and tests) do errors.Is on a
// zero-or-negative env-var rejection without parsing the message string.
var errNotPositive = errors.New("extract: env var must be > 0")

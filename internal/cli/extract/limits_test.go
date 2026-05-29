// SPDX-License-Identifier: Apache-2.0

package extract_test

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
)

// TestLoadLimits_Defaults_NoEnvSet asserts the D-12 default values when
// none of the three env vars are present in the environment. t.Setenv
// with an empty string sets the variable to empty, which our parser
// treats as "unset" (same as `unset VAR`); to actually clear the var
// here we rely on t.Setenv's restore-on-return behavior and the test
// runner's clean baseline.
func TestLoadLimits_Defaults_NoEnvSet(t *testing.T) {
	// Defensive: explicitly unset via Setenv to "" then leave it — this
	// also documents the contract that empty == unset.
	t.Setenv("ACH_MAX_EXTRACTED_PLUGIN_MIB", "")
	t.Setenv("ACH_MAX_EXTRACTED_ARTIFACT_MIB", "")
	t.Setenv("ACH_MAX_ARCHIVE_ENTRIES", "")

	got, err := extract.LoadLimits()
	if err != nil {
		t.Fatalf("LoadLimits() returned error: %v", err)
	}

	const mib = 1024 * 1024
	if got.MaxExtractedPluginBytes != 200*mib {
		t.Errorf("MaxExtractedPluginBytes = %d, want %d (200 MiB)", got.MaxExtractedPluginBytes, 200*mib)
	}
	if got.MaxExtractedArtifactBytes != 500*mib {
		t.Errorf("MaxExtractedArtifactBytes = %d, want %d (500 MiB)", got.MaxExtractedArtifactBytes, 500*mib)
	}
	if got.MaxEntries != 65536 {
		t.Errorf("MaxEntries = %d, want 65536", got.MaxEntries)
	}
}

// TestLoadLimits_RejectsZero asserts ACH_MAX_EXTRACTED_PLUGIN_MIB=0
// produces an error citing the offending variable name (per the
// operator-side ACH_PLUGIN_MAX_SIZE_MIB discipline).
func TestLoadLimits_RejectsZero(t *testing.T) {
	t.Setenv("ACH_MAX_EXTRACTED_PLUGIN_MIB", "0")
	_, err := extract.LoadLimits()
	if err == nil {
		t.Fatalf("LoadLimits() with PLUGIN_MIB=0: want error, got nil")
	}
	if !strings.Contains(err.Error(), "ACH_MAX_EXTRACTED_PLUGIN_MIB") {
		t.Errorf("error %q does not name the offending var ACH_MAX_EXTRACTED_PLUGIN_MIB", err.Error())
	}
}

// TestLoadLimits_RejectsNegative asserts ACH_MAX_ARCHIVE_ENTRIES=-1 errors.
func TestLoadLimits_RejectsNegative(t *testing.T) {
	t.Setenv("ACH_MAX_ARCHIVE_ENTRIES", "-1")
	_, err := extract.LoadLimits()
	if err == nil {
		t.Fatalf("LoadLimits() with ENTRIES=-1: want error, got nil")
	}
	if !strings.Contains(err.Error(), "ACH_MAX_ARCHIVE_ENTRIES") {
		t.Errorf("error %q does not name the offending var ACH_MAX_ARCHIVE_ENTRIES", err.Error())
	}
}

// TestLoadLimits_RejectsNonNumeric asserts ACH_MAX_EXTRACTED_ARTIFACT_MIB=abc errors.
func TestLoadLimits_RejectsNonNumeric(t *testing.T) {
	t.Setenv("ACH_MAX_EXTRACTED_ARTIFACT_MIB", "abc")
	_, err := extract.LoadLimits()
	if err == nil {
		t.Fatalf("LoadLimits() with ARTIFACT_MIB=abc: want error, got nil")
	}
	if !strings.Contains(err.Error(), "ACH_MAX_EXTRACTED_ARTIFACT_MIB") {
		t.Errorf("error %q does not name the offending var ACH_MAX_EXTRACTED_ARTIFACT_MIB", err.Error())
	}
}

// TestLoadLimits_MiBToBytes asserts the MiB → bytes conversion: 10 MiB
// becomes 10 * 1024 * 1024 bytes.
func TestLoadLimits_MiBToBytes(t *testing.T) {
	t.Setenv("ACH_MAX_EXTRACTED_PLUGIN_MIB", "10")
	t.Setenv("ACH_MAX_EXTRACTED_ARTIFACT_MIB", "20")
	t.Setenv("ACH_MAX_ARCHIVE_ENTRIES", "100")

	got, err := extract.LoadLimits()
	if err != nil {
		t.Fatalf("LoadLimits() returned error: %v", err)
	}
	const mib = int64(1024 * 1024)
	if got.MaxExtractedPluginBytes != 10*mib {
		t.Errorf("PluginBytes = %d, want %d (10 MiB)", got.MaxExtractedPluginBytes, 10*mib)
	}
	if got.MaxExtractedArtifactBytes != 20*mib {
		t.Errorf("ArtifactBytes = %d, want %d (20 MiB)", got.MaxExtractedArtifactBytes, 20*mib)
	}
	if got.MaxEntries != 100 {
		t.Errorf("MaxEntries = %d, want 100", got.MaxEntries)
	}
}

// TestMaxBytesForKind_Plugin asserts MaxBytesForKind routes to PluginBytes.
func TestMaxBytesForKind_Plugin(t *testing.T) {
	l := extract.Limits{
		MaxExtractedPluginBytes:   1234,
		MaxExtractedArtifactBytes: 5678,
		MaxEntries:                9,
	}
	if got := l.MaxBytesForKind(extract.KindPlugin); got != 1234 {
		t.Errorf("MaxBytesForKind(plugin) = %d, want 1234", got)
	}
}

// TestMaxBytesForKind_Artifact asserts MaxBytesForKind routes to ArtifactBytes.
func TestMaxBytesForKind_Artifact(t *testing.T) {
	l := extract.Limits{
		MaxExtractedPluginBytes:   1234,
		MaxExtractedArtifactBytes: 5678,
		MaxEntries:                9,
	}
	if got := l.MaxBytesForKind(extract.KindArtifact); got != 5678 {
		t.Errorf("MaxBytesForKind(artifact) = %d, want 5678", got)
	}
}

// TestMaxBytesForKind_Prompt asserts MaxBytesForKind returns 0 (unlimited).
// Prompts are opaque single files served by the Content Service; there is
// no archive to bomb-defend.
func TestMaxBytesForKind_Prompt(t *testing.T) {
	l := extract.DefaultLimits()
	if got := l.MaxBytesForKind(extract.KindPrompt); got != 0 {
		t.Errorf("MaxBytesForKind(prompt) = %d, want 0 (no cap)", got)
	}
}

// TestDefaultLimits_LiteralD12 asserts DefaultLimits returns the literal
// PRD D-12 values so a future refactor of LoadLimits cannot accidentally
// move the defaults out from under code paths that rely on DefaultLimits().
func TestDefaultLimits_LiteralD12(t *testing.T) {
	got := extract.DefaultLimits()
	const mib = int64(1024 * 1024)
	if got.MaxExtractedPluginBytes != 200*mib {
		t.Errorf("DefaultLimits().MaxExtractedPluginBytes = %d, want %d", got.MaxExtractedPluginBytes, 200*mib)
	}
	if got.MaxExtractedArtifactBytes != 500*mib {
		t.Errorf("DefaultLimits().MaxExtractedArtifactBytes = %d, want %d", got.MaxExtractedArtifactBytes, 500*mib)
	}
	if got.MaxEntries != 65536 {
		t.Errorf("DefaultLimits().MaxEntries = %d, want 65536", got.MaxEntries)
	}
}

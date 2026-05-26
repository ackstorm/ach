// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 1: unit tests for compileAnchored + applyFilters.
// Pure-Go (no envtest); runs with `go test ./internal/controller/ach/...`.

package ach

import (
	"errors"
	"testing"
)

func TestCompileAnchored_PrependsCaret(t *testing.T) {
	out, err := compileAnchored([]string{"foo.*"})
	if err != nil {
		t.Fatalf("compile err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 regex; got %d", len(out))
	}
	if out[0].String() != "^foo.*" {
		t.Errorf("Regexp.String() = %q; want %q", out[0].String(), "^foo.*")
	}
}

func TestCompileAnchored_InvalidPattern(t *testing.T) {
	_, err := compileAnchored([]string{"[unclosed"})
	if err == nil {
		t.Fatal("expected err on invalid RE2; got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("err should wrap ErrInvalidConfig; got %v", err)
	}
}

func TestCompileAnchored_EmptyInput(t *testing.T) {
	out, err := compileAnchored(nil)
	if err != nil {
		t.Fatalf("unexpected err on nil input: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil result; got %v", out)
	}
	out, err = compileAnchored([]string{})
	if err != nil {
		t.Fatalf("unexpected err on empty slice: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil result on empty slice; got %v", out)
	}
}

func threePlugins() []ClaudeCodeMarketplacePlugin {
	return []ClaudeCodeMarketplacePlugin{
		{Name: "alpha"},
		{Name: "beta"},
		{Name: "charlie"},
	}
}

func names(ps []ClaudeCodeMarketplacePlugin) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestApplyFilters_IncludeOnly_MatchesSome(t *testing.T) {
	include, _ := compileAnchored([]string{"a.*"})
	kept, matched := applyFilters(threePlugins(), include, nil)
	if !matched {
		t.Error("includeMatchedAny should be true")
	}
	if !equalStrings(names(kept), []string{"alpha"}) {
		t.Errorf("kept = %v; want [alpha]", names(kept))
	}
}

func TestApplyFilters_IncludeOnly_MatchesNone(t *testing.T) {
	include, _ := compileAnchored([]string{"z.*"})
	kept, matched := applyFilters(threePlugins(), include, nil)
	if matched {
		t.Error("includeMatchedAny should be false when include matches nothing")
	}
	if len(kept) != 0 {
		t.Errorf("kept should be empty; got %v", names(kept))
	}
}

func TestApplyFilters_ExcludeOnly(t *testing.T) {
	exclude, _ := compileAnchored([]string{"b.*"})
	kept, matched := applyFilters(threePlugins(), nil, exclude)
	if !matched {
		t.Error("with nil include, matched should be vacuously true")
	}
	if !equalStrings(names(kept), []string{"alpha", "charlie"}) {
		t.Errorf("kept = %v; want [alpha charlie]", names(kept))
	}
}

func TestApplyFilters_IncludeAndExclude(t *testing.T) {
	include, _ := compileAnchored([]string{"a.*", "b.*"})
	exclude, _ := compileAnchored([]string{"b.*"})
	kept, matched := applyFilters(threePlugins(), include, exclude)
	if !matched {
		t.Error("includeMatchedAny should be true")
	}
	if !equalStrings(names(kept), []string{"alpha"}) {
		t.Errorf("kept = %v; want [alpha]", names(kept))
	}
}

func TestApplyFilters_NeitherSet(t *testing.T) {
	kept, matched := applyFilters(threePlugins(), nil, nil)
	if !matched {
		t.Error("with nil include, matched should be vacuously true")
	}
	if !equalStrings(names(kept), []string{"alpha", "beta", "charlie"}) {
		t.Errorf("kept = %v; want all three", names(kept))
	}
}

func TestApplyFilters_AnchorPrependedNotSubstring(t *testing.T) {
	// "lph" matches anywhere in "alpha" with unanchored regex, but the
	// operator-prepended ^ makes it match only at start — so 'lph' alone
	// should NOT match 'alpha'.
	include, _ := compileAnchored([]string{"lph"})
	kept, matched := applyFilters(threePlugins(), include, nil)
	if matched {
		t.Errorf("anchored 'lph' should NOT match 'alpha'; matched=true, kept=%v", names(kept))
	}
	if len(kept) != 0 {
		t.Errorf("kept should be empty under ^-anchored 'lph'; got %v", names(kept))
	}
}

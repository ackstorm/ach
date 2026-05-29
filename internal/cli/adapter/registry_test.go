// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"context"
	"testing"

	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// fakeAdapter is an Adapter impl local to this test file. It returns
// zero values from every method so the tests focus on registry
// behavior, not adapter semantics.
type fakeAdapter struct {
	id      string
	aliases []string
}

func (f *fakeAdapter) ID() string        { return f.id }
func (f *fakeAdapter) Aliases() []string { return f.aliases }
func (f *fakeAdapter) Detect(_ string) (Match, error) {
	return Match{}, nil
}
func (f *fakeAdapter) RenderRuntime(_ context.Context, _ *manifest.Manifest, _ *state.File) ([]FileWrite, error) {
	return nil, nil
}
func (f *fakeAdapter) TransformPlugin(_ context.Context, _, _ string) (PluginWrite, error) {
	return PluginWrite{}, nil
}
func (f *fakeAdapter) MergeStrategies() map[string]MergeKind { return nil }
func (f *fakeAdapter) ResolveOutputContent(_ context.Context, _ *manifest.Manifest, _ string) ([]byte, error) {
	return nil, nil
}

func TestRegister_Duplicate_Panics(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "alpha", aliases: []string{"a"}})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate ID, got nil")
		}
	}()
	Register(&fakeAdapter{id: "alpha", aliases: []string{"alt"}})
}

func TestRegister_DuplicateAlias_Panics(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "alpha", aliases: []string{"a"}})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate alias, got nil")
		}
	}()
	Register(&fakeAdapter{id: "beta", aliases: []string{"a"}})
}

func TestRegister_NilAdapter_Panics(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil Adapter, got nil")
		}
	}()
	Register(nil)
}

func TestRegister_EmptyID_Panics(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty ID, got nil")
		}
	}()
	Register(&fakeAdapter{id: "", aliases: []string{"a"}})
}

func TestLookup_ByCanonicalID(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	a := &fakeAdapter{id: "claude-code", aliases: []string{"claude", "cc"}}
	Register(a)

	got, ok := Lookup("claude-code")
	if !ok {
		t.Fatal("Lookup(canonical) returned false")
	}
	if got.ID() != "claude-code" {
		t.Fatalf("Lookup returned adapter with ID %q, want %q", got.ID(), "claude-code")
	}
}

func TestLookup_ByAlias_CaseInsensitive(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "claude-code", aliases: []string{"claude", "cc"}})

	cases := []string{"claude", "CLAUDE", "Claude", "cc", "CC"}
	for _, input := range cases {
		got, ok := Lookup(input)
		if !ok {
			t.Errorf("Lookup(%q) returned false; expected case-insensitive alias resolution", input)
			continue
		}
		if got.ID() != "claude-code" {
			t.Errorf("Lookup(%q) returned adapter with ID %q; want %q", input, got.ID(), "claude-code")
		}
	}
}

func TestLookup_CanonicalID_CaseInsensitive(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "claude-code", aliases: []string{"cc"}})

	got, ok := Lookup("CLAUDE-CODE")
	if !ok {
		t.Fatal("Lookup(CANONICAL upper-case) returned false; expected case-insensitive canonical resolution")
	}
	if got.ID() != "claude-code" {
		t.Fatalf("Lookup returned adapter with ID %q, want %q", got.ID(), "claude-code")
	}
}

func TestLookup_Unknown_ReturnsFalse(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "alpha", aliases: []string{"a"}})

	if _, ok := Lookup("nonexistent"); ok {
		t.Fatal("Lookup(unknown) returned true; expected false")
	}
}

func TestLookup_EmptyInput_ReturnsFalse(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "alpha", aliases: []string{"a"}})

	if _, ok := Lookup(""); ok {
		t.Fatal("Lookup(\"\") returned true; expected false")
	}
}

func TestIter_ReturnsAllRegistered(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "alpha", aliases: []string{"a"}})
	Register(&fakeAdapter{id: "beta", aliases: []string{"b"}})
	Register(&fakeAdapter{id: "gamma", aliases: []string{"g"}})

	got := Iter()
	if len(got) != 3 {
		t.Fatalf("Iter() returned %d adapters; want 3", len(got))
	}

	ids := make(map[string]bool, 3)
	for _, a := range got {
		ids[a.ID()] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !ids[want] {
			t.Errorf("Iter() missing adapter ID %q", want)
		}
	}
}

func TestIter_EmptyRegistry_ReturnsEmptySlice(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	got := Iter()
	if len(got) != 0 {
		t.Fatalf("Iter() on empty registry returned %d adapters; want 0", len(got))
	}
}

func TestWithCredential_RoundTrips(t *testing.T) {
	ctx := WithCredential(context.Background(), "pk_demo")
	got := CredentialFromContext(ctx)
	if got != "pk_demo" {
		t.Fatalf("CredentialFromContext(WithCredential(ctx, %q)) = %q; want %q", "pk_demo", got, "pk_demo")
	}
}

func TestCredentialFromContext_NoValue_ReturnsEmpty(t *testing.T) {
	got := CredentialFromContext(context.Background())
	if got != "" {
		t.Fatalf("CredentialFromContext(empty ctx) = %q; want empty", got)
	}
}

func TestCredentialFromContext_NilContext_ReturnsEmpty(t *testing.T) {
	got := CredentialFromContext(nil) //nolint:staticcheck // testing the nil-defense
	if got != "" {
		t.Fatalf("CredentialFromContext(nil) = %q; want empty", got)
	}
}

func TestWithCredential_EmptyBearer_StoresEmpty(t *testing.T) {
	ctx := WithCredential(context.Background(), "")
	got := CredentialFromContext(ctx)
	if got != "" {
		t.Fatalf("CredentialFromContext(WithCredential(ctx, \"\")) = %q; want empty", got)
	}
}

func TestRegister_AliasNamespaceFlat_RejectsAliasCollisionWithCanonicalID(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Register(&fakeAdapter{id: "claude-code", aliases: []string{"cc"}})

	// "claude-code" is the canonical ID; a later registration that
	// tries to use "claude-code" as an alias for a different adapter
	// must panic — the namespaces are flat.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on alias colliding with existing canonical ID, got nil")
		}
	}()
	Register(&fakeAdapter{id: "codex", aliases: []string{"claude-code"}})
}

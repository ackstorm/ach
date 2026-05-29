// SPDX-License-Identifier: Apache-2.0

package extract_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/state"
)

// fakeResolver is an inline ContentResolver used by Tier-2 tests. The
// caller controls both the bytes returned and any error surfaced — the
// test asserts on Cascade's downstream handling of either.
type fakeResolver struct {
	result []byte
	err    error
}

func (f *fakeResolver) Resolve(_ context.Context, _ string) ([]byte, error) {
	return f.result, f.err
}

// fakeSourceFn constructs an inline SourceFn closure used by Tier-3
// tests. Same pattern as fakeResolver — caller-controlled return value.
func fakeSourceFn(result []byte, err error) extract.SourceFn {
	return func(_ context.Context, _ string) ([]byte, error) {
		return result, err
	}
}

// ===== Classify =====

func TestClassify_NoFile_None(t *testing.T) {
	tmp := t.TempDir()
	absent := filepath.Join(tmp, "does-not-exist.txt")

	got, err := extract.Classify(absent, tmp, nil)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionNone {
		t.Fatalf("got %v, want CollisionNone", got)
	}
}

func TestClassify_FileInState_OwnedByCurrent(t *testing.T) {
	// Existing test pattern: target is constructed absolute under tmp,
	// and the state entry stores the workspace-RELATIVE form ("foo.md").
	// Classify normalizes via filepath.Join(achDir, entry.Target).
	tmp := t.TempDir()
	target := filepath.Join(tmp, "foo.md")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
		Prompts: []state.FileEntry{
			{Target: "foo.md", Hash: "xxh3:deadbeef", SourceHash: "xxh3:deadbeef"},
		},
	}

	got, err := extract.Classify(target, tmp, sf)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionOwnedByCurrent {
		t.Fatalf("got %v, want CollisionOwnedByCurrent", got)
	}
}

func TestClassify_FileNotInState_ExistsUnowned(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "stray.md")
	if err := os.WriteFile(target, []byte("foreign"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// Empty state — file exists but is not registered.
	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
	}

	got, err := extract.Classify(target, tmp, sf)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionExistsUnowned {
		t.Fatalf("got %v, want CollisionExistsUnowned", got)
	}
}

func TestClassify_NilStateFile_ExistsUnowned(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "stray.md")
	if err := os.WriteFile(target, []byte("foreign"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	got, err := extract.Classify(target, tmp, nil)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionExistsUnowned {
		t.Fatalf("nil state should yield CollisionExistsUnowned, got %v", got)
	}
}

func TestClassify_AdapterFile_OwnedByCurrent(t *testing.T) {
	// Adapter.Files arm of walkAllEntries — a `.claude/.mcp.json` style
	// merged file owned by the platform adapter, not by any resource bucket.
	tmp := t.TempDir()
	target := filepath.Join(tmp, ".claude", ".mcp.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
		Adapter: state.AdapterSection{
			ID: "claude",
			Files: []state.FileEntry{
				{Target: ".claude/.mcp.json", Hash: "xxh3:xx", SourceHash: "xxh3:xx", Merge: "deep"},
			},
		},
	}

	got, err := extract.Classify(target, tmp, sf)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionOwnedByCurrent {
		t.Fatalf("got %v, want CollisionOwnedByCurrent (adapter file)", got)
	}
}

// ===== CR-03 regression set — path normalization in Classify =====

// TestClassifyRelativeTarget_NormalizesToAbsoluteAndReturnsOwned is
// the load-bearing positive case for CR-03 (07-VERIFICATION.md
// gaps[2]). The state entry stores `.claude/.mcp.json` workspace-
// relative; the caller passes the absolute form constructed via
// filepath.Join(achDir, ".claude/.mcp.json"). The fixed Classify
// normalizes entry.Target the same way and returns
// CollisionOwnedByCurrent — the re-hydrate flow no longer fires
// SAFE-04 against the engine's own prior output.
func TestClassifyRelativeTarget_NormalizesToAbsoluteAndReturnsOwned(t *testing.T) {
	achDir := t.TempDir()
	rel := filepath.Join(".claude", ".mcp.json")
	finalAbs := filepath.Join(achDir, rel)

	if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(finalAbs, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
		Adapter: state.AdapterSection{
			ID: "claude",
			Files: []state.FileEntry{
				{Target: rel, Hash: "xxh3:aa", SourceHash: "xxh3:aa", Merge: "deep"},
			},
		},
	}

	got, err := extract.Classify(finalAbs, achDir, sf)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionOwnedByCurrent {
		t.Fatalf("got %v, want CollisionOwnedByCurrent — CR-03 fix unreachable", got)
	}
}

// TestClassifyRelativeTarget_NoMatch_ReturnsUnowned is the negative
// case proving the normalization does not silently match mismatched
// entries. State entry references `.codex/config.toml`; the queried
// file is `.claude/.mcp.json`. Both are relative-Target valid; the
// classification must remain CollisionExistsUnowned. Catches a
// regression where normalization is too loose (e.g. comparing only
// basenames).
func TestClassifyRelativeTarget_NoMatch_ReturnsUnowned(t *testing.T) {
	achDir := t.TempDir()
	finalAbs := filepath.Join(achDir, ".claude", ".mcp.json")

	if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(finalAbs, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
		Adapter: state.AdapterSection{
			ID: "codex",
			Files: []state.FileEntry{
				{Target: ".codex/config.toml", Hash: "xxh3:bb", SourceHash: "xxh3:bb", Merge: "deep"},
			},
		},
	}

	got, err := extract.Classify(finalAbs, achDir, sf)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionExistsUnowned {
		t.Fatalf("got %v, want CollisionExistsUnowned — mismatched entries must not pivot classification", got)
	}
}

// TestClassifyAbsoluteTarget_Rejected proves the defensive arm:
// spec §8.2 mandates relative Target; an absolute value is a malformed
// state.json entry that must surface to the caller via
// ErrTargetNotRelative rather than be silently compared bypassing the
// achDir normalization (T-07-W5-03-03).
func TestClassifyAbsoluteTarget_Rejected(t *testing.T) {
	achDir := t.TempDir()
	finalAbs := filepath.Join(achDir, "foo.md")
	if err := os.WriteFile(finalAbs, []byte("hello"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
		Prompts: []state.FileEntry{
			{Target: "/etc/passwd", Hash: "xxh3:cc", SourceHash: "xxh3:cc"},
		},
	}

	_, err := extract.Classify(finalAbs, achDir, sf)
	if err == nil {
		t.Fatalf("expected ErrTargetNotRelative, got nil")
	}
	if !errors.Is(err, extract.ErrTargetNotRelative) {
		t.Fatalf("error chain does not contain ErrTargetNotRelative: %v", err)
	}
}

// TestClassifyDotDotTarget_DoesNotMatch proves the containment-check
// arm: a relative Target that escapes achDir via `..` must NOT pivot
// the classification to Owned even when filepath.Join(achDir, target)
// happens to equal finalPath (T-07-W5-03-02). The Target
// `../../etc/passwd` normalized against achDir resolves outside the
// workspace; the containment check refuses to treat it as Owned and
// returns CollisionExistsUnowned.
func TestClassifyDotDotTarget_DoesNotMatch(t *testing.T) {
	achDir := t.TempDir()
	// Construct a finalAbs that is outside achDir AND on-disk-present —
	// this is the worst-case scenario where a naive string-prefix or
	// HasPrefix check might fail open. The file must exist for os.Stat
	// to fall through to the state-walk arm.
	finalAbs := filepath.Join(t.TempDir(), "stray.md")
	if err := os.WriteFile(finalAbs, []byte("foreign"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Compute the relative Target that, when Join'd against achDir,
	// resolves to finalAbs. Use filepath.Rel — this produces a path
	// starting with `..` (since finalAbs is in a sibling temp dir).
	rel, err := filepath.Rel(achDir, finalAbs)
	if err != nil {
		t.Fatalf("compute rel: %v", err)
	}
	if !strings.HasPrefix(rel, "..") {
		t.Fatalf("test setup invariant violated — rel %q does not start with ..", rel)
	}

	sf := &state.File{
		SchemaVersion: "2",
		Environment:   "demo",
		Deployment:    "team",
		Prompts: []state.FileEntry{
			{Target: rel, Hash: "xxh3:dd", SourceHash: "xxh3:dd"},
		},
	}

	got, err := extract.Classify(finalAbs, achDir, sf)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got != extract.CollisionOwnedByCurrent && got != extract.CollisionExistsUnowned {
		t.Fatalf("got %v, want CollisionExistsUnowned (or any non-Owned)", got)
	}
	if got == extract.CollisionOwnedByCurrent {
		t.Fatalf("got CollisionOwnedByCurrent — `..`-escaped Target must not pivot classification (T-07-W5-03-02)")
	}
}

// ===== Cascade Tier 1 (eagerBytes) =====

func TestCascade_Tier1_Match_Identical(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "x.txt")
	body := []byte("hello world")
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := extract.Cascade(context.Background(), target, nil, body, nil, nil)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if !out.Identical {
		t.Fatalf("Identical=false, want true")
	}
	if out.Tier != 1 {
		t.Fatalf("Tier=%d, want 1", out.Tier)
	}
}

func TestCascade_Tier1_Differ_NotIdentical(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "x.txt")
	if err := os.WriteFile(target, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := extract.Cascade(context.Background(), target, nil, []byte("DIFFERENT"), nil, nil)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if out.Identical {
		t.Fatalf("Identical=true, want false")
	}
	if out.Tier != 1 {
		t.Fatalf("Tier=%d, want 1", out.Tier)
	}
}

func TestCascade_Tier1_EmptyBytes_AnchorsTier1(t *testing.T) {
	// eagerBytes is a non-nil but length-0 slice — an empty file is a
	// legitimate comparison anchor; Tier 1 must answer (not fall through
	// to Tier 2 or 3).
	tmp := t.TempDir()
	target := filepath.Join(tmp, "empty.txt")
	if err := os.WriteFile(target, []byte{}, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := extract.Cascade(context.Background(), target, nil, []byte{}, nil, nil)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if !out.Identical {
		t.Fatalf("Identical=false on empty-vs-empty, want true")
	}
	if out.Tier != 1 {
		t.Fatalf("Tier=%d, want 1", out.Tier)
	}
}

// ===== Cascade Tier 2 (resolver) =====

func TestCascade_Tier2_ResolverInvoked(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "merged.json")
	body := []byte(`{"merged":true}`)
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := &fakeResolver{result: body}
	out, err := extract.Cascade(context.Background(), target, nil, nil, r, nil)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if !out.Identical {
		t.Fatalf("Identical=false, want true (resolver bytes match file)")
	}
	if out.Tier != 2 {
		t.Fatalf("Tier=%d, want 2", out.Tier)
	}
}

func TestCascade_Tier2_ResolverDiffers_NotIdentical(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "merged.json")
	if err := os.WriteFile(target, []byte(`{"on-disk":1}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := &fakeResolver{result: []byte(`{"resolver":1}`)}
	out, err := extract.Cascade(context.Background(), target, nil, nil, r, nil)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if out.Identical {
		t.Fatalf("Identical=true, want false")
	}
	if out.Tier != 2 {
		t.Fatalf("Tier=%d, want 2", out.Tier)
	}
}

func TestCascade_Tier2_ResolverError_Wrapped(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "merged.json")
	if err := os.WriteFile(target, []byte(`x`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sentinel := errors.New("resolver exploded")
	r := &fakeResolver{err: sentinel}
	_, err := extract.Cascade(context.Background(), target, nil, nil, r, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain does not contain sentinel: %v", err)
	}
}

// ===== Cascade Tier 3 (sourceFn) =====

func TestCascade_Tier3_SourceFnInvoked(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "passthrough.txt")
	body := []byte("passthrough body")
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sf := fakeSourceFn(body, nil)
	out, err := extract.Cascade(context.Background(), target, nil, nil, nil, sf)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if !out.Identical {
		t.Fatalf("Identical=false, want true")
	}
	if out.Tier != 3 {
		t.Fatalf("Tier=%d, want 3", out.Tier)
	}
}

func TestCascade_Tier3_SourceFnDiffers_NotIdentical(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "passthrough.txt")
	if err := os.WriteFile(target, []byte("on-disk"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sf := fakeSourceFn([]byte("source-different"), nil)
	out, err := extract.Cascade(context.Background(), target, nil, nil, nil, sf)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if out.Identical {
		t.Fatalf("Identical=true, want false")
	}
	if out.Tier != 3 {
		t.Fatalf("Tier=%d, want 3", out.Tier)
	}
}

func TestCascade_Tier3_SourceFnError_Wrapped(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "x.txt")
	if err := os.WriteFile(target, []byte(`x`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sentinel := errors.New("source read exploded")
	sf := fakeSourceFn(nil, sentinel)
	_, err := extract.Cascade(context.Background(), target, nil, nil, nil, sf)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain does not contain sentinel: %v", err)
	}
}

// ===== Cascade ordering — Tier 1 wins when supplied alongside Tier 2/3 =====

func TestCascade_TierOrdering_Tier1WinsOverTier2(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "x.txt")
	body := []byte("on disk")
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Resolver returns matching bytes; eagerBytes is a non-match.
	// Tier 1 should answer (non-match), Resolver never consulted.
	resolverCalled := false
	r := &fakeResolverCaptured{result: body, called: &resolverCalled}

	out, err := extract.Cascade(context.Background(), target, nil, []byte("eager mismatch"), r, nil)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if out.Identical {
		t.Fatalf("Identical=true; expected Tier 1 mismatch")
	}
	if out.Tier != 1 {
		t.Fatalf("Tier=%d, want 1", out.Tier)
	}
	if resolverCalled {
		t.Fatalf("resolver called despite eagerBytes supplied — laziness contract broken")
	}
}

func TestCascade_TierOrdering_Tier2WinsOverTier3(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "x.txt")
	body := []byte("on disk")
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Resolver returns matching bytes; sourceFn would be a mismatch.
	// Tier 2 should answer (match), sourceFn never consulted.
	sourceCalled := false
	r := &fakeResolver{result: body}
	sf := func(_ context.Context, _ string) ([]byte, error) {
		sourceCalled = true
		return []byte("source mismatch"), nil
	}

	out, err := extract.Cascade(context.Background(), target, nil, nil, r, sf)
	if err != nil {
		t.Fatalf("Cascade: %v", err)
	}
	if !out.Identical {
		t.Fatalf("Identical=false; expected Tier 2 match")
	}
	if out.Tier != 2 {
		t.Fatalf("Tier=%d, want 2", out.Tier)
	}
	if sourceCalled {
		t.Fatalf("sourceFn called despite resolver supplied — laziness contract broken")
	}
}

// ===== Cascade error paths =====

func TestCascade_AllNil_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "x.txt")
	if err := os.WriteFile(target, []byte(`x`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := extract.Cascade(context.Background(), target, nil, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected ErrCascadeNoTier, got nil")
	}
	if !errors.Is(err, extract.ErrCascadeNoTier) {
		t.Fatalf("error is not ErrCascadeNoTier: %v", err)
	}
}

func TestCascade_FinalPathMissing_Errors(t *testing.T) {
	tmp := t.TempDir()
	absent := filepath.Join(tmp, "absent.txt")

	_, err := extract.Cascade(context.Background(), absent, nil, []byte("anything"), nil, nil)
	if err == nil {
		t.Fatalf("expected error reading absent file, got nil")
	}
}

// ===== WrapCollisionRefuseError → exit code 7 =====

func TestWrapCollisionRefuseError_HasExitCode7(t *testing.T) {
	err := extract.WrapCollisionRefuseError("/some/target.md", 2)
	if err == nil {
		t.Fatalf("WrapCollisionRefuseError returned nil")
	}

	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("error is not *exit.CodedError: %T", err)
	}
	if ce.Code != exit.CollisionRefuse {
		t.Fatalf("Code=%d, want exit.CollisionRefuse=%d", ce.Code, exit.CollisionRefuse)
	}
	if ce.Code != 7 {
		t.Fatalf("CollisionRefuse drifted from 7: %d", ce.Code)
	}
}

func TestWrapCollisionRefuseError_MsgCitesTargetAndTier(t *testing.T) {
	err := extract.WrapCollisionRefuseError("/some/target.md", 3)
	msg := err.Error()
	// Loose contains-check — the exact format may evolve but the two
	// load-bearing facts (target path + tier) must be present.
	if msg == "" {
		t.Fatalf("empty error message")
	}
	if !contains(msg, "/some/target.md") {
		t.Fatalf("message %q does not cite target path", msg)
	}
	if !contains(msg, "3") {
		t.Fatalf("message %q does not cite cascade tier", msg)
	}
}

// fakeResolverCaptured wraps fakeResolver with a per-call counter so
// the ordering tests can assert "resolver NOT called when Tier 1 wins"
// without coupling to package internals.
type fakeResolverCaptured struct {
	result []byte
	err    error
	called *bool
}

func (f *fakeResolverCaptured) Resolve(_ context.Context, _ string) ([]byte, error) {
	if f.called != nil {
		*f.called = true
	}
	return f.result, f.err
}

// contains is a stdlib-only substring check (avoids the strings import
// in test-helper position; the actual implementation file imports
// fmt for Sprintf elsewhere).
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

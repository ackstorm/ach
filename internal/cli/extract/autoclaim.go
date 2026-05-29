// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/state"
)

// CollisionClass is the auto-claim classification of a final-rename target
// per CLI spec §6.4 + SAFE-04. The three-value enum is exhaustive — callers
// switching on this type should cover all three constants.
type CollisionClass int

// CollisionClass constants. Values start at iota+1 so the zero value is
// not silently a meaningful class — a caller that forgets to initialize a
// CollisionClass variable receives `0`, which matches no constant and
// trips an exhaustive switch.
const (
	// CollisionNone means no file exists at the final target path.
	// The hydrate engine may rename the staged file into place without
	// further checks.
	CollisionNone CollisionClass = iota + 1

	// CollisionOwnedByCurrent means a file exists at the final target
	// path AND its on-disk bytes are referenced by the current
	// state.json (any FileEntry in Prompts / Plugins / Artifacts /
	// RuntimeFiles / Adapter.Files whose Target matches). The hydrate
	// engine may overwrite — the file is the engine's prior output.
	CollisionOwnedByCurrent

	// CollisionExistsUnowned means a file exists at the final target
	// path AND no state.json entry references it. This is the SAFE-04
	// auto-claim trigger: the hydrate engine MUST run Cascade to
	// decide between "auto-claim into state" (bytes match the expected
	// output) and "exit 7 / CollisionRefuse" (bytes differ, --force
	// not passed).
	CollisionExistsUnowned
)

// ContentResolver is the Tier-2 cascade input — adapter-provided merged
// content. The 07-W3-01 Adapter interface's ResolveOutputContent method
// satisfies this contract: pass-through adapters return the source bytes
// verbatim; merging adapters (e.g. the Claude `.mcp.json` adapter) return
// the byte-exact result of merging the engine's input into the on-disk
// shared file's prior state.
//
// The single-method shape keeps the autoclaim package from depending on
// the (later-landing) adapter package — D-07 coupling is by interface
// shape, not by import.
type ContentResolver interface {
	Resolve(ctx context.Context, target string) ([]byte, error)
}

// SourceFn is the Tier-3 cascade input — a closure provided by the
// hydrate orchestrator (07-W1-06 commit step 8/9, wired in 07-W3-05)
// that reads the raw source bytes for `target` on demand. The
// orchestrator typically closes over the per-resource staging dir path
// and reads source.bin from there.
//
// Tier 3 is the fallback for pass-through resources where the
// orchestrator did not eagerly buffer Tier 1 bytes AND the resource's
// adapter is a no-op (no Tier 2 resolver supplied). Reading source.bin
// from staging is cheap and the staging dir is guaranteed to live for
// the lifetime of the cascade per SAFE-05.
type SourceFn func(ctx context.Context, target string) ([]byte, error)

// CascadeOutcome is the per-target result of a three-tier comparison.
// Identical drives the auto-claim-into-state decision (true → claim;
// false → caller raises WrapCollisionRefuseError unless --force). Tier
// records which input arm answered the question — surfaced in
// --verbose output and useful for cascade-coverage analysis in tests.
type CascadeOutcome struct {
	// Identical reports whether the on-disk bytes at finalPath equal
	// the bytes supplied by the answering tier. Comparison uses
	// crypto/sha256 + crypto/subtle.ConstantTimeCompare for cheap
	// equality and forward-compatibility with the STATE-11
	// short-circuit logic (which keys on canonical content hashes).
	Identical bool

	// Tier records which arm produced the answer: 1 = eagerBytes
	// (in-memory Tier 1), 2 = resolver.Resolve (adapter Tier 2),
	// 3 = sourceFn (lazy source Tier 3). Set even when Identical is
	// false so observability stays consistent across both outcomes.
	Tier int

	// Error is reserved for the caller's convenience — Cascade itself
	// returns errors via the second return value, never embedded here.
	// Kept on the struct so a future caller batching outcomes can
	// thread per-target failures through a single value.
	Error error
}

// ErrCascadeNoTier is the orchestrator-bug safety case: Cascade was
// called with nil eagerBytes AND nil resolver AND nil sourceFn — there
// is no input to compare against the on-disk bytes. Callers should
// treat this as a programmer error in the orchestrator's wiring, not
// as a user-recoverable hydrate failure. It will not surface in normal
// runs because the orchestrator always supplies at least one tier.
var ErrCascadeNoTier = errors.New("autoclaim: no tier supplied — orchestrator bug")

// Classify maps a final target path + the current state.File to one of
// the three CollisionClass values per SAFE-04.
//
// The state.File walk covers every projection bucket: Prompts, Plugins,
// Artifacts, RuntimeFiles, AND Adapter.Files. A target found in any of
// these buckets is "owned by the current hydration" (CollisionOwnedByCurrent);
// otherwise an existing file is "unowned" (CollisionExistsUnowned) and the
// SAFE-04 cascade must run.
//
// Errors from os.Stat that are NOT IsNotExist are wrapped and returned —
// the caller maps them to exit 1 (General) per CLI spec §9.3.
func Classify(finalPath string, stateFile *state.File) (CollisionClass, error) {
	if _, err := os.Stat(finalPath); err != nil {
		if os.IsNotExist(err) {
			return CollisionNone, nil
		}
		return 0, fmt.Errorf("autoclaim: stat %s: %w", finalPath, err)
	}

	// File exists — is it referenced by the current state?
	if stateFile != nil {
		for _, entry := range walkAllEntries(stateFile) {
			if entry.Target == finalPath {
				return CollisionOwnedByCurrent, nil
			}
		}
	}

	return CollisionExistsUnowned, nil
}

// walkAllEntries flattens every FileEntry in a state.File into a single
// slice of pointers. The order is Prompts → Plugins → Artifacts →
// RuntimeFiles → Adapter.Files; callers must not rely on the order for
// semantic decisions (Classify uses a linear scan and stops on the
// first Target match).
//
// Pointers (not values) keep the slice cheap; FileEntry carries a
// []string for Keys whose copy would otherwise allocate per entry.
func walkAllEntries(f *state.File) []*state.FileEntry {
	if f == nil {
		return nil
	}
	total := len(f.Prompts) + len(f.Plugins) + len(f.Artifacts) +
		len(f.RuntimeFiles) + len(f.Adapter.Files)
	if total == 0 {
		return nil
	}
	out := make([]*state.FileEntry, 0, total)
	for i := range f.Prompts {
		out = append(out, &f.Prompts[i])
	}
	for i := range f.Plugins {
		out = append(out, &f.Plugins[i])
	}
	for i := range f.Artifacts {
		out = append(out, &f.Artifacts[i])
	}
	for i := range f.RuntimeFiles {
		out = append(out, &f.RuntimeFiles[i])
	}
	for i := range f.Adapter.Files {
		out = append(out, &f.Adapter.Files[i])
	}
	return out
}

// Cascade implements the D-17 three-tier lazy collision cascade per
// CLI spec §6.4 + CONTEXT.md `<specifics>` "Auto-claim three-tier
// cascade adapter coupling".
//
// Invocation contract:
//
//   - finalPath MUST refer to an existing file on disk (Classify
//     returned CollisionExistsUnowned). Cascade reads finalPath
//     unconditionally as the comparison anchor.
//   - stateEntry is passed through to the caller's bookkeeping (Cascade
//     does not consult it for the comparison itself); kept as a
//     parameter so the signature matches the orchestrator wiring point
//     in 07-W1-06 step 8/9.
//   - The three tiers are tried in order; the FIRST non-nil tier wins
//     and Cascade returns immediately. There is no "try Tier 2 because
//     Tier 1 mismatched" fallback — the three-tier discipline is about
//     LAZINESS of byte-acquisition, not about retry.
//   - eagerBytes nil means "Tier 1 not supplied"; eagerBytes non-nil
//     means Tier 1 is the answering tier (even if the slice is
//     length 0 — an empty file is a legitimate comparison anchor).
//
// Tier ordering:
//
//	Tier 1 = eagerBytes (in-memory; typical for prompt + scope-object
//	         artifact where the orchestrator already has the bytes).
//	Tier 2 = resolver.Resolve (adapter-merged files; e.g. the Claude
//	         `.mcp.json` adapter recomputes the merged result on demand).
//	Tier 3 = sourceFn (lazy source read; for pass-through resources
//	         where source.bin still lives in staging).
//
// If ALL three tiers are nil, Cascade returns ErrCascadeNoTier — an
// orchestrator-bug safety case (the orchestrator's wiring contract
// guarantees at least one tier per call).
func Cascade(
	ctx context.Context,
	finalPath string,
	stateEntry *state.FileEntry,
	eagerBytes []byte,
	resolver ContentResolver,
	sourceFn SourceFn,
) (CascadeOutcome, error) {
	_ = stateEntry // reserved for future use by callers; see signature contract above

	existingBytes, err := os.ReadFile(finalPath)
	if err != nil {
		return CascadeOutcome{}, fmt.Errorf("autoclaim: read existing %s: %w", finalPath, err)
	}
	existingSha := sha256.Sum256(existingBytes)

	// Tier 1: in-memory eager bytes.
	if eagerBytes != nil {
		sha := sha256.Sum256(eagerBytes)
		return CascadeOutcome{
			Identical: subtle.ConstantTimeCompare(existingSha[:], sha[:]) == 1,
			Tier:      1,
		}, nil
	}

	// Tier 2: adapter resolver.
	if resolver != nil {
		resolved, err := resolver.Resolve(ctx, finalPath)
		if err != nil {
			return CascadeOutcome{}, fmt.Errorf("autoclaim: resolver tier 2 for %s: %w", finalPath, err)
		}
		sha := sha256.Sum256(resolved)
		return CascadeOutcome{
			Identical: subtle.ConstantTimeCompare(existingSha[:], sha[:]) == 1,
			Tier:      2,
		}, nil
	}

	// Tier 3: lazy source read.
	if sourceFn != nil {
		src, err := sourceFn(ctx, finalPath)
		if err != nil {
			return CascadeOutcome{}, fmt.Errorf("autoclaim: sourceFn tier 3 for %s: %w", finalPath, err)
		}
		sha := sha256.Sum256(src)
		return CascadeOutcome{
			Identical: subtle.ConstantTimeCompare(existingSha[:], sha[:]) == 1,
			Tier:      3,
		}, nil
	}

	return CascadeOutcome{}, ErrCascadeNoTier
}

// WrapCollisionRefuseError builds the exit-7 (CollisionRefuse) error the
// orchestrator returns when Cascade reports Identical=false and --force
// is not in effect. The error is the closed-class *exit.CodedError shape
// that cmd/ach-cli/main.go's dispatcher recognizes via errors.As and
// passes to os.Exit.
//
// The rendered message cites both the target path AND the cascade tier
// that decided the comparison — useful for --verbose output and for
// post-mortem debugging when a user hits exit 7 on what they thought
// was an idempotent re-hydrate.
func WrapCollisionRefuseError(target string, tier int) error {
	return &exit.CodedError{
		Code: exit.CollisionRefuse,
		Msg:  fmt.Sprintf("collision refused at %s (cascade tier %d)", target, tier),
	}
}

// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"context"

	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// MergeKind is the typed-int enum classifying how an adapter-written
// file is merged with any pre-existing on-disk content per CLI spec
// §7.1 + ADAPT-05.
//
// The zero value is intentionally invalid (MergeKind = 0 is not a
// constant below) so a forgotten Merge field on a FileWrite trips a
// downstream gate rather than silently defaulting to "deep".
type MergeKind int

const (
	// MergeDeep performs deep-merge of JSON/TOML object trees: existing
	// keys preserved unless the adapter contributes a key of the same
	// path. Used for .claude/.mcp.json and .codex/config.toml.
	MergeDeep MergeKind = iota + 1

	// MergeComposite performs marker-bounded replacement inside a single
	// file: the region between <!-- ach:begin --> and <!-- ach:end -->
	// is replaced verbatim; content outside the markers is preserved.
	// Used for markdown files where adapter contributions slot into a
	// caller-controlled host file.
	MergeComposite

	// MergeReplace replaces the entire file unconditionally. Used for
	// files the adapter exclusively owns (no caller-side content
	// expected).
	MergeReplace
)

// Confidence is the typed-int enum the autodetection layer (plan
// 07-W3-05) uses to rank candidate adapters when multiple Detect()
// calls return non-zero matches.
type Confidence int

const (
	// ConfidenceLow indicates a weak signal — e.g. only a global-mode
	// hint like $HOME/.<platform>/ exists with no local cwd artifacts.
	ConfidenceLow Confidence = iota + 1

	// ConfidenceMedium indicates two signals present — e.g. local
	// .<platform>/ dir plus one well-known file.
	ConfidenceMedium

	// ConfidenceHigh indicates three or more signals — strong autodetect.
	ConfidenceHigh
)

// Match is the value Adapter.Detect returns for a given root. The
// cobra autodetection layer (plan 07-W3-05) uses this to enforce the
// ADAPT-02 zero/one/multi-match outcomes per spec §7.5.
type Match struct {
	// ID is the canonical adapter ID (matches Adapter.ID()). Empty when
	// the adapter saw no signals.
	ID string

	// Confidence ranks the strength of the signal set.
	Confidence Confidence

	// Reasons is human-readable evidence (e.g. "found .claude/",
	// "found .claude/.mcp.json"); surfaced verbatim to stderr on
	// multi-match exit so the user can disambiguate.
	Reasons []string
}

// FileWrite is one materialized file produced by Adapter.RenderRuntime.
// The orchestrator (plan 07-W3-05) consumes a []FileWrite, stages each
// entry per spec §6.7 step 7, then atomically publishes them per
// STATE-07 (tmp + fsync + rename).
//
// Path is workspace-relative (under <ach-dir>); the orchestrator joins
// it with the resolved <ach-dir> at publication time.
type FileWrite struct {
	// Path is the workspace-relative target path (e.g. ".claude/.mcp.json").
	Path string

	// Content is the byte sequence to write at Path.
	Content []byte

	// SourceHash is the xxh3 of the pre-conversion source bytes (D-23).
	// For passthrough files it equals the emitted-content Hash (the
	// Phase-1/2 invariant); for converted files (Phase 3: codex .md→.toml,
	// opencode tools[]→{}) it differs from Hash because the emitted bytes
	// are not the source bytes. route.Project captures it from the source
	// bytes BEFORE the Transform overwrite; publishFile threads it into the
	// state.FileEntry.SourceHash. Empty on adapter RenderRuntime output —
	// publishFile then falls back to the emitted Hash.
	SourceHash string

	// Merge classifies how this file combines with any pre-existing
	// on-disk content (CLI spec §7.1 + ADAPT-05).
	Merge MergeKind

	// Keys is the list of contributed top-level keys (for MergeDeep
	// JSON/TOML files; recorded in state.adapter.files[*].keys[] per
	// STATE-02 + ADAPT-05). Drives `--sync` inverse-merge per
	// CLI spec §8.5. For MergeComposite, Keys may carry a single marker
	// identifier (e.g. "ach:begin"); for MergeReplace, Keys is nil.
	Keys []string
}

// Adapter is the closed-set platform-adapter contract per CONTEXT.md
// D-07 + ADAPT-01. Every adapter MUST implement all four methods.
// Pass-through adapters (claudecode) return source bytes verbatim from
// ResolveOutputContent; merge adapters (codex/gemini/opencode)
// recompute the merged target bytes for the SAFE-04 cascade Tier 2 from
// 07-W2-03.
//
// Signatures match CONTEXT.md D-07 verbatim. Drift from this surface
// (renamed method, changed return type, added required parameter)
// would force a 4-adapter cascade rewrite — the interface is locked
// for v1alpha1.
type Adapter interface {
	// ID returns the canonical platform identifier (e.g. "claude-code",
	// "codex", "gemini-cli", "opencode"). Used as the primary registry
	// key.
	ID() string

	// Aliases returns case-folded alternate identifiers the user may
	// type at the CLI (e.g. claudecode → ["claude", "cc"]). The
	// registry case-folds the input before lookup; aliases MUST NOT
	// collide across adapters.
	Aliases() []string

	// Detect scans root (workspace cwd or $HOME for global mode) for
	// platform-specific signals and returns a Match. A nil Match (zero
	// ID + zero Confidence) means "no signals seen"; the autodetection
	// layer treats that as a no-match.
	Detect(root string) (Match, error)

	// RenderRuntime emits the runtime-config FileWrite list for this
	// platform. m carries the decoded manifest (Hub spec §15.2); s
	// carries the prior state.File or nil for fresh workspaces. The
	// bearer credential, if any, is on ctx via WithCredential — adapters
	// MUST consume it via CredentialFromContext, never via env vars.
	RenderRuntime(ctx context.Context, m *manifest.Manifest, s *state.File) ([]FileWrite, error)

}

// credentialKey is the unexported typed key used to stuff the bearer
// credential into context.Context. The struct{} type guarantees no
// other package can collide with our key (unlike a string-keyed value
// which is at the mercy of every other package's typed-string
// discipline). Never exported; never logged.
type credentialKey struct{}

// WithCredential returns ctx augmented with the bearer credential
// under our unexported typed key. The orchestrator (plan 07-W3-05)
// calls this once before invoking RenderRuntime; adapters call
// CredentialFromContext to read it back. Credentials MUST NOT be
// passed via function parameter or env var — the context-keyed
// discipline keeps the credential invisible to logging/printf
// surfaces by default.
//
// An empty bearer is permitted (and stored verbatim); adapters
// receiving an empty credential will render an empty header value,
// which is the right behavior for offline / dry-run / unit-test
// invocations.
func WithCredential(ctx context.Context, bearer string) context.Context {
	return context.WithValue(ctx, credentialKey{}, bearer)
}

// CredentialFromContext reads the bearer credential stuffed by
// WithCredential. Returns "" when no credential is present (callers
// MUST tolerate empty — adapters render an empty header value rather
// than panic).
func CredentialFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(credentialKey{}).(string); ok {
		return v
	}
	return ""
}

// HeadersWithCredential returns the per-server headers map embedding the
// bearer under "x-ach-key"; an empty credential still emits the header
// (empty value) so the rendered JSON/TOML shape stays stable.
func HeadersWithCredential(cred string) map[string]string {
	return map[string]string{
		"x-ach-key": cred,
	}
}

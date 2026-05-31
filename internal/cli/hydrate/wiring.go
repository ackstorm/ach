// SPDX-License-Identifier: Apache-2.0

// Default Extractor + AdapterDispatcher implementations + STATE-05
// --sync handler — the W3-05 wiring that turns the orchestrator's
// stub-fed interfaces into a working engine.
//
// The orchestrator (07-W1-06 commit.go) holds Extractor and
// AdapterDispatcher as interface fields and short-circuits when they
// are nil (W1 stub). This file provides the concrete impls + a
// NewWiring constructor the cobra layer (cmd/ach-cli/cmd/hydrate.go
// D-03 refactor) uses to populate those fields before dispatching
// Run().
//
// Implementations:
//
//   - extractorImpl wraps internal/cli/extract.{FetchContent,
//     StageAndPublish}. It infers ResourceKind from the
//     ContentRef.DownloadURL path (`/content/{kind}/{name}`) — falling
//     back to KindArtifact when the URL is unparseable so bomb-defense
//     caps remain enforced even for malformed manifests.
//
//   - adapterDispatcherImpl wraps adapter.Lookup + RenderRuntime. Each
//     adapter.FileWrite is published under toolRoot (the tool's native
//     config dir — workspace root, or $HOME under --global; NOT achDir)
//     via SURGICAL forward-merge: only ACH's keys are upserted into the
//     user's existing JSON/TOML config, preserving the user's other
//     servers/settings. A user edit to one of OUR keys is caught by the
//     per-key §8.4 drift check → exit.Drift unless --force.
//
//   - Sync implements STATE-05 / D-16 verbatim: collect targets in
//     `prev` but missing from `new`; sort deepest-first by path depth;
//     for each target compare its on-disk xxh3 to prev's recorded
//     hash; mismatch → preserve with stderr warning (drift wins
//     unless --force); match (or --force) → delete OR inverse-merge
//     (for Merge=deep with Keys[]; for Merge=composite via
//     <!-- ach:begin -->...<!-- ach:end --> regex replacement). After
//     per-file work, walk parent dirs deepest-first calling os.Remove
//     which honors ENOTEMPTY silently — empty dirs prune, non-empty
//     preserved. CLI NEVER recursively deletes a directory.
//
// All file writes go through state.WriteAtomic (the STATE-07 atomic
// publication primitive) so a mid-Sync crash never leaves a partially
// rewritten inverse-merge file. Verbatim deletions use os.Remove
// (single-file unlink — atomic).

package hydrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// File-extension discriminators for adapter runtime-config merge/sync.
const (
	extJSON = ".json"
	extTOML = ".toml"
)

// extractorImpl satisfies the hydrate.Extractor interface declared in
// result.go. It wraps the W2-02 FetchContent + StageAndPublish flow.
// limits + allowSymlinks are bound at construction (the cobra layer
// reads limits via extract.LoadLimits and allowSymlinks via the
// --allow-symlinks flag).
type extractorImpl struct {
	client        *httpclient.Client
	limits        extract.Limits
	allowSymlinks bool
}

// ExtractContent implements hydrate.Extractor. Flow:
//
//  1. Parse ContentRef.DownloadURL to derive the ResourceKind from the
//     "/content/{kind}/{name}" path segment. Defaults to KindArtifact
//     on parse failure (most-restrictive cap → fail-safe).
//  2. Call extract.FetchContent(ctx, client, kind, name) — returns the
//     live *http.Response (body unread).
//  3. Compute the workspace-relative target path:
//     <kind>/<name>(.tar.gz) under achDir.
//  4. Call extract.StageAndPublish(ctx, body, Content-Type, finalAbs,
//     achDir, kind, limits, allowSymlinks).
//  5. Translate PublishResult.WrittenFiles into hydrate.FileWrite
//     entries — Target rooted at the workspace-relative path the
//     orchestrator's state ledger expects.
//
// PublishResult.Skipped is folded into the result as a zero-WrittenFiles
// outcome — the existing file remained in place. SourceHash flows
// through unchanged.
func (e *extractorImpl) ExtractContent(ctx context.Context, ref manifest.ContentRef, achDir string, prev *state.File) (ExtractResult, error) {
	kind, name := classifyDownloadURL(ref.DownloadURL, ref.Name)

	resp, err := extract.FetchContent(ctx, e.client, kind, name)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("extract content %s/%s: %w", kind, name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	finalRelPath := filepath.Join(string(kind), name)
	finalAbs := filepath.Join(achDir, finalRelPath)
	contentType := resp.Header.Get("Content-Type")

	// Re-hydrate no-op skip (W6-01 Bug E): only when prior state records
	// this content's upstream SourceHash do we hash the freshly-fetched
	// bytes first — an unchanged source needs no disk write (the on-disk
	// tree is preserved, FilesWritten==0). A fresh hydrate (no prior entry)
	// streams straight through StageAndPublish with a single spill.
	if prevHash := priorContentSourceHash(prev, kind, finalRelPath); prevHash != "" {
		staged, srcXxh3, serr := extract.SpillAndHashXxh3(achDir, resp.Body)
		if serr != nil {
			return ExtractResult{}, fmt.Errorf("extract content %s/%s: %w", kind, name, serr)
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(staged)) }()
		if srcXxh3 == prevHash {
			return ExtractResult{SourceHash: srcXxh3}, nil
		}
		f, oerr := os.Open(staged)
		if oerr != nil {
			return ExtractResult{}, fmt.Errorf("extract content %s/%s: reopen staged: %w", kind, name, oerr)
		}
		defer func() { _ = f.Close() }()
		return e.stageAndMap(ctx, f, contentType, finalRelPath, finalAbs, achDir, kind)
	}

	return e.stageAndMap(ctx, resp.Body, contentType, finalRelPath, finalAbs, achDir, kind)
}

// stageAndMap performs the delete-before-replace + StageAndPublish + FileWrite
// projection shared by ExtractContent's skip-capable and fresh paths.
//
// Delete-before-replace is DIRECTORY-ONLY (W6-01 Bug E): renameAtomic happily
// replaces a pre-existing regular file, but cannot rename over a non-empty
// directory — and the W5-01 orchestrator "replace step" was never wired. We
// therefore remove only a pre-existing directory target (an already-extracted
// plugin). Leaving regular files in place preserves StageAndPublish's step-3
// single-file no-op short-circuit.
func (e *extractorImpl) stageAndMap(ctx context.Context, body io.Reader, contentType, finalRelPath, finalAbs, achDir string, kind extract.ResourceKind) (ExtractResult, error) {
	if info, statErr := os.Stat(finalAbs); statErr == nil && info.IsDir() {
		if rmErr := os.RemoveAll(finalAbs); rmErr != nil {
			return ExtractResult{}, fmt.Errorf("extract content %s: remove prior dir %s: %w", kind, finalAbs, rmErr)
		}
	}

	pr, err := extract.StageAndPublish(ctx, body, contentType,
		finalAbs, achDir, kind, e.limits, e.allowSymlinks)
	if err != nil {
		return ExtractResult{}, err
	}

	out := ExtractResult{SourceHash: pr.SourceHash}
	for _, fw := range pr.WrittenFiles {
		out.WrittenFiles = append(out.WrittenFiles, FileWrite{
			Target:     filepath.Join(finalRelPath, fw.RelPath),
			Hash:       fw.Hash,
			SourceHash: pr.SourceHash,
		})
	}
	return out, nil
}

// priorContentSourceHash returns the upstream SourceHash recorded for the
// content rooted at finalRelPath in the matching state bucket, or "" when no
// prior entry exists. All per-file entries of one archive share the archive's
// SourceHash (D-14), so the first match suffices.
func priorContentSourceHash(prev *state.File, kind extract.ResourceKind, finalRelPath string) string {
	if prev == nil {
		return ""
	}
	var bucket []state.FileEntry
	switch kind {
	case extract.KindPlugin:
		bucket = prev.Plugins
	case extract.KindArtifact:
		bucket = prev.Artifacts
	case extract.KindPrompt:
		bucket = prev.Prompts
	default:
		return ""
	}
	prefix := finalRelPath + string(filepath.Separator)
	for _, ent := range bucket {
		if ent.SourceHash == "" {
			continue
		}
		if ent.Target == finalRelPath || strings.HasPrefix(ent.Target, prefix) {
			return ent.SourceHash
		}
	}
	return ""
}

// classifyDownloadURL parses `/content/{kind}/{name}` and returns the
// matching ResourceKind + name. Defaults to KindArtifact on parse
// failure (the most restrictive bomb-defense cap so a malformed URL
// cannot bypass enforcement). Falls back to fallbackName when the
// URL has no /content/.../name segment.
func classifyDownloadURL(downloadURL, fallbackName string) (extract.ResourceKind, string) {
	if downloadURL == "" {
		return extract.KindArtifact, fallbackName
	}
	u, err := url.Parse(downloadURL)
	if err != nil {
		return extract.KindArtifact, fallbackName
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Expect ["content", "{kind}", "{name}"] — find the "content" segment
	// and read the two following segments.
	for i, p := range parts {
		if p == "content" && i+2 < len(parts) {
			kind := extract.ResourceKind(parts[i+1])
			switch kind {
			case extract.KindPlugin, extract.KindArtifact, extract.KindPrompt:
				return kind, parts[i+2]
			}
		}
	}
	return extract.KindArtifact, fallbackName
}

// adapterDispatcherImpl satisfies the hydrate.AdapterDispatcher
// interface declared in result.go. platformID is bound at
// construction (the cobra layer resolves it via ResolvePlatform or
// Autodetect). force bypasses the per-key drift refusal (publishRuntimeFile)
// so a user edit to OUR key is overwritten rather than preserved.
type adapterDispatcherImpl struct {
	platformID string
	force      bool
}

// Render implements hydrate.AdapterDispatcher. Flow:
//
//  1. Lookup the adapter by platformID. Miss → typed CodedError
//     (General) so the cobra error envelope stays consistent.
//  2. Call ad.RenderRuntime(ctx, m, s) — returns []adapter.FileWrite.
//  3. For each FileWrite, publishRuntimeFile resolves the target under
//     toolRoot (the tool's native config location, NOT achDir),
//     surgically merges ONLY our keys into the user's existing config,
//     and enforces the per-key §8.4 drift truth table (a user edit to
//     OUR key → exit.Drift unless --force). The user's other keys are
//     preserved untouched.
//
// State records the canonical hash of OUR contribution (not the merged
// file) so --sync inverse-merge and subsequent drift checks operate on
// our keys only. DroppedComponents stays nil for the runtime path
// (TransformPlugin is the source of drops).
func (d *adapterDispatcherImpl) Render(ctx context.Context, m *manifest.Manifest, s *state.File, achDir, toolRoot string) (RenderResult, error) {
	ad, ok := adapter.Lookup(d.platformID)
	if !ok {
		return RenderResult{}, &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("adapter dispatcher: unknown platform %q", d.platformID),
		}
	}

	fws, err := ad.RenderRuntime(ctx, m, s)
	if err != nil {
		return RenderResult{}, fmt.Errorf("adapter %s RenderRuntime: %w", d.platformID, err)
	}

	var result RenderResult
	for _, fw := range fws {
		entry, err := d.publishRuntimeFile(fw, s, toolRoot)
		if err != nil {
			return RenderResult{}, err
		}
		result.WrittenFiles = append(result.WrittenFiles, entry)
	}

	return result, nil
}

// publishRuntimeFile writes one adapter runtime-config FileWrite via
// SURGICAL MERGE + PER-KEY DRIFT.
//
// Target resolution: fw.Path joins toolRoot (workspace root in project
// scope, $HOME in --global) — the tool's native config location — NOT
// achDir. ACH's private state/cache stay under achDir.
//
// Per-key drift (§8.4 truth table, applied to OUR keys only): we compare
// the hash of our contributed subtree as it currently sits on disk
// (onDisk) against the prior state record (Hash/SourceHash) and the fresh
// render (source). A user edit to OUR key surfaces as LocalEditPreserve /
// ConflictPreserve → exit 2 (preserve), unless --force. The user's OTHER
// keys are invisible to this comparison and are never claimed, refused, or
// removed — only merged around. A no-op (disk + upstream both unchanged)
// skips the rewrite so the file's bytes stay byte-identical (sc3 no-op).
//
// 0o600 — these files embed the plaintext x-ach-key bearer (CR-01).
func (d *adapterDispatcherImpl) publishRuntimeFile(fw adapter.FileWrite, s *state.File, toolRoot string) (FileWrite, error) {
	finalAbs := filepath.Join(toolRoot, fw.Path)
	isTOML := strings.ToLower(filepath.Ext(finalAbs)) == extTOML

	// Canonical hash of our freshly-rendered contribution (parsed → map →
	// deterministic re-encode) so it is directly comparable to the on-disk
	// subtree regardless of struct-vs-map field ordering.
	oursMap, err := parseDoc(fw.Content, isTOML)
	if err != nil {
		return FileWrite{}, fmt.Errorf("adapter %s parse rendered %s: %w", d.platformID, finalAbs, err)
	}
	freshHash, err := subtreeHash(oursMap)
	if err != nil {
		return FileWrite{}, fmt.Errorf("adapter %s hash rendered %s: %w", d.platformID, finalAbs, err)
	}

	// Hash of our keys as they currently sit on disk (empty when the file
	// or our keys are absent — first hydrate / user removed them).
	onDiskHash := ""
	if fw.Merge == adapter.MergeDeep {
		diskMap, ok, derr := readParseDoc(finalAbs, isTOML)
		if derr != nil {
			return FileWrite{}, fmt.Errorf("adapter %s read existing %s: %w", d.platformID, finalAbs, derr)
		}
		if ok {
			sub, found := extractByKeys(diskMap, fw.Keys)
			if found {
				if onDiskHash, err = subtreeHash(sub); err != nil {
					return FileWrite{}, fmt.Errorf("adapter %s hash on-disk %s: %w", d.platformID, finalAbs, err)
				}
			}
		}
	}

	prior := findAdapterEntry(s, fw.Path)
	outcome := NewDiffer().Compare(prior, onDiskHash, freshHash)

	// A user edit to OUR key (drift) is preserved with exit 2 unless --force.
	// prior == nil (fresh hydrate) never refuses — there is nothing of ours
	// to preserve yet.
	if prior != nil && ShouldExit2(outcome) && !d.force {
		return FileWrite{}, WrapDriftError(outcome, finalAbs)
	}

	// Skip the rewrite only on a true no-op (prior exists; disk + upstream
	// unchanged) so byte-for-byte stability holds. Otherwise publish.
	skip := prior != nil && outcome == NoOp
	if !skip {
		if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
			return FileWrite{}, fmt.Errorf("adapter %s mkdir parent %s: %w", d.platformID, finalAbs, err)
		}
		switch fw.Merge {
		case adapter.MergeDeep:
			if _, err := mergeForward(finalAbs, fw.Content, 0o600); err != nil {
				return FileWrite{}, fmt.Errorf("adapter %s merge %s: %w", d.platformID, finalAbs, err)
			}
		default:
			if err := state.WriteAtomic(finalAbs, fw.Content, 0o600); err != nil {
				return FileWrite{}, fmt.Errorf("adapter %s write %s: %w", d.platformID, finalAbs, err)
			}
		}
	}

	// State records the canonical hash of OUR contribution (not the merged
	// file) so --sync inverse-merge (via Keys[]) and the next drift check
	// operate on our keys only, never the user's other entries.
	return FileWrite{
		Target:     fw.Path,
		Hash:       freshHash,
		SourceHash: freshHash, // adapter-rendered: the hash IS the source
		Merge:      mergeKindToString(fw.Merge),
		Keys:       append([]string(nil), fw.Keys...),
	}, nil
}

// mergeForward reads the existing file at abs (JSON or TOML by extension),
// deep-merges the keys from `ours` (an adapter-rendered document carrying
// ONLY ACH's contributed entries) into it, and atomic-writes the result at
// the given mode. When abs does not exist, the result is just `ours`. The
// user's pre-existing keys are preserved; ACH's keys upsert same-named
// entries. Returns the merged bytes (for hashing/state if the caller wants
// them). This is the forward counterpart to syncDeep{JSON,TOML}'s removal.
func mergeForward(abs string, ours []byte, mode os.FileMode) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(abs)) {
	case extJSON:
		return mergeForwardJSON(abs, ours, mode)
	case extTOML:
		return mergeForwardTOML(abs, ours, mode)
	default:
		// No structured merge for an unknown extension — write verbatim.
		if err := state.WriteAtomic(abs, ours, mode); err != nil {
			return nil, fmt.Errorf("mergeForward write %s: %w", abs, err)
		}
		return ours, nil
	}
}

// mergeForwardJSON deep-merges `ours` into the existing JSON document at
// abs. A missing file is treated as an empty object. A pre-existing file
// MUST be valid JSON (we never silently discard a user's config).
func mergeForwardJSON(abs string, ours []byte, mode os.FileMode) ([]byte, error) {
	var oursMap map[string]any
	if err := json.Unmarshal(ours, &oursMap); err != nil {
		return nil, fmt.Errorf("mergeForward decode rendered JSON: %w", err)
	}
	existing := map[string]any{}
	if body, err := os.ReadFile(abs); err == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			if derr := json.Unmarshal(body, &existing); derr != nil {
				return nil, fmt.Errorf("mergeForward decode existing JSON %s: %w", abs, derr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mergeForward read %s: %w", abs, err)
	}
	deepMergeInto(existing, oursMap)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(existing); err != nil {
		return nil, fmt.Errorf("mergeForward encode JSON %s: %w", abs, err)
	}
	if err := state.WriteAtomic(abs, buf.Bytes(), mode); err != nil {
		return nil, fmt.Errorf("mergeForward write JSON %s: %w", abs, err)
	}
	return buf.Bytes(), nil
}

// mergeForwardTOML mirrors mergeForwardJSON for TOML files.
func mergeForwardTOML(abs string, ours []byte, mode os.FileMode) ([]byte, error) {
	var oursMap map[string]any
	if err := toml.Unmarshal(ours, &oursMap); err != nil {
		return nil, fmt.Errorf("mergeForward decode rendered TOML: %w", err)
	}
	existing := map[string]any{}
	if body, err := os.ReadFile(abs); err == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			if derr := toml.Unmarshal(body, &existing); derr != nil {
				return nil, fmt.Errorf("mergeForward decode existing TOML %s: %w", abs, derr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mergeForward read %s: %w", abs, err)
	}
	deepMergeInto(existing, oursMap)
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(existing); err != nil {
		return nil, fmt.Errorf("mergeForward encode TOML %s: %w", abs, err)
	}
	if err := state.WriteAtomic(abs, buf.Bytes(), mode); err != nil {
		return nil, fmt.Errorf("mergeForward write TOML %s: %w", abs, err)
	}
	return buf.Bytes(), nil
}

// deepMergeInto recursively merges src into dst: when both sides hold a
// nested object at the same key, recurse; otherwise src's value overwrites
// dst's. This preserves the user's sibling keys (e.g. their other MCP
// servers and unrelated settings) while upserting ACH's entries.
func deepMergeInto(dst, src map[string]any) {
	for k, sv := range src {
		if svMap, ok := sv.(map[string]any); ok {
			if dvMap, ok := dst[k].(map[string]any); ok {
				deepMergeInto(dvMap, svMap)
				continue
			}
		}
		dst[k] = sv
	}
}

// parseDoc unmarshals a JSON or TOML document into a generic map. An
// empty/whitespace body yields an empty map (not an error).
func parseDoc(content []byte, isTOML bool) (map[string]any, error) {
	out := map[string]any{}
	if len(bytes.TrimSpace(content)) == 0 {
		return out, nil
	}
	if isTOML {
		if err := toml.Unmarshal(content, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	if err := json.Unmarshal(content, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// readParseDoc reads + parses abs. Returns (nil, false, nil) when the file
// is absent or empty (no prior on-disk document).
func readParseDoc(abs string, isTOML bool) (map[string]any, bool, error) {
	body, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false, nil
	}
	m, perr := parseDoc(body, isTOML)
	if perr != nil {
		return nil, false, perr
	}
	return m, true, nil
}

// subtreeHash returns the xxh3 of a deterministic (sorted-key) JSON
// encoding of m. Encoding via json.Marshal regardless of the source
// format makes the hash independent of struct-vs-map field ordering and
// of JSON-vs-TOML provenance, so the freshly-rendered and on-disk subtrees
// are directly comparable.
func subtreeHash(m map[string]any) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return hash.Hash(bytes.NewReader(b))
}

// extractByKeys builds a document containing ONLY the dotted `keys` lifted
// from src (preserving nesting). found reports whether at least one key was
// present — used to distinguish "our keys absent on disk" from "present".
func extractByKeys(src map[string]any, keys []string) (map[string]any, bool) {
	out := map[string]any{}
	found := false
	for _, k := range keys {
		if v, ok := getDottedKey(src, k); ok {
			setDottedKey(out, k, v)
			found = true
		}
	}
	return out, found
}

// getDottedKey reads the value at a dotted path from a nested map. Returns
// (nil, false) when any segment is missing or a non-map intermediate is hit.
func getDottedKey(root map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	cur := root
	for i, p := range parts {
		v, ok := cur[p]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return v, true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

// setDottedKey sets val at a dotted path, creating intermediate maps.
func setDottedKey(root map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = val
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// findAdapterEntry locates the prior state.FileEntry for target under
// s.Adapter.Files, or nil when absent (fresh hydrate).
func findAdapterEntry(s *state.File, target string) *state.FileEntry {
	if s == nil {
		return nil
	}
	for i := range s.Adapter.Files {
		if s.Adapter.Files[i].Target == target {
			return &s.Adapter.Files[i]
		}
	}
	return nil
}

// mergeKindToString translates adapter.MergeKind into the state.json
// canonical string (state.FileEntry.Merge is a string per the §8.2
// schema). Unknown values fall through as empty string so the field
// is omitted from JSON.
func mergeKindToString(k adapter.MergeKind) string {
	switch k {
	case adapter.MergeDeep:
		return "deep"
	case adapter.MergeComposite:
		return "composite"
	case adapter.MergeReplace:
		return "replace"
	}
	return ""
}

// achMarkerRE matches the composite-merge inverse path: replace
// "<!-- ach:begin -->...<!-- ach:end -->" with the empty string,
// trimming a trailing newline if present. Per spec §8.5 for
// MergeComposite the inverse-merge is block deletion; the marker
// boundaries themselves are removed too.
//
// (?s) enables . to match newlines so a multi-line block is
// captured.
var achMarkerRE = regexp.MustCompile(`(?s)<!-- ach:begin -->.*?<!-- ach:end -->\n?`)

// SyncOptions packages the Sync handler's behavior toggles so the
// signature stays narrow. Force overrides the drift-wins arm
// (mismatch → delete anyway). Stderr is the channel for the
// "preserved due to drift" warnings; nil disables emission.
type SyncOptions struct {
	Force  bool
	Stderr io.Writer
}

// SyncStats reports the per-call Sync outcome. Pruned counts entries
// the engine removed (file or inverse-merge); Preserved counts
// entries the engine refused to touch (drift-wins).
type SyncStats struct {
	Pruned    int
	Preserved int
}

// Sync implements STATE-05 / D-16. Walks prev's file entries; for
// any entry whose Target is NOT in `new`, classify and act:
//
//   - On-disk hash differs from prev.Hash → preserve with stderr
//     warning (drift wins) UNLESS opts.Force.
//   - Merge=="" / MergeReplace → os.Remove the file.
//   - Merge=="deep" + Keys[] → JSON/TOML inverse-merge: read the
//     file, delete the listed top-level keys (or dotted paths),
//     re-write atomically.
//   - Merge=="composite" → regex-replace the
//     "<!-- ach:begin -->...<!-- ach:end -->" block with empty.
//     No marker found → preserve with warning.
//
// Sort the to-delete set deepest-first by path-depth (more slashes
// first) so child files are removed before parent dirs.
//
// After per-file deletion/inverse-merge, walk every parent dir
// deepest-first and call os.Remove — kernels return ENOTEMPTY for
// non-empty dirs which we silently swallow. CLI NEVER recursively
// deletes.
func Sync(prev, newFile *state.File, achDir string, opts SyncOptions) (SyncStats, error) {
	var stats SyncStats
	if prev == nil {
		return stats, nil
	}

	// Build the set of Targets present in newFile so we can compute
	// the to-delete set as set-difference.
	keep := map[string]struct{}{}
	if newFile != nil {
		for _, e := range walkEntries(newFile) {
			keep[e.Target] = struct{}{}
		}
	}

	// Collect deletable entries — those in prev but not in newFile.
	type del struct {
		entry state.FileEntry
		abs   string
	}
	prevEntries := walkEntries(prev)
	dels := make([]del, 0, len(prevEntries))
	parentDirs := map[string]struct{}{}
	for _, e := range prevEntries {
		if _, ok := keep[e.Target]; ok {
			continue
		}
		abs := e.Target
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(achDir, e.Target)
		}
		dels = append(dels, del{entry: e, abs: abs})
		parentDirs[filepath.Dir(abs)] = struct{}{}
	}

	// Sort deepest-first by path depth (more separators → first).
	sort.SliceStable(dels, func(i, j int) bool {
		return strings.Count(dels[i].abs, string(os.PathSeparator)) >
			strings.Count(dels[j].abs, string(os.PathSeparator))
	})

	for _, d := range dels {
		preserved, err := syncOne(d.entry, d.abs, opts)
		if err != nil {
			return stats, err
		}
		if preserved {
			stats.Preserved++
		} else {
			stats.Pruned++
		}
	}

	// Walk parent dirs deepest-first; os.Remove honors ENOTEMPTY by
	// returning an error which we silently swallow.
	pruneEmptyDirs(parentDirs, achDir)

	return stats, nil
}

// syncOne handles a single state entry's removal/inverse-merge.
// Returns (preserved, err) — preserved=true when drift-wins skipped
// the work.
func syncOne(e state.FileEntry, abs string, opts SyncOptions) (bool, error) {
	// Read the on-disk file; absence means already gone — count as
	// pruned (the engine's bookkeeping treats the entry as removed).
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("sync stat %s: %w", abs, statErr)
	}
	if info.IsDir() {
		// CLI never recursively deletes directories — skip.
		warnPreserved(opts.Stderr, abs, "directory entries are never recursively deleted")
		return true, nil
	}

	// Drift-wins gate: compare on-disk xxh3 to prev.Hash. Mismatch +
	// !Force → preserve.
	if !opts.Force && e.Hash != "" {
		current, err := hashFileXxh3(abs)
		if err != nil {
			return false, fmt.Errorf("sync hash %s: %w", abs, err)
		}
		if current != e.Hash {
			warnPreserved(opts.Stderr, abs,
				"local edits detected; pass --force to remove")
			return true, nil
		}
	}

	// Classify by Merge kind.
	switch e.Merge {
	case "composite":
		return syncComposite(e, abs, opts)
	case "deep":
		return syncDeep(e, abs, opts)
	case "", "replace":
		// Replace / unmerged → unlink.
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("sync remove %s: %w", abs, err)
		}
		return false, nil
	default:
		// Unknown merge kind — preserve defensively.
		warnPreserved(opts.Stderr, abs,
			fmt.Sprintf("unknown merge kind %q; refusing to inverse-merge", e.Merge))
		return true, nil
	}
}

// syncComposite handles MergeComposite by regex-replacing the
// "<!-- ach:begin -->...<!-- ach:end -->" block with empty. If the
// marker is absent the file is preserved with a warning (the user
// must have authored the file outside the engine's contract).
func syncComposite(_ state.FileEntry, abs string, opts SyncOptions) (bool, error) {
	body, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("sync read composite %s: %w", abs, err)
	}
	if !achMarkerRE.Match(body) {
		warnPreserved(opts.Stderr, abs,
			"composite marker not found; refusing to inverse-merge")
		return true, nil
	}
	updated := achMarkerRE.ReplaceAll(body, nil)
	// 0o600 — composite inverse-merge rewrites the same credential-
	// bearing adapter runtime-config file (CR-01).
	if err := state.WriteAtomic(abs, updated, 0o600); err != nil {
		return false, fmt.Errorf("sync write composite %s: %w", abs, err)
	}
	return false, nil
}

// syncDeep handles MergeDeep by removing the listed Keys[] paths
// from a JSON or TOML document. Dispatch on file extension:
//
//   - .json → encoding/json decode → walk → re-encode.
//   - .toml → BurntSushi/toml decode → walk → re-encode.
//   - other → preserve with warning (unknown shape — refuse to mutate).
//
// Each Key is a dotted-path expression — "mcpServers.foo" removes
// the "foo" subkey under "mcpServers". A single-segment key
// ("mcpServers") removes the whole top-level entry.
func syncDeep(e state.FileEntry, abs string, opts SyncOptions) (bool, error) {
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case extJSON:
		return syncDeepJSON(e, abs, opts)
	case extTOML:
		return syncDeepTOML(e, abs, opts)
	}
	warnPreserved(opts.Stderr, abs,
		fmt.Sprintf("unsupported merge=deep file extension %q; refusing to inverse-merge", ext))
	return true, nil
}

// syncDeepJSON loads a JSON file as map[string]any, removes the
// dotted-path Keys, re-encodes, and atomically rewrites. An empty
// resulting map → the file is deleted entirely (the user's whole
// document was engine-contributed).
func syncDeepJSON(e state.FileEntry, abs string, _ SyncOptions) (bool, error) {
	body, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("sync read JSON %s: %w", abs, err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return false, fmt.Errorf("sync decode JSON %s: %w", abs, err)
	}
	for _, k := range e.Keys {
		removeDottedKey(root, k)
	}
	if len(root) == 0 {
		if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return false, fmt.Errorf("sync remove JSON %s: %w", abs, rerr)
		}
		return false, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return false, fmt.Errorf("sync encode JSON %s: %w", abs, err)
	}
	// 0o600 — deep-merge JSON inverse rewrites the same credential-
	// bearing adapter runtime-config file (CR-01).
	if err := state.WriteAtomic(abs, buf.Bytes(), 0o600); err != nil {
		return false, fmt.Errorf("sync write JSON %s: %w", abs, err)
	}
	return false, nil
}

// syncDeepTOML mirrors syncDeepJSON for TOML files via the
// BurntSushi/toml decode-modify-encode roundtrip.
func syncDeepTOML(e state.FileEntry, abs string, _ SyncOptions) (bool, error) {
	body, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("sync read TOML %s: %w", abs, err)
	}
	var root map[string]any
	if err := toml.Unmarshal(body, &root); err != nil {
		return false, fmt.Errorf("sync decode TOML %s: %w", abs, err)
	}
	for _, k := range e.Keys {
		removeDottedKey(root, k)
	}
	if len(root) == 0 {
		if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return false, fmt.Errorf("sync remove TOML %s: %w", abs, rerr)
		}
		return false, nil
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(root); err != nil {
		return false, fmt.Errorf("sync encode TOML %s: %w", abs, err)
	}
	// 0o600 — deep-merge TOML inverse rewrites the same credential-
	// bearing adapter runtime-config file (CR-01).
	if err := state.WriteAtomic(abs, buf.Bytes(), 0o600); err != nil {
		return false, fmt.Errorf("sync write TOML %s: %w", abs, err)
	}
	return false, nil
}

// removeDottedKey deletes the leaf at a dotted-path expression from
// a nested map[string]any. Missing intermediate keys are no-ops —
// removing a key that does not exist is idempotent. Non-map
// intermediates are also no-ops (cannot recurse into a scalar).
func removeDottedKey(root map[string]any, path string) {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return
	}
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			delete(cur, p)
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

// pruneEmptyDirs walks parent dirs deepest-first calling os.Remove.
// Honors ENOTEMPTY silently (non-empty dir → preserved). Stops at
// achDir's parent so the engine never escapes the workspace root.
func pruneEmptyDirs(parents map[string]struct{}, achDir string) {
	// Expand the set to include each parent's parents up to achDir's
	// parent so a deep-nested prune cascades cleanly.
	all := map[string]struct{}{}
	parentRoot := filepath.Dir(achDir)
	for p := range parents {
		for p != "" && p != "." && p != parentRoot && p != string(filepath.Separator) {
			if _, ok := all[p]; ok {
				break
			}
			all[p] = struct{}{}
			next := filepath.Dir(p)
			if next == p {
				break
			}
			p = next
		}
	}

	dirs := make([]string, 0, len(all))
	for p := range all {
		dirs = append(dirs, p)
	}
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(os.PathSeparator)) >
			strings.Count(dirs[j], string(os.PathSeparator))
	})
	for _, d := range dirs {
		// os.Remove returns *os.PathError wrapping ENOTEMPTY for
		// non-empty directories; we silently swallow per D-16.
		_ = os.Remove(d)
	}
}

// walkEntries flattens every FileEntry across all projection buckets
// on a state.File (Prompts → Plugins → Artifacts → RuntimeFiles →
// Adapter.Files). The deterministic order mirrors extract.walkAllEntries
// (the autoclaim package's flattener) so behavior stays consistent
// across the Phase 7 surface.
func walkEntries(f *state.File) []state.FileEntry {
	if f == nil {
		return nil
	}
	total := len(f.Prompts) + len(f.Plugins) + len(f.Artifacts) +
		len(f.RuntimeFiles) + len(f.Adapter.Files)
	out := make([]state.FileEntry, 0, total)
	out = append(out, f.Prompts...)
	out = append(out, f.Plugins...)
	out = append(out, f.Artifacts...)
	out = append(out, f.RuntimeFiles...)
	out = append(out, f.Adapter.Files...)
	return out
}

// warnPreserved emits a single-line stderr message naming the
// preserved file and the reason. stderr=nil disables emission
// (useful for unit tests that don't care about the warning text).
func warnPreserved(stderr io.Writer, abs, reason string) {
	if stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(stderr, "warning: preserving %s — %s\n", abs, reason)
}

// hashFileXxh3 returns the canonical "xxh3:<32hex>" digest of the
// file at path. Wraps the W1-04 hash.Hash with file open/close
// discipline — same shape as extract.hashFileXxh3 (kept package-local
// here to avoid exporting from extract).
func hashFileXxh3(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 — Sync handler reads paths from prior state ledger
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return hash.Hash(f)
}

// NewWiring constructs the default Extractor + AdapterDispatcher
// pair the cobra layer (07-W3-05 cmd/ach-cli/cmd/hydrate.go) injects
// into commit.go via fields on *commit. force is the --force flag;
// allowSymlinks is --allow-symlinks; limits flow through from
// extract.LoadLimits.
//
// The constructor returns the impls as the public interfaces so the
// cobra layer can wire commit.extractor = ext / commit.adapter = ad
// without exposing the unexported impl types.
func NewWiring(
	client *httpclient.Client,
	platformID string,
	limits extract.Limits,
	allowSymlinks bool,
	force bool,
) (Extractor, AdapterDispatcher) {
	ext := &extractorImpl{
		client:        client,
		limits:        limits,
		allowSymlinks: allowSymlinks,
	}
	disp := &adapterDispatcherImpl{
		platformID: platformID,
		force:      force,
	}
	return ext, disp
}

// SPDX-License-Identifier: Apache-2.0

// Package merge provides pure document-merge, composite-write, and dotted-key
// helpers for JSON and TOML runtime-config files. It is intentionally k8s-free
// so both the hydrate dispatcher and the localpkg installer can import it
// without pulling in controller-runtime or k8s.io/* dependencies.
//
// Callers:
//   - internal/cli/hydrate: forward-merge of adapter configs + composite
//     host-memory files (CLAUDE.md / GEMINI.md) during hydrate.
//   - internal/cli/localpkg (future): reuse the same merge primitives for
//     local package installation.
package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/state"
)

// File-extension discriminators for JSON/TOML format detection.
const (
	extJSON = ".json"
	extTOML = ".toml"
)

// MergeForward reads the existing file at abs (JSON or TOML by extension),
// deep-merges the keys from ours (an adapter-rendered document carrying ONLY
// ACH's contributed entries) into it, and atomic-writes the result at the
// given mode. When abs does not exist, the result is just ours. The user's
// pre-existing keys are preserved; ACH's keys upsert same-named entries.
// Returns the merged bytes (for hashing/state if the caller wants them). This
// is the forward counterpart to syncDeep{JSON,TOML}'s removal.
//
// Concurrency note (security 2.4 — accept-disposition): the read-merge-write
// sequence is NOT atomic against external writers. The <achDir>/lock flock
// excludes other ach-cli processes, but a concurrent editor save on the
// runtime-config file between our read and our atomic-rename will be silently
// clobbered. Pragmatic trade-off for v1; see CLAUDE.md "Common failure modes".
func MergeForward(abs string, ours []byte, mode os.FileMode) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(abs)) {
	case extJSON:
		return MergeDoc(abs, ours, mode, false)
	case extTOML:
		return MergeDoc(abs, ours, mode, true)
	default:
		// No structured merge for an unknown extension — write verbatim.
		if err := state.WriteAtomic(abs, ours, mode); err != nil {
			return nil, fmt.Errorf("mergeForward write %s: %w", abs, err)
		}
		return ours, nil
	}
}

// MergeDoc deep-merges ours into the existing JSON or TOML document at abs
// (isTOML selects the codec). A missing file is treated as an empty object. A
// pre-existing file MUST be valid in the selected format (we never silently
// discard a user's config). Returns the merged bytes after atomic-writing at
// the given mode.
func MergeDoc(abs string, ours []byte, mode os.FileMode, isTOML bool) ([]byte, error) {
	format := "JSON"
	if isTOML {
		format = "TOML"
	}
	oursMap, err := parseRendered(ours, isTOML)
	if err != nil {
		return nil, fmt.Errorf("mergeForward decode rendered %s: %w", format, err)
	}
	existing := map[string]any{}
	if body, err := os.ReadFile(abs); err == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			if derr := UnmarshalDoc(body, &existing, isTOML); derr != nil {
				return nil, fmt.Errorf("mergeForward decode existing %s %s: %w", format, abs, derr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mergeForward read %s: %w", abs, err)
	}
	DeepMergeInto(existing, oursMap)
	out, err := EncodeDoc(existing, isTOML)
	if err != nil {
		return nil, fmt.Errorf("mergeForward encode %s %s: %w", format, abs, err)
	}
	if err := state.WriteAtomic(abs, out, mode); err != nil {
		return nil, fmt.Errorf("mergeForward write %s %s: %w", format, abs, err)
	}
	return out, nil
}

// DeepMergeInto recursively merges src into dst: when both sides hold a
// nested object at the same key, recurse; otherwise src's value overwrites
// dst's. This preserves the user's sibling keys (e.g. their other MCP
// servers and unrelated settings) while upserting ACH's entries.
func DeepMergeInto(dst, src map[string]any) {
	for k, sv := range src {
		if svMap, ok := sv.(map[string]any); ok {
			if dvMap, ok := dst[k].(map[string]any); ok {
				DeepMergeInto(dvMap, svMap)
				continue
			}
		}
		dst[k] = sv
	}
}

// ParseDoc unmarshals a JSON or TOML document into a generic map. An
// empty/whitespace body yields an empty map (not an error).
func ParseDoc(content []byte, isTOML bool) (map[string]any, error) {
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

// EncodeDoc renders a generic map back to JSON or TOML bytes. It is the
// inverse of ParseDoc and the single encoder both the forward-merge and
// the --sync inverse-merge paths share. The settings are pinned to match
// the prior per-format encoders byte-for-byte (JSON: 2-space indent,
// HTML-escaping disabled; TOML: BurntSushi default encoder) so drift
// hashing and idempotence stay stable across the consolidation.
func EncodeDoc(m map[string]any, isTOML bool) ([]byte, error) {
	var buf bytes.Buffer
	if isTOML {
		enc := toml.NewEncoder(&buf)
		if err := enc.Encode(m); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ReadParseDoc reads + parses abs. Returns (nil, false, nil) when the file
// is absent or empty (no prior on-disk document).
func ReadParseDoc(abs string, isTOML bool) (map[string]any, bool, error) {
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
	m, perr := ParseDoc(body, isTOML)
	if perr != nil {
		return nil, false, perr
	}
	return m, true, nil
}

// SubtreeHash returns the xxh3 of a deterministic (sorted-key) JSON
// encoding of m. Encoding via json.Marshal regardless of the source
// format makes the hash independent of struct-vs-map field ordering and
// of JSON-vs-TOML provenance, so the freshly-rendered and on-disk subtrees
// are directly comparable.
func SubtreeHash(m map[string]any) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return hash.Hash(bytes.NewReader(b))
}

// ExtractByKeys builds a document containing ONLY the dotted keys lifted
// from src (preserving nesting). found reports whether at least one key was
// present — used to distinguish "our keys absent on disk" from "present".
func ExtractByKeys(src map[string]any, keys []string) (map[string]any, bool) {
	out := map[string]any{}
	found := false
	for _, k := range keys {
		if v, ok := GetDottedKey(src, k); ok {
			SetDottedKey(out, k, v)
			found = true
		}
	}
	return out, found
}

// GetDottedKey reads the value at a dotted path from a nested map. Returns
// (nil, false) when any segment is missing or a non-map intermediate is hit.
func GetDottedKey(root map[string]any, path string) (any, bool) {
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

// SetDottedKey sets val at a dotted path, creating intermediate maps.
func SetDottedKey(root map[string]any, path string, val any) {
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

// RemoveDottedKey deletes the leaf at a dotted-path expression from
// a nested map[string]any. Missing intermediate keys are no-ops —
// removing a key that does not exist is idempotent. Non-map
// intermediates are also no-ops (cannot recurse into a scalar).
func RemoveDottedKey(root map[string]any, path string) {
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

// PruneDottedKeys removes the dotted-path keys from doc and reports the
// inverse-merge outcome shared by hydrate's syncDeepDoc and the local
// installer's classifyUninstall: empty=true → nothing engine-contributed
// remains, the caller deletes the file; else out is the re-encoded
// document (JSON or TOML per isTOML, byte-stable via EncodeDoc).
func PruneDottedKeys(doc map[string]any, keys []string, isTOML bool) (out []byte, empty bool, err error) {
	for _, k := range keys {
		RemoveDottedKey(doc, k)
	}
	if len(doc) == 0 {
		return nil, true, nil
	}
	out, err = EncodeDoc(doc, isTOML)
	return out, false, err
}

// WriteComposite performs the forward composite merge: a marker-bounded
// insert (no prior block) or replace (existing per-plugin block) of block
// into the host memory file at abs. block must already be wrapped in the
// plugin's outer markers (produced by the caller via PluginMarkerRE or a
// custom builder). Any forged inner markers in untrusted plugin prose are
// inert text inside the outer boundary (T-02-03 marker-injection mitigation).
//
// mode is the file permission applied via state.WriteAtomic (typically 0o644
// for host memory files like CLAUDE.md which carry no credential).
func WriteComposite(abs, id string, block []byte, mode os.FileMode) error {
	body, rerr := os.ReadFile(abs)
	if rerr != nil && !os.IsNotExist(rerr) {
		return rerr
	}
	var merged []byte
	re := PluginMarkerRE(id)
	if re.Match(body) {
		merged = re.ReplaceAll(body, block)
	} else {
		merged = append(append([]byte(nil), body...), block...)
	}
	return state.WriteAtomic(abs, merged, mode)
}

// CompositeBlock wraps content in the per-id composite markers
// ("<!-- ach:begin:<id> -->\n<body>\n<!-- ach:end:<id> -->\n") that
// WriteComposite and PluginMarkerRE insert/replace/match. Trailing newlines on
// content are trimmed before wrapping so the block ends with exactly one
// newline — deterministic for idempotent re-writes. This is the single
// marker-wrapper shared by the hydrate composite path and the localpkg
// installer, keeping both byte-identical with the inverse PluginMarkerRE regex.
func CompositeBlock(id string, content []byte) []byte {
	body := bytes.TrimRight(content, "\n")
	return []byte("<!-- ach:begin:" + id + " -->\n" +
		string(body) + "\n<!-- ach:end:" + id + " -->\n")
}

// PluginMarkerRE builds the per-plugin composite marker regex:
// "<!-- ach:begin:<plugin> -->...<!-- ach:end:<plugin> -->" with an
// optional trailing newline. The plugin id is regexp-escaped via
// QuoteMeta so a forged inner marker carried in untrusted plugin prose
// (T-02-03) cannot widen or hijack another plugin's region — the per-id
// boundary is the OUTER real markers only. (?s) lets . span newlines so
// a multi-line block is captured.
//
// Both the forward composite arm (insert/replace) and the inverse path
// (syncComposite deletion) build the regex from the same builder,
// keeping the forward and inverse merges symmetric on the exact same
// marked region.
func PluginMarkerRE(pluginID string) *regexp.Regexp {
	return regexp.MustCompile("(?s)<!-- ach:begin:" + regexp.QuoteMeta(pluginID) +
		" -->.*?<!-- ach:end:" + regexp.QuoteMeta(pluginID) + " -->\\n?")
}

// parseRendered unmarshals the freshly-rendered ours bytes into a
// generic map via the selected codec. Unlike ParseDoc it does NOT treat
// an empty body as an empty map — the rendered content always parses as
// the adapter emitted it; preserving the prior strict-decode semantics.
func parseRendered(ours []byte, isTOML bool) (map[string]any, error) {
	var m map[string]any
	if err := UnmarshalDoc(ours, &m, isTOML); err != nil {
		return nil, err
	}
	return m, nil
}

// UnmarshalDoc decodes b into v via the JSON or TOML codec. Unlike ParseDoc it
// does NOT treat an empty/whitespace body as an empty map — an empty body
// yields a codec error. This is the strict variant used by paths that must
// reject a missing or blank on-disk document (e.g. syncDeepDoc in the hydrate
// sync path).
func UnmarshalDoc(b []byte, v any, isTOML bool) error {
	if isTOML {
		return toml.Unmarshal(b, v)
	}
	return json.Unmarshal(b, v)
}

// SPDX-License-Identifier: Apache-2.0

// Claude Code marketplace.json wire-format types + parser. The schema
// is the upstream Claude Code real schema (TODO §5):
//
//   plugins[].source can be either a bare string (local-path) or an
//   object with a "source" discriminator that is either "git-subdir"
//   or "url". Any other shape (unknown discriminator, malformed JSON)
//   resolves to Kind="" so the per-entry Stage-2 path can surface
//   ReasonUnsupportedPluginSource without aborting the whole catalog.
//
// Per-entry dispatch + fetch lives in marketplace_dispatch.go.

package ach

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// ClaudeCodeMarketplace is the parsed top-level marketplace.json (Hub
// §12.1 + Claude Code upstream schema). owner is preserved verbatim
// for parity with the wire format but is not inspected by Stage-1 /
// Stage-2.
type ClaudeCodeMarketplace struct {
	Name    string                        `json:"name"`
	Owner   ClaudeCodeMarketplaceOwner    `json:"owner"`
	Plugins []ClaudeCodeMarketplacePlugin `json:"plugins"`
}

// ClaudeCodeMarketplaceOwner mirrors the upstream owner block.
// Email is a real upstream field (anthropics/claude-plugins-official
// emits "email" — was "url" in the placeholder schema; both are kept
// for backward-compat with older fixtures).
type ClaudeCodeMarketplaceOwner struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// ClaudeCodeMarketplacePlugin is one entry under plugins[]. Source is
// a discriminated union — see ClaudeCodeMarketplaceSource.
//
// Upstream schema also allows description, version, author, category,
// homepage, license — accepted but not modeled (forward-compat).
type ClaudeCodeMarketplacePlugin struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description,omitempty"`
	Source      ClaudeCodeMarketplaceSource `json:"source"`
	Version     string                      `json:"version,omitempty"`
}

// ClaudeCodeMarketplaceSource is the normalized per-entry source. The
// wire format is heterogeneous:
//
//   - bare string:        "source": "./relative/path"
//     → Kind="local-path", Path="./relative/path"
//   - object git-subdir:  "source": {"source":"git-subdir","url":"...","path":"...","ref":"v1","sha":"<40hex>"}
//     → Kind="git-subdir", URL/Path/Ref/SHA populated
//   - object url:         "source": {"source":"url","url":"...","sha":"<40hex>"}
//     → Kind="url", URL/SHA populated (whole-repo, no Path)
//
// Any other shape (object with unknown source.source, malformed JSON
// for the source field) → Kind="" so Stage-2 surfaces
// ReasonUnsupportedPluginSource per-entry.
type ClaudeCodeMarketplaceSource struct {
	Kind string // "git-subdir" | "url" | "local-path" | "" (unsupported)
	URL  string
	Path string
	Ref  string
	SHA  string
}

// UnmarshalJSON implements the string-or-object union. Never returns an
// error: malformed shapes resolve to Kind="" so the per-entry path can
// flip UnsupportedPluginSource via the parser's discriminator check.
// The reason: a single bad entry must not abort the whole marketplace.
func (s *ClaudeCodeMarketplaceSource) UnmarshalJSON(data []byte) error {
	// Bare string form.
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Kind = "local-path"
		s.Path = str
		return nil
	}
	// Object form.
	var obj struct {
		Source string `json:"source"`
		URL    string `json:"url"`
		Path   string `json:"path"`
		Ref    string `json:"ref"`
		SHA    string `json:"sha"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		// Neither string nor object — leave Kind="" so the per-entry
		// path surfaces UnsupportedPluginSource.
		return nil
	}
	switch obj.Source {
	case "git-subdir", "url":
		s.Kind = obj.Source
	default:
		// Unknown discriminator — Kind="" → per-entry unsupported.
		return nil
	}
	s.URL = obj.URL
	s.Path = obj.Path
	s.Ref = obj.Ref
	s.SHA = obj.SHA
	return nil
}

// dns1123SubdomainRe validates a plugin name as a DNS-1123 subdomain (RFC
// 1123): lowercase ASCII + digits + '-' + '.', start and end with
// alphanumeric. This excludes '/', '..', leading '.', non-printable
// chars, and control sequences — the T-02-06-01 mitigation surface.
var dns1123SubdomainRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// dns1123MaxLen is the standard DNS-1123 subdomain length cap.
const dns1123MaxLen = 253

// marketplaceMaxPluginsPerCatalog bounds the number of plugins[] entries
// a single marketplace.json may declare. 5000 is generous: Anthropic's
// catalog currently has ~250 entries; the bound exists only to stop a
// pathological 10M-entry marketplace from making Stage-1 unresponsive.
const marketplaceMaxPluginsPerCatalog = 5000

// parseClaudeCodeMarketplace unmarshals the upstream marketplace.json
// (Claude Code real schema) and performs Stage-1 validation. Every
// failure wraps sources.ErrUpstreamInvalid so the caller's
// classifyFetchError maps to ReasonUpstreamInvalid uniformly.
//
// Per-plugin validation rules:
//
//  1. plugin.Name MUST match DNS-1123 subdomain rules — adversarial
//     names like ../etc/passwd are rejected before they reach
//     materializeMarketplacePlugin (T-02-06-01 mitigation, preserved
//     from the placeholder parser).
//  2. plugin.Source.Kind MUST be one of {"git-subdir","url","local-path"}
//     OR empty (empty signals the per-entry UnsupportedPluginSource
//     branch in Stage-2 — kept so a single bad entry never aborts the
//     whole marketplace).
//  3. For Kind=="git-subdir" / "url": URL and SHA MUST be non-empty.
//     For Kind=="local-path": Path MUST be non-empty AND MUST NOT
//     escape the marketplace repo root (no leading "/", no "..")
//     — defense-in-depth path-traversal gate even though Stage-2
//     uses the path purely as a tar subtree filter.
//
// Per-entry validation surface bounds (audit findings folded in):
//   - plugin.Name truncated to dns1123MaxLen (already enforced by the
//     regex which caps at 253 chars; per-label 63-char cap enforced by
//     a separate check because the regex misses it).
//   - plugins[] length capped at marketplaceMaxPluginsPerCatalog.
//
// An empty plugins[] is treated as ErrUpstreamInvalid — a marketplace
// that ships no plugins is not legitimate steady-state.
func parseClaudeCodeMarketplace(body []byte) (*ClaudeCodeMarketplace, error) {
	var mkt ClaudeCodeMarketplace
	if err := json.Unmarshal(body, &mkt); err != nil {
		return nil, fmt.Errorf("marketplace.json: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	if len(mkt.Plugins) == 0 {
		return nil, fmt.Errorf("marketplace.json: zero plugins declared: %w", sources.ErrUpstreamInvalid)
	}
	if len(mkt.Plugins) > marketplaceMaxPluginsPerCatalog {
		return nil, fmt.Errorf("marketplace.json: %d plugins exceeds cap %d: %w",
			len(mkt.Plugins), marketplaceMaxPluginsPerCatalog, sources.ErrUpstreamInvalid)
	}
	for i := range mkt.Plugins {
		p := &mkt.Plugins[i]
		// (1) name validation — bounded length + DNS-1123 + per-label.
		if err := validatePluginName(p.Name); err != nil {
			return nil, fmt.Errorf("marketplace.json: plugin[%d].name %q: %v: %w",
				i, truncateErrField(p.Name), err, sources.ErrUpstreamInvalid)
		}
		// (2)+(3) per-Kind validation.
		switch p.Source.Kind {
		case "git-subdir":
			if p.Source.URL == "" || p.Source.SHA == "" || p.Source.Path == "" {
				return nil, fmt.Errorf("marketplace.json: plugin %q: git-subdir requires url+path+sha: %w",
					truncateErrField(p.Name), sources.ErrUpstreamInvalid)
			}
		case "url":
			if p.Source.URL == "" || p.Source.SHA == "" {
				return nil, fmt.Errorf("marketplace.json: plugin %q: url requires url+sha: %w",
					truncateErrField(p.Name), sources.ErrUpstreamInvalid)
			}
		case "local-path":
			if p.Source.Path == "" {
				return nil, fmt.Errorf("marketplace.json: plugin %q: local-path requires path: %w",
					truncateErrField(p.Name), sources.ErrUpstreamInvalid)
			}
			if err := validateLocalPath(p.Source.Path); err != nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: local-path %q: %v: %w",
					truncateErrField(p.Name), truncateErrField(p.Source.Path), err, sources.ErrUpstreamInvalid)
			}
		case "":
			// Tolerated — Stage-2 emits ReasonUnsupportedPluginSource.
		default:
			// Should be unreachable (UnmarshalJSON resolves unknowns to "")
			// but defensive: tolerate the same way the empty case does.
		}
	}
	return &mkt, nil
}

// truncateErrFieldMax bounds the length of upstream-supplied values
// (plugin name, path string, source-type echo) embedded in error
// messages — k8s status.message is capped at ~4096 chars and individual
// condition messages are far smaller in practice.
const truncateErrFieldMax = 64

// truncateErrField returns at most truncateErrFieldMax bytes of s.
func truncateErrField(s string) string {
	if len(s) <= truncateErrFieldMax {
		return s
	}
	return s[:truncateErrFieldMax] + "…"
}

// validatePluginName enforces RFC 1123 subdomain rules plus the per-label
// 63-char cap that the dns1123SubdomainRe regex misses.
func validatePluginName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("empty")
	}
	if len(name) > dns1123MaxLen {
		return fmt.Errorf("length %d exceeds %d", len(name), dns1123MaxLen)
	}
	if !dns1123SubdomainRe.MatchString(name) {
		return fmt.Errorf("not a DNS-1123 subdomain")
	}
	// Per-label 63-char cap (RFC 1123 §2.1).
	for _, label := range strings.Split(name, ".") {
		if len(label) > 63 {
			return fmt.Errorf("label %q exceeds 63 chars", label)
		}
	}
	return nil
}

// validateLocalPath rejects path traversal and absolute paths for the
// local-path Kind. Cleaning is intentionally NOT applied — we want to
// flag the raw upstream string, not silently rewrite it.
func validateLocalPath(p string) error {
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("must be relative")
	}
	// Reject any segment that is "..", regardless of position.
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("contains '..' segment")
		}
	}
	return nil
}

// SPDX-License-Identifier: Apache-2.0

// Claude Code marketplace.json wire-format types + parser. The schema
// is the upstream Claude Code real schema:
//
//   plugins[].source can be either a bare string (local-path) or an
//   object with a "source" discriminator. Recognised discriminators:
//
//     - "git-subdir": {url, path, ref?, sha?}
//     - "url":        {url, path?, ref?, sha?}   (path accepts upstream
//                                                  drift — schema says
//                                                  no path, real catalogs
//                                                  ship it; treated as
//                                                  git-subdir when set)
//     - "github":     {repo, ref?, sha?} → cloned as
//                     https://github.com/<repo>.git
//
//   Any other discriminator (npm, ftp, ...) resolves to Kind="" so the
//   per-entry Stage-2 path surfaces ReasonUnsupportedPluginSource
//   without aborting the whole catalog.
//
// Per-entry dispatch + fetch lives in marketplace_dispatch.go.
// Schema URLs: see CLAUDE.md "External references".

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
//   - object git-subdir:  "source": {"source":"git-subdir","url":"...","path":"...","ref":"v1","sha":"..."}
//     → Kind="git-subdir", URL/Path set; Ref/SHA optional (Phase 2 resolves
//     ref→sha at dispatch time if SHA is absent).
//   - object url:         "source": {"source":"url","url":"...","path":"?"}
//     → Kind="url", URL set; Path preserved when present (upstream-drift
//     ack); Ref/SHA optional (Phase 2 resolves at dispatch).
//   - object github:      "source": {"source":"github","repo":"owner/name","ref":"?","sha":"?"}
//     → Kind="github", Repo set; Ref/SHA optional (Phase 2 resolves at
//     dispatch). Cloned as https://github.com/<repo>.git.
//
// Any other shape (object with unknown source.source, malformed JSON
// for the source field) → Kind="" so Stage-2 surfaces
// ReasonUnsupportedPluginSource per-entry.
type ClaudeCodeMarketplaceSource struct {
	Kind string // "git-subdir" | "url" | "github" | "local-path" | "" (unsupported)
	URL  string
	Repo string // github-Kind only: "owner/name" → cloned as https://github.com/<repo>.git
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
		Repo   string `json:"repo"`
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
	case "git-subdir", "url", "github":
		s.Kind = obj.Source
	default:
		// Unknown discriminator (npm, ftp, ...) — Kind="" → per-entry
		// unsupported. npm is a known wire-format value we intentionally
		// route here per the v1alpha1 git-only operator scope.
		return nil
	}
	s.URL = obj.URL
	s.Repo = obj.Repo
	s.Path = obj.Path
	s.Ref = obj.Ref
	s.SHA = obj.SHA
	return nil
}

// pluginNameSafeRe is the T-02-06-08 mitigation: entry.Name is used as
// a filename segment (<CacheRoot>/marketplace/<mp>/plugin/<name>.tar.gz)
// AND as a Postgres text column, so the threat surface is path-traversal
// + control-character injection — NOT Kubernetes DNS-1123 (the name
// never becomes a CR identifier; the marketplace_plugins row is keyed
// by (marketplace_name, name) and the row never round-trips through
// the k8s API). Marketplace catalog names like "Notion" or
// "API-Reference" are legitimate and must pass.
//
// Allowed: ASCII alphanumerics (mixed case) + the three separators
// '.', '-', '_'. The leading character must be alphanumeric so a name
// can never start with a separator (no hidden-file confusion, no
// leading-hyphen flag confusion in any downstream CLI).
//
// Rejected: '/', '\', NUL, any control char, leading separator, empty.
// '..' is rejected by an explicit strings.Contains check in
// validatePluginName because the regex would otherwise admit it.
var pluginNameSafeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// pluginNameMaxLen bounds entry.Name embedded into a filesystem path
// segment + DB text column. 253 mirrors the historical DNS-1123 cap so
// existing storage_location strings remain bounded.
const pluginNameMaxLen = 253

// marketplaceMaxPluginsPerCatalog bounds the number of plugins[] entries
// a single marketplace.json may declare. 5000 is generous: Anthropic's
// catalog currently has ~250 entries; the bound exists only to stop a
// pathological 10M-entry marketplace from making Stage-1 unresponsive.
const marketplaceMaxPluginsPerCatalog = 5000

// parseClaudeCodeMarketplace unmarshals the upstream marketplace.json
// (Claude Code real schema — see CLAUDE.md "External references" for
// the schemastore URLs) and performs Stage-1 validation.
//
// Validation surface:
//
// Catalog-level HARD FAIL (returns wrapped sources.ErrUpstreamInvalid):
//   - JSON-level unmarshal failure.
//   - len(plugins) == 0 — a marketplace with zero entries is not legit.
//   - len(plugins) > marketplaceMaxPluginsPerCatalog (DoS guard).
//
// Per-entry DEMOTE (sets Source.Kind="", catalog continues):
//   - plugin.Name fails the filename-safety check
//     (T-02-06-08: name is used as a filename segment + DB text; the
//     adversarial-name surface is bounded by truncateErrField when the
//     demote feeds formatStage2Message).
//   - git-subdir entry missing url OR path.
//   - url entry missing url (sha is optional; Phase 2 resolves ref→sha).
//   - github entry missing repo.
//   - local-path entry with empty / path-traversal path.
//   - Unknown source discriminator (already demoted by UnmarshalJSON).
//
// Per-entry validation is intentionally minimal — sha / ref are both
// optional and Phase-2 pre-resolution handles the rest at dispatch time.
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
		// (1) name validation — filename-safe + bounded length. Failure
		// demotes the entry to Kind="" so Stage-2 emits
		// ReasonUnsupportedPluginSource per-entry; the catalog continues.
		// (#15 follow-up: previously catalog-wide hard fail — one upstream
		// `name: "Notion"` entry would doom the whole anthropic catalog.)
		if validatePluginName(p.Name) != nil {
			p.Source = ClaudeCodeMarketplaceSource{} // demote
			continue
		}
		// (2)+(3) per-Kind field validation. Failure demotes the entry to
		// Kind="" so Stage-2 emits ReasonUnsupportedPluginSource per-entry
		// (issue #15 contract). The catalog continues.
		switch p.Source.Kind {
		case "git-subdir":
			if p.Source.URL == "" || p.Source.Path == "" {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "url":
			if p.Source.URL == "" {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "github":
			if p.Source.Repo == "" {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "local-path":
			if p.Source.Path == "" || validateLocalPath(p.Source.Path) != nil {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "":
			// Already demoted upstream by UnmarshalJSON (unknown discriminator).
		default:
			// Should be unreachable.
			p.Source = ClaudeCodeMarketplaceSource{}
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

// validatePluginName enforces filename-safety + bounded length on a
// marketplace plugin name. See pluginNameSafeRe for the threat-model
// rationale. The function is intentionally permissive about case +
// separators (allows "Notion", "API-Reference", "code-review.v2")
// because the name does NOT become a Kubernetes resource identifier —
// it lives only as a Postgres text column and a filename segment.
func validatePluginName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("empty")
	}
	if len(name) > pluginNameMaxLen {
		return fmt.Errorf("length %d exceeds %d", len(name), pluginNameMaxLen)
	}
	if !pluginNameSafeRe.MatchString(name) {
		return fmt.Errorf("not filename-safe (allowed: [A-Za-z0-9._-], must start with alphanumeric)")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("contains '..' (path-traversal)")
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

// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 1: Claude Code marketplace.json wire-format types,
// parser, and per-entry SourceSpec builder for the PluginMarketplace
// Stage-1 / Stage-2 lifecycle.
//
// Marketplace schema is the Claude Code format mirrored VERBATIM (Hub
// §12.1) — ACH does not extend or redefine it. parseClaudeCodeMarketplace
// performs minimal defensive validation:
//   - reject non-JSON bodies via json.Unmarshal (wrapped ErrUpstreamInvalid)
//   - reject empty plugins[] (a well-formed marketplace declares at least
//     one plugin)
//   - per-plugin discriminator check: the matching source subobject MUST
//     be non-nil; unknown source.type → wrapped ErrUpstreamInvalid
//   - DNS-1123-subdomain validation on plugin.Name (T-02-06-01 mitigation:
//     adversarial plugin names like ../../etc/passwd are rejected before
//     they reach computeFinalPath)
//   - npm is KEPT in the parse output; Stage-2 surfaces
//     UnsupportedPluginSource per-entry via the errUnsupportedPluginSource
//     sentinel that marketplacePluginToSourceSpec returns.

package ach

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// ClaudeCodeMarketplace is the parsed top-level marketplace.json (Hub
// §12.1). owner is preserved for parity with the wire format but is not
// inspected by the Stage-1 / Stage-2 reconciler.
type ClaudeCodeMarketplace struct {
	Name    string                        `json:"name"`
	Owner   ClaudeCodeMarketplaceOwner    `json:"owner"`
	Plugins []ClaudeCodeMarketplacePlugin `json:"plugins"`
}

// ClaudeCodeMarketplaceOwner mirrors the Claude Code owner block.
type ClaudeCodeMarketplaceOwner struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ClaudeCodeMarketplacePlugin is one entry under plugins[]. The Source
// discriminator drives Stage-2 fetcher dispatch via
// marketplacePluginToSourceSpec.
type ClaudeCodeMarketplacePlugin struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Source      ClaudeCodeMarketplaceSource `json:"source"`
	Version     string                      `json:"version,omitempty"`
}

// ClaudeCodeMarketplaceSource embeds the six source-type subobject
// pointers that map cleanly into the existing achv1alpha1.*Source types
// the rest of ACH already uses (no duplication of wire types). The npm
// subobject is NOT included — Type=="npm" is REJECTED at conversion time
// by marketplacePluginToSourceSpec, but the parser tolerates the entry
// so Stage-2 can flip its per-plugin status via the partial-failure path
// without aborting the entire marketplace refresh.
type ClaudeCodeMarketplaceSource struct {
	Type      string                       `json:"type"`
	GitHub    *achv1alpha1.GitHubSource    `json:"github,omitempty"`
	GitLab    *achv1alpha1.GitLabSource    `json:"gitlab,omitempty"`
	Bitbucket *achv1alpha1.BitbucketSource `json:"bitbucket,omitempty"`
	S3        *achv1alpha1.S3Source        `json:"s3,omitempty"`
	GCS       *achv1alpha1.GCSSource       `json:"gcs,omitempty"`
	HTTP      *achv1alpha1.HTTPSource      `json:"http,omitempty"`
}

// errUnsupportedPluginSource is the typed sentinel
// marketplacePluginToSourceSpec returns when source.type == "npm" (the
// only wire-format type ACH v1alpha1 declines to materialize). Stage-2
// in pluginmarketplace_controller.go checks errors.Is(err,
// errUnsupportedPluginSource) BEFORE classifyFetchError so the result
// maps to ReasonUnsupportedPluginSource (a marketplace-only reason not
// in the SourceReachable enum).
var errUnsupportedPluginSource = errors.New("unsupported plugin source (e.g. npm)")

// dns1123SubdomainRe validates a plugin name as a DNS-1123 subdomain (RFC
// 1123): lowercase ASCII + digits + '-' + '.', start and end with
// alphanumeric. This excludes '/', '..', leading '.', non-printable
// chars, and control sequences — the T-02-06-01 mitigation surface.
// 63-char limit also enforced by the regex (CRD-08 parity).
var dns1123SubdomainRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// dns1123MaxLen is the standard DNS-1123 subdomain length cap.
const dns1123MaxLen = 253

// parseClaudeCodeMarketplace unmarshals the upstream marketplace.json
// body and performs Stage-1 validation. Every error wraps
// sources.ErrUpstreamInvalid so the caller's classifyFetchError maps to
// ReasonUpstreamInvalid uniformly.
//
// Per-plugin validation rules:
//
//  1. discriminator: source.type MUST be one of the closed enum
//     {"github","gitlab","bitbucket","s3","gcs","http","npm"}; unknown
//     values → ErrUpstreamInvalid with the plugin name + bad type.
//  2. subobject presence: for github/gitlab/bitbucket/s3/gcs/http, the
//     matching subobject pointer MUST be non-nil. (npm has no subobject
//     in our parser type — see comment above.)
//  3. plugin.Name MUST match DNS-1123 subdomain rules — T-02-06-01
//     adversarial-name mitigation. This also bounds the per-entry name
//     length to 253 chars (computeFinalPath safety + status.message
//     T-02-06-08 mitigation).
//
// An empty plugins[] array is treated as ErrUpstreamInvalid — a marketplace
// without plugins is not legitimate steady-state for our purposes.
func parseClaudeCodeMarketplace(body []byte) (*ClaudeCodeMarketplace, error) {
	var mkt ClaudeCodeMarketplace
	if err := json.Unmarshal(body, &mkt); err != nil {
		// Don't echo the upstream body in the wrapped error (T-02-06-03);
		// json.Unmarshal's own err string is short and bounded.
		return nil, fmt.Errorf("marketplace.json: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	if len(mkt.Plugins) == 0 {
		return nil, fmt.Errorf("marketplace.json: zero plugins declared: %w", sources.ErrUpstreamInvalid)
	}
	for i := range mkt.Plugins {
		p := &mkt.Plugins[i]
		if len(p.Name) == 0 || len(p.Name) > dns1123MaxLen || !dns1123SubdomainRe.MatchString(p.Name) {
			return nil, fmt.Errorf("marketplace.json: plugin[%d].name %q: not a DNS-1123 subdomain: %w", i, p.Name, sources.ErrUpstreamInvalid)
		}
		switch p.Source.Type {
		case "github":
			if p.Source.GitHub == nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: source.type=github but source.github missing: %w", p.Name, sources.ErrUpstreamInvalid)
			}
		case "gitlab":
			if p.Source.GitLab == nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: source.type=gitlab but source.gitlab missing: %w", p.Name, sources.ErrUpstreamInvalid)
			}
		case "bitbucket":
			if p.Source.Bitbucket == nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: source.type=bitbucket but source.bitbucket missing: %w", p.Name, sources.ErrUpstreamInvalid)
			}
		case "s3":
			if p.Source.S3 == nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: source.type=s3 but source.s3 missing: %w", p.Name, sources.ErrUpstreamInvalid)
			}
		case "gcs":
			if p.Source.GCS == nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: source.type=gcs but source.gcs missing: %w", p.Name, sources.ErrUpstreamInvalid)
			}
		case "http":
			if p.Source.HTTP == nil {
				return nil, fmt.Errorf("marketplace.json: plugin %q: source.type=http but source.http missing: %w", p.Name, sources.ErrUpstreamInvalid)
			}
		case "npm":
			// Kept; Stage-2 flips per-entry to UnsupportedPluginSource via
			// marketplacePluginToSourceSpec returning errUnsupportedPluginSource.
		default:
			return nil, fmt.Errorf("marketplace.json: plugin %q: unknown source.type=%q: %w", p.Name, p.Source.Type, sources.ErrUpstreamInvalid)
		}
	}
	return &mkt, nil
}

// marketplacePluginToSourceSpec converts one parsed plugin entry into a
// sources.SourceSpec the Stage-2 fetcher dispatch can consume. For
// source.type == "npm" returns (zero-SourceSpec, errUnsupportedPluginSource)
// — the caller MUST check errors.Is(err, errUnsupportedPluginSource)
// BEFORE classifyFetchError so the result maps to
// ReasonUnsupportedPluginSource (marketplace-only reason).
//
// All other types are mechanically converted; parseClaudeCodeMarketplace
// has already enforced that the matching subobject pointer is non-nil so
// the registry.For dispatcher won't nil-dereference.
func marketplacePluginToSourceSpec(p ClaudeCodeMarketplacePlugin) (sources.SourceSpec, error) {
	switch p.Source.Type {
	case "github":
		return sources.SourceSpec{Type: "github", GitHub: p.Source.GitHub}, nil
	case "gitlab":
		return sources.SourceSpec{Type: "gitlab", GitLab: p.Source.GitLab}, nil
	case "bitbucket":
		return sources.SourceSpec{Type: "bitbucket", Bitbucket: p.Source.Bitbucket}, nil
	case "s3":
		return sources.SourceSpec{Type: "s3", S3: p.Source.S3}, nil
	case "gcs":
		return sources.SourceSpec{Type: "gcs", GCS: p.Source.GCS}, nil
	case "http":
		return sources.SourceSpec{Type: "http", HTTP: p.Source.HTTP}, nil
	case "npm":
		return sources.SourceSpec{}, errUnsupportedPluginSource
	default:
		// Unreachable in practice: parseClaudeCodeMarketplace already
		// rejected unknown types. Defensive return so a future bypass of
		// the parser surfaces as a typed error instead of a panic.
		return sources.SourceSpec{}, fmt.Errorf("plugin %q: unknown source.type=%q: %w", p.Name, p.Source.Type, sources.ErrUpstreamInvalid)
	}
}

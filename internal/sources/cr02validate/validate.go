// SPDX-License-Identifier: Apache-2.0

// Package cr02validate provides URL-metacharacter rejection helpers
// for git source CR fields (CR-02 mitigation). Used by the three
// per-provider source fetchers (github/gitlab/bitbucket) to ensure
// user-supplied spec.{Workspace,Repo,Project,Host,Ref} cannot smuggle
// query strings, fragments, path traversals, or whitespace into the
// constructed clone URL OR into the git subprocess argv.
//
// The bitbucket fetcher introduced these helpers inline in v1alpha1;
// extracting them here ensures github + gitlab apply identical rules
// (PR #9 follow-up review finding #1).
package cr02validate

import (
	"fmt"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// FlatIdentifier rejects URL-structural metacharacters in a flat
// identifier (workspace / repo / project namespace segment). Forbidden:
//
//	/  ?  #  \  space  tab  CR  LF
//
// Returns wrapped sources.ErrUpstreamInvalid on failure. The `field`
// argument names the offending CR field for the error message
// (operator-readable).
func FlatIdentifier(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty: %w", field, sources.ErrUpstreamInvalid)
	}
	if strings.ContainsAny(value, "/?#\\ \t\r\n") {
		return fmt.Errorf("%s %q contains forbidden URL metacharacter: %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	return nil
}

// RefIdentifier permits '/' (feature/branch shapes are legal git refs)
// but otherwise rejects the same metacharacter set as FlatIdentifier.
// Forbidden:
//
//	?  #  \  space  tab  CR  LF
func RefIdentifier(value string) error {
	if value == "" {
		return fmt.Errorf("ref must not be empty: %w", sources.ErrUpstreamInvalid)
	}
	if strings.ContainsAny(value, "?#\\ \t\r\n") {
		return fmt.Errorf("ref %q contains forbidden URL metacharacter: %w",
			value, sources.ErrUpstreamInvalid)
	}
	return nil
}

// RepoSlashIdentifier validates a `<owner>/<name>`-style two-segment
// identifier (e.g. github spec.Repo, gitlab spec.Project). Splits on
// '/', then runs FlatIdentifier-equivalent rejection on each segment.
// The split itself permits exactly ONE '/' separator when
// allowMultiSegment=false (github repos); allowMultiSegment=true
// permits deeper namespaces (gitlab projects).
func RepoSlashIdentifier(field, value string, allowMultiSegment bool) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty: %w", field, sources.ErrUpstreamInvalid)
	}
	// Disallow the metachars FlatIdentifier would on the whole string
	// EXCEPT '/'.
	if strings.ContainsAny(value, "?#\\ \t\r\n") {
		return fmt.Errorf("%s %q contains forbidden URL metacharacter: %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	parts := strings.Split(value, "/")
	if !allowMultiSegment && len(parts) != 2 {
		return fmt.Errorf("%s %q must be exactly <segment>/<segment>: %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	if allowMultiSegment && len(parts) < 2 {
		return fmt.Errorf("%s %q must contain at least one '/': %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	for _, seg := range parts {
		if seg == "" {
			return fmt.Errorf("%s %q has empty segment: %w",
				field, value, sources.ErrUpstreamInvalid)
		}
	}
	return nil
}

// HostIdentifier validates a hostname-like spec.Host (gitlab only).
// Accepts `gitlab.com`, `gitlab.example.com`, optionally with port
// `gitlab.example.com:8080`. Rejects scheme prefixes (those are
// stripped at construction time), paths, queries, fragments,
// whitespace. Empty is permitted (caller substitutes the per-provider
// default).
func HostIdentifier(value string) error {
	if value == "" {
		// Empty host means "use the provider default" — allowed.
		return nil
	}
	if strings.ContainsAny(value, "/?#\\ \t\r\n") {
		return fmt.Errorf("host %q contains forbidden URL metacharacter: %w",
			value, sources.ErrUpstreamInvalid)
	}
	return nil
}

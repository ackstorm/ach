// SPDX-License-Identifier: Apache-2.0

// Package manager resolves a name@repo (+lens) to a staged plugin/skill
// tree and projects it through an adapter into planned writes. It is the
// core of the ach-cli local install engine (Task 2 of Phase 2.2).
//
// No disk writes happen here — the caller (Task 3) commits the
// PlannedWrite list produced by Project.
package manager

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ackstorm/ach/internal/contentkit"
	"github.com/ackstorm/ach/internal/gitfetch"
)

// defaultRef returns "main" when ref is empty — mirrors the operator's
// marketplace_dispatch.go defaultRef helper.
func defaultRef(ref string) string {
	if ref == "" {
		return "main"
	}
	return ref
}

// BuildEntrySpec maps a marketplace plugin entry's source to a
// gitfetch.Spec. marketplaceCloneURL and marketplaceRef identify the
// marketplace's OWN repo (used for local-path entries). token+scheme are
// the registered repo's creds — attached only to entries on the
// marketplace's own host (see tokenFor); a foreign host gets neither the
// token nor its AuthScheme.
//
// Mapping is a k8s-free reimplementation of the operator's
// marketplace_dispatch.go buildGitSpecForEntry: the same four Kinds map
// to the same gitfetch.Spec fields, minus the k8s corev1.Secret / CRD
// references.
func BuildEntrySpec(
	src contentkit.ClaudeCodeMarketplaceSource,
	marketplaceCloneURL, marketplaceRef, token string,
	scheme gitfetch.AuthScheme,
) (gitfetch.Spec, error) {
	spec, err := buildEntrySpec(src, marketplaceCloneURL, marketplaceRef)
	if err != nil {
		return spec, err
	}
	// local-path's URL IS the marketplace URL, so it always keeps the token.
	if spec.Token = tokenFor(spec.URL, marketplaceCloneURL, token); spec.Token != "" {
		spec.AuthScheme = scheme
	}
	return spec, nil
}

// tokenFor returns the marketplace token only for an https entry on the
// marketplace's own host AND port (default 443) — never a PAT to a host,
// port or cleartext scheme a marketplace.json names (mirrors the operator's
// tokenForHost).
func tokenFor(entryURL, marketplaceCloneURL, token string) string {
	e, err1 := url.Parse(entryURL)
	m, err2 := url.Parse(marketplaceCloneURL)
	if err1 != nil || err2 != nil || e.Scheme != "https" || e.Hostname() == "" ||
		!strings.EqualFold(e.Hostname(), m.Hostname()) || portOrDefault(e) != portOrDefault(m) {
		return ""
	}
	return token
}

func portOrDefault(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return "443"
}

func buildEntrySpec(src contentkit.ClaudeCodeMarketplaceSource, marketplaceCloneURL, marketplaceRef string) (gitfetch.Spec, error) {
	switch src.Kind {
	case "git-subdir", "url":
		// git-subdir and url are structurally identical in the CLI (both
		// carry URL+Path and behave as subtree fetches). The url+path
		// collapse mirrors the operator's buildGitSpecForEntry comment:
		// "when path is non-empty the entry behaves like git-subdir".
		return gitfetch.Spec{
			URL:     src.URL,
			Ref:     defaultRef(src.Ref),
			SHA:     src.SHA,
			Subtree: src.Path,
		}, nil

	case "github":
		return gitfetch.Spec{
			URL:     "https://github.com/" + src.Repo + ".git",
			Ref:     defaultRef(src.Ref),
			SHA:     src.SHA,
			Subtree: "", // github Kind always fetches the whole worktree
		}, nil

	case "local-path":
		return gitfetch.Spec{
			URL:     marketplaceCloneURL,
			Ref:     marketplaceRef,
			SHA:     "", // resolved by Resolve via LsRemote
			Subtree: src.Path,
		}, nil

	case "":
		return gitfetch.Spec{}, fmt.Errorf("unsupported plugin source kind %q", src.Kind)

	default:
		return gitfetch.Spec{}, fmt.Errorf("unsupported plugin source kind %q", src.Kind)
	}
}

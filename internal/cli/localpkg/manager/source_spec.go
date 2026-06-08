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
// the registered repo's creds — reused verbatim for every entry in this
// repo regardless of the entry's target host (the CLI registers one repo
// at a time; per-host token scoping is a future enhancement).
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
	switch src.Kind {
	case "git-subdir", "url":
		// git-subdir and url are structurally identical in the CLI (both
		// carry URL+Path and behave as subtree fetches). The url+path
		// collapse mirrors the operator's buildGitSpecForEntry comment:
		// "when path is non-empty the entry behaves like git-subdir".
		return gitfetch.Spec{
			URL:        src.URL,
			Ref:        defaultRef(src.Ref),
			SHA:        src.SHA,
			Subtree:    src.Path,
			Token:      token,
			AuthScheme: scheme,
		}, nil

	case "github":
		return gitfetch.Spec{
			URL:        "https://github.com/" + src.Repo + ".git",
			Ref:        defaultRef(src.Ref),
			SHA:        src.SHA,
			Subtree:    "", // github Kind always fetches the whole worktree
			Token:      token,
			AuthScheme: scheme,
		}, nil

	case "local-path":
		return gitfetch.Spec{
			URL:        marketplaceCloneURL,
			Ref:        marketplaceRef,
			SHA:        "", // resolved by Resolve via LsRemote
			Subtree:    src.Path,
			Token:      token,
			AuthScheme: scheme,
		}, nil

	case "":
		return gitfetch.Spec{}, fmt.Errorf("unsupported plugin source kind %q", src.Kind)

	default:
		return gitfetch.Spec{}, fmt.Errorf("unsupported plugin source kind %q", src.Kind)
	}
}

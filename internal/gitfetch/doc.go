// SPDX-License-Identifier: Apache-2.0

// Package gitfetch is the generic git-remote fetcher (Hub §10.1 + TODO §5).
// It is the INNER-fetch counterpart to the six per-source-type
// subpackages (github/gitlab/bitbucket/s3/gcs/http) that handle the
// OUTER fetch of a marketplace catalog file.
//
// Unlike the github subpackage (which uses the GitHub REST API to fetch
// a repo tarball), this package shells out to `git clone --depth=1
// --branch=<ref> <url> <dst>` followed by `git fetch origin <sha>` to
// pin the worktree. This is the right tool for marketplace plugin
// entries because:
//
//   - Per-entry sources point at arbitrary git remotes (self-hosted
//     gitea, gitlab, GitHub, bitbucket — anything that speaks the git
//     protocol). The github SDK can't reach a gitea instance.
//   - Per-entry sources carry a pinned commit SHA. The plumbing
//     (`git fetch origin <sha>`) lets us short-circuit when the local
//     worktree is already at the pin.
//   - We tar a subtree (`path/`) for git-subdir entries — git is the
//     simplest way to materialize a real worktree to walk.
//
// This package is NOT registered with internal/sources/registry — the
// OUTER fetch dispatcher in registry.For is keyed by CRD-discriminator
// strings and stays unchanged. The PluginMarketplace reconciler calls
// gitfetch.Fetch directly from materializeMarketplacePlugin (Stage-2).
package gitfetch

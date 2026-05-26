// SPDX-License-Identifier: Apache-2.0

// Package cachefs implements the PVC cache-layout bootstrap for the Operator
// per Hub spec §10.3 and PRD decision D-13.
//
// Contract:
//
//   - EnsureLayout(root) creates the five OP-10 cache subdirectories
//     (prompt/, plugin/, marketplace/, artifact/, .tmp/) under root. The
//     call is idempotent — re-running on an already-initialized root is a
//     no-op.
//   - The four publish subdirs and .tmp/ live under the SAME root by
//     construction, so the §10.3 atomic-rename invariant (staging file
//     under .tmp/ and final published path on the same filesystem) holds
//     whenever the cluster operator mounts a single PVC at root.
//   - ErrCacheRootMissing is returned when root is the empty string, does
//     not exist on disk, or exists but is not a directory. Any other I/O
//     error (permission denied, ENOSPC, etc.) is returned verbatim from
//     os.MkdirAll so the caller (the operator main) can surface a
//     structured startup error per D-13.
//
// This package does NOT read env vars; the caller passes root (resolved from
// ACH_CACHE_ROOT, default /var/cache/ach) in. This package also does NOT
// verify that root is actually a PVC mount point — that is a Kubernetes
// concern. The Stat-on-root check is a sanity guard, not a mount probe.
//
// Stdlib-only. No logging — failures are surfaced via returned errors so
// the operator main can attach structured context (OP-10, OP-11, D-13).
package cachefs

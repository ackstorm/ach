// SPDX-License-Identifier: Apache-2.0

// Package extract owns the single safe-extraction surface the CLI hydrate
// engine consumes to materialize plugin and artifact `.tar.gz` archives
// onto disk. It is the most security-critical surface in Phase 7.
//
// Contract (CLI spec §6.4):
//
//   - Extract(ctx, gzReader, dst, kind, limits, allowSymlinks) decompresses
//     a gzipped tar archive into dst with a hand-rolled safety policy. The
//     gzip stream is wrapped via compress/gzip.NewReader around
//     archive/tar.NewReader so the archive is NEVER fully buffered (SAFE-06).
//   - File modes are masked to `mode & 0o0755`; setuid (`04000`), setgid
//     (`02000`), sticky (`01000`), group-write (`0020`), and world-write
//     (`0002`) bits are unconditionally stripped (SAFE-02). Directory
//     modes are forced to `0o0755` regardless of the recorded value.
//     Mtime/atime are NOT preserved.
//   - LoadLimits reads the three bomb-defense env vars at hydrate start:
//     ACH_MAX_EXTRACTED_PLUGIN_MIB (default 200), ACH_MAX_EXTRACTED_ARTIFACT_MIB
//     (default 500), ACH_MAX_ARCHIVE_ENTRIES (default 65536). Zero, negative,
//     or non-numeric values are rejected at hydrate start with a clear error
//     citing the offending variable — matching the operator-side
//     `ACH_PLUGIN_MAX_SIZE_MIB` discipline established in Phase 1 / OP-09.
//   - Per-entry byte counters fire BEFORE writing the offending entry's body
//     (CLAUDE.md "Decompression-bomb caps" failure mode + CONTEXT.md
//     `<specifics>` "Bomb defense ordering"). The implementation counts
//     uncompressed bytes per-entry AS the bytes stream — never after the
//     archive is materialized.
//
// SAFE-01 unconditional rejection set:
//
//   - Absolute paths (Header.Name starts with `/` or Windows drive letter)
//   - `..` segments / paths normalized outside dst
//   - Hardlinks (`tar.TypeLink`) — even if the link target appears in-tree
//   - Device files (`tar.TypeChar`, `tar.TypeBlock`)
//   - FIFOs (`tar.TypeFifo`)
//   - Sockets / any non-{Reg,Dir,Symlink} Typeflag
//   - Pax-extended-header path injections (`tar.TypeXHeader` /
//     `tar.TypeXGlobalHeader` whose embedded `path` fails the same
//     path-safety check)
//
// Symlinks are rejected by default; `allowSymlinks=true` admits ONLY
// in-tree-resolved targets (the resolved target MUST live inside dst).
// Out-of-tree symlinks remain rejected even with `allowSymlinks=true`.
//
// Discipline (mirrors internal/cachefs + internal/credhash):
//
//   - Stdlib only beyond the in-repo internal/cli/hash dependency
//     (D-11 — archive/tar + compress/gzip). No third-party tar/gzip
//     library is introduced.
//   - SPDX header on every source file; the pre-push gate enforces.
//   - No `log` or `log/slog` imports — errors surface as wrapped Go
//     errors that the hydrate orchestrator maps to `*exit.CodedError`.
//
// References:
//
//   - CLI spec §6.4 (tar safety table, mode discipline, bomb defense)
//   - PRD D-11 (stdlib archive/tar + compress/gzip)
//   - PRD D-12 (env-var bomb caps + defaults)
//   - SAFE-01 / SAFE-02 / SAFE-03 / SAFE-06
//   - CLAUDE.md "Common failure modes" → "Decompression-bomb caps"
package extract

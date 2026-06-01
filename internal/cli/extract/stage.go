// SPDX-License-Identifier: Apache-2.0

// Per-resource staging + atomic publication for the hydrate engine.
//
// StageAndPublish is the per-resource lifecycle the orchestrator drives:
//
//  1. Stage the freshly-downloaded body in a private per-call
//     <achDir>/tmp/<rand>/ directory so a mid-extraction crash leaves no
//     half-extracted output at the final location (SAFE-05 + spec §6.4).
//  2. Spill the body to a `source.bin` file in the staging dir as it
//     streams (SAFE-06: never buffer the whole archive in memory).
//     During the spill we tee the bytes through BOTH a streaming sha256
//     accumulator (for the D-15 disk-write short-circuit comparison) and
//     a streaming xxh3 accumulator (for the PublishResult.SourceHash
//     return value per D-14).
//  3. D-15 / STATE-11 disk-write short-circuit — if a file already
//     exists at finalRelPath, compute its sha256 and compare to the
//     freshly-downloaded sha256. On equality, SKIP the rename (the
//     existing file is byte-equal already) and return Skipped=true.
//     Compute PublishResult.Hash by re-hashing the existing file with
//     xxh3 (so the state ledger's hash field is xxh3 per STATE-02; the
//     sha256 is transient and is never persisted per D-14).
//  4. Otherwise, dispatch on Content-Type:
//     - application/gzip (or filename ending .tar.gz) → tar.Extract via
//       stdlib archive/tar + compress/gzip per D-11, into an
//       `extracted/` subdir of the staging dir, with D-12 byte caps
//       flowing through the Limits parameter.
//     - anything else → verbatim file write: rename source.bin to
//       extracted/<basename>.
//  5. Atomic publication — os.Rename(extracted/, finalRelPath) — a
//     single rename(2) call so partial output is unobservable. For
//     verbatim writes the rename moves a single file; for tar
//     extractions it moves the entire extracted dir.
//  6. The staging dir is removed on success and on failure via a
//     deferred os.RemoveAll.
//
// Citations:
//   - D-11 — stdlib archive/tar + compress/gzip only via tar.Extract.
//   - D-12 — Limits carries env-var-derived byte + entry caps; passed
//     through to tar.Extract.
//   - D-14 — dual-hash discipline: PublishResult exposes xxh3 Hash +
//     xxh3 SourceHash for state.FileEntry. sha256 is TRANSIENT — used
//     ONLY for the disk-write short-circuit comparison; never persisted.
//   - D-15 — fetch is unconditional (lives in FetchContent); the
//     disk-write short-circuit lives here.
//   - SAFE-05 — per-resource atomic publication via single rename(2).
//   - SAFE-06 — streaming spill (no in-memory buffering of plugin
//     archives) — source.bin is on disk under the staging dir.
//
// References:
//   - CLI spec §6.4 — per-resource atomic publication contract.
//   - 07-PATTERNS.md — "internal/cli/extract/stage.go" atomic-rename
//     pattern.
//   - internal/cachefs/sweep.go — analog atomic-publication-via-rename.

package extract

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/ach/internal/cli/hash"
)

// PublishResult is the per-resource lifecycle outcome.
//
// Hash + SourceHash are both canonical xxh3 strings ("xxh3:<32hex>")
// per D-14. The sha256 used for the D-15 short-circuit is transient
// and is NOT carried on PublishResult (it would be misleading — state
// stores xxh3 only per STATE-02).
//
// WrittenFiles lists every regular file that landed at the final
// location (in the same order tar.Extract emitted them). For a
// verbatim write WrittenFiles has exactly one entry.
//
// Skipped=true means the D-15 disk-write short-circuit fired — the
// freshly-downloaded bytes were byte-equal to the existing on-disk
// file, so no rename was performed. The existing file is left
// untouched (mtime unchanged) and Hash is the xxh3 of the existing
// file's bytes.
type PublishResult struct {
	// FinalPath is the absolute on-disk path of the resource after
	// publication. For tar extractions, this is the extracted
	// directory; for verbatim writes, this is the single file.
	FinalPath string

	// Hash is the xxh3 of the on-disk bytes (D-14). For a verbatim
	// write this equals SourceHash (pass-through). For a tar extract
	// each WrittenFiles[i].Hash carries its per-file xxh3; this
	// field is the SourceHash for the unwrapped-archive use case.
	Hash string

	// SourceHash is the xxh3 of the upstream source bytes BEFORE any
	// transformation (D-14). For pass-through files (no adapter
	// dispatch) Hash == SourceHash. For an archive, SourceHash is the
	// xxh3 of the .tar.gz body the Content Service emitted.
	SourceHash string

	// Skipped is true when the D-15 disk-write short-circuit fired
	// (sha256(fresh) == sha256(existing-at-finalRelPath)). When true,
	// no rename was performed.
	Skipped bool

	// WrittenFiles lists each regular file the publication produced.
	// Empty for Skipped=true. Length == 1 for verbatim writes.
	WrittenFiles []FileWrite
}

// StagingDir mints a unique per-resource staging directory under
// <achDir>/tmp/<rand>/ and returns its absolute path. The parent
// <achDir>/tmp/ is created with 0o755 if absent.
//
// The random suffix is 16 hex characters drawn from crypto/rand (8
// random bytes hex-encoded), so collisions across concurrent hydrate
// calls are statistically impossible. The staging dir itself is
// created with 0o755 — operator-readable but only owner-writable.
//
// The W2-02 contract is that the caller deletes the staging dir on
// success AND on failure; StagingDir itself does not arrange for
// cleanup. StageAndPublish does this via a deferred os.RemoveAll.
func StagingDir(achDir string) (string, error) {
	parent := filepath.Join(achDir, "tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("extract: ensure tmp parent %s: %w", parent, err)
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("extract: random staging name: %w", err)
	}
	name := hex.EncodeToString(raw[:])
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("extract: mkdir staging %s: %w", dir, err)
	}
	return dir, nil
}

// gzipContentType is the canonical Content-Type the Content Service
// returns for plugin and artifact tar.gz bodies. We accept any
// Content-Type that begins with this string so a charset parameter
// (rare for binary but legal) does not bypass the gzip dispatch.
const gzipContentType = "application/gzip"

// StageAndPublish is the per-resource extract pipeline.
//
// Flow:
//
//  1. Mint staging dir; arrange for full cleanup via defer.
//  2. Spill body to stageDir/source.bin while teeing through sha256
//     + xxh3 streaming accumulators (SAFE-06: streaming spill, NOT
//     in-memory buffer).
//  3. D-15 short-circuit: if finalRelPath exists, compute its sha256
//     and compare to the just-computed source sha256; on equality,
//     re-hash the existing file with xxh3 (NEVER store sha256 per
//     D-14), populate PublishResult with Skipped=true, and return.
//  4. Decide gzip vs verbatim:
//     - contentType has prefix "application/gzip" OR finalRelPath
//     ends in ".tar.gz" → call tar.Extract on stageDir/source.bin,
//     writing to stageDir/extracted/ (D-11 stdlib path; D-12 caps
//     enforced via limits).
//     - else → mkdir stageDir/extracted/; rename source.bin into
//     stageDir/extracted/<basename of finalRelPath>; synthesize
//     WrittenFiles with hash.HashBytes/Hash computed on the file.
//  5. Atomic rename stageDir/extracted/ → finalRelPath. The parent
//     directory of finalRelPath is created with 0o755 if absent.
//  6. PublishResult.Hash for the verbatim case is the xxh3 of the
//     single file (same as WrittenFiles[0].Hash). For tar, Hash is
//     the xxh3 of the source archive (== SourceHash) — per-file
//     hashes live on WrittenFiles[i].Hash already.
//
// Parameters mirror the orchestrator's per-resource view (07-W1-06
// step 8): kind drives the cap routing inside tar.Extract; limits
// flows through unchanged; allowSymlinks is the SAFE-01 in-tree-
// symlink opt-in.
func StageAndPublish(
	ctx context.Context,
	body io.Reader,
	contentType, finalRelPath, achDir string,
	kind ResourceKind,
	limits Limits,
	allowSymlinks bool,
) (*PublishResult, error) {
	// Step 1: staging dir + deferred cleanup. Even on Skipped=true the
	// staging dir is removed by the defer below — source.bin is
	// transient.
	stageDir, err := StagingDir(achDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	// Step 2: spill body to source.bin while teeing through sha256
	// (D-15 short-circuit) + xxh3 (D-14 SourceHash). SAFE-06 streaming
	// discipline — source.bin is on disk; the hash accumulators are
	// O(state). We re-read the file later for the per-file extracted
	// hashes; that keeps the W1-04 hash.Hash contract honest (Hash
	// takes an io.Reader; no Writer surface is exposed).
	sourceBin := filepath.Join(stageDir, "source.bin")
	srcSha256, err := spillAndHashSha256(body, sourceBin)
	if err != nil {
		return nil, err
	}
	// Re-read source.bin to compute the xxh3 SourceHash. The extra
	// read is cache-hot — same trade-off tar.go's writeRegular makes
	// for per-file hashes.
	srcXxh3, err := hash.HashFile(sourceBin)
	if err != nil {
		return nil, fmt.Errorf("extract: xxh3 source.bin: %w", err)
	}

	// Step 3: D-15 / STATE-11 disk-write short-circuit. We compute
	// the EXISTING finalRelPath's sha256 and compare with the freshly
	// downloaded one; on equality, we skip the rename and return the
	// existing file's xxh3 as Hash (per D-14 — state stores xxh3 only,
	// sha256 is transient).
	if existingSha256, ok, err := fileSha256IfExists(finalRelPath); err != nil {
		return nil, err
	} else if ok && subtle.ConstantTimeCompare(existingSha256, srcSha256) == 1 {
		existingXxh3, herr := hash.HashFile(finalRelPath)
		if herr != nil {
			return nil, fmt.Errorf("extract: xxh3 existing %s: %w", finalRelPath, herr)
		}
		return &PublishResult{
			FinalPath:  finalRelPath,
			Hash:       existingXxh3,
			SourceHash: srcXxh3,
			Skipped:    true,
		}, nil
	}

	// Step 4: decide gzip vs verbatim. Lower-case the contentType
	// prefix check so "Application/GZIP" (unlikely but legal) still
	// dispatches.
	extractedDir := filepath.Join(stageDir, "extracted")
	if isGzip(contentType, finalRelPath) {
		return publishGzip(ctx, sourceBin, extractedDir, finalRelPath,
			kind, limits, allowSymlinks, srcXxh3)
	}
	return publishVerbatim(sourceBin, extractedDir, finalRelPath, srcXxh3)
}

// isGzip decides the dispatch. application/gzip OR a .tar.gz suffix
// on finalRelPath triggers extraction; everything else is treated as
// a verbatim opaque file (single-file prompt body, raw artifact).
func isGzip(contentType, finalRelPath string) bool {
	if strings.HasPrefix(strings.ToLower(contentType), gzipContentType) {
		return true
	}
	if strings.HasSuffix(strings.ToLower(finalRelPath), ".tar.gz") {
		return true
	}
	return false
}

// publishGzip runs tar.Extract (D-11 stdlib path) over the spilled
// source.bin into stageDir/extracted/, then atomically renames the
// extracted dir to the final location. The destination parent is
// created with 0o755 if absent; the destination itself must NOT
// pre-exist (the caller deletes prior content via Skipped=false
// followed by a Phase-7 replace step in the orchestrator).
func publishGzip(
	ctx context.Context,
	sourceBin, extractedDir, finalRelPath string,
	kind ResourceKind,
	limits Limits,
	allowSymlinks bool,
	srcXxh3 string,
) (*PublishResult, error) {
	if err := os.Mkdir(extractedDir, 0o755); err != nil {
		return nil, fmt.Errorf("extract: mkdir extracted %s: %w", extractedDir, err)
	}
	src, err := os.Open(sourceBin)
	if err != nil {
		return nil, fmt.Errorf("extract: open source.bin for gzip: %w", err)
	}
	defer func() { _ = src.Close() }()

	res, err := Extract(ctx, src, extractedDir, kind, limits, allowSymlinks)
	if err != nil {
		// Partial output stays inside stageDir/extracted/ — the
		// deferred RemoveAll(stageDir) cleans it up so nothing lands
		// at finalRelPath. SAFE-03 ordering for the bomb-cap path is
		// preserved (tar.Extract already removed the in-flight file).
		return nil, err
	}

	if err := renameAtomic(extractedDir, finalRelPath); err != nil {
		return nil, err
	}

	// Adjust each WrittenFile.RelPath/FinalPath bookkeeping: the
	// per-file RelPath is workspace-relative to finalRelPath, which
	// matches what state.FileEntry.Target expects.
	return &PublishResult{
		FinalPath:    finalRelPath,
		Hash:         srcXxh3, // archive-level hash == source xxh3
		SourceHash:   srcXxh3,
		WrittenFiles: res.WrittenFiles,
	}, nil
}

// publishVerbatim writes a single opaque file: rename source.bin into
// stageDir/extracted/<basename>, then atomic-rename the extracted dir
// to the final location.
//
// For the pass-through case (no adapter, no extraction) Hash ==
// SourceHash — the on-disk bytes are literally the upstream bytes.
func publishVerbatim(
	sourceBin, extractedDir, finalRelPath, srcXxh3 string,
) (*PublishResult, error) {
	if err := os.Mkdir(extractedDir, 0o755); err != nil {
		return nil, fmt.Errorf("extract: mkdir extracted %s: %w", extractedDir, err)
	}
	basename := filepath.Base(finalRelPath)
	stagedFinal := filepath.Join(extractedDir, basename)
	if err := os.Rename(sourceBin, stagedFinal); err != nil {
		return nil, fmt.Errorf("extract: rename source.bin -> %s: %w", stagedFinal, err)
	}

	if err := renameAtomic(stagedFinal, finalRelPath); err != nil {
		return nil, err
	}

	// Per FileWrite contract — RelPath is relative to the publish
	// root. For the verbatim case the publish root IS the file
	// itself, so RelPath is "." (or empty). We pick the basename to
	// keep the field round-trippable with target= filenames.
	fw := FileWrite{
		RelPath: basename,
		Hash:    srcXxh3,
		Mode:    0o644,
	}
	return &PublishResult{
		FinalPath:    finalRelPath,
		Hash:         srcXxh3,
		SourceHash:   srcXxh3,
		WrittenFiles: []FileWrite{fw},
	}, nil
}

// renameAtomic publishes src at dst with a single os.Rename. Ensures
// the parent dir exists; rejects via the underlying os.Rename error
// when dst already exists in a way that the rename cannot overwrite
// (rename-onto-an-existing-non-empty-dir for the tar case). Per
// SAFE-05 the operation MUST be a single rename(2) call — no copy
// fallback, no per-file rename loop.
func renameAtomic(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("extract: mkdir publish parent %s: %w",
			filepath.Dir(dst), err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("extract: atomic rename %s -> %s: %w", src, dst, err)
	}
	return nil
}

// spillAndHashSha256 reads body to sourcePath while teeing through a
// streaming sha256.Hash. Returns the 32-byte sha256 sum. SAFE-06: the
// body is NOT buffered in memory — bytes stream to disk as they come.
func spillAndHashSha256(body io.Reader, sourcePath string) ([]byte, error) {
	out, err := os.OpenFile(sourcePath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("extract: create source.bin: %w", err)
	}
	h := sha256.New()
	mw := io.MultiWriter(out, h)
	if _, copyErr := io.Copy(mw, body); copyErr != nil {
		_ = out.Close()
		return nil, fmt.Errorf("extract: spill body: %w", copyErr)
	}
	if err := out.Close(); err != nil {
		return nil, fmt.Errorf("extract: close source.bin: %w", err)
	}
	return h.Sum(nil), nil
}

// fileSha256IfExists computes the sha256 of the file at path if it
// exists, returning (sum, true, nil). When the file is absent returns
// (nil, false, nil). Other I/O errors surface as (nil, false, err).
//
// D-15 cite: the sha256 here is used ONLY for the disk-write short-
// circuit comparison; the value is NEVER persisted to state.json.
// The state ledger stores xxh3 exclusively per STATE-02 + D-14.
func fileSha256IfExists(path string) ([]byte, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("extract: stat existing for sha256: %w", err)
	}
	// A directory target (a previously-extracted plugin) has no single-file
	// sha256 to short-circuit against. Report "no short-circuit" rather than
	// erroring — the re-hydrate decision (no-op skip vs delete-before-replace)
	// is owned by the orchestrator (W6-01 Bug E), not this leaf helper.
	if info.IsDir() {
		return nil, false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("extract: open existing for sha256: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, copyErr := io.Copy(h, f); copyErr != nil {
		return nil, false, fmt.Errorf("extract: read existing for sha256: %w", copyErr)
	}
	return h.Sum(nil), true, nil
}

// SpillAndHashXxh3 streams body to a fresh staging file under <achDir>/tmp/
// and returns that file's path plus the canonical "xxh3:<32hex>" digest of
// the bytes (the same format StageAndPublish records as PublishResult.SourceHash).
//
// The caller owns deletion of the staging DIRECTORY (filepath.Dir(path)).
//
// W6-01 Bug E: the hydrate orchestrator uses this to compute the upstream
// SourceHash BEFORE StageAndPublish, so the re-hydrate no-op-skip decision
// lives at the orchestrator layer (which holds prior state) rather than
// inside StageAndPublish. The returned file can then be re-opened and fed to
// StageAndPublish for the non-skip path.
func SpillAndHashXxh3(achDir string, body io.Reader) (path, xxh3 string, err error) {
	dir, err := StagingDir(achDir)
	if err != nil {
		return "", "", err
	}
	p := filepath.Join(dir, "source.bin")
	if _, err := spillAndHashSha256(body, p); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	sum, err := hash.HashFile(p)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("extract: xxh3 staged source: %w", err)
	}
	return p, sum, nil
}

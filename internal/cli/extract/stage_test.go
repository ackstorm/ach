// SPDX-License-Identifier: Apache-2.0

package extract_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/extract"
)

// stageLimits returns Limits generous enough that bomb-cap behavior
// does not interfere with the staging-layer tests (those are covered
// by tar_test.go).
func stageLimits() extract.Limits {
	const mib = int64(1024 * 1024)
	return extract.Limits{
		MaxExtractedPluginBytes:   200 * mib,
		MaxExtractedArtifactBytes: 500 * mib,
		MaxEntries:                65536,
	}
}

// TestStagingDir_ReturnsUnderTmp asserts the returned path lives at
// <achDir>/tmp/<hex>/ with the 16-hex-char suffix the contract
// promises (crypto/rand → 8 bytes → 16 hex chars).
func TestStagingDir_ReturnsUnderTmp(t *testing.T) {
	achDir := t.TempDir()
	dir, err := extract.StagingDir(achDir)
	if err != nil {
		t.Fatalf("StagingDir: %v", err)
	}
	if !strings.HasPrefix(dir, filepath.Join(achDir, "tmp")+string(os.PathSeparator)) {
		t.Errorf("StagingDir = %q, want under %s/tmp/", dir, achDir)
	}
	suffix := filepath.Base(dir)
	if len(suffix) != 16 {
		t.Errorf("staging suffix %q len=%d, want 16", suffix, len(suffix))
	}
	for _, r := range suffix {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			t.Errorf("staging suffix %q contains non-hex %q", suffix, r)
			break
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat staging dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("staging path is not a dir: %v", info.Mode())
	}
}

// TestStagingDir_CreatesParentTmp asserts the parent <achDir>/tmp/
// is created on demand (first hydrate of a fresh achDir).
func TestStagingDir_CreatesParentTmp(t *testing.T) {
	achDir := t.TempDir()
	// Confirm parent absent at start.
	if _, err := os.Stat(filepath.Join(achDir, "tmp")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("precondition: tmp parent already exists: %v", err)
	}
	if _, err := extract.StagingDir(achDir); err != nil {
		t.Fatalf("StagingDir: %v", err)
	}
	info, err := os.Stat(filepath.Join(achDir, "tmp"))
	if err != nil {
		t.Fatalf("Stat tmp parent: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("tmp parent not a dir")
	}
}

// TestStagingDir_DistinctAcrossCalls asserts crypto/rand suffix
// distinctness — two back-to-back calls return different dirs.
func TestStagingDir_DistinctAcrossCalls(t *testing.T) {
	achDir := t.TempDir()
	a, err := extract.StagingDir(achDir)
	if err != nil {
		t.Fatalf("StagingDir A: %v", err)
	}
	b, err := extract.StagingDir(achDir)
	if err != nil {
		t.Fatalf("StagingDir B: %v", err)
	}
	if a == b {
		t.Errorf("StagingDir returned identical paths %q across calls", a)
	}
}

// TestStageAndPublish_VerbatimWrite_HappyPath asserts the verbatim
// path: non-gzip Content-Type → single-file publish at finalRelPath
// with bytes byte-equal to the upstream body and Hash populated.
func TestStageAndPublish_VerbatimWrite_HappyPath(t *testing.T) {
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "prompt.txt")
	body := []byte("the upstream prompt body")

	res, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"text/plain",
		finalPath,
		achDir,
		extract.KindPrompt,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	if res == nil {
		t.Fatalf("PublishResult is nil")
	}
	if res.Skipped {
		t.Errorf("Skipped = true; want false (fresh finalPath)")
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("bytes mismatch: got %q want %q", got, body)
	}
	if !strings.HasPrefix(res.Hash, "xxh3:") {
		t.Errorf("Hash = %q, want xxh3: prefix", res.Hash)
	}
	if !strings.HasPrefix(res.SourceHash, "xxh3:") {
		t.Errorf("SourceHash = %q, want xxh3: prefix", res.SourceHash)
	}
	if len(res.WrittenFiles) != 1 {
		t.Errorf("WrittenFiles len = %d, want 1", len(res.WrittenFiles))
	}
}

// TestStageAndPublish_GzipExtract_DispatchToExtract asserts the
// gzip dispatch: application/gzip body → tar.Extract path (D-11
// stdlib archive/tar + compress/gzip) → extracted dir at finalRelPath
// containing the per-file hashes via WrittenFiles.
func TestStageAndPublish_GzipExtract_DispatchToExtract(t *testing.T) {
	body := buildBenignTarGz(t, []tarFile{
		{name: "a.txt", body: []byte("alpha")},
		{name: "b.txt", body: []byte("beta")},
	})
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "demo-plugin")

	res, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"application/gzip",
		finalPath,
		achDir,
		extract.KindPlugin,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	// finalPath should exist as a directory containing a.txt + b.txt.
	info, err := os.Stat(finalPath)
	if err != nil {
		t.Fatalf("Stat finalPath: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("finalPath %s is not a directory after tar extract", finalPath)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		_, err := os.Stat(filepath.Join(finalPath, name))
		if err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
	if len(res.WrittenFiles) != 2 {
		t.Errorf("WrittenFiles len = %d, want 2", len(res.WrittenFiles))
	}
	for _, fw := range res.WrittenFiles {
		if !strings.HasPrefix(fw.Hash, "xxh3:") {
			t.Errorf("FileWrite.Hash %q lacks xxh3: prefix", fw.Hash)
		}
	}
	if !strings.HasPrefix(res.SourceHash, "xxh3:") {
		t.Errorf("SourceHash = %q, want xxh3: prefix", res.SourceHash)
	}
}

// TestStageAndPublish_DotTarGzSuffix_DispatchToExtract asserts the
// secondary gzip-dispatch path: empty Content-Type but a .tar.gz
// suffix on finalRelPath also routes to tar.Extract.
func TestStageAndPublish_DotTarGzSuffix_DispatchToExtract(t *testing.T) {
	body := buildBenignTarGz(t, []tarFile{
		{name: "x.txt", body: []byte("y")},
	})
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "demo.tar.gz")

	res, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"", // no Content-Type — suffix triggers extract
		finalPath,
		achDir,
		extract.KindArtifact,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(finalPath, "x.txt")); err != nil {
		t.Errorf("extracted x.txt missing: %v", err)
	}
	if len(res.WrittenFiles) != 1 {
		t.Errorf("WrittenFiles len = %d, want 1", len(res.WrittenFiles))
	}
}

// TestStageAndPublish_Sha256ShortCircuit_SkipsWrite asserts D-15:
// pre-seed the final file with known content; run StageAndPublish
// with identical bytes; assert Skipped == true AND the existing
// file's mtime is unchanged.
func TestStageAndPublish_Sha256ShortCircuit_SkipsWrite(t *testing.T) {
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "stable.txt")
	body := []byte("identical bytes both sides")

	if err := os.WriteFile(finalPath, body, 0o644); err != nil {
		t.Fatalf("seed final file: %v", err)
	}
	// Capture pre-call mtime; the call MUST NOT touch the file.
	preInfo, err := os.Stat(finalPath)
	if err != nil {
		t.Fatalf("pre-Stat: %v", err)
	}
	preMtime := preInfo.ModTime()
	// Force a fresh mtime far enough back that a same-second write
	// would still register as a change. Without this the on-the-second
	// mtime equality might be a false positive.
	older := preMtime.Add(-2 * time.Hour)
	if err := os.Chtimes(finalPath, older, older); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	preInfo, _ = os.Stat(finalPath)
	preMtime = preInfo.ModTime()

	res, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"text/plain",
		finalPath,
		achDir,
		extract.KindPrompt,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	if !res.Skipped {
		t.Errorf("Skipped = false; want true (D-15 short-circuit)")
	}
	postInfo, err := os.Stat(finalPath)
	if err != nil {
		t.Fatalf("post-Stat: %v", err)
	}
	if !postInfo.ModTime().Equal(preMtime) {
		t.Errorf("mtime changed: pre=%v post=%v (D-15 should not touch existing file)",
			preMtime, postInfo.ModTime())
	}
	// Hash + SourceHash MUST still be populated (state.json v2 STATE-02).
	if !strings.HasPrefix(res.Hash, "xxh3:") {
		t.Errorf("Hash = %q, want xxh3: prefix even when Skipped", res.Hash)
	}
	if !strings.HasPrefix(res.SourceHash, "xxh3:") {
		t.Errorf("SourceHash = %q, want xxh3: prefix", res.SourceHash)
	}
}

// TestStageAndPublish_DualHash_BothPopulated asserts D-14: both
// PublishResult.Hash and SourceHash are populated xxh3 strings AND
// (for the pass-through verbatim case) they are equal.
func TestStageAndPublish_DualHash_BothPopulated(t *testing.T) {
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "f.txt")
	body := []byte("dual-hash discipline")

	res, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"text/plain",
		finalPath,
		achDir,
		extract.KindPrompt,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	if !strings.HasPrefix(res.Hash, "xxh3:") || len(res.Hash) != len("xxh3:")+32 {
		t.Errorf("Hash = %q, want canonical xxh3:<32hex>", res.Hash)
	}
	if !strings.HasPrefix(res.SourceHash, "xxh3:") || len(res.SourceHash) != len("xxh3:")+32 {
		t.Errorf("SourceHash = %q, want canonical xxh3:<32hex>", res.SourceHash)
	}
	if res.Hash != res.SourceHash {
		t.Errorf("verbatim pass-through: Hash %q != SourceHash %q (must be equal)",
			res.Hash, res.SourceHash)
	}
}

// TestStageAndPublish_StagingDirCleaned asserts the deferred
// RemoveAll fires on success — no <achDir>/tmp/<rand>/ leftover.
func TestStageAndPublish_StagingDirCleaned(t *testing.T) {
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "p.txt")
	body := []byte("cleanup-check")

	_, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"text/plain",
		finalPath,
		achDir,
		extract.KindPrompt,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	// <achDir>/tmp/ may exist (mkdir on demand persists it), but
	// must be empty after a successful publish.
	tmp := filepath.Join(achDir, "tmp")
	entries, err := os.ReadDir(tmp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return // also acceptable
		}
		t.Fatalf("ReadDir tmp: %v", err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("staging dir not cleaned: %v", names)
	}
}

// TestStageAndPublish_MidExtractFailure_LeavesNoPartialDir asserts
// that a tar.Extract failure (e.g. unsafe entry) leaves NO
// half-extracted directory at finalRelPath — the deferred
// RemoveAll(stageDir) handles the cleanup and the atomic rename is
// never reached.
func TestStageAndPublish_MidExtractFailure_LeavesNoPartialDir(t *testing.T) {
	// Build a tar.gz containing an absolute-path entry — SAFE-01
	// rejection will fire mid-extract.
	body := buildUnsafeTarGz(t)
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "malicious-plugin")

	_, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"application/gzip",
		finalPath,
		achDir,
		extract.KindPlugin,
		stageLimits(),
		false,
	)
	if err == nil {
		t.Fatalf("StageAndPublish on malicious archive: want error, got nil")
	}
	if !errors.Is(err, extract.ErrUnsafeTarEntry) {
		t.Fatalf("err = %v; want ErrUnsafeTarEntry", err)
	}
	// finalPath MUST NOT exist (no partial directory).
	if _, statErr := os.Stat(finalPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("finalPath %s exists after mid-extract failure: %v", finalPath, statErr)
	}
	// Staging dir is also gone.
	entries, _ := os.ReadDir(filepath.Join(achDir, "tmp"))
	if len(entries) != 0 {
		t.Errorf("staging dir not cleaned after failure: %v", entries)
	}
}

// TestStageAndPublish_AtomicRename_DistinctSourceHash asserts that
// SourceHash reflects the upstream BODY (the archive), not the
// per-file extracted contents. For the gzip dispatch the body hash
// will differ from any single extracted file's hash (in general),
// which is the D-14 invariant — SourceHash mirrors upstream
// transformation-free bytes.
func TestStageAndPublish_AtomicRename_DistinctSourceHash(t *testing.T) {
	body := buildBenignTarGz(t, []tarFile{
		{name: "only.txt", body: []byte("only entry")},
	})
	achDir := t.TempDir()
	finalDir := t.TempDir()
	finalPath := filepath.Join(finalDir, "demo")

	res, err := extract.StageAndPublish(
		context.Background(),
		bytes.NewReader(body),
		"application/gzip",
		finalPath,
		achDir,
		extract.KindPlugin,
		stageLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("StageAndPublish: %v", err)
	}
	if len(res.WrittenFiles) != 1 {
		t.Fatalf("WrittenFiles len = %d, want 1", len(res.WrittenFiles))
	}
	// SourceHash hashes the archive bytes; per-file Hash hashes the
	// extracted inner-file bytes. These MUST differ for any
	// non-trivial archive (gzip framing alone changes the hash).
	if res.SourceHash == res.WrittenFiles[0].Hash {
		t.Errorf("expected SourceHash (%q) != WrittenFiles[0].Hash (%q) for tar dispatch",
			res.SourceHash, res.WrittenFiles[0].Hash)
	}
}

// ----- helpers -----

type tarFile struct {
	name string
	body []byte
}

func buildBenignTarGz(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     f.name,
			Mode:     0o644,
			Size:     int64(len(f.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

// buildUnsafeTarGz builds a tar.gz whose first entry has an absolute
// path. tar.Extract will return ErrUnsafeTarEntry on the first call
// to its inner loop.
func buildUnsafeTarGz(t *testing.T) []byte {
	t.Helper()
	body := []byte("malicious")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "/etc/passwd",
		Mode:     0o644,
		Size:     int64(len(body)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("Write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

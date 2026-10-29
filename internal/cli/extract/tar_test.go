// SPDX-License-Identifier: Apache-2.0

package extract_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
	maliciousfixtures "github.com/ackstorm/ach/test/fixtures/malicious-archives"
)

// generousLimits is the default-but-relaxed Limits used by tests that
// don't care about caps. MaxEntries set high so the entry-count check
// is never the failure surface.
func generousLimits() extract.Limits {
	const mib = int64(1024 * 1024)
	return extract.Limits{
		MaxExtractedPluginBytes:   200 * mib,
		MaxExtractedArtifactBytes: 500 * mib,
		MaxEntries:                65536,
	}
}

// makeTarGz builds a one-entry or multi-entry tar.gz in memory.
type tarEntry struct {
	hdr  *tar.Header
	body []byte
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if e.hdr.Size == 0 && len(e.body) > 0 {
			e.hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(e.hdr); err != nil {
			t.Fatalf("makeTarGz: WriteHeader: %v", err)
		}
		if len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("makeTarGz: Write body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("makeTarGz: tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("makeTarGz: gz.Close: %v", err)
	}
	return buf.Bytes()
}

// TestExtract_MaliciousFixtures iterates the SAFE-01 fixture set and
// asserts every one rejects with ErrUnsafeTarEntry. Two of the
// fixtures (symlink_default, symlink_escape) are class-specific:
//   - symlink_default rejects with allowSymlinks=false (default policy)
//   - symlink_escape rejects EVEN with allowSymlinks=true (in-tree only)
func TestExtract_MaliciousFixtures(t *testing.T) {
	fixDir := t.TempDir()
	paths, err := maliciousfixtures.BuildAll(fixDir)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	cases := []struct {
		name          string
		fixture       string
		allowSymlinks bool
	}{
		{"absolute_path_default", "absolute_path.tar.gz", false},
		{"absolute_path_allow_symlinks", "absolute_path.tar.gz", true},
		{"dotdot_default", "dotdot.tar.gz", false},
		{"dotdot_allow_symlinks", "dotdot.tar.gz", true},
		{"symlink_default", "symlink_default.tar.gz", false},
		{"symlink_escape_allow_symlinks", "symlink_escape.tar.gz", true},
		{"hardlink", "hardlink.tar.gz", false},
		{"device", "device.tar.gz", false},
		{"fifo", "fifo.tar.gz", false},
		{"pax_injection", "pax_injection.tar.gz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(paths[tc.fixture])
			if err != nil {
				t.Fatalf("read fixture %s: %v", tc.fixture, err)
			}
			dst := t.TempDir()
			_, err = extract.Extract(
				context.Background(),
				bytes.NewReader(data),
				dst,
				extract.KindPlugin,
				generousLimits(),
				tc.allowSymlinks,
			)
			if err == nil {
				t.Fatalf("Extract(%s, allowSymlinks=%v): want error, got nil",
					tc.fixture, tc.allowSymlinks)
			}
			if !errors.Is(err, extract.ErrUnsafeTarEntry) {
				t.Fatalf("Extract(%s) = %v, want errors.Is ErrUnsafeTarEntry",
					tc.fixture, err)
			}
		})
	}
}

// TestExtract_HappyPath_OneFile asserts a single-regular-entry archive
// extracts cleanly: file contents byte-equal input, mode masked,
// RelPath equals the header name, hash is the canonical "xxh3:" form.
func TestExtract_HappyPath_OneFile(t *testing.T) {
	body := []byte("hello")
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "x.txt",
		Mode:     0o644,
		Size:     int64(len(body)),
	}
	data := makeTarGz(t, []tarEntry{{hdr: hdr, body: body}})

	dst := t.TempDir()
	res, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPlugin,
		generousLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.WrittenFiles) != 1 {
		t.Fatalf("WrittenFiles len = %d, want 1", len(res.WrittenFiles))
	}
	if res.WrittenFiles[0].RelPath != "x.txt" {
		t.Errorf("RelPath = %q, want %q", res.WrittenFiles[0].RelPath, "x.txt")
	}
	if res.BytesWritten != int64(len(body)) {
		t.Errorf("BytesWritten = %d, want %d", res.BytesWritten, len(body))
	}
	got, err := os.ReadFile(filepath.Join(dst, "x.txt"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("extracted bytes %q != input %q", got, body)
	}
	// Hash must be the canonical form.
	if got := res.WrittenFiles[0].Hash; len(got) < len("xxh3:") || got[:5] != "xxh3:" {
		t.Errorf("Hash = %q, want xxh3: prefix", got)
	}
}

// TestExtract_ModeMasked asserts SAFE-02 — input mode 0o4755 (with
// setuid set) results in on-disk mode 0o0755 (setuid stripped).
func TestExtract_ModeMasked(t *testing.T) {
	body := []byte("y")
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "exec",
		Mode:     0o4755, // setuid set
		Size:     int64(len(body)),
	}
	data := makeTarGz(t, []tarEntry{{hdr: hdr, body: body}})

	dst := t.TempDir()
	_, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPlugin,
		generousLimits(),
		false,
	)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "exec"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Mask off any high bits that os.Stat may set (e.g. setuid would
	// still show through info.Mode().Perm() depending on OS). We assert
	// the relevant 12-bit mode-and-perm region is exactly 0o0755 — no
	// setuid, no setgid, no sticky, no group-write, no world-write.
	const safeMask os.FileMode = 0o7777
	if got := info.Mode() & safeMask; got != 0o0755 {
		t.Errorf("on-disk mode = %#o, want 0o0755 (mask stripped setuid)", got)
	}
}

// TestExtract_BombCapTrip_FileNotWritten asserts SAFE-03 ordering:
// when a single-entry archive's body would push past the per-kind
// cap, Extract returns ErrBombCapExceeded AND the partial file does
// not survive on disk. The capWriter design rejects the write BEFORE
// it touches the underlying file, so the file is created (O_CREATE)
// but receives no bytes, then is removed on error.
func TestExtract_BombCapTrip_FileNotWritten(t *testing.T) {
	const oneKB = 1024
	body := bytes.Repeat([]byte("A"), oneKB)
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "big.bin",
		Mode:     0o644,
		Size:     int64(len(body)),
	}
	data := makeTarGz(t, []tarEntry{{hdr: hdr, body: body}})

	dst := t.TempDir()
	limits := generousLimits()
	limits.MaxExtractedPluginBytes = 512 // cap below entry size

	_, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPlugin,
		limits,
		false,
	)
	if err == nil {
		t.Fatalf("Extract: want ErrBombCapExceeded, got nil")
	}
	if !errors.Is(err, extract.ErrBombCapExceeded) {
		t.Fatalf("Extract: want ErrBombCapExceeded, got %v", err)
	}

	// SAFE-03 / bomb-defense ordering: NO bytes from this entry
	// should be on disk. Either the file does not exist OR it
	// exists at size 0 (the implementation removes it on cap trip;
	// asserting IsNotExist is the strong invariant).
	_, statErr := os.Stat(filepath.Join(dst, "big.bin"))
	if statErr == nil {
		t.Errorf("file big.bin still present after bomb-cap trip — SAFE-03 ordering violated")
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("Stat after bomb-cap trip: want IsNotExist, got %v", statErr)
	}
}

// TestExtract_ArchiveWideBombCap asserts the cap is cumulative across
// entries: N files each individually under the per-file cap must still
// trip ErrBombCapExceeded once their archive-wide total exceeds it. (#4)
func TestExtract_ArchiveWideBombCap(t *testing.T) {
	// 4 files of 200 bytes each = 800 total; cap at 512 => the archive
	// total trips even though each file is individually under cap.
	body := bytes.Repeat([]byte("A"), 200)
	entries := make([]tarEntry, 4)
	for i := range entries {
		entries[i] = tarEntry{hdr: &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     fmt.Sprintf("f%d.bin", i),
			Mode:     0o644,
			Size:     int64(len(body)),
		}, body: body}
	}
	data := makeTarGz(t, entries)

	dst := t.TempDir()
	limits := generousLimits()
	limits.MaxExtractedPluginBytes = 512 // below the 800-byte archive total

	_, err := extract.Extract(context.Background(), bytes.NewReader(data), dst,
		extract.KindPlugin, limits, false)
	if !errors.Is(err, extract.ErrBombCapExceeded) {
		t.Fatalf("want ErrBombCapExceeded for over-budget archive, got %v", err)
	}
}

// TestExtract_TooManyEntries asserts the entry-count cap fires BEFORE
// the offending entry's body is read.
func TestExtract_TooManyEntries(t *testing.T) {
	const cap = 3
	entries := make([]tarEntry, cap+1)
	for i := 0; i <= cap; i++ {
		entries[i] = tarEntry{
			hdr: &tar.Header{
				Typeflag: tar.TypeReg,
				Name:     filepath.Join("dir", string(rune('a'+i))+".txt"),
				Mode:     0o644,
			},
			body: []byte{'x'},
		}
	}
	data := makeTarGz(t, entries)

	dst := t.TempDir()
	limits := generousLimits()
	limits.MaxEntries = cap

	_, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPlugin,
		limits,
		false,
	)
	if err == nil {
		t.Fatalf("Extract: want ErrTooManyEntries, got nil")
	}
	if !errors.Is(err, extract.ErrTooManyEntries) {
		t.Fatalf("Extract: want ErrTooManyEntries, got %v", err)
	}
}

// TestExtract_SymlinkAllowed_InTree asserts that allowSymlinks=true
// admits a symlink whose Linkname resolves to an in-tree target.
func TestExtract_SymlinkAllowed_InTree(t *testing.T) {
	entries := []tarEntry{
		{hdr: &tar.Header{Typeflag: tar.TypeReg, Name: "target", Mode: 0o644}, body: []byte("hi")},
		{hdr: &tar.Header{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "target"}},
	}
	data := makeTarGz(t, entries)

	dst := t.TempDir()
	_, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPlugin,
		generousLimits(),
		true,
	)
	if err != nil {
		t.Fatalf("Extract with in-tree symlink: %v", err)
	}
	lstat, err := os.Lstat(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatalf("Lstat link: %v", err)
	}
	if lstat.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link is not a symlink, mode=%v", lstat.Mode())
	}
}

// TestExtract_SymlinkAllowed_Escape_Rejected asserts that
// allowSymlinks=true STILL rejects a symlink whose Linkname escapes dst.
func TestExtract_SymlinkAllowed_Escape_Rejected(t *testing.T) {
	entries := []tarEntry{
		{hdr: &tar.Header{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "../../etc/passwd"}},
	}
	data := makeTarGz(t, entries)

	dst := t.TempDir()
	_, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPlugin,
		generousLimits(),
		true,
	)
	if err == nil {
		t.Fatalf("Extract: want ErrUnsafeTarEntry (symlink escape), got nil")
	}
	if !errors.Is(err, extract.ErrUnsafeTarEntry) {
		t.Fatalf("Extract: want ErrUnsafeTarEntry, got %v", err)
	}
}

// TestExtract_PromptKind_NoLimit asserts KindPrompt has
// MaxBytesForKind == 0 ("no cap") so a 1 MiB write succeeds even
// against tiny per-kind plugin/artifact caps.
func TestExtract_PromptKind_NoLimit(t *testing.T) {
	const oneMiB = 1 << 20
	body := bytes.Repeat([]byte("Z"), oneMiB)
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "huge.bin",
		Mode:     0o644,
		Size:     int64(len(body)),
	}
	data := makeTarGz(t, []tarEntry{{hdr: hdr, body: body}})

	dst := t.TempDir()
	limits := generousLimits()
	limits.MaxExtractedPluginBytes = 1 // 1 byte cap — irrelevant for KindPrompt
	limits.MaxExtractedArtifactBytes = 1

	res, err := extract.Extract(
		context.Background(),
		bytes.NewReader(data),
		dst,
		extract.KindPrompt,
		limits,
		false,
	)
	if err != nil {
		t.Fatalf("Extract(KindPrompt, 1MiB body): %v", err)
	}
	if res.BytesWritten != int64(oneMiB) {
		t.Errorf("BytesWritten = %d, want %d", res.BytesWritten, oneMiB)
	}
}

// TestExtract_GzipReaderError asserts a non-gzip stream surfaces as a
// wrapped error (no silent partial-extract).
func TestExtract_GzipReaderError(t *testing.T) {
	dst := t.TempDir()
	_, err := extract.Extract(
		context.Background(),
		bytes.NewReader([]byte("not gzip at all")),
		dst,
		extract.KindPlugin,
		generousLimits(),
		false,
	)
	if err == nil {
		t.Fatalf("Extract on bogus stream: want error, got nil")
	}
}

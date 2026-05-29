// SPDX-License-Identifier: Apache-2.0

// Package maliciousfixtures emits the deterministic .tar.gz fixture set
// used by the Phase 7 extract package's SAFE-01 rejection tests (and the
// W4 e2e safe-extract subtest). Each fixture is a one-entry archive that
// violates a single SAFE-01 class; the names and class mapping are:
//
//	absolute_path.tar.gz   — Header.Name = "/etc/passwd"          (absolute)
//	dotdot.tar.gz          — Header.Name = "../escape.txt"        (dotdot)
//	symlink_default.tar.gz — Typeflag TypeSymlink, in-tree target
//	                          (rejected when allowSymlinks=false)
//	symlink_escape.tar.gz  — Typeflag TypeSymlink, target escapes  dst
//	                          (rejected even when allowSymlinks=true)
//	hardlink.tar.gz        — Typeflag TypeLink                    (unconditional)
//	device.tar.gz          — Typeflag TypeChar                    (unconditional)
//	fifo.tar.gz            — Typeflag TypeFifo                    (unconditional)
//	pax_injection.tar.gz   — Typeflag TypeXHeader, PaxHeaders["path"]
//	                          = "/etc/escape"                     (pax-path injection)
//
// Fixture determinism: every header carries Uid=0, Gid=0,
// ModTime=time.Unix(0,0).UTC() so the byte output is reproducible.
// Tests pin to byte content via the package's BuildAll helper, which
// writes into a t.TempDir at test time — no committed .tar.gz files
// in the repo (keeps the tree lean, no large binary blobs in `git log`).
package maliciousfixtures

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// epoch is the deterministic timestamp on every header — UTC unix-epoch
// zero. Fixed value means the same fixture byte-for-byte across runs and
// machines, which is useful for any future content-hash invariant test.
var epoch = time.Unix(0, 0).UTC()

// Names is the canonical fixture order. Iteration order is fixed so the
// test table in tar_test.go can range over it deterministically.
var Names = []string{
	"absolute_path.tar.gz",
	"dotdot.tar.gz",
	"symlink_default.tar.gz",
	"symlink_escape.tar.gz",
	"hardlink.tar.gz",
	"device.tar.gz",
	"fifo.tar.gz",
	"pax_injection.tar.gz",
}

// BuildAll materializes every fixture under dir and returns a map from
// fixture name to its filesystem path. dir must already exist.
//
// The function is reentrant (BuildAll on the same dir overwrites the
// fixtures byte-for-byte). Tests typically call it with t.TempDir() so
// cleanup is automatic.
func BuildAll(dir string) (map[string]string, error) {
	builders := map[string]func() ([]byte, error){
		"absolute_path.tar.gz":   buildAbsolutePath,
		"dotdot.tar.gz":          buildDotdot,
		"symlink_default.tar.gz": buildSymlinkInTree,
		"symlink_escape.tar.gz":  buildSymlinkEscape,
		"hardlink.tar.gz":        buildHardlink,
		"device.tar.gz":          buildDevice,
		"fifo.tar.gz":            buildFifo,
		"pax_injection.tar.gz":   buildPaxInjection,
	}
	out := make(map[string]string, len(builders))
	for _, name := range Names {
		build, ok := builders[name]
		if !ok {
			return nil, fmt.Errorf("maliciousfixtures: no builder for %q", name)
		}
		data, err := build()
		if err != nil {
			return nil, fmt.Errorf("maliciousfixtures: build %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, fmt.Errorf("maliciousfixtures: write %s: %w", name, err)
		}
		out[name] = path
	}
	return out, nil
}

// writeArchive runs the supplied per-entry builder under a fresh gzip+tar
// stream and returns the .tar.gz byte slice. The build callback writes
// one or more headers (+ optional bodies) to the supplied writer.
func writeArchive(build func(tw *tar.Writer) error) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := build(tw); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// detEntry returns a baseline header with deterministic Uid/Gid/ModTime.
// Callers override Name/Typeflag/Mode/Size/Linkname/PaxHeaders per fixture.
func detEntry() *tar.Header {
	return &tar.Header{
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Uid:      0,
		Gid:      0,
		ModTime:  epoch,
	}
}

func buildAbsolutePath() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		body := []byte("pwned")
		h := detEntry()
		h.Name = "/etc/passwd"
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	})
}

func buildDotdot() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		body := []byte("escape")
		h := detEntry()
		h.Name = "../escape.txt"
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	})
}

func buildSymlinkInTree() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		h := detEntry()
		h.Typeflag = tar.TypeSymlink
		h.Name = "link"
		h.Linkname = "target"
		h.Size = 0
		return tw.WriteHeader(h)
	})
}

func buildSymlinkEscape() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		h := detEntry()
		h.Typeflag = tar.TypeSymlink
		h.Name = "link"
		h.Linkname = "../../etc/passwd"
		h.Size = 0
		return tw.WriteHeader(h)
	})
}

func buildHardlink() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		h := detEntry()
		h.Typeflag = tar.TypeLink
		h.Name = "hardlink"
		h.Linkname = "/etc/passwd"
		h.Size = 0
		return tw.WriteHeader(h)
	})
}

func buildDevice() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		h := detEntry()
		h.Typeflag = tar.TypeChar
		h.Name = "tty"
		h.Devmajor = 5
		h.Devminor = 0
		h.Size = 0
		return tw.WriteHeader(h)
	})
}

func buildFifo() ([]byte, error) {
	return writeArchive(func(tw *tar.Writer) error {
		h := detEntry()
		h.Typeflag = tar.TypeFifo
		h.Name = "fifo"
		h.Size = 0
		return tw.WriteHeader(h)
	})
}

// ──────────────────────────────────────────────────────────────────────────
// Raw tar-header builders for the PAX-injection fixture. The stdlib's
// tar.Writer refuses to emit TypeXHeader manually AND deduplicates
// PAXRecords["path"] against a USTAR-fitting Name, so we hand-roll the
// 512-byte header for this one fixture. The format is POSIX.1-2001
// PAX extended (man 1 pax §5.3): `<len> <key>=<value>\n` where `<len>`
// is the decimal byte length of the entire record including the length
// digits themselves and the trailing newline.
// ──────────────────────────────────────────────────────────────────────────

const (
	tarTypeReg     = byte('0')
	tarTypeXHeader = byte('x')
)

// paxRecord returns one POSIX.1-2001 PAX extended record line of the
// form "<len> key=value\n". <len> is the total byte length of the
// returned string including the length digits and the trailing newline.
func paxRecord(key, value string) string {
	core := " " + key + "=" + value + "\n"
	// Iteratively pick the number of digits in <len> such that the
	// length of the digit string equals <len> minus len(core).
	for n := 1; n <= 10; n++ {
		total := n + len(core)
		s := fmt.Sprintf("%d", total)
		if len(s) == n {
			return s + core
		}
	}
	panic("paxRecord: cannot encode length")
}

// writeRawTarHeader writes a single 512-byte ustar header block with
// fixed Uid=0/Gid=0/Mode=0644/Mtime=0 + the supplied name/typeflag/size.
// The checksum field is computed by summing all 512 bytes with the
// checksum field initially treated as 8 spaces (per POSIX).
func writeRawTarHeader(out *bytes.Buffer, name string, typeflag byte, size int64) {
	hdr := make([]byte, 512)
	copy(hdr[0:100], name)
	copyOctalNul(hdr[100:108], 0o644)
	copyOctalNul(hdr[108:116], 0)
	copyOctalNul(hdr[116:124], 0)
	copyOctalNul(hdr[124:136], size)
	copyOctalNul(hdr[136:148], 0)
	// Checksum field initialized with 8 spaces for the sum calculation.
	for i := 148; i < 156; i++ {
		hdr[i] = ' '
	}
	hdr[156] = typeflag
	copy(hdr[257:263], "ustar\x00")
	copy(hdr[263:265], "00")

	sum := 0
	for _, b := range hdr {
		sum += int(b)
	}
	// 7-digit octal + trailing NUL + space — per POSIX, the canonical
	// form is "<6 octal digits>\0 " but stdlib accepts the 6-digit form
	// too. We write a 7-char field by using 7 octal digits then space.
	octalStr := fmt.Sprintf("%06o", sum)
	copy(hdr[148:154], octalStr)
	hdr[154] = 0
	hdr[155] = ' '

	out.Write(hdr)
}

// copyOctalNul writes v as a zero-padded octal string into buf followed
// by a single NUL terminator. The numeric width is len(buf)-1 so the
// final byte is always 0.
func copyOctalNul(buf []byte, v int64) {
	width := len(buf) - 1
	if width < 1 {
		width = len(buf)
	}
	s := fmt.Sprintf("%0*o", width, v)
	copy(buf, s)
	if len(buf) > width {
		buf[width] = 0
	}
}

// padTo512 zero-pads out to the next 512-byte boundary.
func padTo512(out *bytes.Buffer) {
	mod := out.Len() % 512
	if mod != 0 {
		out.Write(make([]byte, 512-mod))
	}
}

// buildPaxInjection emits a hand-crafted tar archive containing a PAX
// extended header (TypeXHeader) whose `path` record overrides the
// following regular entry's name with "/etc/escape".
//
// We can't use tar.Writer for this fixture: the stdlib writer
// (a) refuses TypeXHeader on manual WriteHeader calls, and (b) when
// asked to write a regular entry with PAXRecords["path"] set,
// silently drops the record if the header's Name already fits USTAR
// (it deduplicates against the same key). So we lay down two raw
// 512-byte headers ourselves — exactly what a real malicious archive
// in the wild looks like — then gzip the result.
func buildPaxInjection() ([]byte, error) {
	var raw bytes.Buffer
	body := []byte("pwned via pax")
	paxBody := paxRecord("path", "/etc/escape")

	// 1. PAX extended header — TypeXHeader, body holds the path-override record.
	writeRawTarHeader(&raw, "PaxHeaders/innocent.txt", tarTypeXHeader, int64(len(paxBody)))
	raw.WriteString(paxBody)
	padTo512(&raw)

	// 2. Regular entry — tar.Reader transparently overlays the PAX
	//    `path` onto Name when it returns this header.
	writeRawTarHeader(&raw, "innocent.txt", tarTypeReg, int64(len(body)))
	raw.Write(body)
	padTo512(&raw)

	// 3. End-of-archive: two zero blocks.
	raw.Write(make([]byte, 1024))

	// gzip-wrap.
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

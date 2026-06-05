// SPDX-License-Identifier: Apache-2.0

package sources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
)

// buildTarGz builds a gzip-tar from path→content entries. A dir entry is
// emitted for every parent path so the archive resembles a real repo tarball.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	dirs := map[string]struct{}{}
	for name := range files {
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			dirs[strings.Join(parts[:i], "/")+"/"] = struct{}{}
		}
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	for _, d := range dirList {
		if err := tw.WriteHeader(&tar.Header{Name: d, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			t.Fatalf("write dir %s: %v", d, err)
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		body := []byte(files[n])
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("write hdr %s: %v", n, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %s: %v", n, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// regularNames extracts the sorted regular-file entry names from a gzip-tar.
func regularNames(t *testing.T, tarball []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var out []string
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatalf("tar read: %v", e)
		}
		if hdr.Typeflag == tar.TypeReg {
			out = append(out, hdr.Name)
		}
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNarrowArchiveSubtree(t *testing.T) {
	// git-protocol shape: no archive-root wrapper.
	gitShape := map[string]string{
		"README.md":            "# repo\n",
		"skills/pdf/SKILL.md":  "---\nname: pdf\n---\n",
		"skills/pdf/run.sh":    "echo hi\n",
		"skills/docx/SKILL.md": "---\nname: docx\n---\n",
	}
	// REST shape: everything wrapped under "<repo>-<sha>/".
	restShape := map[string]string{
		"repo-abc123/README.md":            "# repo\n",
		"repo-abc123/skills/pdf/SKILL.md":  "---\nname: pdf\n---\n",
		"repo-abc123/skills/pdf/run.sh":    "echo hi\n",
		"repo-abc123/skills/docx/SKILL.md": "---\nname: docx\n---\n",
	}

	t.Run("git_shape_narrow_to_skill_dir", func(t *testing.T) {
		out, err := NarrowArchiveSubtree(bytes.NewReader(buildTarGz(t, gitShape)), "skills/pdf", DefaultArchiveIngressCap)
		if err != nil {
			t.Fatalf("narrow: %v", err)
		}
		want := []string{"SKILL.md", "run.sh"}
		if got := regularNames(t, out); !equalStrings(got, want) {
			t.Errorf("entries = %v; want %v", got, want)
		}
	})

	t.Run("rest_shape_narrow_to_skill_dir", func(t *testing.T) {
		out, err := NarrowArchiveSubtree(bytes.NewReader(buildTarGz(t, restShape)), "skills/pdf", DefaultArchiveIngressCap)
		if err != nil {
			t.Fatalf("narrow: %v", err)
		}
		want := []string{"SKILL.md", "run.sh"}
		if got := regularNames(t, out); !equalStrings(got, want) {
			t.Errorf("entries = %v; want %v (wrapper not stripped?)", got, want)
		}
	})

	t.Run("narrow_to_skills_root_keeps_child_dirs", func(t *testing.T) {
		out, err := NarrowArchiveSubtree(bytes.NewReader(buildTarGz(t, restShape)), "skills", DefaultArchiveIngressCap)
		if err != nil {
			t.Fatalf("narrow: %v", err)
		}
		want := []string{"docx/SKILL.md", "pdf/SKILL.md", "pdf/run.sh"}
		if got := regularNames(t, out); !equalStrings(got, want) {
			t.Errorf("entries = %v; want %v", got, want)
		}
	})

	t.Run("path_is_a_file_returns_raw_bytes", func(t *testing.T) {
		out, err := NarrowArchiveSubtree(bytes.NewReader(buildTarGz(t, gitShape)), "skills/pdf/SKILL.md", DefaultArchiveIngressCap)
		if err != nil {
			t.Fatalf("narrow: %v", err)
		}
		// A file path returns the file's RAW bytes (no tar wrapper).
		if got := string(out); got != gitShape["skills/pdf/SKILL.md"] {
			t.Errorf("raw bytes = %q; want %q", got, gitShape["skills/pdf/SKILL.md"])
		}
	})

	t.Run("path_not_found_rejected", func(t *testing.T) {
		_, err := NarrowArchiveSubtree(bytes.NewReader(buildTarGz(t, gitShape)), "nope/missing", DefaultArchiveIngressCap)
		if !errors.Is(err, ErrUpstreamInvalid) {
			t.Errorf("err = %v; want ErrUpstreamInvalid (path not found)", err)
		}
	})

	t.Run("empty_path_passthrough", func(t *testing.T) {
		in := buildTarGz(t, gitShape)
		out, err := NarrowArchiveSubtree(bytes.NewReader(in), "", DefaultArchiveIngressCap)
		if err != nil {
			t.Fatalf("narrow: %v", err)
		}
		if !bytes.Equal(in, out) {
			t.Errorf("empty path should pass the archive through unchanged")
		}
	})

	t.Run("oversize_rejected", func(t *testing.T) {
		_, err := NarrowArchiveSubtree(bytes.NewReader(buildTarGz(t, gitShape)), "skills/pdf", 8)
		if !errors.Is(err, ErrUpstreamInvalid) {
			t.Errorf("err = %v; want ErrUpstreamInvalid (oversize)", err)
		}
	})
}

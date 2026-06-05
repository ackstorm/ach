// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// tarEntry is one header (+ optional body) used to build a test tarball.
type tarEntry struct {
	name     string
	typeflag byte
	linkname string
	body     string
	pax      map[string]string
}

func buildSafetyTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: tf, Linkname: e.linkname}
		if tf == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if e.pax != nil {
			hdr.PAXRecords = e.pax
			hdr.Format = tar.FormatPAX
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write hdr %s: %v", e.name, err)
		}
		if tf == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %s: %v", e.name, err)
			}
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

func TestTarEntrySafe(t *testing.T) {
	cases := []struct {
		name string
		hdr  tar.Header
		safe bool
	}{
		{"regular", tar.Header{Name: "commands/cmd.md", Typeflag: tar.TypeReg}, true},
		{"dir", tar.Header{Name: "commands/", Typeflag: tar.TypeDir}, true},
		{"in_tree_symlink", tar.Header{Name: "a/link", Typeflag: tar.TypeSymlink, Linkname: "../cmd.md"}, true},
		{"abs_path", tar.Header{Name: "/etc/passwd", Typeflag: tar.TypeReg}, false},
		{"dotdot_path", tar.Header{Name: "../evil", Typeflag: tar.TypeReg}, false},
		{"hardlink", tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "cmd.md"}, false},
		{"device_char", tar.Header{Name: "d", Typeflag: tar.TypeChar}, false},
		{"device_block", tar.Header{Name: "d", Typeflag: tar.TypeBlock}, false},
		{"fifo", tar.Header{Name: "f", Typeflag: tar.TypeFifo}, false},
		{"abs_symlink", tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, false},
		{"escaping_symlink", tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd"}, false},
		{"empty_symlink", tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: ""}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tarEntrySafe(&tc.hdr)
			if tc.safe && err != nil {
				t.Errorf("tarEntrySafe(%+v) = %v; want nil", tc.hdr, err)
			}
			if !tc.safe {
				if err == nil {
					t.Errorf("tarEntrySafe(%+v) = nil; want error", tc.hdr)
				} else if !errors.Is(err, sources.ErrUpstreamInvalid) {
					t.Errorf("err = %v; want wrap ErrUpstreamInvalid", err)
				}
			}
		})
	}
}

func TestTarEntrySafe_PaxInjection(t *testing.T) {
	hdr := tar.Header{Name: "ok.md", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"path": "../escape"}}
	if err := tarEntrySafe(&hdr); !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("pax-injected path err = %v; want ErrUpstreamInvalid", err)
	}
}

func TestVerifyPluginContents_FullTarSafety(t *testing.T) {
	validComponent := tarEntry{name: "commands/cmd.md", body: "# cmd\n"}
	t.Run("clean_plugin_accepted", func(t *testing.T) {
		tb := buildSafetyTarGz(t, []tarEntry{validComponent})
		if err := verifyPluginContents(bytes.NewReader(tb)); err != nil {
			t.Errorf("verifyPluginContents = %v; want nil", err)
		}
	})
	t.Run("in_tree_symlink_accepted", func(t *testing.T) {
		tb := buildSafetyTarGz(t, []tarEntry{validComponent, {name: "commands/alias.md", typeflag: tar.TypeSymlink, linkname: "cmd.md"}})
		if err := verifyPluginContents(bytes.NewReader(tb)); err != nil {
			t.Errorf("verifyPluginContents = %v; want nil (in-tree symlink admitted)", err)
		}
	})
	unsafe := map[string]tarEntry{
		"traversal": {name: "../evil.sh", body: "x"},
		"hardlink":  {name: "h", typeflag: tar.TypeLink, linkname: "commands/cmd.md"},
		"device":    {name: "dev", typeflag: tar.TypeChar},
		"fifo":      {name: "pipe", typeflag: tar.TypeFifo},
		"abs_link":  {name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	}
	for label, bad := range unsafe {
		bad := bad
		t.Run("valid_plus_"+label+"_rejected", func(t *testing.T) {
			// The recognized component appears BEFORE the unsafe entry — proving
			// the walk does not early-exit on the first signal (F3).
			tb := buildSafetyTarGz(t, []tarEntry{validComponent, bad})
			err := verifyPluginContents(bytes.NewReader(tb))
			if !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("verifyPluginContents = %v; want ErrUpstreamInvalid", err)
			}
		})
	}
}

func TestVerifySkillContents_FullTarSafety(t *testing.T) {
	validSkill := tarEntry{name: "pdf/SKILL.md", body: "---\nname: pdf\ndescription: does pdf things\n---\n"}
	t.Run("clean_skill_accepted", func(t *testing.T) {
		tb := buildSafetyTarGz(t, []tarEntry{validSkill})
		if err := verifySkillContents(bytes.NewReader(tb)); err != nil {
			t.Errorf("verifySkillContents = %v; want nil", err)
		}
	})
	t.Run("valid_plus_traversal_rejected", func(t *testing.T) {
		tb := buildSafetyTarGz(t, []tarEntry{validSkill, {name: "../evil", body: "x"}})
		if err := verifySkillContents(bytes.NewReader(tb)); !errors.Is(err, sources.ErrUpstreamInvalid) {
			t.Errorf("verifySkillContents = %v; want ErrUpstreamInvalid", err)
		}
	})
	t.Run("valid_plus_hardlink_rejected", func(t *testing.T) {
		tb := buildSafetyTarGz(t, []tarEntry{validSkill, {name: "h", typeflag: tar.TypeLink, linkname: "pdf/SKILL.md"}})
		if err := verifySkillContents(bytes.NewReader(tb)); !errors.Is(err, sources.ErrUpstreamInvalid) {
			t.Errorf("verifySkillContents = %v; want ErrUpstreamInvalid", err)
		}
	})
	t.Run("valid_plus_in_tree_symlink_accepted", func(t *testing.T) {
		tb := buildSafetyTarGz(t, []tarEntry{validSkill, {name: "pdf/alias.md", typeflag: tar.TypeSymlink, linkname: "SKILL.md"}})
		if err := verifySkillContents(bytes.NewReader(tb)); err != nil {
			t.Errorf("verifySkillContents = %v; want nil (in-tree symlink admitted)", err)
		}
	})
}

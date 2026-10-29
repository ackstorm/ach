// SPDX-License-Identifier: Apache-2.0

package contentkit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/sourceserr"
)

func skillTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestValidateSkillName(t *testing.T) {
	good := []string{"pdf-processing", "a", "data-analysis", "x9"}
	bad := []string{"PDF", "-pdf", "pdf-", "pdf--proc", "", "pdf_proc", "pdf proc"}
	for _, n := range good {
		if err := validateSkillName(n); err != nil {
			t.Errorf("validateSkillName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range bad {
		if err := validateSkillName(n); err == nil {
			t.Errorf("validateSkillName(%q) = nil, want error", n)
		}
	}
}

func TestVerifySkillContents(t *testing.T) {
	good := skillTarGz(t, map[string]string{"SKILL.md": "---\nname: pdf-processing\ndescription: do pdf things\n---\nbody"})
	if err := VerifySkillContents(bytes.NewReader(good)); err != nil {
		t.Errorf("valid skill rejected: %v", err)
	}
	nested := skillTarGz(t, map[string]string{"pdf-processing/SKILL.md": "---\nname: pdf-processing\ndescription: x\n---\nb"})
	if err := VerifySkillContents(bytes.NewReader(nested)); err != nil {
		t.Errorf("valid nested skill rejected: %v", err)
	}
	for _, bad := range [][]byte{
		skillTarGz(t, map[string]string{"README.md": "hi"}),                                      // no SKILL.md
		skillTarGz(t, map[string]string{"SKILL.md": "---\nname: x\n---\nb"}),                     // no description
		skillTarGz(t, map[string]string{"SKILL.md": "---\nname: Bad_Name\ndescription: d\n---"}), // invalid name
	} {
		err := VerifySkillContents(bytes.NewReader(bad))
		if err == nil || !errors.Is(err, sourceserr.ErrUpstreamInvalid) {
			t.Errorf("bad skill: err=%v, want wraps ErrUpstreamInvalid", err)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0

package contentkit

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sourceserr"
)

func TestVerifyInstruction(t *testing.T) {
	cases := map[string]struct {
		in   []byte
		want string // substring of the error; "" means valid
	}{
		"plain markdown":          {[]byte("# How we work\n- tests first\n"), ""},
		"exactly the limit":       {bytes.Repeat([]byte("a"), MaxInstructionBytes), ""},
		"one byte over":           {bytes.Repeat([]byte("a"), MaxInstructionBytes+1), "larger than 23900 bytes"},
		"empty":                   {nil, "empty"},
		"gzip is not utf-8":       {[]byte{0x1f, 0x8b, 0x08, 0x00}, "UTF-8"},
		"invalid utf-8":           {[]byte{'a', 0xff, 'b'}, "UTF-8"},
		"frontmatter":             {[]byte("---\ntitle: x\n---\nbody\n"), "frontmatter"},
		"frontmatter crlf":        {[]byte("---\r\ntitle: x\r\n---\r\nbody\r\n"), "frontmatter"},
		"frontmatter after bom":   {[]byte("\ufeff---\ntitle: x\n---\n"), "frontmatter"},
		"frontmatter trailing ws": {[]byte("---  \ntitle: x\n---\n"), "frontmatter"},
		"only a fence":            {[]byte("---"), "frontmatter"},
		"rule line not at top":    {[]byte("intro\n---\nmore\n"), ""},
		"heading starting dashes": {[]byte("--- not a fence\n"), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := VerifyInstruction(tc.in)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if !errors.Is(err, sourceserr.ErrUpstreamInvalid) {
				t.Fatalf("error must wrap ErrUpstreamInvalid: %v", err)
			}
		})
	}
}

func TestStageInstructionBody_RejectsOversizeWithoutReadingAll(t *testing.T) {
	r := strings.NewReader(strings.Repeat("a", 10*MaxInstructionBytes))
	if _, err := StageInstructionBody(r); err == nil || !errors.Is(err, sourceserr.ErrUpstreamInvalid) {
		t.Fatalf("want ErrUpstreamInvalid, got %v", err)
	}
	if r.Len() == 0 {
		t.Fatal("StageInstructionBody read the whole body; it must stop after MaxInstructionBytes+1")
	}
}

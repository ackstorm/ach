// SPDX-License-Identifier: Apache-2.0

package contentkit

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/ackstorm/ach/internal/sourceserr"
)

// MaxInstructionBytes caps an Instruction's text. Antigravity truncates any
// rules file above 24,000 bytes, the lowest limit among the tools ach-cli
// delivers to; the margin covers the frontmatter ach-cli adds on top.
const MaxInstructionBytes = 23900

// StageInstructionBody reads at most MaxInstructionBytes+1 bytes and verifies
// them.
func StageInstructionBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxInstructionBytes+1))
	if err != nil {
		return nil, err
	}
	if err := VerifyInstruction(b); err != nil {
		return nil, err
	}
	return b, nil
}

// VerifyInstruction accepts plain UTF-8 Markdown without frontmatter. ach-cli
// writes each tool's own frontmatter (Cursor alwaysApply, Antigravity
// trigger), so a text that brings its own would end up with two.
func VerifyInstruction(b []byte) error {
	switch {
	case len(b) == 0:
		return fmt.Errorf("%w: instruction is empty", sourceserr.ErrUpstreamInvalid)
	case len(b) > MaxInstructionBytes:
		return fmt.Errorf("%w: instruction is larger than %d bytes", sourceserr.ErrUpstreamInvalid, MaxInstructionBytes)
	case !utf8.Valid(b):
		return fmt.Errorf("%w: instruction is not valid UTF-8", sourceserr.ErrUpstreamInvalid)
	case startsWithFence(b):
		return fmt.Errorf("%w: instruction must not start with frontmatter", sourceserr.ErrUpstreamInvalid)
	}
	return nil
}

// startsWithFence reports whether the first line, without a byte-order mark
// and trailing whitespace, is a YAML frontmatter fence.
func startsWithFence(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("\ufeff"))
	line, _, _ := bytes.Cut(b, []byte("\n"))
	return string(bytes.TrimRight(line, " \t\r")) == "---"
}

// SPDX-License-Identifier: Apache-2.0

package contentservice

import "testing"

func TestContentTypeForFile_Static(t *testing.T) {
	cases := []struct {
		kind     string
		filename string
		want     string
	}{
		{"plugin", "caveman.tar.gz", "application/gzip"},
		{"artifact", "openclaw-templates.tar.gz", "application/gzip"},
		{"artifact", "single-file", "application/octet-stream"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.filename, func(t *testing.T) {
			got := ContentTypeForFile(tc.kind, tc.filename, "")
			if got != tc.want {
				t.Errorf("ContentTypeForFile(%q,%q,_)=%q, want %q",
					tc.kind, tc.filename, got, tc.want)
			}
		})
	}
}

func TestContentTypeForFile_PromptOverride(t *testing.T) {
	got := ContentTypeForFile("prompt", "claude-code-system-prompt", "text/markdown")
	if got != "text/markdown" {
		t.Errorf("got %q, want text/markdown", got)
	}
}

func TestContentTypeForFile_PromptDefault(t *testing.T) {
	got := ContentTypeForFile("prompt", "p", "")
	if got != "text/markdown" {
		t.Errorf("got %q, want default text/markdown", got)
	}
}

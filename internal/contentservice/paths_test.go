// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolvePath_HappyPath(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		resource  string
		wantPaths []string
	}{
		{
			name:      "prompt resolves to bare name under prompt/",
			kind:      "prompt",
			resource:  "claude-code-system-prompt",
			wantPaths: []string{filepath.Join("/c", "prompt", "claude-code-system-prompt")},
		},
		{
			name:      "plugin resolves to .tar.gz under plugin/",
			kind:      "plugin",
			resource:  "caveman",
			wantPaths: []string{filepath.Join("/c", "plugin", "caveman.tar.gz")},
		},
		{
			name:     "artifact returns .tar.gz first, bare second",
			kind:     "artifact",
			resource: "openclaw-templates",
			wantPaths: []string{
				filepath.Join("/c", "artifact", "openclaw-templates.tar.gz"),
				filepath.Join("/c", "artifact", "openclaw-templates"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolvePath("/c", tc.kind, tc.resource)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.wantPaths) {
				t.Fatalf("len(paths)=%d, want %d (%v)", len(got), len(tc.wantPaths), got)
			}
			for i, p := range got {
				if p != tc.wantPaths[i] {
					t.Errorf("paths[%d]=%q, want %q", i, p, tc.wantPaths[i])
				}
			}
		})
	}
}

func TestResolvePath_InvalidKind(t *testing.T) {
	_, err := ResolvePath("/c", "marketplace", "anything")
	if !errors.Is(err, ErrInvalidKind) {
		t.Errorf("want ErrInvalidKind, got %v", err)
	}
}

func TestResolvePath_InvalidName(t *testing.T) {
	bad := []string{
		"",
		"foo/bar",
		"foo\\bar",
		"..",
		".",
		".hidden",
		"../etc/passwd",
	}
	for _, n := range bad {
		t.Run("name="+n, func(t *testing.T) {
			_, err := ResolvePath("/c", "prompt", n)
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("name=%q: want ErrInvalidName, got %v", n, err)
			}
		})
	}
}

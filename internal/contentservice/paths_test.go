// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolvePath_HappyPath(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		resource string
		scope    string
		wantPath string
	}{
		{
			name:     "prompt resolves to bare name under prompt/",
			kind:     "prompt",
			resource: "claude-code-system-prompt",
			scope:    "",
			wantPath: filepath.Join("/c", "prompt", "claude-code-system-prompt"),
		},
		{
			name:     "plugin resolves to .tar.gz under plugin/",
			kind:     "plugin",
			resource: "caveman",
			scope:    "",
			wantPath: filepath.Join("/c", "plugin", "caveman.tar.gz"),
		},
		{
			name:     "artifact scope=directory uses .tar.gz",
			kind:     "artifact",
			resource: "openclaw-templates",
			scope:    "directory",
			wantPath: filepath.Join("/c", "artifact", "openclaw-templates.tar.gz"),
		},
		{
			name:     "artifact scope=object uses bare name",
			kind:     "artifact",
			resource: "single-file",
			scope:    "object",
			wantPath: filepath.Join("/c", "artifact", "single-file"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolvePath("/c", tc.kind, tc.resource, tc.scope)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantPath {
				t.Errorf("path=%q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestResolvePath_Artifact_DirectoryScope(t *testing.T) {
	got, err := ResolvePath("/c", "artifact", "foo", "directory")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join("/c", "artifact", "foo.tar.gz")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_Artifact_ObjectScope(t *testing.T) {
	got, err := ResolvePath("/c", "artifact", "foo", "object")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join("/c", "artifact", "foo")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_Artifact_InvalidScope(t *testing.T) {
	_, err := ResolvePath("/c", "artifact", "foo", "invalid")
	if !errors.Is(err, ErrInvalidScope) {
		t.Errorf("want ErrInvalidScope, got %v", err)
	}
}

func TestResolvePath_Artifact_EmptyScope(t *testing.T) {
	// Empty scope for artifact MUST be rejected — caller has not yet
	// resolved the projection row, which is a wiring bug.
	_, err := ResolvePath("/c", "artifact", "foo", "")
	if !errors.Is(err, ErrInvalidScope) {
		t.Errorf("want ErrInvalidScope, got %v", err)
	}
}

func TestResolvePath_InvalidKind(t *testing.T) {
	_, err := ResolvePath("/c", "marketplace", "anything", "")
	if !errors.Is(err, ErrInvalidKind) {
		t.Errorf("want ErrInvalidKind, got %v", err)
	}
}

func TestPluginStoragePathWithinRoot(t *testing.T) {
	const root = "/var/cache/ach"
	cases := []struct {
		desc     string
		location string
		wantPath string
		wantOK   bool
	}{
		{
			desc:     "valid path inside root",
			location: "/var/cache/ach/plugin/foo.tar.gz",
			wantPath: "/var/cache/ach/plugin/foo.tar.gz",
			wantOK:   true,
		},
		{
			desc:     "absolute path outside root (/etc/passwd)",
			location: "/etc/passwd",
			wantPath: "",
			wantOK:   false,
		},
		{
			desc:     "path that escapes root after Clean",
			location: "/var/cache/ach/../../etc/passwd",
			wantPath: "",
			wantOK:   false,
		},
		{
			desc:     "relative path (not absolute)",
			location: "relative/path",
			wantPath: "",
			wantOK:   false,
		},
		{
			desc:     "root itself is accepted",
			location: "/var/cache/ach",
			wantPath: "/var/cache/ach",
			wantOK:   true,
		},
		{
			// Prefix-trap: /var/cache/ach-evil/x shares the prefix
			// "/var/cache/ach" but is NOT under /var/cache/ach/.
			// A naive strings.HasPrefix check (without the trailing
			// separator) would incorrectly accept this path.
			desc:     "prefix-trap: sibling dir with same prefix must be rejected",
			location: "/var/cache/ach-evil/x",
			wantPath: "",
			wantOK:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			gotPath, gotOK := PluginStoragePathWithinRoot(root, tc.location)
			if gotOK != tc.wantOK {
				t.Errorf("ok=%v, want %v", gotOK, tc.wantOK)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path=%q, want %q", gotPath, tc.wantPath)
			}
		})
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
			_, err := ResolvePath("/c", "prompt", n, "")
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("name=%q: want ErrInvalidName, got %v", n, err)
			}
		})
	}
}

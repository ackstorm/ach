// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"strings"
	"testing"
)

// TestValidateContentName covers the server-supplied-name traversal vectors
// that W6-01 security 2.3 / code-review F-12 flagged as a path-traversal
// pre-condition for the stageAndMap delete-before-replace step.
func TestValidateContentName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    string
		wantErr  bool
		errMatch string
	}{
		{name: "empty", input: "", wantErr: true, errMatch: "empty"},
		{name: "dot", input: ".", wantErr: true, errMatch: "traversal"},
		{name: "dot_dot", input: "..", wantErr: true, errMatch: "traversal"},
		{name: "hidden_prefix_git", input: ".git", wantErr: true, errMatch: "hidden"},
		{name: "hidden_prefix_env", input: ".env", wantErr: true, errMatch: "hidden"},
		{name: "slash_separator", input: "foo/bar", wantErr: true, errMatch: "separator"},
		{name: "slash_traversal", input: "../etc", wantErr: true, errMatch: "hidden"},
		{name: "deep_traversal", input: "foo/../bar", wantErr: true, errMatch: "separator"},
		{name: "backslash_separator", input: `foo\bar`, wantErr: true, errMatch: "separator"},
		{name: "absolute_unix", input: "/etc/passwd", wantErr: true, errMatch: "separator"},
		{name: "valid_simple", input: "caveman", wantErr: false},
		{name: "valid_with_dash", input: "my-plugin", wantErr: false},
		{name: "valid_with_underscore", input: "demo_artifact", wantErr: false},
		{name: "valid_with_digits", input: "plugin123", wantErr: false},
		{name: "valid_archive_extension", input: "blob.tar.gz", wantErr: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateContentName(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateContentName(%q): want error, got nil", tc.input)
				}
				if tc.errMatch != "" && !strings.Contains(err.Error(), tc.errMatch) {
					t.Fatalf("validateContentName(%q): error %q does not contain %q", tc.input, err.Error(), tc.errMatch)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateContentName(%q): want nil, got %v", tc.input, err)
			}
		})
	}
}

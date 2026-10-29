// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import "testing"

func TestDefaultAuthSecretKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		typ  string
		want string
	}{
		{"github", "GITHUB_TOKEN"},
		{"gitlab", "GITLAB_TOKEN"},
		{"bitbucket", "BITBUCKET_TOKEN"},
		{"s3", ""},
		{"gcs", ""},
		{"http", ""},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.typ, func(t *testing.T) {
			t.Parallel()
			if got := DefaultAuthSecretKey(tc.typ); got != tc.want {
				t.Errorf("DefaultAuthSecretKey(%q)=%q want %q", tc.typ, got, tc.want)
			}
		})
	}
}

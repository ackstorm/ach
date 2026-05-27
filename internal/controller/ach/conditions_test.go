// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

func TestResolveTransportName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec sources.SourceSpec
		want string
	}{
		{
			name: "github default",
			spec: sources.SourceSpec{Type: "github", GitHub: &achv1alpha1.GitHubSource{}},
			want: "git",
		},
		{
			name: "github explicit git",
			spec: sources.SourceSpec{Type: "github", GitHub: &achv1alpha1.GitHubSource{Transport: "git"}},
			want: "git",
		},
		{
			name: "github rest",
			spec: sources.SourceSpec{Type: "github", GitHub: &achv1alpha1.GitHubSource{Transport: "rest"}},
			want: "rest",
		},
		{
			name: "gitlab default",
			spec: sources.SourceSpec{Type: "gitlab", GitLab: &achv1alpha1.GitLabSource{}},
			want: "git",
		},
		{
			name: "gitlab rest",
			spec: sources.SourceSpec{Type: "gitlab", GitLab: &achv1alpha1.GitLabSource{Transport: "rest"}},
			want: "rest",
		},
		{
			name: "bitbucket default",
			spec: sources.SourceSpec{Type: "bitbucket", Bitbucket: &achv1alpha1.BitbucketSource{}},
			want: "git",
		},
		{
			name: "bitbucket rest",
			spec: sources.SourceSpec{Type: "bitbucket", Bitbucket: &achv1alpha1.BitbucketSource{Transport: "rest"}},
			want: "rest",
		},
		{
			name: "s3",
			spec: sources.SourceSpec{Type: "s3", S3: &achv1alpha1.S3Source{}},
			want: "n/a",
		},
		{
			name: "gcs",
			spec: sources.SourceSpec{Type: "gcs", GCS: &achv1alpha1.GCSSource{}},
			want: "n/a",
		},
		{
			name: "http",
			spec: sources.SourceSpec{Type: "http", HTTP: &achv1alpha1.HTTPSource{}},
			want: "n/a",
		},
		{
			name: "empty",
			spec: sources.SourceSpec{},
			want: "n/a",
		},
		{
			// PR #9 follow-up review finding #8: if CEL admission is
			// ever bypassed and multiple per-type pointers are non-nil,
			// the label must match the fetcher the registry actually
			// dispatches (which is sourceSpec.Type), not whichever
			// pointer happens to be checked first.
			name: "multi-pointer-respects-type",
			spec: sources.SourceSpec{
				Type:   "gitlab",
				GitHub: &achv1alpha1.GitHubSource{Transport: "git"},
				GitLab: &achv1alpha1.GitLabSource{Transport: "rest"},
			},
			want: "rest",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveTransportName(tc.spec); got != tc.want {
				t.Errorf("resolveTransportName(%s) = %q; want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestSourceReachableMessage(t *testing.T) {
	t.Parallel()
	got := sourceReachableMessage(sources.SourceSpec{
		Type:   "github",
		GitHub: &achv1alpha1.GitHubSource{Transport: "git"},
	})
	want := "transport=git"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

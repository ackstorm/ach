// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func TestReasonToConditionStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason              string
		wantSourceReachable metav1.ConditionStatus
		wantSynced          metav1.ConditionStatus
	}{
		// Happy path.
		{ReasonSynced, metav1.ConditionTrue, metav1.ConditionTrue},

		// Did not obtain the bytes we asked for.
		{ReasonUnreachable, metav1.ConditionFalse, metav1.ConditionFalse},
		{ReasonUnauthorized, metav1.ConditionFalse, metav1.ConditionFalse},
		{ReasonNotFound, metav1.ConditionFalse, metav1.ConditionFalse},
		{ReasonStaleCacheExpired, metav1.ConditionFalse, metav1.ConditionFalse},

		// Got the bytes, content was the problem.
		{ReasonUpstreamInvalid, metav1.ConditionTrue, metav1.ConditionFalse},
		{ReasonPluginTooLarge, metav1.ConditionTrue, metav1.ConditionFalse},

		// No fetch attempted.
		{ReasonInvalidConfig, metav1.ConditionUnknown, metav1.ConditionFalse},
		{ReasonUnsupportedPluginSource, metav1.ConditionUnknown, metav1.ConditionFalse},
		{ReasonInitializing, metav1.ConditionUnknown, metav1.ConditionFalse},

		// Unknown reason → conservative default.
		{"NotInTheEnumYet", metav1.ConditionUnknown, metav1.ConditionFalse},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			sr, sync := reasonToConditionStates(tc.reason)
			if sr != tc.wantSourceReachable {
				t.Errorf("SourceReachable = %q; want %q", sr, tc.wantSourceReachable)
			}
			if sync != tc.wantSynced {
				t.Errorf("Synced = %q; want %q", sync, tc.wantSynced)
			}
		})
	}
}

func TestApplyReconcileConditions_WritesBoth(t *testing.T) {
	t.Parallel()
	var conds []metav1.Condition
	applyReconcileConditions(&conds, ReasonUpstreamInvalid, "bad json", 7)
	if len(conds) != 2 {
		t.Fatalf("want 2 conditions; got %d", len(conds))
	}
	gotTypes := map[string]metav1.Condition{}
	for _, c := range conds {
		gotTypes[c.Type] = c
	}
	sr, ok := gotTypes[ConditionSourceReachable]
	if !ok {
		t.Fatal("SourceReachable condition missing")
	}
	if sr.Status != metav1.ConditionTrue {
		t.Errorf("SourceReachable.Status = %q; want True (UpstreamInvalid means we DID get bytes)", sr.Status)
	}
	if sr.Reason != ReasonUpstreamInvalid {
		t.Errorf("SourceReachable.Reason = %q; want %q", sr.Reason, ReasonUpstreamInvalid)
	}
	if sr.Message != "bad json" {
		t.Errorf("SourceReachable.Message = %q; want %q", sr.Message, "bad json")
	}
	sync, ok := gotTypes[ConditionSynced]
	if !ok {
		t.Fatal("Synced condition missing")
	}
	if sync.Status != metav1.ConditionFalse {
		t.Errorf("Synced.Status = %q; want False", sync.Status)
	}
	if sync.ObservedGeneration != 7 {
		t.Errorf("Synced.ObservedGeneration = %d; want 7", sync.ObservedGeneration)
	}
}

func TestSetConflictWithUIRowCondition(t *testing.T) {
	var conds []metav1.Condition
	setConflictWithUIRowCondition(&conds, "Synced", 7)

	if len(conds) != 1 {
		t.Fatalf("want 1 condition, got %d", len(conds))
	}
	c := conds[0]
	if c.Type != "Synced" {
		t.Errorf("Type = %q, want Synced", c.Type)
	}
	if c.Status != metav1.ConditionFalse {
		t.Errorf("Status = %q, want False", c.Status)
	}
	if c.Reason != ReasonConflictWithUIRow {
		t.Errorf("Reason = %q, want %q", c.Reason, ReasonConflictWithUIRow)
	}
	if c.Message != ConflictWithUIRowMessage {
		t.Errorf("Message = %q, want %q", c.Message, ConflictWithUIRowMessage)
	}
	if c.ObservedGeneration != 7 {
		t.Errorf("ObservedGeneration = %d, want 7", c.ObservedGeneration)
	}
}

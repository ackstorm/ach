// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// TestNew_NilSpec asserts the nil-spec defense-in-depth branch returns
// the documented sentinel.
func TestNew_NilSpec(t *testing.T) {
	t.Parallel()

	f, err := New(nil)
	if f != nil {
		t.Errorf("expected nil Fetcher when spec is nil; got %T", f)
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected ErrUpstreamInvalid; got %v", err)
	}
}

// TestNew_AcceptsAnonymousSpec asserts that a GitHubSource with no
// AuthSecretRef constructs cleanly (Phase 02.1 anonymous-fetch path).
func TestNew_AcceptsAnonymousSpec(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "octocat/Hello-World",
		Ref:  "master",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil Fetcher for anonymous spec")
	}
	if f.spec.AuthSecretRef != nil {
		t.Errorf("expected nil AuthSecretRef on anonymous spec")
	}
}

// TestFetch_AnonymousIgnoresSecret asserts that when spec.AuthSecretRef
// is nil, Fetch does NOT trip the ErrUnauthorized branches — even with
// req.Secret = nil. The fetch will still fail (no real network here),
// but the failure mode must be Unreachable, not Unauthorized.
func TestFetch_AnonymousIgnoresSecret(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "no-such-owner-x/no-such-repo-y",
		Ref:  "main",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: nil})
	if errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected non-Unauthorized error for anonymous spec with nil Secret; got %v", err)
	}
}

// TestFetch_NilSecret asserts a nil corev1.Secret on the FetchRequest
// classifies as ErrUnauthorized (defense in depth above the reconciler
// which is expected to resolve the Secret before calling Fetch). Only
// applies when spec.AuthSecretRef is set — anonymous specs are covered
// by TestFetch_AnonymousIgnoresSecret.
func TestFetch_NilSecret(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "owner/repo",
		Ref:  "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "secret",
			Key:  "token",
		},
	})
	if err != nil {
		t.Fatalf("New unexpected error: %v", err)
	}

	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: nil})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for nil Secret; got %v", err)
	}
}

// TestFetch_MissingAuthKey asserts a Secret whose Data does not carry
// the configured AuthSecretRef.Key returns ErrUnauthorized. Threat
// T-02-02-01: the error message includes the KEY NAME but never the
// (absent) value.
func TestFetch_MissingAuthKey(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "owner/repo",
		Ref:  "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "secret",
			Key:  "token",
		},
	})
	if err != nil {
		t.Fatalf("New unexpected error: %v", err)
	}

	emptySecret := &corev1.Secret{Data: map[string][]byte{}}
	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: emptySecret})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
	// Threat T-02-02-01: error string must mention the key NAME
	// (operator-readable) but the (absent) value cannot leak — there is
	// no value to begin with, so this is the easy case.
	if !contains(err.Error(), "token") {
		t.Errorf("error should mention key name 'token'; got %q", err.Error())
	}
}

// TestFetch_MalformedRepo asserts a spec.Repo that is not <owner>/<name>
// returns ErrUpstreamInvalid (do NOT reach the network).
func TestFetch_MalformedRepo(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "no-slash",
		Ref:  "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "secret",
			Key:  "token",
		},
	})
	if err != nil {
		t.Fatalf("New unexpected error: %v", err)
	}

	secret := &corev1.Secret{Data: map[string][]byte{"token": []byte("ghp_x")}}
	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: secret})
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected ErrUpstreamInvalid for malformed repo; got %v", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

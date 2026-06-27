// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"strings"
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
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should mention key name 'token'; got %q", err.Error())
	}
}

// TestFetch_DefaultedKeyMissing_ErrorMessageHasHint asserts the error
// when AuthSecretRef.Key is empty AND the Secret lacks GITHUB_TOKEN
// includes a hint pointing at the default-key convention so the
// operator knows where the GITHUB_TOKEN name came from. PR #9
// follow-up review finding #9.
func TestFetch_DefaultedKeyMissing_ErrorMessageHasHint(t *testing.T) {
	t.Parallel()
	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "owner/repo",
		Ref:  "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "s",
			// Key intentionally empty → resolved to GITHUB_TOKEN.
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	emptySecret := &corev1.Secret{Data: map[string][]byte{}}
	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: emptySecret})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should still mention the resolved key name; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("error should hint that GITHUB_TOKEN was the default-key fallback; got %q", err.Error())
	}
}

// TestFetch_MalformedRepo asserts a spec.Repo that is not <owner>/<name>
// is rejected at New time (CR-02 parity; see internal/sources/cr02validate).
// The network is never reached.
func TestFetch_MalformedRepo(t *testing.T) {
	t.Parallel()

	_, err := New(&achv1alpha1.GitHubSource{
		Repo: "no-slash",
		Ref:  "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "secret",
			Key:  "token",
		},
	})
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected New to reject malformed repo with ErrUpstreamInvalid; got %v", err)
	}
}

// TestNew_RejectsMetacharRepo asserts CR-02 mitigation: crafted Repo
// values with URL-structural metacharacters are rejected at New time,
// never reaching the git subprocess.
func TestNew_RejectsMetacharRepo(t *testing.T) {
	t.Parallel()
	cases := []string{
		"owner/repo\n",
		"owner/repo?evil=1",
		"owner/repo#frag",
		"owner/repo with space",
		"a/b/c",
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			_, err := New(&achv1alpha1.GitHubSource{Repo: c, Ref: "main"})
			if err == nil {
				t.Errorf("expected New to reject %q", c)
			}
			if err != nil && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
			}
		})
	}
}

// TestNew_RejectsMetacharRef asserts CR-02 mitigation for spec.Ref.
func TestNew_RejectsMetacharRef(t *testing.T) {
	t.Parallel()
	cases := []string{"main\n", "main?evil", "main#frag"}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			_, err := New(&achv1alpha1.GitHubSource{Repo: "owner/repo", Ref: c})
			if err == nil {
				t.Errorf("expected New to reject ref %q", c)
			}
			if err != nil && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
			}
		})
	}
}

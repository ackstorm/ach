// SPDX-License-Identifier: Apache-2.0

package gitlab

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// TestNew_NilSpec asserts the nil-spec branch returns ErrUpstreamInvalid.
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

// TestFetch_NilSecret asserts a nil corev1.Secret classifies as
// ErrUnauthorized.
func TestFetch_NilSecret(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitLabSource{
		Project: "group/project",
		Ref:     "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "secret",
			Key:  "token",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: nil})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for nil Secret; got %v", err)
	}
}

// TestFetch_MissingAuthKey asserts a Secret without the configured key
// classifies as ErrUnauthorized; T-02-02-01: error message mentions the
// key NAME but never any value.
func TestFetch_MissingAuthKey(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.GitLabSource{
		Project: "group/project",
		Ref:     "main",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "secret",
			Key:  "token",
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
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should mention key NAME 'token'; got %q", err.Error())
	}
}

// TestNew_RejectsMetacharProject asserts CR-02 mitigation: crafted
// Project values with URL-structural metacharacters are rejected at
// New time, never reaching the git subprocess.
func TestNew_RejectsMetacharProject(t *testing.T) {
	t.Parallel()
	cases := []string{
		"group/sub\n",
		"group/sub?evil=1",
		"group/sub#frag",
		"group/sub with space",
		"no-slash",
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			_, err := New(&achv1alpha1.GitLabSource{Project: c, Ref: "main"})
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
			_, err := New(&achv1alpha1.GitLabSource{Project: "g/p", Ref: c})
			if err == nil {
				t.Errorf("expected New to reject ref %q", c)
			}
			if err != nil && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
			}
		})
	}
}

// TestNew_RejectsMetacharHost asserts CR-02 mitigation for spec.Host.
func TestNew_RejectsMetacharHost(t *testing.T) {
	t.Parallel()
	cases := []string{
		"gitlab.example.com/path",
		"gitlab.example.com\n",
		"gitlab.example.com?evil",
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			_, err := New(&achv1alpha1.GitLabSource{
				Host: c, Project: "g/p", Ref: "main",
			})
			if err == nil {
				t.Errorf("expected New to reject host %q", c)
			}
			if err != nil && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
			}
		})
	}
}

// TestFetch_DefaultedKeyMissing_ErrorMessageHasHint asserts the error
// when AuthSecretRef.Key is empty AND the Secret lacks GITLAB_TOKEN
// includes a hint pointing at the default-key convention. PR #9
// follow-up review finding #9.
func TestFetch_DefaultedKeyMissing_ErrorMessageHasHint(t *testing.T) {
	t.Parallel()
	f, err := New(&achv1alpha1.GitLabSource{
		Project:   "group/proj",
		Ref:       "main",
		Transport: "rest",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "s",
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
	if !strings.Contains(err.Error(), "GITLAB_TOKEN") {
		t.Errorf("error should mention the resolved key name; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("error should hint that GITLAB_TOKEN was the default-key fallback; got %q", err.Error())
	}
}

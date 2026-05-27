// SPDX-License-Identifier: Apache-2.0

package bitbucket

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
		t.Errorf("expected nil Fetcher; got %T", f)
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected ErrUpstreamInvalid; got %v", err)
	}
}

// TestFetch_NilSecret asserts a nil Secret classifies as ErrUnauthorized.
func TestFetch_NilSecret(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.BitbucketSource{
		Workspace: "workspace",
		Repo:      "repo",
		Ref:       "main",
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
// classifies as ErrUnauthorized; T-02-02-01: error mentions key NAME.
func TestFetch_MissingAuthKey(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.BitbucketSource{
		Workspace: "workspace",
		Repo:      "repo",
		Ref:       "main",
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

// TestFetch_DefaultedKeyMissing_ErrorMessageHasHint asserts the error
// when AuthSecretRef.Key is empty AND the Secret lacks BITBUCKET_TOKEN
// includes a hint pointing at the default-key convention. PR #9
// follow-up review finding #9.
func TestFetch_DefaultedKeyMissing_ErrorMessageHasHint(t *testing.T) {
	t.Parallel()
	f, err := New(&achv1alpha1.BitbucketSource{
		Workspace: "ws",
		Repo:      "repo",
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
	if !strings.Contains(err.Error(), "BITBUCKET_TOKEN") {
		t.Errorf("error should mention the resolved key name; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("error should hint that BITBUCKET_TOKEN was the default-key fallback; got %q", err.Error())
	}
}

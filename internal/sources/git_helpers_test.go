// SPDX-License-Identifier: Apache-2.0

package sources_test

import (
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

func TestExtractBearerToken_NoAuthRef(t *testing.T) {
	tok, err := sources.ExtractBearerToken("github", nil, nil)
	if err != nil || tok != "" {
		t.Fatalf("nil authRef: got (%q,%v), want (\"\",nil)", tok, err)
	}
}

func TestExtractBearerToken_NilSecret(t *testing.T) {
	ref := &achv1alpha1.SourceAuthSecretRef{Name: "s"}
	_, err := sources.ExtractBearerToken("github", ref, nil)
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("nil secret: err=%v, want ErrUnauthorized", err)
	}
}

func TestExtractBearerToken_MissingDefaultedKey(t *testing.T) {
	ref := &achv1alpha1.SourceAuthSecretRef{Name: "s"} // empty Key → defaulted
	sec := &corev1.Secret{Data: map[string][]byte{}}
	_, err := sources.ExtractBearerToken("gitlab", ref, sec)
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("missing key: err=%v, want ErrUnauthorized", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "default for gitlab") {
		t.Errorf("error message %q lost the defaulted-key hint", msg)
	}
}

func TestExtractBearerToken_Success(t *testing.T) {
	ref := &achv1alpha1.SourceAuthSecretRef{Name: "s", Key: "token"}
	sec := &corev1.Secret{Data: map[string][]byte{"token": []byte("ghp_x")}}
	tok, err := sources.ExtractBearerToken("github", ref, sec)
	if err != nil || tok != "ghp_x" {
		t.Fatalf("got (%q,%v), want (ghp_x,nil)", tok, err)
	}
}

func TestNormalizeGitLabHost(t *testing.T) {
	cases := map[string]string{
		"git.example.com":          "git.example.com",
		"https://git.example.com":  "git.example.com",
		"http://git.example.com":   "git.example.com",
		"HTTPS://Git.Example.com/": "Git.Example.com",
		"":                         "",
		"gitlab.com/":              "gitlab.com",
	}
	for in, want := range cases {
		if got := sources.NormalizeGitLabHost(in); got != want {
			t.Errorf("NormalizeGitLabHost(%q) = %q; want %q", in, got, want)
		}
	}
}

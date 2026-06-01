// SPDX-License-Identifier: Apache-2.0

package sources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// ExtractBearerToken resolves the auth token for a git/HTTP source from
// its AuthSecretRef + the fetched Secret. provider is the lowercase
// source-type literal ("github"/"gitlab"/"bitbucket") used in error
// prefixes and DefaultAuthSecretKey. Returns ("",nil) when authRef is nil
// (anonymous public-repo fetch). All error paths wrap ErrUnauthorized and
// SECURITY-CRITICAL: they surface the key NAME, never the absent value
// (threat T-02-02-01).
func ExtractBearerToken(provider string, authRef *achv1alpha1.SourceAuthSecretRef, secret *corev1.Secret) (string, error) {
	if authRef == nil {
		return "", nil
	}
	if secret == nil {
		return "", fmt.Errorf("%s: auth secret %q is nil: %w",
			provider, authRef.Name, ErrUnauthorized)
	}
	key := authRef.Key
	defaulted := false
	if key == "" {
		key = achv1alpha1.DefaultAuthSecretKey(provider)
		defaulted = true
	}
	raw := secret.Data[key]
	if len(raw) == 0 {
		if defaulted {
			return "", fmt.Errorf(
				"%s: missing auth secret key %q (default for %s; set authSecretRef.key to override): %w",
				provider, key, provider, ErrUnauthorized)
		}
		return "", fmt.Errorf("%s: missing auth secret key %q: %w",
			provider, key, ErrUnauthorized)
	}
	return string(raw), nil
}

// ResolvedTransport returns "rest" when transport == "rest", else "git".
func ResolvedTransport(transport string) string {
	if transport == "rest" {
		return "rest"
	}
	return "git"
}

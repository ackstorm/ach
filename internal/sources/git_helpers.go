// SPDX-License-Identifier: Apache-2.0

package sources

import (
	"fmt"
	"strings"

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

// NormalizeGitLabHost strips a case-variant http:// or https:// scheme
// prefix and any trailing slash from a GitLab host, returning the bare
// host[:port]. Idempotent. Both the gitlab source fetcher and the
// PluginMarketplace dispatch path call this so a GitLabSource.Host of
// "git.example.com" and "https://git.example.com" normalize to the SAME
// canonical form; callers always rebuild the clone/REST URL as
// https://<host>. CR-02 (cr02validate.HostIdentifier) rejects '/' in a
// flat host identifier, so normalizing BEFORE validation is what lets the
// scheme form pass.
func NormalizeGitLabHost(host string) string {
	low := strings.ToLower(host)
	switch {
	case strings.HasPrefix(low, "https://"):
		host = host[len("https://"):]
	case strings.HasPrefix(low, "http://"):
		host = host[len("http://"):]
	}
	return strings.TrimRight(host, "/")
}

// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// DefaultAuthSecretKey returns the provider-specific Secret data-key
// the operator falls back to when authSecretRef.key is omitted on a
// git source type. Mirrors the ecosystem env-var convention:
//
//	github    → GITHUB_TOKEN
//	gitlab    → GITLAB_TOKEN
//	bitbucket → BITBUCKET_TOKEN
//
// Returns "" for source types that don't have a default (s3 / gcs /
// http carry their own per-type key fields).
func DefaultAuthSecretKey(sourceType string) string {
	switch sourceType {
	case "github":
		return "GITHUB_TOKEN"
	case "gitlab":
		return "GITLAB_TOKEN"
	case "bitbucket":
		return "BITBUCKET_TOKEN"
	default:
		return ""
	}
}

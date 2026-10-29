// SPDX-License-Identifier: Apache-2.0

package jwt

import "strings"

// ASMetadata is the RFC 8414 authorization-server document. The AS lives in
// platform-api (/platform/oauth/*) but the document is SERVED BY THE
// FORWARDER at {issuer}/.well-known/oauth-authorization-server (the gateway
// routes /.well-known/ there); it lives in this package so both services
// compose the endpoint URLs from one source. Every value is a client
// requirement from docs/developer-guide/oauth-client-conformance.md §2.
func ASMetadata(issuer string) map[string]any {
	issuer = strings.TrimRight(issuer, "/")
	return map[string]any{
		"issuer":                        issuer,
		"authorization_endpoint":        issuer + "/platform/oauth/authorize",
		"token_endpoint":                issuer + "/platform/oauth/token",
		"registration_endpoint":         issuer + "/platform/oauth/register",
		"device_authorization_endpoint": issuer + "/platform/oauth/device_authorization",
		"jwks_uri":                      issuer + "/.well-known/jwks.json",
		"response_types_supported":      []string{"code"},
		"grant_types_supported": []string{
			"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code",
		},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"offline_access"},
	}
}

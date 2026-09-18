// SPDX-License-Identifier: Apache-2.0

// Package oauthsvc is the static MCP-service map the OAuth broker chain
// runs on: /mcp/<key> → the broker that fronts it and the store name the
// broker knows the service by. One JSON blob (ACH_OAUTH_SERVICES), the same
// shape as alitellm-auth's authServer.services, read by platform-api (the
// chain, scope on the token) and the forwarder (PRM scopes_supported, the
// insufficient_scope gate).
package oauthsvc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Service is one /mcp/<key> entry.
type Service struct {
	Store  string `json:"store"`  // the broker's name for it: its scope + the login_hint aud
	Broker string `json:"broker"` // the broker's OAuth AS base (has /register, /authorize); no trailing slash
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`) // the forwarder's serviceRe character class

// Parse decodes ACH_OAUTH_SERVICES. "" → nil map (feature dormant).
func Parse(raw string) (map[string]Service, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var m map[string]Service
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("ACH_OAUTH_SERVICES: %w", err)
	}
	for k, s := range m {
		if !keyRe.MatchString(k) {
			return nil, fmt.Errorf("ACH_OAUTH_SERVICES: key %q is not a /mcp/<name> segment", k)
		}
		if s.Store == "" {
			return nil, fmt.Errorf("ACH_OAUTH_SERVICES[%s]: store required", k)
		}
		u, err := url.Parse(s.Broker)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("ACH_OAUTH_SERVICES[%s]: broker must be an http(s) URL", k)
		}
		s.Broker = strings.TrimRight(s.Broker, "/")
		m[k] = s
	}
	return m, nil
}

// Keys returns the service keys sorted.
func Keys(m map[string]Service) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

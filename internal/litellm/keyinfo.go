// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ProbeConnection issues GET /models with the master key and returns
// nil on HTTP 200, *Auth401Error on 401, or a transient error on
// 5xx / network failure. Used by the LiteLLMConnection reconciler
// (Phase 2) to set Ready=True / Ready=False.
//
// Why /models (NOT the legacy spec-§6.1 key-info path): the spike
// (plan 01-01, Probe 1) empirically verified that LITELLM_MASTER_KEY
// env var does NOT auto-store the key in the database, so the legacy
// path returns 404 "Key not found in database" with the master key.
// /models is
// auth-protected, returns 200 cheaply when LiteLLM is up AND the key
// is honored, and serves as both liveness and auth-validation in one
// call. See 01-01-SUMMARY.md decisions block.
//
// The filename keyinfo.go is retained for git-history continuity — the
// file owns the "connection probing" concern, not the specific endpoint.
func (c *RESTClient) ProbeConnection(ctx context.Context) error {
	_, err := c.makeRequest(ctx, "GET", "/models", nil)
	return err
}

// ListUserKeys issues
// GET /key/list?user_id=<userID>&return_full_object=true&include_team_keys=false
// and returns the LiteLLM keys owned by a specific user. Used by the
// Plan 08 orphan-cleanup Runnable (D-16) to enumerate all keys
// associated with an ACH-managed LiteLLM user; the caller cross-
// references the returned set against active personal_keys /
// environment_keys rows per Hub §18.4 to identify orphans.
//
// Endpoint history: the legacy per-user lookup against the singular
// /key/info path returns 404 not_found_error on LiteLLM v1.83.10 —
// that endpoint looks up a SPECIFIC key by ?key=<token>, not by user.
// Phase 02.2 Plan 1 (Gap G1 fix) swapped to /key/list, whose response
// `token` field is the LiteLLM-internal opaque hex key id (NOT ACH's
// `pkid_*` / `ekid_*` prefix). The orphan loop's set-difference key
// shifts accordingly.
//
// Empty result is NOT ErrNotFound — a user may legitimately have zero
// keys (e.g. their pk_ was just revoked and there is no ek_ for them).
// Callers decide whether absence is interesting.
//
// §9.1: only the user_id and status code are logged — no response body.
func (c *RESTClient) ListUserKeys(ctx context.Context, userID string) ([]UserKeyInfo, error) {
	path := "/key/list?user_id=" + url.QueryEscape(userID) + "&return_full_object=true&include_team_keys=false"
	raw, err := c.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp ListUserKeysResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /key/list: %w", err)
	}
	return resp.Keys, nil
}

// RevokeKey issues POST /key/delete with body {"keys": [keyID]} —
// revoking a single LiteLLM key by its LiteLLM-internal key_id (NOT the
// plaintext bearer prefix). Used by the orphan-cleanup Runnable (D-16,
// Plan 08).
//
// Audit emission is the caller's responsibility — RevokeKey itself does
// NOT emit audit events. This preserves separation of concerns and
// matches the [feedback_litellm_operator_no_redaction_filter] memory
// pattern: the operator code never logs bodies or constructs audit
// payloads from within the client surface.
func (c *RESTClient) RevokeKey(ctx context.Context, keyID string) error {
	_, err := c.makeRequest(ctx, "POST", "/key/delete", map[string]any{"keys": []string{keyID}})
	return err
}

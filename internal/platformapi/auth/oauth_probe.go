// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
)

type probeOutcome string

const (
	probeOK           probeOutcome = "ok"
	probeAuthRequired probeOutcome = "auth_required"
	probeOther        probeOutcome = "other"
)

const probeTTL = 60 * time.Second

// probeGrant checks the backend through the public Forwarder path as the
// user. Only LiteLLM's auth_required outcome triggers chained consent.
func (d OAuthDeps) probeGrant(ctx context.Context, email, userID, key string) probeOutcome {
	if err := d.ensureOAuthPK(ctx, email, userID); err != nil {
		d.Auth.Logger.Warn("oauth: probe: ensure pk_ failed", "err", err)
		return probeOther
	}
	token, err := d.Signer.Sign(ctx, jwt.Claims{Iss: d.Issuer, Sub: email, Aud: d.Audience, Email: email, TTL: probeTTL})
	if err != nil {
		return probeOther
	}
	endpoint := strings.TrimRight(d.Issuer, "/") + "/mcp/" + key
	post := func(session, body string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		return d.httpClient().Do(req)
	}

	initResp, err := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ach-consent-probe","version":"1"}}}`)
	if err != nil {
		return probeOther
	}
	_, _ = io.Copy(io.Discard, initResp.Body)
	_ = initResp.Body.Close()
	if initResp.StatusCode != http.StatusOK {
		return probeOther
	}

	resp, err := post(initResp.Header.Get("Mcp-Session-Id"), `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if err != nil {
		return probeOther
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return probeOther
	}
	result, err := mcpResult(resp.Header.Get("Content-Type"), body)
	if err != nil {
		return probeOther
	}
	meta, _ := result["_meta"].(map[string]any)
	outcomes, _ := meta["litellm.ai/server_outcomes"].(map[string]any)
	outcome, _ := outcomes[key].(map[string]any)
	switch status, _ := outcome["status"].(string); status {
	case "ok":
		return probeOK
	case "auth_required":
		return probeAuthRequired
	default:
		return probeOther
	}
}

// mcpResult extracts the JSON-RPC result from JSON or streamable-HTTP SSE.
func mcpResult(contentType string, body []byte) (map[string]any, error) {
	raw := body
	if strings.HasPrefix(contentType, "text/event-stream") {
		raw = nil
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data:") {
				raw = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, errors.New("no data line")
		}
	}
	var envelope struct {
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Error) > 0 {
		return nil, fmt.Errorf("jsonrpc error: %s", envelope.Error)
	}
	if envelope.Result == nil {
		return nil, errors.New("no result")
	}
	return envelope.Result, nil
}

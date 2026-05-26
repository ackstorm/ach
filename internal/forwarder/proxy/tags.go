// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	// tagPrefix is the FWD-06 tag namespace. All Environment attribution
	// tags ship as "environment:<name>". Greppable, parseable upstream by
	// LiteLLM's tag aggregation per Hub §6.3.
	tagPrefix = "environment:"

	// maxBodyForTagInjection caps request body size for tag injection.
	// Mirrors http.Server.MaxHeaderBytes (1 MiB) per D-04 spirit.
	maxBodyForTagInjection = 1 << 20 // 1 MiB
)

var (
	errBodyTooLarge     = errors.New("request body exceeds tag-injection cap")
	errNotJSON          = errors.New("request Content-Type is not application/json")
	errEmptyEnvironment = errors.New("environmentName is empty")
)

// InjectEnvironmentTag mutates req.Body in place to add
// "environment:<environmentName>" to metadata.tags[] in the JSON request
// body, per FWD-06 (Hub §5.1, §6.3). Fail-open: returns a non-nil error
// on schema/transport problems but the caller is EXPECTED to ignore it.
//
// Safe to call alongside SSE: SSE is RESPONSE-side; request body for
// LiteLLM chat completions is always buffered JSON.
//
// Scope (v1alpha1): /v1 + /gemini per-route handlers only.
// MCP/A2A tag injection deferred to v1beta1.
func InjectEnvironmentTag(req *http.Request, environmentName string) error {
	if environmentName == "" {
		return errEmptyEnvironment
	}
	if req == nil || req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	ct := req.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json") {
		return errNotJSON
	}
	if req.ContentLength > maxBodyForTagInjection {
		return errBodyTooLarge
	}
	limited := io.LimitReader(req.Body, maxBodyForTagInjection+1)
	raw, readErr := io.ReadAll(limited)
	_ = req.Body.Close()
	if readErr != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return readErr
	}
	if len(raw) > maxBodyForTagInjection {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return errBodyTooLarge
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return err
	}

	metaRaw, metaPresent := doc["metadata"]
	var meta map[string]any
	if metaPresent {
		var ok bool
		meta, ok = metaRaw.(map[string]any)
		if !ok {
			req.Body = io.NopCloser(bytes.NewReader(raw))
			return errors.New("metadata is not a JSON object")
		}
	} else {
		meta = map[string]any{}
	}

	tagsRaw, tagsPresent := meta["tags"]
	var tags []any
	if tagsPresent {
		var ok bool
		tags, ok = tagsRaw.([]any)
		if !ok {
			req.Body = io.NopCloser(bytes.NewReader(raw))
			return errors.New("metadata.tags is not a JSON array")
		}
	} else {
		tags = []any{}
	}

	tags = append(tags, tagPrefix+environmentName)
	meta["tags"] = tags
	doc["metadata"] = meta

	out, err := json.Marshal(doc)
	if err != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return err
	}

	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	req.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}
